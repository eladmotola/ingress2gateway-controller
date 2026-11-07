//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ============================================================================
// Shell helpers
// ============================================================================

// runCmd runs a command and returns its combined stdout. Returns an error that
// includes stdout+stderr on failure.
func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("command %q %v: %w\nstdout: %s\nstderr: %s",
			name, args, err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

// runCmdSilent runs a command, discarding output. Returns error on failure.
func runCmdSilent(name string, args ...string) error {
	_, err := runCmd(name, args...)
	return err
}

// ============================================================================
// Infrastructure helpers
// ============================================================================

// findRepoRoot walks up from the test package dir until it finds go.mod.
func findRepoRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("could not find repo root (go.mod not found)")
		}
		dir = parent
	}
}

// testdataPath returns the absolute path to a file in test/e2e/testdata/.
func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	return filepath.Join(dir, "testdata", name)
}

// waitForCRDsEstablished waits for the Gateway API CRDs to become Established.
func waitForCRDsEstablished(timeout time.Duration) error {
	crds := []string{
		"gateways.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
		"grpcroutes.gateway.networking.k8s.io",
		"tlsroutes.gateway.networking.k8s.io",
		"backendtlspolicies.gateway.networking.k8s.io",
	}
	timeoutSec := fmt.Sprintf("%ds", int(timeout.Seconds()))
	for _, crd := range crds {
		if _, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
			"wait", "--for=condition=Established",
			"crd/"+crd, "--timeout="+timeoutSec); err != nil {
			return fmt.Errorf("CRD %q not established: %w", crd, err)
		}
	}
	return nil
}

// ============================================================================
// Helm helpers
// ============================================================================

// helmInstallOrUpgrade does `helm upgrade --install` with the given values
// file against the main controller release. withWait=true adds --wait.
func helmInstallOrUpgrade(t testing.TB, valuesFile string, withWait bool) {
	t.Helper()
	chartPath := filepath.Join(findRepoRoot(), "helm", "ingress2gateway-controller")
	args := []string{
		"upgrade", "--install", helmReleaseName, chartPath,
		"--kubeconfig", kubeconfigPath,
		"--namespace", helmNamespace,
		"--create-namespace",
		"--values", valuesFile,
	}
	args = append(args, imageHelmSetArgs()...)
	// Unique annotation so every values change forces a new ReplicaSet (needed
	// so Group C16 / D see a fresh process, not leftover logs).
	args = append(args,
		"--set-string", "podAnnotations.e2e-values="+filepath.Base(valuesFile),
		"--set-string", fmt.Sprintf("podAnnotations.e2e-ts=%d", time.Now().UnixNano()),
	)
	if withWait {
		args = append(args, "--wait", "--timeout", "120s")
	}
	if _, err := runCmd("helm", args...); err != nil {
		t.Fatalf("helm upgrade --install failed: %v", err)
	}
}

// helmInstallSeparate installs a SEPARATE, temporary helm release (used by GroupD crash tests).
// Returns the release name; also registers t.Cleanup to uninstall it.
func helmInstallSeparate(t *testing.T, releaseName, namespace, valuesFile string) {
	t.Helper()
	chartPath := filepath.Join(findRepoRoot(), "helm", "ingress2gateway-controller")
	// No --wait: we expect the pod to crash. Unique fullname so ClusterRole
	// names do not collide with the main release.
	args := []string{
		"upgrade", "--install", releaseName, chartPath,
		"--kubeconfig", kubeconfigPath,
		"--namespace", namespace,
		"--create-namespace",
		"--values", valuesFile,
		"--set", "fullnameOverride=" + releaseName,
	}
	args = append(args, imageHelmSetArgs()...)
	args = append(args,
		"--set-string", "podAnnotations.e2e-ts="+fmt.Sprintf("%d", time.Now().UnixNano()),
	)
	if _, err := runCmd("helm", args...); err != nil {
		t.Fatalf("helm upgrade --install (separate) failed: %v", err)
	}
	t.Cleanup(func() {
		_ = runCmdSilent("helm", "uninstall", releaseName, "--kubeconfig", kubeconfigPath, "--namespace", namespace, "--ignore-not-found")
		_ = runCmdSilent("kubectl", "--kubeconfig", kubeconfigPath,
			"delete", "namespace", namespace, "--ignore-not-found", "--wait=false")
	})
}

func imageHelmSetArgs() []string {
	return []string{
		"--set", "image.registry=" + imageRegistry,
		"--set", "image.repository=" + imageRepository,
		"--set", "image.tag=" + imageTag,
		"--set", "image.pullPolicy=Never",
	}
}

