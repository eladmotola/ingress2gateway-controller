package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestGetConfig(t *testing.T) {
	tests := []struct {
		name             string
		ingress          *networkingv1.Ingress
		defaultProvider  string
		defaultEmitter   string
		classMapping     map[string]string
		externalGateways map[string]ExternalGateway
		want             *Config
		wantErr          bool
		errContains      string
	}{
		{
			name: "valid mapping with enabled annotation",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationEnabled:  "true",
						AnnotationProvider: "nginx",
					},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			classMapping: map[string]string{
				"nginx-internal": "nginx",
			},
			want: &Config{
				Provider:         "nginx",
				GatewayName:      "nginx-internal",
				GatewayClass:     "nginx",
				GatewayNamespace: "default",
				Emitter:          DefaultEmitterName,
				ExternalGateway:  false,
			},
			wantErr: false,
		},
		{
			name: "missing IngressClassName",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationEnabled: "true",
					},
				},
			},
			classMapping: map[string]string{"nginx": "nginx"},
			wantErr:      true,
			errContains:  "must have an ingressClassName",
		},
		{
			name: "empty IngressClassName",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationEnabled: "true",
					},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To(""),
				},
			},
			classMapping: map[string]string{"nginx": "nginx"},
			wantErr:      true,
			errContains:  "must have an ingressClassName",
		},
		{
			name: "IngressClass not in mapping (managed)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationEnabled: "true",
					},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("unknown-class"),
				},
			},
			defaultProvider: "ingress-nginx",
			classMapping:    map[string]string{"nginx": "nginx"},
			wantErr:         true,
			errContains:     "not in the global mapping",
		},
		{
			name: "external by class - unmapped class is not an error",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "app-ns",
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("external-class"),
				},
			},
			defaultProvider: "ingress-nginx",
			classMapping:    map[string]string{"other-class": "other"},
			externalGateways: map[string]ExternalGateway{
				"external-class": {
					Name:      "my-ext-gw",
					Namespace: "infra-ns",
				},
			},
			want: &Config{
				Provider:         "ingress-nginx",
				GatewayName:      "my-ext-gw",
				GatewayNamespace: "infra-ns",
				GatewayClass:     "",
				Emitter:          DefaultEmitterName,
				ExternalGateway:  true,
			},
			wantErr: false,
		},
		{
			name: "external gateway mapped by ingressClass",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "app-ns",
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("shared-gw"),
				},
			},
			defaultProvider: "ingress-nginx",
			externalGateways: map[string]ExternalGateway{
				"shared-gw": {
					Name:      "shared-gw",
					Namespace: "shared-infra",
				},
			},
			want: &Config{
				Provider:         "ingress-nginx",
				GatewayName:      "shared-gw",
				GatewayNamespace: "shared-infra",
				Emitter:          DefaultEmitterName,
				ExternalGateway:  true,
			},
			wantErr: false,
		},
		{
			name: "nil annotations for external gateway",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "app-ns",
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("ext-class"),
				},
			},
			defaultProvider: "ingress-nginx",
			externalGateways: map[string]ExternalGateway{
				"ext-class": {
					Name:      "ext-gw",
					Namespace: "infra-ns",
				},
			},
			want: &Config{
				Provider:         "ingress-nginx",
				GatewayName:      "ext-gw",
				GatewayNamespace: "infra-ns",
				Emitter:          DefaultEmitterName,
				ExternalGateway:  true,
			},
			wantErr: false,
		},
		{
			name: "nil annotations for managed gateway with default provider",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			defaultProvider: "ingress-nginx",
			classMapping: map[string]string{
				"nginx-internal": "nginx",
			},
			want: &Config{
				Provider:         "ingress-nginx",
				GatewayName:      "nginx-internal",
				GatewayClass:     "nginx",
				GatewayNamespace: "default",
				Emitter:          DefaultEmitterName,
				ExternalGateway:  false,
			},
			wantErr: false,
		},
		{
			name: "legacy class annotation for managed gateway",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-legacy",
					Namespace: "default",
					Annotations: map[string]string{
						"kubernetes.io/ingress.class": "legacy-nginx",
					},
				},
			},
			defaultProvider: "ingress-nginx",
			classMapping: map[string]string{
				"legacy-nginx": "nginx",
			},
			want: &Config{
				Provider:         "ingress-nginx",
				GatewayName:      "legacy-nginx",
				GatewayClass:     "nginx",
				GatewayNamespace: "default",
				Emitter:          DefaultEmitterName,
				ExternalGateway:  false,
			},
			wantErr: false,
		},
		{
			name: "legacy class annotation for external gateway",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-legacy",
					Namespace: "default",
					Annotations: map[string]string{
						"kubernetes.io/ingress.class": "legacy-ext",
					},
				},
			},
			defaultProvider: "ingress-nginx",
			externalGateways: map[string]ExternalGateway{
				"legacy-ext": {
					Name:      "ext-gw",
					Namespace: "infra",
				},
			},
			want: &Config{
				Provider:         "ingress-nginx",
				GatewayName:      "ext-gw",
				GatewayNamespace: "infra",
				Emitter:          DefaultEmitterName,
				ExternalGateway:  true,
			},
			wantErr: false,
		},
		{
			name: "valid mapping with traefik provider",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress-traefik",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationEnabled:  "true",
						AnnotationProvider: "traefik",
					},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("traefik-class"),
				},
			},
			classMapping: map[string]string{
				"traefik-class": "traefik",
			},
			want: &Config{
				Provider:         "traefik",
				GatewayName:      "traefik-class",
				GatewayClass:     "traefik",
				GatewayNamespace: "default",
				Emitter:          DefaultEmitterName,
				ExternalGateway:  false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetConfig(tt.ingress, tt.defaultProvider, tt.defaultEmitter, tt.classMapping, tt.externalGateways)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsEnabled(t *testing.T) {
	classes := []string{"nginx-internal", "nginx-external"}

	tests := []struct {
		name       string
		ingress    *networkingv1.Ingress
		mode       string
		classes    []string
		wantResult bool
	}{
		{
			name:       "nil ingress",
			ingress:    nil,
			mode:       IngressSelectionModeAnnotation,
			classes:    classes,
			wantResult: false,
		},
		{
			name:       "empty ingress in annotation mode",
			ingress:    &networkingv1.Ingress{},
			mode:       IngressSelectionModeAnnotation,
			classes:    classes,
			wantResult: false,
		},
		{
			name: "annotation mode with enabled=true (spec class)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationEnabled: "true"},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			mode:       IngressSelectionModeAnnotation,
			classes:    classes,
			wantResult: true,
		},
		{
			name: "annotation mode with enabled=TRUE (legacy annotation class)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationEnabled:             "TRUE",
						"kubernetes.io/ingress.class": "nginx-internal",
					},
				},
			},
			mode:       IngressSelectionModeAnnotation,
			classes:    classes,
			wantResult: true,
		},
		{
			name: "annotation mode with enabled=false (spec class in classes)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationEnabled: "false"},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			mode:       IngressSelectionModeAnnotation,
			classes:    classes,
			wantResult: false,
		},
		{
			name: "annotation mode without enabled annotation (spec class in classes)",
			ingress: &networkingv1.Ingress{
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			mode:       IngressSelectionModeAnnotation,
			classes:    classes,
			wantResult: false,
		},
		{
			name: "annotation mode enabled=true but class not in classes",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationEnabled: "true"},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("unrelated-class"),
				},
			},
			mode:       IngressSelectionModeAnnotation,
			classes:    classes,
			wantResult: true,
		},
		{
			name: "ingressclass mode with spec class in classes (no enabled annotation)",
			ingress: &networkingv1.Ingress{
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			mode:       IngressSelectionModeIngressClass,
			classes:    classes,
			wantResult: true,
		},
		{
			name: "ingressclass mode with legacy annotation class in classes (no enabled annotation)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kubernetes.io/ingress.class": "nginx-external",
					},
				},
			},
			mode:       IngressSelectionModeIngressClass,
			classes:    classes,
			wantResult: true,
		},
		{
			name: "ingressclass mode with class NOT in classes",
			ingress: &networkingv1.Ingress{
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("other-class"),
				},
			},
			mode:       IngressSelectionModeIngressClass,
			classes:    classes,
			wantResult: false,
		},
		{
			name: "ingressclass mode with class in classes but enabled=false (opts out)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationEnabled: "false"},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			mode:       IngressSelectionModeIngressClass,
			classes:    classes,
			wantResult: false,
		},
		{
			name: "ingressclass mode with class in classes but enabled=FALSE (opts out)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationEnabled: "FALSE"},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			mode:       IngressSelectionModeIngressClass,
			classes:    classes,
			wantResult: false,
		},
		{
			name: "ingressclass mode with class NOT in classes even if enabled=true (not selected)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationEnabled: "true"},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("unrelated-class"),
				},
			},
			mode:       IngressSelectionModeIngressClass,
			classes:    classes,
			wantResult: false,
		},
		{
			name: "ingressclass mode with class in classes and enabled=true",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationEnabled: "true"},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			mode:       IngressSelectionModeIngressClass,
			classes:    classes,
			wantResult: true,
		},
		{
			name: "ingressclass mode with no class and no enabled annotation",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{},
			},
			mode:       IngressSelectionModeIngressClass,
			classes:    classes,
			wantResult: false,
		},
		{
			name: "default mode (empty string) acts as annotation mode",
			ingress: &networkingv1.Ingress{
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("nginx-internal"),
				},
			},
			mode:       "",
			classes:    classes,
			wantResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsEnabled(tt.ingress, tt.mode, tt.classes)
			assert.Equal(t, tt.wantResult, got)
		})
	}
}

