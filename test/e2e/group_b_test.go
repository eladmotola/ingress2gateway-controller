//go:build e2e

package e2e

// ---------------------------------------------------------------------------
// Group B: ingressclass-mode (ingressSelectionMode=ingressclass)
//
// Helm values: testdata/values-b.yaml
//   ingressSelectionMode:           ingressclass
//   defaultProvider:                ingress-nginx
//   ingressClassToGatewayClassMapping:
//     nginx-test: test-gc
// ---------------------------------------------------------------------------

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func groupB(t *testing.T) {
	t.Run("B7_MappedClassNoAnnotation", testB7MappedClassNoAnnotation)
	t.Run("B8_LegacyIngressClassAnnotation", testB8LegacyIngressClassAnnotation)
	t.Run("B9a_MappedClassExplicitFalse", testB9aMappedClassExplicitFalse)
	t.Run("B9b_UnmappedClassWithEnabledTrue", testB9bUnmappedClassWithEnabledTrue)
	t.Run("B10_UnmappedClassNoAnnotation", testB10UnmappedClassNoAnnotation)
	t.Run("B11_ClassChangeMappedToUnmapped", testB11ClassChange)
}

// B7: Mapped class, no enabled annotation → converted (ingressclass mode selects it).
func testB7MappedClassNoAnnotation(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "b7")
	createService(t, ns, "my-svc", 80)
	ing := createIngress(t, ns, "my-ingress", "nginx-test",
		nil, // no enabled annotation
		simpleIngressRule("b7.example.com", "my-svc", 80),
	)
	sourceVal := fmt.Sprintf("%s/my-ingress", ns)

	gw := waitForGateway(t, ns, "nginx-test")
	assert.Equal(t, "test-gc", string(gw.Spec.GatewayClassName))
	assertGatewayOwnerRef(t, gw, ing)

	route := waitForHTTPRoute(t, ns, sourceVal)
	assertSourceAnnotations(t, route.Annotations, sourceVal, "ingress-nginx")

	waitForFinalizer(t, ns, "my-ingress")
}

// B8: Legacy kubernetes.io/ingress.class annotation (no spec.ingressClassName)
// with a mapped class → controller reads the annotation and converts.
func testB8LegacyIngressClassAnnotation(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "b8")
	createService(t, ns, "my-svc", 80)

	// Use class="" to NOT set spec.ingressClassName, but set the legacy annotation
	ing := createIngress(t, ns, "my-ingress", "", /*no spec.ingressClassName*/
		map[string]string{
			"kubernetes.io/ingress.class": "nginx-test",
		},
		simpleIngressRule("b8.example.com", "my-svc", 80),
	)
	sourceVal := fmt.Sprintf("%s/my-ingress", ns)

	gw := waitForGateway(t, ns, "nginx-test")
	assert.Equal(t, "test-gc", string(gw.Spec.GatewayClassName))
	assertGatewayOwnerRef(t, gw, ing)

	waitForHTTPRoute(t, ns, sourceVal)
	waitForFinalizer(t, ns, "my-ingress")
}

// B9a: Mapped class + explicitly enabled=false → skipped; no resources, no finalizer.
func testB9aMappedClassExplicitFalse(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "b9a")
	createService(t, ns, "my-svc", 80)
	createIngress(t, ns, "my-ingress", "nginx-test",
		map[string]string{"ingress2gateway.io/enabled": "false"},
		simpleIngressRule("b9a.example.com", "my-svc", 80),
	)

	assertNoGateway(t, ns, "nginx-test")
	assertNoHTTPRoute(t, ns, fmt.Sprintf("%s/my-ingress", ns))
	assertNoFinalizer(t, ns, "my-ingress")
}

// B9b: Unmapped class + enabled=true → Warning event reason UnmappedIngressClass;
// no resources, no finalizer. Event emitted once (not repeatedly).
func testB9bUnmappedClassWithEnabledTrue(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "b9b")
	createService(t, ns, "my-svc", 80)
	createIngress(t, ns, "my-ingress", "not-in-mapping",
		map[string]string{"ingress2gateway.io/enabled": "true"},
		simpleIngressRule("b9b.example.com", "my-svc", 80),
	)

	// Warning event must appear
	waitForEvent(t, ns, "UnmappedIngressClass", "my-ingress", "Warning")

	// No resources should have been created
	assertNoGateway(t, ns, "not-in-mapping")
	assertNoHTTPRoute(t, ns, fmt.Sprintf("%s/my-ingress", ns))
	assertNoFinalizer(t, ns, "my-ingress")

	initialCount := eventSeriesCount(t, ns, "UnmappedIngressClass", "my-ingress")
	require.Equal(t, int32(1), initialCount, "UnmappedIngressClass should appear once")
	neverOccurs(t, func() error {
		if eventSeriesCount(t, ns, "UnmappedIngressClass", "my-ingress") > initialCount {
			return nil
		}
		return fmt.Errorf("count still %d", initialCount)
	}, 8*time.Second)
}

// B10: Unmapped class, no annotation → untouched, no finalizer.
func testB10UnmappedClassNoAnnotation(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "b10")
	createService(t, ns, "my-svc", 80)
	createIngress(t, ns, "my-ingress", "unmapped-class",
		nil,
		simpleIngressRule("b10.example.com", "my-svc", 80),
	)

	assertNoGateway(t, ns, "unmapped-class")
	assertNoHTTPRoute(t, ns, fmt.Sprintf("%s/my-ingress", ns))
	assertNoFinalizer(t, ns, "my-ingress")
}

// B11: Change Ingress class from mapped to unmapped → resources cleaned up,
// finalizer removed; Ingress itself is not deleted.
func testB11ClassChange(t *testing.T) {
	defer dumpArtifacts(t)

	ns := createNamespace(t, "b11")
	createService(t, ns, "my-svc", 80)
	createIngress(t, ns, "my-ingress", "nginx-test",
		nil,
		simpleIngressRule("b11.example.com", "my-svc", 80),
	)
	sourceVal := fmt.Sprintf("%s/my-ingress", ns)

	// Wait for initial conversion
	waitForGateway(t, ns, "nginx-test")
	waitForHTTPRoute(t, ns, sourceVal)
	waitForFinalizer(t, ns, "my-ingress")

	// Change class to unmapped
	patchIngressClass(t, ns, "my-ingress", "unmapped-class")

	// Resources should be cleaned up and finalizer removed
	waitForGatewayGone(t, ns, "nginx-test")
	waitForHTTPRouteGone(t, ns, sourceVal)
	waitForFinalizerGone(t, ns, "my-ingress")

	ing := getIngress(t, ns, "my-ingress")
	require.NotNil(t, ing, "Ingress itself should still exist after class change")
}