// waitForRollout waits for the main controller deployment to be fully rolled out.
func waitForRollout(t testing.TB) {
	t.Helper()
	if _, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"rollout", "status", "deployment/ingress2gateway-controller",
		"-n", helmNamespace,
		"--timeout=120s"); err != nil {
		t.Fatalf("controller rollout failed: %v", err)
	}
}

// waitForDeploymentRollout waits for a named deployment in a given namespace.
func waitForDeploymentRollout(t testing.TB, ns, name string) {
	t.Helper()
	if _, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"rollout", "status", "deployment/"+name,
		"-n", ns,
		"--timeout=120s"); err != nil {
		t.Fatalf("deployment %s/%s rollout failed: %v", ns, name, err)
	}
}

// ============================================================================
// Namespace helpers
// ============================================================================

// createNamespace creates a namespace with a random suffix and registers a
// t.Cleanup that waits for full deletion (so namespaces are gone before any
// subsequent helm upgrade).
func createNamespace(t *testing.T, prefix string) string {
	t.Helper()
	name := fmt.Sprintf("%s-%s", prefix, randSuffix(6))
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	require.NoError(t, k8sClient.Create(context.Background(), ns), "create namespace %q", name)
	t.Cleanup(func() {
		deleteNamespaceAndWait(t, name)
	})
	return name
}

// deleteNamespaceAndWait deletes a namespace and blocks until the API server
// confirms it is gone (max 3 minutes).
func deleteNamespaceAndWait(t testing.TB, name string) {
	ctx := context.Background()
	ns := &corev1.Namespace{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: name}, ns); apierrors.IsNotFound(err) {
		return // already gone
	}
	_ = k8sClient.Delete(ctx, ns)
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		err := k8sClient.Get(ctx, client.ObjectKey{Name: name}, &corev1.Namespace{})
		if apierrors.IsNotFound(err) {
			return
		}
		time.Sleep(time.Second)
	}
	if t != nil {
		t.Logf("warning: namespace %q did not disappear within 3 minutes", name)
	}
}

// ============================================================================
// Fixture helpers
// ============================================================================

// createService creates a ClusterIP Service with a single port.
func createService(t *testing.T, ns, name string, port int32) *corev1.Service {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: port, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	require.NoError(t, k8sClient.Create(context.Background(), svc), "create service %s/%s", ns, name)
	return svc
}

// simpleIngressRule returns a single-host, single-path IngressRule.
func simpleIngressRule(host, svcName string, svcPort int32) networkingv1.IngressRule {
	prefix := networkingv1.PathTypePrefix
	return networkingv1.IngressRule{
		Host: host,
		IngressRuleValue: networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{
					{
						Path:     "/",
						PathType: &prefix,
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: svcName,
								Port: networkingv1.ServiceBackendPort{Number: svcPort},
							},
						},
					},
				},
			},
		},
	}
}

// createIngress creates a networking/v1 Ingress.
// class sets spec.ingressClassName (use "" to leave unset).
// annotations is merged on top.
func createIngress(t *testing.T, ns, name, class string, annotations map[string]string, rules ...networkingv1.IngressRule) *networkingv1.Ingress {
	t.Helper()
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			Rules: rules,
		},
	}
	if class != "" {
		ing.Spec.IngressClassName = &class
	}
	require.NoError(t, k8sClient.Create(context.Background(), ing), "create ingress %s/%s", ns, name)
	return ing
}

// createExternalGateway creates a minimal Gateway in a pre-existing namespace.
// The caller is responsible for creating/deleting the namespace.
func createExternalGateway(t *testing.T, ns, name, gatewayClass string) *gatewayv1.Gateway {
	t.Helper()
	prot := gatewayv1.HTTPProtocolType
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(gatewayClass),
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Port:     80,
					Protocol: prot,
				},
			},
		},
	}
	require.NoError(t, k8sClient.Create(context.Background(), gw), "create external gateway %s/%s", ns, name)
	return gw
}

// ============================================================================
// Eventually / polling helpers
// ============================================================================

// eventually polls condition (which returns nil when satisfied) until timeout.
func eventually(t testing.TB, condition func() error, timeout, interval time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := condition(); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}
	t.Fatalf("condition not met after %v: %v", timeout, lastErr)
}