func TestGetConfig_Additional(t *testing.T) {
	mapping := map[string]string{
		"nginx-class": "nginx",
	}

	t.Run("nil ingress", func(t *testing.T) {
		cfg, err := GetConfig(nil, "nginx", "", mapping, nil)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("nil annotations does not error if class and provider valid", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: ptr.To("nginx-class"),
			},
		}
		cfg, err := GetConfig(ing, "nginx", "", mapping, nil)
		assert.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, "nginx", cfg.Provider)
		assert.Equal(t, "nginx-class", cfg.GatewayName)
		assert.Equal(t, "default", cfg.GatewayNamespace)
	})

	t.Run("missing provider", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test",
				Namespace:   "default",
				Annotations: map[string]string{},
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: ptr.To("nginx-class"),
			},
		}
		cfg, err := GetConfig(ing, "", "", mapping, nil)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("unsupported provider", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test",
				Namespace:   "default",
				Annotations: map[string]string{AnnotationProvider: "unsupported-p"},
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: ptr.To("nginx-class"),
			},
		}
		cfg, err := GetConfig(ing, "", "", mapping, nil)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("empty mapped gateway class", func(t *testing.T) {
		badMapping := map[string]string{"nginx-class": ""}
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test",
				Namespace:   "default",
				Annotations: map[string]string{AnnotationProvider: "nginx"},
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: ptr.To("nginx-class"),
			},
		}
		cfg, err := GetConfig(ing, "", "", badMapping, nil)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("custom gateway namespace and emitter", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
				Annotations: map[string]string{
					AnnotationGatewayNamespace: "custom-gw-ns",
					AnnotationEmitter:          "custom-emitter",
				},
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: ptr.To("nginx-class"),
			},
		}
		cfg, err := GetConfig(ing, "nginx", "default-emitter", mapping, nil)
		assert.NoError(t, err)
		assert.Equal(t, "custom-gw-ns", cfg.GatewayNamespace)
		assert.Equal(t, "custom-emitter", cfg.Emitter)
	})

	t.Run("fallback default emitter", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test",
				Namespace:   "default",
				Annotations: map[string]string{},
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: ptr.To("nginx-class"),
			},
		}
		cfg, err := GetConfig(ing, "nginx", "my-default-emitter", mapping, nil)
		assert.NoError(t, err)
		assert.Equal(t, "my-default-emitter", cfg.Emitter)
	})
}

