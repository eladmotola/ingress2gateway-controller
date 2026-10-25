package converter

import (
	"context"
	"strings"
	"testing"

	"github.com/eladmotola/ingress2gateway-controller/pkg/config"
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
			assert.Equal(t, "default", route.Namespace)
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
				assert.Equal(t, "default", route.Namespace)
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

func TestConvertTLSRoute(t *testing.T) {
	c, ctx := newTestConverter(t)

	pathTypePrefix := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tls-passthrough",
			Namespace: "default",
			Annotations: map[string]string{
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

	cfg := &config.Config{
		GatewayName:      "nginx",
		GatewayNamespace: "default",
		GatewayClass:     "nginx",
		Provider:         "ingress-nginx",
		Emitter:          "standard",
	}

	result, err := c.Convert(ctx, []networkingv1.Ingress{*ingress}, cfg)
	require.NoError(t, err)
	require.NotNil(t, result.Gateway)
	assert.Equal(t, "nginx", result.Gateway.Name)
	assert.Equal(t, "default", result.Gateway.Namespace)
	assert.Equal(t, gatewayv1.ObjectName("nginx"), result.Gateway.Spec.GatewayClassName)

	// Validate Gateway Listeners
	require.Len(t, result.Gateway.Spec.Listeners, 2)
	assert.Equal(t, gatewayv1.PortNumber(80), result.Gateway.Spec.Listeners[0].Port)
	assert.Equal(t, gatewayv1.HTTPProtocolType, result.Gateway.Spec.Listeners[0].Protocol)

	tlsListener := result.Gateway.Spec.Listeners[1]
	assert.Equal(t, gatewayv1.PortNumber(443), tlsListener.Port)
	assert.Equal(t, gatewayv1.TLSProtocolType, tlsListener.Protocol)
	require.NotNil(t, tlsListener.TLS)
	assert.Equal(t, gatewayv1.TLSModePassthrough, *tlsListener.TLS.Mode)

	// Validate TLSRoute
	require.Len(t, result.TLSRoutes, 1)
	tlsRoute := result.TLSRoutes[0]
	assert.Equal(t, "tls-passthrough-app-example-com-tls-passthrough", tlsRoute.Name)
	assert.Equal(t, "default", tlsRoute.Namespace)

	require.Len(t, tlsRoute.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("app.example.com"), tlsRoute.Spec.Hostnames[0])

	require.Len(t, tlsRoute.Spec.ParentRefs, 1)
	parentRef := tlsRoute.Spec.ParentRefs[0]
	assert.Equal(t, gatewayv1.ObjectName("nginx"), parentRef.Name)
	assert.Equal(t, gatewayv1.Namespace("default"), *parentRef.Namespace)
	require.NotNil(t, parentRef.SectionName)
	assert.Equal(t, gatewayv1.SectionName("app-example-com-tls-passthrough"), *parentRef.SectionName)

	require.Len(t, tlsRoute.Spec.Rules, 1)
	require.Len(t, tlsRoute.Spec.Rules[0].BackendRefs, 1)
	backend := tlsRoute.Spec.Rules[0].BackendRefs[0]
	assert.Equal(t, gatewayv1.ObjectName("app"), backend.Name)
	assert.Equal(t, gatewayv1.PortNumber(443), *backend.Port)

	// Validate source annotations
	assert.Equal(t, "default/tls-passthrough", tlsRoute.Annotations[config.AnnotationSourceIngress])
	assert.Equal(t, "ingress-nginx", tlsRoute.Annotations[config.AnnotationSourceProvider])

	// Validate Report and warnings
	require.NotNil(t, result.Report)
	notifications := result.Report.Notifications()
	assert.NotEmpty(t, notifications)
	assert.Contains(t, notifications, "ingress-nginx")
	rendered := result.Report.Render()
	assert.Contains(t, rendered, "self-signed certificate")
}

func TestConvertBackendTLSPolicy(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))

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

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	c := New(fakeClient, scheme)
	ctx := context.Background()

	pathTypePrefix := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-tls",
			Namespace: "default",
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/backend-protocol":       "HTTPS",
				"nginx.ingress.kubernetes.io/proxy-ssl-verify":       "on",
				"nginx.ingress.kubernetes.io/proxy-ssl-secret":       "default/backend-ca",
				"nginx.ingress.kubernetes.io/proxy-ssl-name":         "backend.example.com",
				"nginx.ingress.kubernetes.io/proxy-ssl-server-name":  "on",
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

	cfg := &config.Config{
		GatewayName:      "nginx",
		GatewayNamespace: "default",
		GatewayClass:     "nginx",
		Provider:         "ingress-nginx",
		Emitter:          "standard",
	}

	result, err := c.Convert(ctx, []networkingv1.Ingress{*ingress}, cfg)
	require.NoError(t, err)

	// Validate Gateway
	require.NotNil(t, result.Gateway)
	assert.Equal(t, "nginx", result.Gateway.Name)

	// Validate HTTPRoute
	require.Len(t, result.HTTPRoutes, 1)
	assert.Equal(t, "backend-tls-app-example-com", result.HTTPRoutes[0].Name)

	// Validate BackendTLSPolicy
	require.Len(t, result.BackendTLSPolicies, 1)
	policy := result.BackendTLSPolicies[0]
	assert.Equal(t, "backend-backend-tls", policy.Name)
	assert.Equal(t, "default", policy.Namespace)

	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("backend"), policy.Spec.TargetRefs[0].Name)
	assert.Equal(t, gatewayv1.Kind("Service"), policy.Spec.TargetRefs[0].Kind)

	assert.Equal(t, gatewayv1.PreciseHostname("backend.example.com"), policy.Spec.Validation.Hostname)
	require.Len(t, policy.Spec.Validation.CACertificateRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("backend-ca"), policy.Spec.Validation.CACertificateRefs[0].Name)
	assert.Equal(t, gatewayv1.Kind("ConfigMap"), policy.Spec.Validation.CACertificateRefs[0].Kind)

	// Validate source annotations
	assert.Equal(t, "default/backend-tls", policy.Annotations[config.AnnotationSourceIngress])
	assert.Equal(t, "ingress-nginx", policy.Annotations[config.AnnotationSourceProvider])

	// Validate warnings/report
	require.NotNil(t, result.Report)
	rendered := result.Report.Render()
	assert.Contains(t, rendered, "mTLS will not be configured")
	assert.Contains(t, rendered, "ConfigMap")
}