// neverOccurs polls condition for duration and fails immediately if it ever
// returns nil (i.e., the "bad" thing happened).
func neverOccurs(t *testing.T, condition func() error, duration time.Duration) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if err := condition(); err == nil {
			t.Fatalf("condition became true within %v (expected it to never occur)", duration)
		}
		time.Sleep(defaultInterval)
	}
}

// ============================================================================
// Gateway helpers
// ============================================================================

func getGateway(ns, name string) (*gatewayv1.Gateway, error) {
	gw := &gatewayv1.Gateway{}
	err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, gw)
	return gw, err
}

func waitForGateway(t *testing.T, ns, name string) *gatewayv1.Gateway {
	t.Helper()
	var gw *gatewayv1.Gateway
	eventually(t, func() error {
		g, err := getGateway(ns, name)
		if err != nil {
			return fmt.Errorf("gateway %s/%s not found: %w", ns, name, err)
		}
		gw = g
		return nil
	}, defaultTimeout, defaultInterval)
	return gw
}

func waitForGatewayGone(t *testing.T, ns, name string) {
	t.Helper()
	eventually(t, func() error {
		_, err := getGateway(ns, name)
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("gateway %s/%s still exists", ns, name)
	}, defaultTimeout, defaultInterval)
}

// assertNoGateway fails immediately if a Gateway appears within duration.
func assertNoGateway(t *testing.T, ns, name string) {
	t.Helper()
	neverOccurs(t, func() error {
		_, err := getGateway(ns, name)
		if err == nil {
			return nil // gateway exists → "bad thing happened"
		}
		return err // not found or other error → condition not met
	}, negativeWait)
}

// ============================================================================
// HTTPRoute helpers
// ============================================================================

const annotationSourceIngress = "ingress2gateway.io/source-ingress"
const annotationSourceProvider = "ingress2gateway.io/source-provider"
const finalizerName = "ingress2gateway.io/finalizer"

// findHTTPRouteBySource finds an HTTPRoute in ns whose source-ingress annotation
// equals sourceVal ("namespace/name").
func findHTTPRouteBySource(ns, sourceVal string) (*gatewayv1.HTTPRoute, error) {
	var list gatewayv1.HTTPRouteList
	if err := k8sClient.List(context.Background(), &list, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].Annotations[annotationSourceIngress] == sourceVal {
			return &list.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no HTTPRoute with annotation %s=%q in namespace %q", annotationSourceIngress, sourceVal, ns)
}

func waitForHTTPRoute(t *testing.T, ns, sourceVal string) *gatewayv1.HTTPRoute {
	t.Helper()
	var route *gatewayv1.HTTPRoute
	eventually(t, func() error {
		r, err := findHTTPRouteBySource(ns, sourceVal)
		if err != nil {
			return err
		}
		route = r
		return nil
	}, defaultTimeout, defaultInterval)
	return route
}

func waitForHTTPRouteGone(t *testing.T, ns, sourceVal string) {
	t.Helper()
	eventually(t, func() error {
		_, err := findHTTPRouteBySource(ns, sourceVal)
		if err != nil {
			return nil // gone
		}
		return fmt.Errorf("HTTPRoute with source %q still exists in %q", sourceVal, ns)
	}, defaultTimeout, defaultInterval)
}

func assertNoHTTPRoute(t *testing.T, ns, sourceVal string) {
	t.Helper()
	neverOccurs(t, func() error {
		_, err := findHTTPRouteBySource(ns, sourceVal)
		if err == nil {
			return nil // route appeared → bad
		}
		return err
	}, negativeWait)
}

// ============================================================================
// GRPCRoute helpers
// ============================================================================

func findGRPCRouteBySource(ns, sourceVal string) (*gatewayv1.GRPCRoute, error) {
	var list gatewayv1.GRPCRouteList
	if err := k8sClient.List(context.Background(), &list, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].Annotations[annotationSourceIngress] == sourceVal {
			return &list.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no GRPCRoute with annotation %s=%q in namespace %q", annotationSourceIngress, sourceVal, ns)
}

func waitForGRPCRoute(t *testing.T, ns, sourceVal string) *gatewayv1.GRPCRoute {
	t.Helper()
	var route *gatewayv1.GRPCRoute
	eventually(t, func() error {
		r, err := findGRPCRouteBySource(ns, sourceVal)
		if err != nil {
			return err
		}
		route = r
		return nil
	}, defaultTimeout, defaultInterval)
	return route
}

// ============================================================================
// Ingress helpers
// ============================================================================

func getIngress(t testing.TB, ns, name string) *networkingv1.Ingress {
	t.Helper()
	ing := &networkingv1.Ingress{}
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, ing))
	return ing
}

