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
				RouteNamespace:   "default",
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
