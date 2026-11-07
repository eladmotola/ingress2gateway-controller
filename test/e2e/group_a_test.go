//go:build e2e

package e2e

// ---------------------------------------------------------------------------
// Group A: annotation-mode (ingressSelectionMode=annotation)
//
// Helm values: testdata/values-a.yaml
//   ingressSelectionMode:           annotation
//   defaultProvider:                ingress-nginx
//   ingressClassToGatewayClassMapping:
//     nginx-test: test-gc
// ---------------------------------------------------------------------------

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func groupA(t *testing.T) {
	t.Run("A1_AnnotationEnabled", testA1AnnotationEnabled)
	t.Run("A2_AnnotationMissing", testA2AnnotationMissing)
	t.Run("A3_DisabledFlip", testA3DisabledFlip)
	t.Run("A4_TwoIngressesSharedGateway", testA4TwoIngressesSharedGateway)
	t.Run("A5_UnmappedClass", testA5UnmappedClass)
	t.Run("A6_GRPCAnnotation", testA6GRPCAnnotation)
}

// A1: Ingress with enabled=true produces a Gateway (name=class, class=test-gc,
// ownerRef→Ingress, no controller ref), one HTTPRoute with parentRef and source
// annotations, and adds the finalizer to the Ingress.
func testA1AnnotationEnabled(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "a1")
	createService(t, ns, "my-svc", 80)

	ing := createIngress(t, ns, "my-ingress", "nginx-test",
		map[string]string{"ingress2gateway.io/enabled": "true"},
		simpleIngressRule("foo.example.com", "my-svc", 80),
	)
	sourceVal := fmt.Sprintf("%s/%s", ns, "my-ingress")

	// Gateway
	gw := waitForGateway(t, ns, "nginx-test")
	assert.Equal(t, "test-gc", string(gw.Spec.GatewayClassName), "GatewayClassName")
	assertGatewayOwnerRef(t, gw, ing)

	// HTTPRoute
	route := waitForHTTPRoute(t, ns, sourceVal)
	assertHTTPRouteParentRef(t, route, ns, "nginx-test", false)
	assertSourceAnnotations(t, route.Annotations, sourceVal, "ingress-nginx")

	// Finalizer
	waitForFinalizer(t, ns, "my-ingress")
}

// A2: Ingress without the enabled annotation → nothing created, no finalizer.
func testA2AnnotationMissing(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "a2")
	createService(t, ns, "my-svc", 80)
	createIngress(t, ns, "my-ingress", "nginx-test", nil,
		simpleIngressRule("bar.example.com", "my-svc", 80),
	)
	sourceVal := fmt.Sprintf("%s/my-ingress", ns)

	assertNoGateway(t, ns, "nginx-test")
	assertNoHTTPRoute(t, ns, sourceVal)
	assertNoFinalizer(t, ns, "my-ingress")
}

// A3: enabled=true → resources created; flip to enabled=false → resources
// cleaned up and finalizer removed.
func testA3DisabledFlip(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "a3")
	createService(t, ns, "my-svc", 80)
	createIngress(t, ns, "my-ingress", "nginx-test",
		map[string]string{"ingress2gateway.io/enabled": "true"},
		simpleIngressRule("flip.example.com", "my-svc", 80),
	)
	sourceVal := fmt.Sprintf("%s/my-ingress", ns)

	// Ensure resources were created
	waitForGateway(t, ns, "nginx-test")
	waitForHTTPRoute(t, ns, sourceVal)
	waitForFinalizer(t, ns, "my-ingress")

	// Flip to disabled
	patchIngressAnnotation(t, ns, "my-ingress", "ingress2gateway.io/enabled", "false")

	// Resources and finalizer should disappear
	waitForGatewayGone(t, ns, "nginx-test")
	waitForHTTPRouteGone(t, ns, sourceVal)
	waitForFinalizerGone(t, ns, "my-ingress")
}