func waitForFinalizer(t *testing.T, ns, name string) {
	t.Helper()
	eventually(t, func() error {
		ing := &networkingv1.Ingress{}
		if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, ing); err != nil {
			return err
		}
		for _, f := range ing.Finalizers {
			if f == finalizerName {
				return nil
			}
		}
		return fmt.Errorf("finalizer %q not on Ingress %s/%s", finalizerName, ns, name)
	}, defaultTimeout, defaultInterval)
}

func waitForFinalizerGone(t *testing.T, ns, name string) {
	t.Helper()
	eventually(t, func() error {
		ing := &networkingv1.Ingress{}
		if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, ing); apierrors.IsNotFound(err) {
			return nil // Ingress itself gone → finalizer definitely gone
		} else if err != nil {
			return err
		}
		for _, f := range ing.Finalizers {
			if f == finalizerName {
				return fmt.Errorf("finalizer %q still on Ingress %s/%s", finalizerName, ns, name)
			}
		}
		return nil
	}, defaultTimeout, defaultInterval)
}

func waitForIngressGone(t *testing.T, ns, name string) {
	t.Helper()
	eventually(t, func() error {
		err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, &networkingv1.Ingress{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("Ingress %s/%s still exists", ns, name)
	}, defaultTimeout, defaultInterval)
}

// patchIngressAnnotation patches a single annotation on an Ingress in-place.
func patchIngressAnnotation(t *testing.T, ns, name, key, value string) {
	t.Helper()
	ing := getIngress(t, ns, name)
	patch := client.MergeFrom(ing.DeepCopy())
	if ing.Annotations == nil {
		ing.Annotations = map[string]string{}
	}
	ing.Annotations[key] = value
	require.NoError(t, k8sClient.Patch(context.Background(), ing, patch),
		"patch ingress annotation %s=%s", key, value)
}

// patchIngressClass patches spec.ingressClassName.
func patchIngressClass(t *testing.T, ns, name, class string) {
	t.Helper()
	ing := getIngress(t, ns, name)
	patch := client.MergeFrom(ing.DeepCopy())
	ing.Spec.IngressClassName = &class
	require.NoError(t, k8sClient.Patch(context.Background(), ing, patch),
		"patch ingress class to %q", class)
}

// ============================================================================
// Assertion helpers
// ============================================================================

// assertGatewayOwnerRef verifies that gw.OwnerReferences contains a reference
// to ingress and that no ownerRef has Controller=true.
func assertGatewayOwnerRef(t *testing.T, gw *gatewayv1.Gateway, ing *networkingv1.Ingress) {
	t.Helper()
	found := false
	for _, ref := range gw.OwnerReferences {
		if ref.UID == ing.UID {
			found = true
		}
		// Controller field must be nil (not the controller owner)
		assert.Nil(t, ref.Controller,
			"expected no ownerRef on Gateway %s/%s to have Controller=true", gw.Namespace, gw.Name)
	}
	assert.True(t, found,
		"expected Ingress %s/%s (UID %s) to be an ownerRef of Gateway %s/%s",
		ing.Namespace, ing.Name, ing.UID, gw.Namespace, gw.Name)
}

// assertGatewayNotOwnedBy verifies that gw.OwnerReferences does NOT contain
// a reference to ingress.
func assertGatewayNotOwnedBy(t *testing.T, gw *gatewayv1.Gateway, ing *networkingv1.Ingress) {
	t.Helper()
	for _, ref := range gw.OwnerReferences {
		assert.NotEqual(t, ing.UID, ref.UID,
			"expected Ingress %s/%s NOT to be an ownerRef of Gateway %s/%s",
			ing.Namespace, ing.Name, gw.Namespace, gw.Name)
	}
}

// assertHTTPRouteParentRef verifies the first parentRef of the HTTPRoute.
func assertHTTPRouteParentRef(t *testing.T, route *gatewayv1.HTTPRoute, gwNs, gwName string, expectNoSectionName bool) {
	t.Helper()
	require.NotEmpty(t, route.Spec.ParentRefs, "HTTPRoute %s/%s has no parentRefs", route.Namespace, route.Name)
	ref := route.Spec.ParentRefs[0]
	assert.Equal(t, gatewayv1.ObjectName(gwName), ref.Name)
	if ref.Namespace != nil {
		assert.Equal(t, gatewayv1.Namespace(gwNs), *ref.Namespace)
	}
	if expectNoSectionName {
		assert.Nil(t, ref.SectionName, "expected no SectionName for external gateway parentRef")
	}
}

// assertSourceAnnotations verifies the tracking annotations on a k8s object.
func assertSourceAnnotations(t *testing.T, ann map[string]string, sourceVal, provider string) {
	t.Helper()
	assert.Equal(t, sourceVal, ann[annotationSourceIngress],
		"source-ingress annotation mismatch")
	assert.Equal(t, provider, ann[annotationSourceProvider],
		"source-provider annotation mismatch")
	assert.NotEmpty(t, ann["ingress2gateway.io/converted-at"],
		"converted-at annotation should be present")
}

// assertNoFinalizer asserts that the Ingress has no i2g finalizer.
func assertNoFinalizer(t *testing.T, ns, name string) {
	t.Helper()
	ing := getIngress(t, ns, name)
	for _, f := range ing.Finalizers {
		assert.NotEqual(t, finalizerName, f,
			"expected no finalizer %q on Ingress %s/%s", finalizerName, ns, name)
	}
}

// ============================================================================
// Events helpers
// ============================================================================

// listEventsByReason lists core/v1 Events in ns with the given reason,
// optionally filtered to events about objName.
func listEventsByReason(ns, reason, objName string) ([]corev1.Event, error) {
	var list corev1.EventList
	if err := k8sClient.List(context.Background(), &list, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	var result []corev1.Event
	for _, ev := range list.Items {
		if ev.Reason != reason {
			continue
		}
		if objName != "" && ev.InvolvedObject.Name != objName {
			continue
		}
		result = append(result, ev)
	}
	return result, nil
}

// waitForEvent polls until at least one Event with the given reason (and
// optional type Normal/Warning) appears for objName in ns.
func waitForEvent(t *testing.T, ns, reason, objName, eventType string) {
	t.Helper()
	eventually(t, func() error {
		evs, err := listEventsByReason(ns, reason, objName)
		if err != nil {
			return err
		}
		for _, ev := range evs {
			if eventType == "" || ev.Type == eventType {
				return nil
			}
		}
		return fmt.Errorf("no %q event with reason %q for object %q in ns %q", eventType, reason, objName, ns)
	}, defaultTimeout, defaultInterval)
}

// countEvents returns how many Event objects with the given reason exist for objName in ns.
func countEvents(t testing.TB, ns, reason, objName string) int {
	t.Helper()
	evs, err := listEventsByReason(ns, reason, objName)
	require.NoError(t, err)
	return len(evs)
}

// eventSeriesCount sums Event.Count (aggregator increments Count instead of creating new objects).
func eventSeriesCount(t testing.TB, ns, reason, objName string) int32 {
	t.Helper()
	evs, err := listEventsByReason(ns, reason, objName)
	require.NoError(t, err)
	var n int32
	for _, ev := range evs {
		c := ev.Count
		if c == 0 {
			c = 1
		}
		n += c
	}
	return n
}

// ============================================================================
// Controller log helpers
// ============================================================================

// controllerPodLogs returns the full logs of all controller pods in helmNamespace.
func controllerPodLogs(t testing.TB) string {
	t.Helper()
	out, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"logs", "-n", helmNamespace,
		"-l", "app=ingress2gateway-controller",
		"--tail=-1")
	if err != nil {
		t.Logf("warning: failed to get controller logs: %v", err)
		return ""
	}
	return out
}

// controllerPodLogsInNamespace returns logs from a controller in a given namespace.
func controllerPodLogsInNamespace(t testing.TB, ns string) string {
	t.Helper()
	out, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"logs", "-n", ns,
		"-l", "app=ingress2gateway-controller",
		"--tail=-1", "--ignore-errors=true")
	if err != nil {
		t.Logf("warning: failed to get controller logs in %q: %v", ns, err)
	}
	return out
}

