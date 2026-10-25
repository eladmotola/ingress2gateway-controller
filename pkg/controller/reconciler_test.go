package controller

import (
	"context"
	"testing"

	"time"

	"github.com/eladmotola/ingress2gateway-controller/pkg/config"
	"github.com/eladmotola/ingress2gateway-controller/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func stringPtr(s string) *string {
	return &s
}

func setupTestReconciler(t *testing.T, initObjs ...client.Object) (*IngressReconciler, client.Client, *record.FakeRecorder) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initObjs...).
		Build()

	fakeRecorder := record.NewFakeRecorder(100)
	conv := converter.New(fakeClient, scheme)

	reconciler := &IngressReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Recorder:        fakeRecorder,
		Converter:       conv,
		DefaultProvider: "ingress-nginx",
		DefaultEmitter:  "standard",
		IngressClassToGatewayClassMapping: map[string]string{
			"nginx": "nginx",
		},
	}

	return reconciler, fakeClient, fakeRecorder
}

func TestReconcileTLSRoute(t *testing.T) {
	ctx := context.Background()
	pathTypePrefix := networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tls-passthrough",
			Namespace: "default",
			Annotations: map[string]string{
				config.AnnotationEnabled:                      "true",
				"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("nginx"),
			Rules: []networkingv1.IngressRule{
				{
					Host: "app.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "app",
											Port: networkingv1.ServiceBackendPort{
												Number: 443,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	reconciler, fakeClient, fakeRecorder := setupTestReconciler(t, ingress)

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "tls-passthrough",
		},
	}

	// 1. Reconcile Ingress
	res, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	// 2. Verify Gateway was created
	gw := &gatewayv1.Gateway{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nginx"}, gw)
	require.NoError(t, err)
	assert.Equal(t, gatewayv1.ObjectName("nginx"), gw.Spec.GatewayClassName)
	require.Len(t, gw.Spec.Listeners, 2)

	// 3. Verify TLSRoute was created
	tlsRoute := &gatewayv1.TLSRoute{}
	err = fakeClient.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      "tls-passthrough-app-example-com-tls-passthrough",
	}, tlsRoute)
	require.NoError(t, err)
	assert.Equal(t, gatewayv1.Hostname("app.example.com"), tlsRoute.Spec.Hostnames[0])
	require.Len(t, tlsRoute.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("nginx"), tlsRoute.Spec.ParentRefs[0].Name)
	assert.Equal(t, "default/tls-passthrough", tlsRoute.Annotations[config.AnnotationSourceIngress])

	// 4. Verify warning events were recorded
	var events []string
	for len(fakeRecorder.Events) > 0 {
		events = append(events, <-fakeRecorder.Events)
	}
	assert.NotEmpty(t, events, "Expected warning events to be emitted to the recorder")
	foundWarning := false
	for _, e := range events {
		if assert.ObjectsAreEqual(true, len(e) > 0) {
			foundWarning = true
			break
		}
	}
	assert.True(t, foundWarning)

	// 5. Test deletion cleanup
	// Delete the ingress via client so deletion timestamp is set with finalizer
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "tls-passthrough"}, ingress)
	require.NoError(t, err)
	err = fakeClient.Delete(ctx, ingress)
	require.NoError(t, err)

	res, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	// TLSRoute should now be deleted
	err = fakeClient.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      "tls-passthrough-app-example-com-tls-passthrough",
	}, tlsRoute)
	assert.True(t, err != nil, "TLSRoute should be deleted after Ingress deletion")
}

