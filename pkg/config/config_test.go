package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestGetConfig(t *testing.T) {
	tests := []struct {
		name            string
		ingress         *networkingv1.Ingress
		defaultProvider string
		defaultEmitter  string
		classMapping    map[string]string
		want            *Config
		wantErr         bool
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
		},
		{
			name: "IngressClass not in mapping",
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
			classMapping: map[string]string{"nginx": "nginx"},
			wantErr:      true,
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
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetConfig(tt.ingress, tt.defaultProvider, tt.defaultEmitter, tt.classMapping)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsEnabled(t *testing.T) {
	assert.False(t, IsEnabled(nil))
	assert.False(t, IsEnabled(&networkingv1.Ingress{}))
	assert.False(t, IsEnabled(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{AnnotationEnabled: "false"},
		},
	}))
	assert.False(t, IsEnabled(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{AnnotationEnabled: "random"},
		},
	}))
	assert.True(t, IsEnabled(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{AnnotationEnabled: "true"},
		},
	}))
	assert.True(t, IsEnabled(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{AnnotationEnabled: "TRUE"},
		},
	}))
}

func TestGetConfig_Additional(t *testing.T) {
	mapping := map[string]string{
		"nginx-class": "nginx",
	}

	t.Run("nil ingress", func(t *testing.T) {
		cfg, err := GetConfig(nil, "nginx", "", mapping)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("nil annotations", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: ptr.To("nginx-class"),
			},
		}
		cfg, err := GetConfig(ing, "nginx", "", mapping)
		assert.Error(t, err)
		assert.Nil(t, cfg)
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
		cfg, err := GetConfig(ing, "", "", mapping)
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
		cfg, err := GetConfig(ing, "", "", mapping)
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
		cfg, err := GetConfig(ing, "", "", badMapping)
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
		cfg, err := GetConfig(ing, "nginx", "default-emitter", mapping)
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
		cfg, err := GetConfig(ing, "nginx", "my-default-emitter", mapping)
		assert.NoError(t, err)
		assert.Equal(t, "my-default-emitter", cfg.Emitter)
	})
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