// waitForPodCrashLoop polls until at least one pod in ns with label
// app=ingress2gateway-controller is in CrashLoopBackOff or has exit code != 0.
func waitForPodCrashLoop(t *testing.T, ns string) {
	t.Helper()
	eventually(t, func() error {
		var podList corev1.PodList
		if err := k8sClient.List(context.Background(), &podList,
			client.InNamespace(ns),
			client.MatchingLabels{"app": "ingress2gateway-controller"}); err != nil {
			return err
		}
		for _, pod := range podList.Items {
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil &&
					(cs.State.Waiting.Reason == "CrashLoopBackOff" ||
						cs.State.Waiting.Reason == "Error") {
					return nil
				}
				if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
					return nil
				}
				if cs.RestartCount > 0 {
					return nil // at least one restart → it crashed
				}
			}
		}
		return fmt.Errorf("no crashing controller pod found in ns %q (pods: %d)", ns, len(podList.Items))
	}, 90*time.Second, 2*time.Second)
}

// crashedPodLogs attempts to retrieve logs from a crashed/previous container.
func crashedPodLogs(t testing.TB, ns string) string {
	t.Helper()
	out, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"logs", "-n", ns,
		"-l", "app=ingress2gateway-controller",
		"--previous", "--ignore-errors=true")
	if err == nil && strings.TrimSpace(out) != "" && !strings.Contains(out, "unable to retrieve") && !strings.Contains(out, "not found") {
		return out
	}
	cur := controllerPodLogsInNamespace(t, ns)
	desc, _ := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"describe", "pods", "-n", ns, "-l", "app=ingress2gateway-controller")
	term, _ := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"get", "pods", "-n", ns, "-l", "app=ingress2gateway-controller",
		"-o", "jsonpath={range .items[*]}{.status.containerStatuses[*].lastState.terminated.message}{'\\n'}{.status.containerStatuses[*].state.terminated.message}{'\\n'}{.status.containerStatuses[*].state.waiting.message}{end}")
	return strings.Join([]string{out, cur, term, desc}, "\n")
}