func TestExternalGatewayParsingAndValidation(t *testing.T) {
	t.Run("valid external gateway flag parsing", func(t *testing.T) {
		cls, eg, err := ParseExternalGatewayMapping("external-nginx=gateway-infra/my-gw")
		require.NoError(t, err)
		assert.Equal(t, "external-nginx", cls)
		assert.Equal(t, "gateway-infra", eg.Namespace)
		assert.Equal(t, "my-gw", eg.Name)
	})

	t.Run("invalid external gateway flag parsing", func(t *testing.T) {
		_, _, err := ParseExternalGatewayMapping("")
		assert.Error(t, err)

		_, _, err = ParseExternalGatewayMapping("invalid-format")
		assert.Error(t, err)

		_, _, err = ParseExternalGatewayMapping("=gateway-infra/my-gw")
		assert.Error(t, err)

		_, _, err = ParseExternalGatewayMapping("class=/name")
		assert.Error(t, err)

		_, _, err = ParseExternalGatewayMapping("class=ns/")
		assert.Error(t, err)

		_, _, err = ParseExternalGatewayMapping("class=ns/name/extra")
		assert.Error(t, err)
	})
}

func TestValidateIngressSelectionMode(t *testing.T) {
	assert.NoError(t, ValidateIngressSelectionMode("annotation"))
	assert.NoError(t, ValidateIngressSelectionMode("ingressclass"))
	assert.Error(t, ValidateIngressSelectionMode("all"))
	assert.Error(t, ValidateIngressSelectionMode(""))
	assert.Error(t, ValidateIngressSelectionMode("unknown"))
}