func TestReconcileBackendTLSPolicy(t *testing.T) {
	ctx := context.Background()
	pathTypePrefix := networkingv1.PathTypePrefix

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-ca",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt":  []byte("dummy-ca"),
			"tls.crt": []byte("dummy-cert"),
			"tls.key": []byte("dummy-key"),
		},
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-tls",
			Namespace: "default",
			Annotations: map[string]string{
				config.AnnotationEnabled:                            "true",
				"nginx.ingress.kubernetes.io/backend-protocol":      "HTTPS",
				"nginx.ingress.kubernetes.io/proxy-ssl-verify":      "on",
				"nginx.ingress.kubernetes.io/proxy-ssl-secret":      "default/backend-ca",
				"nginx.ingress.kubernetes.io/proxy-ssl-name":        "backend.example.com",
				"nginx.ingress.kubernetes.io/proxy-ssl-server-name": "on",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("nginx"),
			Rules: []networkingv1.IngressRule{
				{
					Host: "app.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "backend",
											Port: networkingv1.ServiceBackendPort{
												Number: 443,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	reconciler, fakeClient, fakeRecorder := setupTestReconciler(t, ingress, secret)

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "backend-tls",
		},
	}

	// 1. Reconcile Ingress
	res, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	// 2. Verify Gateway was created
	gw := &gatewayv1.Gateway{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nginx"}, gw)
	require.NoError(t, err)

	// 3. Verify HTTPRoute was created
	httpRoute := &gatewayv1.HTTPRoute{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "backend-tls-app-example-com"}, httpRoute)
	require.NoError(t, err)

	// 4. Verify BackendTLSPolicy was created
	policy := &gatewayv1.BackendTLSPolicy{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "backend-backend-tls"}, policy)
	require.NoError(t, err)
	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("backend"), policy.Spec.TargetRefs[0].Name)
	assert.Equal(t, gatewayv1.PreciseHostname("backend.example.com"), policy.Spec.Validation.Hostname)
	assert.Equal(t, "default/backend-tls", policy.Annotations[config.AnnotationSourceIngress])

	// 5. Verify events were emitted
	assert.NotEmpty(t, fakeRecorder.Events)

	// 6. Test deletion cleanup
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "backend-tls"}, ingress)
	require.NoError(t, err)
	err = fakeClient.Delete(ctx, ingress)
	require.NoError(t, err)

	res, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	// BackendTLSPolicy should be deleted
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "backend-backend-tls"}, policy)
	assert.True(t, err != nil, "BackendTLSPolicy should be deleted after Ingress deletion")
}

func TestDeleteOrphanRoutes_NewKinds(t *testing.T) {
	ctx := context.Background()

	tlsRoute1 := &gatewayv1.TLSRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tls-route-1",
			Namespace: "default",
			Annotations: map[string]string{
				config.AnnotationSourceIngress: "default/my-ingress",
			},
		},
	}
	tlsRouteOther := &gatewayv1.TLSRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tls-route-other",
			Namespace: "default",
			Annotations: map[string]string{
				config.AnnotationSourceIngress: "default/other-ingress",
			},
		},
	}
	policy1 := &gatewayv1.BackendTLSPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "policy-1",
			Namespace: "default",
			Annotations: map[string]string{
				config.AnnotationSourceIngress: "default/my-ingress",
			},
		},
	}

	reconciler, fakeClient, _ := setupTestReconciler(t, tlsRoute1, tlsRouteOther, policy1)

	err := reconciler.deleteOrphanRoutes(ctx, "default", "default/my-ingress")
	require.NoError(t, err)

	// tlsRoute1 and policy1 should be deleted
	checkTLSRoute := &gatewayv1.TLSRoute{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "tls-route-1"}, checkTLSRoute)
	assert.Error(t, err, "tls-route-1 should have been deleted")

	checkPolicy := &gatewayv1.BackendTLSPolicy{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "policy-1"}, checkPolicy)
	assert.Error(t, err, "policy-1 should have been deleted")

	// tlsRouteOther should remain
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "tls-route-other"}, checkTLSRoute)
	assert.NoError(t, err, "tls-route-other must not be deleted")
}

func TestReconciler_DeduplicateNotificationEvents(t *testing.T) {
	ctx := context.Background()
	pathTypePrefix := networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dedup-test",
			Namespace: "default",
			Annotations: map[string]string{
				config.AnnotationEnabled:                      "true",
				"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("nginx"),
			Rules: []networkingv1.IngressRule{
				{
					Host: "app.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "app",
											Port: networkingv1.ServiceBackendPort{Number: 443},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	reconciler, _, fakeRecorder := setupTestReconciler(t, ingress)
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "dedup-test",
		},
	}

	// 1. First reconcile emits events
	_, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	eventCount1 := len(fakeRecorder.Events)
	assert.Greater(t, eventCount1, 0, "First reconcile should emit events")

	// Drain events
	for len(fakeRecorder.Events) > 0 {
		<-fakeRecorder.Events
	}

	// 2. Second reconcile (periodic resync, unchanged report) should not emit duplicate events
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 0, len(fakeRecorder.Events), "Periodic resync should deduplicate and not re-emit duplicate events")
}