func TestConvertNginxBackendTLSPolicy_MissingSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))

	// Fake client without the referenced secret
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := New(fakeClient, scheme)
	ctx := context.Background()

	pathTypePrefix := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-tls-missing-secret",
			Namespace: "default",
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/backend-protocol":      "HTTPS",
				"nginx.ingress.kubernetes.io/proxy-ssl-verify":      "on",
				"nginx.ingress.kubernetes.io/proxy-ssl-secret":      "default/non-existent-secret",
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

	cfg := &config.Config{
		GatewayName:      "nginx",
		GatewayNamespace: "default",
		GatewayClass:     "nginx",
		Provider:         "ingress-nginx",
		Emitter:          "standard",
	}

	result, err := c.Convert(ctx, []networkingv1.Ingress{*ingress}, cfg)
	require.NoError(t, err)
	require.NotNil(t, result.Report)

	// Verify notification about missing secret is recorded
	notifs := result.Report.Notifications()
	require.NotNil(t, notifs)
	found := false
	for _, nList := range notifs {
		for _, n := range nList {
			if strings.Contains(n.Message, "proxy-ssl-secret") && strings.Contains(n.Message, "non-existent-secret") {
				found = true
				break
			}
		}
	}
	assert.True(t, found, "Expected notification about missing proxy-ssl-secret in report")
}