func TestAddSourceAnnotations(t *testing.T) {
	t.Run("nil receiver or ingress", func(t *testing.T) {
		AddSourceAnnotations(nil, &networkingv1.Ingress{}, "nginx")
		ing := &networkingv1.Ingress{}
		AddSourceAnnotations(ing, nil, "nginx")
	})

	t.Run("adds annotations to object with nil annotations", func(t *testing.T) {
		obj := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "target",
				Namespace: "default",
			},
		}
		srcIngress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "source",
				Namespace: "prod",
			},
		}
		AddSourceAnnotations(obj, srcIngress, "nginx")
		ann := obj.GetAnnotations()
		assert.NotNil(t, ann)
		assert.Equal(t, "prod/source", ann[AnnotationSourceIngress])
		assert.Equal(t, "nginx", ann[AnnotationSourceProvider])
		assert.NotEmpty(t, ann[AnnotationConvertedAt])
	})

	t.Run("preserves existing annotations", func(t *testing.T) {
		obj := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "target",
				Namespace: "default",
				Annotations: map[string]string{
					"custom.io/tag": "preserved",
				},
			},
		}
		srcIngress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "source",
				Namespace: "prod",
			},
		}
		AddSourceAnnotations(obj, srcIngress, "traefik")
		ann := obj.GetAnnotations()
		assert.Equal(t, "preserved", ann["custom.io/tag"])
		assert.Equal(t, "prod/source", ann[AnnotationSourceIngress])
		assert.Equal(t, "traefik", ann[AnnotationSourceProvider])
	})
}

func TestProviders(t *testing.T) {
	assert.True(t, IsProviderSupported("nginx"))
	assert.True(t, IsProviderSupported("ingress-nginx"))
	assert.True(t, IsProviderSupported("traefik"))
	assert.False(t, IsProviderSupported("nonexistent"))

	providers := GetSupportedProviders()
	assert.NotEmpty(t, providers)
	assert.Contains(t, providers, "nginx")
}

func TestGetIngressClass(t *testing.T) {
	assert.Empty(t, GetIngressClass(nil))

	assert.Equal(t, "spec-class", GetIngressClass(&networkingv1.Ingress{
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To("spec-class"),
		},
	}))

	assert.Equal(t, "ann-class", GetIngressClass(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"kubernetes.io/ingress.class": "  ann-class  ",
			},
		},
	}))

	assert.Empty(t, GetIngressClass(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"kubernetes.io/ingress.class": "   ",
			},
		},
	}))

	assert.Empty(t, GetIngressClass(&networkingv1.Ingress{}))
}