func TestFilterAnnotations(t *testing.T) {
	assert.Nil(t, filterAnnotations(nil))

	onlyTimestamp := map[string]string{
		config.AnnotationConvertedAt: "2026-10-18T00:00:00Z",
	}
	assert.Nil(t, filterAnnotations(onlyTimestamp))

	mixed := map[string]string{
		config.AnnotationConvertedAt: "2026-10-18T00:00:00Z",
		"other-key":                  "value",
	}
	filtered := filterAnnotations(mixed)
	assert.Equal(t, map[string]string{"other-key": "value"}, filtered)
}

func TestIsResourceEqual_And_IsObjectEqual(t *testing.T) {
	reconciler, _, _ := setupTestReconciler(t)

	meta1 := metav1.ObjectMeta{
		Name:      "gw",
		Namespace: "default",
		Labels:    map[string]string{"env": "test"},
		Annotations: map[string]string{
			config.AnnotationConvertedAt: "time-1",
			"custom":                     "val",
		},
	}
	meta2 := metav1.ObjectMeta{
		Name:      "gw",
		Namespace: "default",
		Labels:    map[string]string{"env": "test"},
		Annotations: map[string]string{
			config.AnnotationConvertedAt: "time-2",
			"custom":                     "val",
		},
	}

	spec1 := gatewayv1.GatewaySpec{GatewayClassName: "nginx"}
	spec2 := gatewayv1.GatewaySpec{GatewayClassName: "nginx"}
	specDiff := gatewayv1.GatewaySpec{GatewayClassName: "other"}

	// Equals ignoring timestamp
	assert.True(t, reconciler.isResourceEqual(meta1, meta2, spec1, spec2))

	// Spec differs
	assert.False(t, reconciler.isResourceEqual(meta1, meta2, spec1, specDiff))

	// Labels differ
	metaDiffLabels := meta2
	metaDiffLabels.Labels = map[string]string{"env": "prod"}
	assert.False(t, reconciler.isResourceEqual(meta1, metaDiffLabels, spec1, spec2))

	// OwnerReferences differ
	metaDiffOwner := meta2
	metaDiffOwner.OwnerReferences = []metav1.OwnerReference{{Name: "owner"}}
	assert.False(t, reconciler.isResourceEqual(meta1, metaDiffOwner, spec1, spec2))

	// Test isObjectEqual
	obj1 := &gatewayv1.Gateway{ObjectMeta: meta1, Spec: spec1}
	obj2 := &gatewayv1.Gateway{ObjectMeta: meta2, Spec: spec2}
	assert.True(t, reconciler.isObjectEqual(obj1, obj2, obj1.Spec, obj2.Spec))
}

func TestCreateOrUpdateGateway_Conflict(t *testing.T) {
	ctx := context.Background()
	existingGw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "nginx",
		},
	}
	reconciler, _, _ := setupTestReconciler(t, existingGw)

	owner := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ing",
			Namespace: "default",
			UID:       "123",
		},
	}

	conflictingGw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "conflicting-class",
		},
	}

	err := reconciler.createOrUpdateGateway(ctx, conflictingGw, owner)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conflict")
}

