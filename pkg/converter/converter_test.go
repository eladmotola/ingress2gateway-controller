package converter

import (
	"context"
	"testing"

	"github.com/eladm/ingress2gateway-controller/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestConvert(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := New(fakeClient, scheme)
	ctx := context.Background()

	pathTypePrefix := networkingv1.PathTypePrefix

	tests := []struct {
		name            string
		ingress         *networkingv1.Ingress
		config          *config.Config
		expectedGateway *gatewayv1.Gateway // nil if not expected
		validateRoutes  func(t *testing.T, routes []*gatewayv1.HTTPRoute)
		expectError     bool
	}{
		{
			name: "basic ingress conversion",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: stringPtr("nginx"),
					Rules: []networkingv1.IngressRule{
						{
							Host: "example.com",
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{
											Path:     "/foo",
											PathType: &pathTypePrefix,
											Backend: networkingv1.IngressBackend{
												Service: &networkingv1.IngressServiceBackend{
													Name: "foo-service",
													Port: networkingv1.ServiceBackendPort{
														Number: 8080,
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
			},
			config: &config.Config{
				GatewayName:      "nginx",
				GatewayNamespace: "gateway-ns",
				GatewayClass:     "my-gateway-class",
				RouteNamespace:   "route-ns",
				Provider:         "ingress-nginx",
			},
			expectedGateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nginx",
					Namespace: "gateway-ns",
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "my-gateway-class",
				},
			},
			validateRoutes: func(t *testing.T, routes []*gatewayv1.HTTPRoute) {
				require.Len(t, routes, 1)
				route := routes[0]
				assert.Equal(t, "route-ns", route.Namespace)
				assert.Equal(t, "example.com", string(route.Spec.Hostnames[0]))

				require.Len(t, route.Spec.Rules, 1)
				rule := route.Spec.Rules[0]

				require.Len(t, rule.Matches, 1)
				match := rule.Matches[0]
				assert.Equal(t, "/foo", *match.Path.Value)
				assert.Equal(t, gatewayv1.PathMatchPathPrefix, *match.Path.Type)

				require.Len(t, rule.BackendRefs, 1)
				backend := rule.BackendRefs[0]
				assert.Equal(t, "foo-service", string(backend.Name))
				assert.Equal(t, int32(8080), int32(*backend.Port))
			},
			expectError: false,
		},
		{
			name: "ingress with no rules",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-ingress",
					Namespace: "default",
				},
				Spec: networkingv1.IngressSpec{},
			},
			config: &config.Config{
				GatewayNamespace: "gateway-ns",
				GatewayClass:     "my-gateway-class",
				Provider:         "ingress-nginx",
			},
			expectError: false, // The library might return empty or error depending on implementation options, let's see.
			// If it's valid to have empty ingress (e.g. for default backend only?), then no error.
			// But here we have nothing. Library usually returns empty IR.
			validateRoutes: func(t *testing.T, routes []*gatewayv1.HTTPRoute) {
				assert.Empty(t, routes)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := c.Convert(ctx, []networkingv1.Ingress{*tt.ingress}, tt.config)
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.expectedGateway != nil {
				require.NotNil(t, result.Gateway)
				assert.Equal(t, tt.expectedGateway.Name, result.Gateway.Name)
				assert.Equal(t, tt.expectedGateway.Namespace, result.Gateway.Namespace)
				assert.Equal(t, tt.expectedGateway.Spec.GatewayClassName, result.Gateway.Spec.GatewayClassName)
			}

			if tt.validateRoutes != nil {
				tt.validateRoutes(t, result.HTTPRoutes)
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
