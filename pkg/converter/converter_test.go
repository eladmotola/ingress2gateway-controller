package converter

import (
	"context"
	"strings"
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

func newTestConverter(t *testing.T) (*Converter, context.Context) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	return New(fakeClient, scheme), context.Background()
}

func basicIngress() *networkingv1.Ingress {
	pathTypePrefix := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
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
	}
}

func basicConfig(emitter string) *config.Config {
	return &config.Config{
		GatewayName:      "nginx",
		GatewayNamespace: "gateway-ns",
		GatewayClass:     "my-gateway-class",
		RouteNamespace:   "route-ns",
		Provider:         "ingress-nginx",
		Emitter:          emitter,
	}
}

func TestConvertEmitters(t *testing.T) {
	c, ctx := newTestConverter(t)
	ingress := basicIngress()

	tests := []struct {
		name          string
		emitter       string
		expectError   bool
		errorContains string
	}{
		{
			name:    "standard emitter",
			emitter: "standard",
		},
		{
			name:    "envoy-gateway emitter",
			emitter: "envoy-gateway",
		},
		{
			name:    "default emitter when empty",
			emitter: "",
		},
		{
			name:          "unknown emitter",
			emitter:       "does-not-exist",
			expectError:   true,
			errorContains: "not registered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := c.Convert(ctx, []networkingv1.Ingress{*ingress}, basicConfig(tt.emitter))
			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result.Gateway)
			assert.Equal(t, "nginx", result.Gateway.Name)
			assert.Equal(t, "gateway-ns", result.Gateway.Namespace)
			assert.Equal(t, gatewayv1.ObjectName("my-gateway-class"), result.Gateway.Spec.GatewayClassName)

			require.Len(t, result.HTTPRoutes, 1)
			route := result.HTTPRoutes[0]
			assert.Equal(t, "route-ns", route.Namespace)
			assert.Equal(t, "example.com", string(route.Spec.Hostnames[0]))
			require.Len(t, route.Spec.Rules, 1)
			require.Len(t, route.Spec.Rules[0].BackendRefs, 1)
			assert.Equal(t, "foo-service", string(route.Spec.Rules[0].BackendRefs[0].Name))
		})
	}
}

func TestConvertEnvoyGatewayWithNginxAnnotation(t *testing.T) {
	c, ctx := newTestConverter(t)

	ingress := basicIngress()
	ingress.Annotations = map[string]string{
		"nginx.ingress.kubernetes.io/proxy-body-size": "10m",
	}

	result, err := c.Convert(ctx, []networkingv1.Ingress{*ingress}, basicConfig("envoy-gateway"))
	require.NoError(t, err)
	require.NotNil(t, result.Gateway)
	require.Len(t, result.HTTPRoutes, 1)
	assert.Equal(t, "foo-service", string(result.HTTPRoutes[0].Spec.Rules[0].BackendRefs[0].Name))
}

func TestConvert(t *testing.T) {
	c, ctx := newTestConverter(t)

	pathTypePrefix := networkingv1.PathTypePrefix

	tests := []struct {
		name            string
		ingress         *networkingv1.Ingress
		config          *config.Config
		expectedGateway *gatewayv1.Gateway // nil if not expected
		validateRoutes  func(t *testing.T, routes []*gatewayv1.HTTPRoute)
		expectError     bool
		errorContains   string
	}{		{
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
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.True(t, strings.Contains(err.Error(), tt.errorContains), "error: %v", err)
				}
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