func TestRemoveOwnerReference(t *testing.T) {
	ctx := context.Background()
	owner := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			UID: "my-uid",
		},
	}

	t.Run("owner not found", func(t *testing.T) {
		reconciler, _, _ := setupTestReconciler(t)
		obj := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "route",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{UID: "other-uid"},
				},
			},
		}
		err := reconciler.removeOwnerReference(ctx, obj, owner)
		assert.NoError(t, err)
	})

	t.Run("last owner deletes object", func(t *testing.T) {
		obj := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "route",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{UID: "my-uid"},
				},
			},
		}
		reconciler, fakeClient, _ := setupTestReconciler(t, obj)
		err := reconciler.removeOwnerReference(ctx, obj, owner)
		assert.NoError(t, err)

		getErr := fakeClient.Get(ctx, client.ObjectKeyFromObject(obj), &gatewayv1.HTTPRoute{})
		assert.True(t, apierrors.IsNotFound(getErr))
	})

	t.Run("multiple owners updates object", func(t *testing.T) {
		obj := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "route",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{UID: "my-uid"},
					{UID: "keep-uid"},
				},
			},
		}
		reconciler, fakeClient, _ := setupTestReconciler(t, obj)
		err := reconciler.removeOwnerReference(ctx, obj, owner)
		assert.NoError(t, err)

		updated := &gatewayv1.HTTPRoute{}
		err = fakeClient.Get(ctx, client.ObjectKeyFromObject(obj), updated)
		assert.NoError(t, err)
		assert.Len(t, updated.OwnerReferences, 1)
		assert.Equal(t, types.UID("keep-uid"), updated.OwnerReferences[0].UID)
	})
}

func TestReconcile_ExternalGateway(t *testing.T) {
	ctx := context.Background()
	pathTypePrefix := networkingv1.PathTypePrefix

	extGW := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-external-gw",
			Namespace:       "infra-ns",
			ResourceVersion: "100",
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "pre-existing-class",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Port:     80,
					Protocol: gatewayv1.HTTPProtocolType,
				},
			},
		},
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-ingress",
			Namespace: "default",
			Annotations: map[string]string{
				config.AnnotationEnabled: "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("nginx-external"),
			Rules: []networkingv1.IngressRule{
				{
					Host: "app.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "app-svc",
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	reconciler, fakeClient, fakeRecorder := setupTestReconciler(t, extGW, ingress)
	reconciler.IngressClassToExternalGatewayMapping = map[string]config.ExternalGateway{
		"nginx-external": {
			Name:      "my-external-gw",
			Namespace: "infra-ns",
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "app-ingress",
		},
	}

	// 1. Reconcile Ingress targeting external Gateway
	res, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	// 2. Verify external Gateway was NOT created and NOT updated
	checkGW := &gatewayv1.Gateway{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "infra-ns", Name: "my-external-gw"}, checkGW)
	require.NoError(t, err)
	assert.Equal(t, "100", checkGW.ResourceVersion, "External Gateway must not be updated")
	assert.Equal(t, gatewayv1.ObjectName("pre-existing-class"), checkGW.Spec.GatewayClassName)
	assert.Empty(t, checkGW.OwnerReferences, "External Gateway must have NO owner references")

	// 3. Verify HTTPRoute was created and points to external Gateway with no sectionName
	routeList := &gatewayv1.HTTPRouteList{}
	err = fakeClient.List(ctx, routeList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, routeList.Items, 1)
	route := routeList.Items[0]
	require.Len(t, route.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("my-external-gw"), route.Spec.ParentRefs[0].Name)
	assert.Equal(t, gatewayv1.Namespace("infra-ns"), *route.Spec.ParentRefs[0].Namespace)
	assert.Nil(t, route.Spec.ParentRefs[0].SectionName, "SectionName must be nil for external Gateway")

	// 4. Verify Normal Event with reason ExternalGateway was emitted
	var events []string
	for len(fakeRecorder.Events) > 0 {
		events = append(events, <-fakeRecorder.Events)
	}
	require.NotEmpty(t, events)
	foundNormalEvent := false
	for _, e := range events {
		if assert.ObjectsAreEqual(true, len(e) > 0) && assert.ObjectsAreEqual(true, stringContains(e, "ExternalGateway")) {
			foundNormalEvent = true
			assert.Contains(t, e, "Gateway infra-ns/my-external-gw is listed in externalGateways; not creating or updating it")
			break
		}
	}
	assert.True(t, foundNormalEvent, "Expected Normal Event with reason ExternalGateway")

	// 5. Test Ingress deletion: Gateway must be untouched while routes are cleaned up
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "app-ingress"}, ingress)
	require.NoError(t, err)
	err = fakeClient.Delete(ctx, ingress)
	require.NoError(t, err)

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	// Routes deleted
	err = fakeClient.List(ctx, routeList, client.InNamespace("default"))
	require.NoError(t, err)
	assert.Empty(t, routeList.Items, "HTTPRoutes should be deleted on Ingress deletion")

	// External Gateway still intact and untouched
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "infra-ns", Name: "my-external-gw"}, checkGW)
	require.NoError(t, err)
	assert.Equal(t, "100", checkGW.ResourceVersion)
	assert.Empty(t, checkGW.OwnerReferences)
}

