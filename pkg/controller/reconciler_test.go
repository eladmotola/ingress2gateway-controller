package controller

import (
	"context"
	"testing"

	"github.com/eladm/ingress2gateway-controller/pkg/config"
	"github.com/eladm/ingress2gateway-controller/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