// ============================================================================
// Scale helpers
// ============================================================================

func scaleDeployment(t testing.TB, ns, name string, replicas int32) {
	t.Helper()
	_, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"scale", "deployment/"+name,
		"-n", ns,
		fmt.Sprintf("--replicas=%d", replicas))
	require.NoError(t, err, "scale deployment %s/%s to %d", ns, name, replicas)
}

func waitForDeploymentReplicas(t *testing.T, ns, name string, replicas int32) {
	t.Helper()
	eventually(t, func() error {
		out, err := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
			"get", "deployment/"+name, "-n", ns,
			"-o", "jsonpath={.status.readyReplicas}")
		if err != nil {
			return err
		}
		actual := strings.TrimSpace(out)
		if replicas == 0 {
			if actual == "" || actual == "0" {
				return nil
			}
			return fmt.Errorf("deployment %s/%s has %q readyReplicas, want 0", ns, name, actual)
		}
		if actual == fmt.Sprintf("%d", replicas) {
			return nil
		}
		return fmt.Errorf("deployment %s/%s has %q readyReplicas, want %d", ns, name, actual, replicas)
	}, 2*time.Minute, 2*time.Second)
}

// ============================================================================
// Diagnostics
// ============================================================================

// dumpArtifacts writes controller logs and a resource dump into artifactsDir
// when t has failed. Call via `defer dumpArtifacts(t)` at the start of each
// scenario so it runs before t.Cleanup namespace deletions.
func dumpArtifacts(t *testing.T) {
	if !t.Failed() {
		return
	}
	t.Helper()
	dir := filepath.Join(artifactsDir, sanitizeName(t.Name()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("warning: failed to create artifact dir %q: %v", dir, err)
		return
	}
	allLogs, _ := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"logs", "-A", "-l", "app=ingress2gateway-controller",
		"--tail=-1", "--ignore-errors=true")
	if allLogs != "" {
		_ = os.WriteFile(filepath.Join(dir, "controller.log"), []byte(allLogs), 0o644)
	}
	prevLogs, _ := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"logs", "-A", "-l", "app=ingress2gateway-controller",
		"--previous", "--ignore-errors=true")
	if strings.TrimSpace(prevLogs) != "" {
		_ = os.WriteFile(filepath.Join(dir, "controller-previous.log"), []byte(prevLogs), 0o644)
	}
	out, _ := runCmd("kubectl", "--kubeconfig", kubeconfigPath,
		"get", "ingress,gateway,httproute,grpcroute,events",
		"-A", "-o", "yaml")
	_ = os.WriteFile(filepath.Join(dir, "resources.yaml"), []byte(out), 0o644)
	vals, _ := runCmd("helm", "get", "values", helmReleaseName, "--kubeconfig", kubeconfigPath, "--namespace", helmNamespace, "--all")
	_ = os.WriteFile(filepath.Join(dir, "helm-values.yaml"), []byte(vals), 0o644)
	t.Logf("artifacts written to %s", dir)
}

// ============================================================================
// Misc utilities
// ============================================================================

func randSuffix(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func sanitizeName(s string) string {
	return strings.NewReplacer("/", "_", " ", "_", ":", "-").Replace(s)
}