func TestReconcile_ExternalGateway_NotFound(t *testing.T) {
	ctx := context.Background()
	pathTypePrefix := networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-ingress",
			Namespace: "default",
			Annotations: map[string]string{
				config.AnnotationEnabled: "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("nginx-external"),
			Rules: []networkingv1.IngressRule{
				{
					Host: "app.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "app-svc",
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Gateway does NOT exist in fake client
	reconciler, fakeClient, fakeRecorder := setupTestReconciler(t, ingress)
	reconciler.IngressClassToExternalGatewayMapping = map[string]config.ExternalGateway{
		"nginx-external": {
			Name:      "missing-ext-gw",
			Namespace: "infra-ns",
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "app-ingress",
		},
	}

	res, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err, "Missing external Gateway must not fail reconcile")
	assert.Equal(t, 30*time.Second, res.RequeueAfter, "Reconcile should requeue after 30s when external Gateway not found")

	// Routes are still created!
	routeList := &gatewayv1.HTTPRouteList{}
	err = fakeClient.List(ctx, routeList, client.InNamespace("default"))
	require.NoError(t, err)
	assert.Len(t, routeList.Items, 1)

	// Warning Event emitted
	var events []string
	for len(fakeRecorder.Events) > 0 {
		events = append(events, <-fakeRecorder.Events)
	}
	foundWarning := false
	for _, e := range events {
		if stringContains(e, "ExternalGatewayNotFound") {
			foundWarning = true
			break
		}
	}
	assert.True(t, foundWarning, "Expected Warning Event with reason ExternalGatewayNotFound")

	// Reconcile again: verify ExternalGatewayNotFound warning is deduplicated
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Empty(t, fakeRecorder.Events, "Warning event should be deduplicated on subsequent reconciles")
}

func TestCleanupManagedResources_ExternalGatewayNeverTouched(t *testing.T) {
	ctx := context.Background()
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-ingress",
			Namespace: "default",
			UID:       types.UID("test-ingress-uid"),
		},
	}

	extGW := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ext-gw",
			Namespace: "infra-ns",
			OwnerReferences: []metav1.OwnerReference{
				{
					UID:  ingress.UID,
					Name: ingress.Name,
				},
			},
		},
	}

	reconciler, fakeClient, _ := setupTestReconciler(t, extGW, ingress)
	reconciler.IngressClassToExternalGatewayMapping = map[string]config.ExternalGateway{
		"nginx-external": {
			Name:      "ext-gw",
			Namespace: "infra-ns",
		},
	}

	err := reconciler.cleanupManagedResources(ctx, ingress)
	require.NoError(t, err)

	checkGW := &gatewayv1.Gateway{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "infra-ns", Name: "ext-gw"}, checkGW)
	require.NoError(t, err, "External Gateway must not be deleted or modified by cleanupManagedResources")
}