// A4: Two Ingresses in different namespaces, same class, same Gateway namespace
// (via ingress2gateway.io/gateway-namespace). One Gateway with two owners;
// deleting one removes only its route; deleting the last removes the Gateway.
func testA4TwoIngressesSharedGateway(t *testing.T) {
	defer dumpArtifacts(t)

	ns1 := createNamespace(t, "a4a")
	ns2 := createNamespace(t, "a4b")
	createService(t, ns1, "svc-a", 80)
	createService(t, ns2, "svc-b", 80)

	ann := func() map[string]string {
		return map[string]string{
			"ingress2gateway.io/enabled":           "true",
			"ingress2gateway.io/gateway-namespace": ns1,
		}
	}
	ing1 := createIngress(t, ns1, "ing-a", "nginx-test", ann(),
		simpleIngressRule("alpha.example.com", "svc-a", 80),
	)
	ing2 := createIngress(t, ns2, "ing-b", "nginx-test", ann(),
		simpleIngressRule("beta.example.com", "svc-b", 80),
	)
	source1 := fmt.Sprintf("%s/ing-a", ns1)
	source2 := fmt.Sprintf("%s/ing-b", ns2)

	waitForHTTPRoute(t, ns1, source1)
	waitForHTTPRoute(t, ns2, source2)
	gw := waitForGateway(t, ns1, "nginx-test")

	assertGatewayOwnerRef(t, gw, ing1)
	// Kubernetes forbids namespaced OwnerReferences across namespaces.
	// SetOwnerReference for ing-b therefore fails (logged at V(1)) and is not stored.
	assertGatewayNotOwnedBy(t, gw, ing2)

	require.NoError(t, k8sClient.Delete(context.Background(), ing2))
	waitForHTTPRouteGone(t, ns2, source2)

	_, err := getGateway(ns1, "nginx-test")
	require.NoError(t, err, "gateway should still exist after deleting one ingress")
	_, err = findHTTPRouteBySource(ns1, source1)
	require.NoError(t, err, "HTTPRoute for ing-a should still exist")

	require.NoError(t, k8sClient.Delete(context.Background(), ing1))
	waitForGatewayGone(t, ns1, "nginx-test")
	waitForHTTPRouteGone(t, ns1, source1)
}

// A5: Ingress with an unmapped class + enabled=true → no resources created;
// controller logs the error; controller stays healthy (no crash).
func testA5UnmappedClass(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "a5")
	createService(t, ns, "my-svc", 80)
	createIngress(t, ns, "my-ingress", "unmapped-class",
		map[string]string{"ingress2gateway.io/enabled": "true"},
		simpleIngressRule("unmapped.example.com", "my-svc", 80),
	)

	// Nothing should be created
	assertNoGateway(t, ns, "unmapped-class")
	assertNoHTTPRoute(t, ns, fmt.Sprintf("%s/my-ingress", ns))

	// Controller should log the mapping error (and keep running — rollout healthy)
	eventually(t, func() error {
		logs := controllerPodLogs(t)
		if logs == "" {
			return fmt.Errorf("no controller logs yet")
		}
		if strings.Contains(logs, "not in the global mapping") && strings.Contains(logs, "unmapped-class") {
			return nil
		}
		return fmt.Errorf("controller log does not contain expected mapping error yet")
	}, defaultTimeout, defaultInterval)

	// Controller is still healthy (rollout unchanged)
	waitForRollout(t)
}

// A6: nginx backend-protocol GRPC annotation → GRPCRoute produced.
func testA6GRPCAnnotation(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "a6")
	createService(t, ns, "grpc-svc", 8080)
	createIngress(t, ns, "grpc-ingress", "nginx-test",
		map[string]string{
			"ingress2gateway.io/enabled":                   "true",
			"nginx.ingress.kubernetes.io/backend-protocol": "GRPC",
		},
		simpleIngressRule("grpc.example.com", "grpc-svc", 8080),
	)
	sourceVal := fmt.Sprintf("%s/grpc-ingress", ns)

	// GRPCRoute should be produced
	route := waitForGRPCRoute(t, ns, sourceVal)
	assert.NotEmpty(t, route.Spec.ParentRefs, "GRPCRoute should have parentRefs")
}