func TestConvertOverlappingIngressNames(t *testing.T) {
	c, ctx := newTestConverter(t)
	pathTypePrefix := networkingv1.PathTypePrefix

	ingApp := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
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
											Name: "app-svc",
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
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

	ingAppApi := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-api",
			Namespace: "default",
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("nginx"),
			Rules: []networkingv1.IngressRule{
				{
					Host: "api.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "api-svc",
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
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

	cfg := &config.Config{
		GatewayName:      "nginx",
		GatewayNamespace: "default",
		GatewayClass:     "nginx",
		Provider:         "ingress-nginx",
		Emitter:          "standard",
	}

	result, err := c.Convert(ctx, []networkingv1.Ingress{ingApp, ingAppApi}, cfg)
	require.NoError(t, err)
	require.Len(t, result.HTTPRoutes, 2)

	for _, route := range result.HTTPRoutes {
		if strings.Contains(route.Name, "api.example.com") || strings.HasPrefix(route.Name, "app-api") {
			assert.Equal(t, "default/app-api", route.Annotations[config.AnnotationSourceIngress], "Route for app-api must not match app")
		} else {
			assert.Equal(t, "default/app", route.Annotations[config.AnnotationSourceIngress])
		}
	}
}

func TestConvertRouteNamespaceAnnotationIgnored(t *testing.T) {
	c, ctx := newTestConverter(t)
	ingress := basicIngress()
	ingress.Annotations = map[string]string{
		"ingress2gateway.io/route-namespace": "ignored-custom-ns",
	}

	cfg := basicConfig("standard")
	result, err := c.Convert(ctx, []networkingv1.Ingress{*ingress}, cfg)
	require.NoError(t, err)
	require.Len(t, result.HTTPRoutes, 1)

	// Route must be created in Ingress's namespace ("default"), ignoring route-namespace annotation
	assert.Equal(t, "default", result.HTTPRoutes[0].Namespace)
	assert.NotEqual(t, "ignored-custom-ns", result.HTTPRoutes[0].Namespace)
}

func TestConvertExternalGateway(t *testing.T) {
	c, ctx := newTestConverter(t)
	pathTypePrefix := networkingv1.PathTypePrefix

	// 1. HTTPRoute conversion with ExternalGateway
	ingHTTP := basicIngress()
	cfgHTTP := &config.Config{
		GatewayName:      "external-gw",
		GatewayNamespace: "infra-ns",
		Provider:         "ingress-nginx",
		Emitter:          "standard",
		ExternalGateway:  true,
	}

	resHTTP, err := c.Convert(ctx, []networkingv1.Ingress{*ingHTTP}, cfgHTTP)
	require.NoError(t, err)
	assert.Nil(t, resHTTP.Gateway, "Gateway must be nil for external Gateway conversion")
	require.Len(t, resHTTP.HTTPRoutes, 1)
	httpRoute := resHTTP.HTTPRoutes[0]
	require.Len(t, httpRoute.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("external-gw"), httpRoute.Spec.ParentRefs[0].Name)
	assert.Equal(t, gatewayv1.Namespace("infra-ns"), *httpRoute.Spec.ParentRefs[0].Namespace)
	assert.Nil(t, httpRoute.Spec.ParentRefs[0].SectionName, "SectionName must be nil for external Gateway")

	// 2. TLSRoute conversion with ExternalGateway
	ingTLS := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tls-passthrough",
			Namespace: "default",
			Annotations: map[string]string{
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
	cfgTLS := &config.Config{
		GatewayName:      "external-gw",
		GatewayNamespace: "infra-ns",
		Provider:         "ingress-nginx",
		Emitter:          "standard",
		ExternalGateway:  true,
	}
	resTLS, err := c.Convert(ctx, []networkingv1.Ingress{*ingTLS}, cfgTLS)
	require.NoError(t, err)
	assert.Nil(t, resTLS.Gateway, "Gateway must be nil for external Gateway conversion")
	require.Len(t, resTLS.TLSRoutes, 1)
	tlsRoute := resTLS.TLSRoutes[0]
	require.Len(t, tlsRoute.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("external-gw"), tlsRoute.Spec.ParentRefs[0].Name)
	assert.Equal(t, gatewayv1.Namespace("infra-ns"), *tlsRoute.Spec.ParentRefs[0].Namespace)
	assert.Nil(t, tlsRoute.Spec.ParentRefs[0].SectionName, "SectionName must be nil for external Gateway TLSRoute")
}

func TestSetParentRef_AllRouteTypes(t *testing.T) {
	c, _ := newTestConverter(t)
	secName := gatewayv1.SectionName("https-listener")

	// Test all three route types: HTTPRoute, GRPCRoute, TLSRoute
	t.Run("external drops sectionName for all three route types", func(t *testing.T) {
		httpRoute := &gatewayv1.HTTPRoute{
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{
							Name:        "original-gw",
							SectionName: &secName,
						},
					},
				},
			},
		}
		grpcRoute := &gatewayv1.GRPCRoute{
			Spec: gatewayv1.GRPCRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{
							Name:        "original-gw",
							SectionName: &secName,
						},
					},
				},
			},
		}
		tlsRoute := &gatewayv1.TLSRoute{
			Spec: gatewayv1.TLSRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{
							Name:        "original-gw",
							SectionName: &secName,
						},
					},
				},
			},
		}

		c.setParentRef(&httpRoute.Spec.CommonRouteSpec, "ext-gw", "ext-ns", true)
		c.setParentRef(&grpcRoute.Spec.CommonRouteSpec, "ext-gw", "ext-ns", true)
		c.setParentRef(&tlsRoute.Spec.CommonRouteSpec, "ext-gw", "ext-ns", true)

		for _, spec := range []*gatewayv1.CommonRouteSpec{
			&httpRoute.Spec.CommonRouteSpec,
			&grpcRoute.Spec.CommonRouteSpec,
			&tlsRoute.Spec.CommonRouteSpec,
		} {
			require.Len(t, spec.ParentRefs, 1)
			assert.Equal(t, gatewayv1.ObjectName("ext-gw"), spec.ParentRefs[0].Name)
			assert.Equal(t, gatewayv1.Namespace("ext-ns"), *spec.ParentRefs[0].Namespace)
			assert.Nil(t, spec.ParentRefs[0].SectionName, "SectionName must be dropped for external gateway")
		}
	})

	t.Run("managed preserves sectionName for all three route types", func(t *testing.T) {
		httpRoute := &gatewayv1.HTTPRoute{
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{
							Name:        "original-gw",
							SectionName: &secName,
						},
					},
				},
			},
		}
		grpcRoute := &gatewayv1.GRPCRoute{
			Spec: gatewayv1.GRPCRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{
							Name:        "original-gw",
							SectionName: &secName,
						},
					},
				},
			},
		}
		tlsRoute := &gatewayv1.TLSRoute{
			Spec: gatewayv1.TLSRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{
							Name:        "original-gw",
							SectionName: &secName,
						},
					},
				},
			},
		}

		c.setParentRef(&httpRoute.Spec.CommonRouteSpec, "managed-gw", "managed-ns", false)
		c.setParentRef(&grpcRoute.Spec.CommonRouteSpec, "managed-gw", "managed-ns", false)
		c.setParentRef(&tlsRoute.Spec.CommonRouteSpec, "managed-gw", "managed-ns", false)

		for _, spec := range []*gatewayv1.CommonRouteSpec{
			&httpRoute.Spec.CommonRouteSpec,
			&grpcRoute.Spec.CommonRouteSpec,
			&tlsRoute.Spec.CommonRouteSpec,
		} {
			require.Len(t, spec.ParentRefs, 1)
			assert.Equal(t, gatewayv1.ObjectName("managed-gw"), spec.ParentRefs[0].Name)
			assert.Equal(t, gatewayv1.Namespace("managed-ns"), *spec.ParentRefs[0].Namespace)
			require.NotNil(t, spec.ParentRefs[0].SectionName, "SectionName must be preserved for managed gateway")
			assert.Equal(t, secName, *spec.ParentRefs[0].SectionName)
		}
	})
}