func TestReconcile_IngressSelectionMode(t *testing.T) {
	ctx := context.Background()
	pathTypePrefix := networkingv1.PathTypePrefix

	makeIngress := func(name, class, enabledAnn string) *networkingv1.Ingress {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   "default",
				Annotations: map[string]string{},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{
					{
						Host: name + ".example.com",
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: &pathTypePrefix,
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: "svc",
												Port: networkingv1.ServiceBackendPort{Number: 80},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		if class != "" {
			ing.Spec.IngressClassName = &class
		}
		if enabledAnn != "" {
			ing.Annotations[config.AnnotationEnabled] = enabledAnn
		}
		return ing
	}

	t.Run("ingressclass mode selects matching class without enabled annotation", func(t *testing.T) {
		ing := makeIngress("ing-selected", "nginx", "")
		reconciler, fakeClient, _ := setupTestReconciler(t, ing)
		reconciler.IngressSelectionMode = config.IngressSelectionModeIngressClass

		req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "ing-selected"}}
		_, err := reconciler.Reconcile(ctx, req)
		require.NoError(t, err)

		checkIng := &networkingv1.Ingress{}
		err = fakeClient.Get(ctx, req.NamespacedName, checkIng)
		require.NoError(t, err)
		assert.True(t, controllerutil.ContainsFinalizer(checkIng, finalizerName), "Selected ingress must receive finalizer")
	})

	t.Run("ingressclass mode skips matching class with enabled=false", func(t *testing.T) {
		ing := makeIngress("ing-optout", "nginx", "false")
		reconciler, fakeClient, _ := setupTestReconciler(t, ing)
		reconciler.IngressSelectionMode = config.IngressSelectionModeIngressClass

		req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "ing-optout"}}
		_, err := reconciler.Reconcile(ctx, req)
		require.NoError(t, err)

		checkIng := &networkingv1.Ingress{}
		err = fakeClient.Get(ctx, req.NamespacedName, checkIng)
		require.NoError(t, err)
		assert.False(t, controllerutil.ContainsFinalizer(checkIng, finalizerName), "Opted-out ingress must not receive finalizer")
	})

	t.Run("ingressclass mode skips unmapped class without enabled annotation", func(t *testing.T) {
		ing := makeIngress("ing-unmapped", "unmapped-class", "")
		reconciler, fakeClient, _ := setupTestReconciler(t, ing)
		reconciler.IngressSelectionMode = config.IngressSelectionModeIngressClass

		req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "ing-unmapped"}}
		_, err := reconciler.Reconcile(ctx, req)
		require.NoError(t, err)

		checkIng := &networkingv1.Ingress{}
		err = fakeClient.Get(ctx, req.NamespacedName, checkIng)
		require.NoError(t, err)
		assert.False(t, controllerutil.ContainsFinalizer(checkIng, finalizerName), "Unselected ingress must not receive finalizer")
	})

	t.Run("ingressclass mode skips unmapped class even with enabled=true", func(t *testing.T) {
		ing := makeIngress("ing-unmapped-enabled", "unmapped-class", "true")
		reconciler, fakeClient, fakeRecorder := setupTestReconciler(t, ing)
		reconciler.IngressSelectionMode = config.IngressSelectionModeIngressClass

		req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "ing-unmapped-enabled"}}
		_, err := reconciler.Reconcile(ctx, req)
		require.NoError(t, err)

		checkIng := &networkingv1.Ingress{}
		err = fakeClient.Get(ctx, req.NamespacedName, checkIng)
		require.NoError(t, err)
		assert.False(t, controllerutil.ContainsFinalizer(checkIng, finalizerName), "Unmapped class with enabled=true must not receive finalizer")

		// Verify warning event was emitted to clarify why it was skipped
		require.NotEmpty(t, fakeRecorder.Events)
		event := <-fakeRecorder.Events
		assert.Contains(t, event, "UnmappedIngressClass")
		assert.Contains(t, event, "unmapped-class")
	})

	t.Run("ingressclass mode cleans up when ingress is no longer selected but has finalizer", func(t *testing.T) {
		ing := makeIngress("ing-deselected", "nginx", "false")
		controllerutil.AddFinalizer(ing, finalizerName)

		route := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "orphan-route",
				Namespace: "default",
				Annotations: map[string]string{
					config.AnnotationSourceIngress: "default/ing-deselected",
				},
			},
		}

		reconciler, fakeClient, _ := setupTestReconciler(t, ing, route)
		reconciler.IngressSelectionMode = config.IngressSelectionModeIngressClass

		req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "ing-deselected"}}
		_, err := reconciler.Reconcile(ctx, req)
		require.NoError(t, err)

		checkIng := &networkingv1.Ingress{}
		err = fakeClient.Get(ctx, req.NamespacedName, checkIng)
		require.NoError(t, err)
		assert.False(t, controllerutil.ContainsFinalizer(checkIng, finalizerName), "Finalizer must be removed")

		// Route deleted
		err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "orphan-route"}, &gatewayv1.HTTPRoute{})
		assert.True(t, apierrors.IsNotFound(err))
	})
}

func stringContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && searchSubstr(s, substr)))
}

func searchSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
