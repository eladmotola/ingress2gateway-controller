//go:build e2e

package e2e

// ---------------------------------------------------------------------------
// Group E: controller restart and finalizer while scaled to 0.
// Helm values: testdata/values-a.yaml (annotation mode), restored before this group.
// ---------------------------------------------------------------------------

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func groupE(t *testing.T) {
	t.Run("E_RestartIdempotent", testERestartIdempotent)
	t.Run("E_DeleteWhileScaledToZero", testEDeleteWhileScaledToZero)
}

// Controller restart while an Ingress exists → same Gateway/HTTPRoute, no duplicates.
func testERestartIdempotent(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "e-restart")
	createService(t, ns, "my-svc", 80)
	createIngress(t, ns, "my-ingress", "nginx-test",
		map[string]string{"ingress2gateway.io/enabled": "true"},
		simpleIngressRule("restart.example.com", "my-svc", 80),
	)
	sourceVal := fmt.Sprintf("%s/my-ingress", ns)

	waitForGateway(t, ns, "nginx-test")
	waitForHTTPRoute(t, ns, sourceVal)
	waitForFinalizer(t, ns, "my-ingress")

	_, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"rollout", "restart", "deployment/ingress2gateway-controller",
		"-n", helmNamespace)
	require.NoError(t, err, "rollout restart")
	waitForRollout(t)

	gwList := &gatewayv1.GatewayList{}
	require.NoError(t, k8sClient.List(context.Background(), gwList, client.InNamespace(ns)))
	assert.Equal(t, 1, len(gwList.Items), "expected exactly one Gateway after restart")

	rtList := &gatewayv1.HTTPRouteList{}
	require.NoError(t, k8sClient.List(context.Background(), rtList, client.InNamespace(ns)))
	assert.Equal(t, 1, len(rtList.Items), "expected exactly one HTTPRoute after restart")
	assert.Equal(t, sourceVal, rtList.Items[0].Annotations[annotationSourceIngress])
}

// Delete an Ingress while the controller is scaled to 0 → deletion blocks on the
// finalizer and completes after scaling back to 1.
func testEDeleteWhileScaledToZero(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "e-scale")
	createService(t, ns, "my-svc", 80)
	ing := createIngress(t, ns, "my-ingress", "nginx-test",
		map[string]string{"ingress2gateway.io/enabled": "true"},
		simpleIngressRule("scale.example.com", "my-svc", 80),
	)
	sourceVal := fmt.Sprintf("%s/my-ingress", ns)

	waitForGateway(t, ns, "nginx-test")
	waitForHTTPRoute(t, ns, sourceVal)
	waitForFinalizer(t, ns, "my-ingress")

	scaleDeployment(t, helmNamespace, "ingress2gateway-controller", 0)
	waitForDeploymentReplicas(t, helmNamespace, "ingress2gateway-controller", 0)
	t.Cleanup(func() {
		scaleDeployment(t, helmNamespace, "ingress2gateway-controller", 1)
		waitForDeploymentReplicas(t, helmNamespace, "ingress2gateway-controller", 1)
	})

	require.NoError(t, k8sClient.Delete(context.Background(), ing))

	// Ingress must remain with our finalizer while the controller is down.
	stuck := &networkingv1.Ingress{}
	require.NoError(t, k8sClient.Get(context.Background(),
		client.ObjectKey{Namespace: ns, Name: "my-ingress"}, stuck))
	require.NotNil(t, stuck.DeletionTimestamp, "expected DeletionTimestamp while finalizer blocks")
	found := false
	for _, f := range stuck.Finalizers {
		if f == finalizerName {
			found = true
			break
		}
	}
	require.True(t, found, "expected %q finalizer to block deletion", finalizerName)

	scaleDeployment(t, helmNamespace, "ingress2gateway-controller", 1)
	waitForDeploymentReplicas(t, helmNamespace, "ingress2gateway-controller", 1)
	waitForRollout(t)

	waitForIngressGone(t, ns, "my-ingress")
	waitForHTTPRouteGone(t, ns, sourceVal)
	waitForGatewayGone(t, ns, "nginx-test")
}
