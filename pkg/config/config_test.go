package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestIsEnabled(t *testing.T) {
	tests := []struct {
		name          string
		ingress       *networkingv1.Ingress
		selectionMode string
		classMapping  map[string]string
		want          bool
	}{
		{name: "annotation mode - enabled true", ingress: &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationEnabled: "true"}}}, selectionMode: SelectionModeAnnotation, want: true},
		{name: "annotation mode - no annotation", ingress: &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}, selectionMode: SelectionModeAnnotation, want: false},
		{name: "mapped mode - class in mapping", ingress: &networkingv1.Ingress{Spec: networkingv1.IngressSpec{IngressClassName: ptr.To("nginx")}}, selectionMode: SelectionModeMapped, classMapping: map[string]string{"nginx": "gateway-class"}, want: true},
		{name: "mapped mode - legacy annotation class", ingress: &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"kubernetes.io/ingress.class": "nginx"}}}, selectionMode: SelectionModeMapped, classMapping: map[string]string{"nginx": "gateway-class"}, want: true},
		{name: "mapped mode - class not in mapping", ingress: &networkingv1.Ingress{Spec: networkingv1.IngressSpec{IngressClassName: ptr.To("other")}}, selectionMode: SelectionModeMapped, classMapping: map[string]string{"nginx": "gateway-class"}, want: false},
		{name: "all mode - unannotated ingress", ingress: &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "app"}}, selectionMode: SelectionModeAll, want: true},
		{name: "all mode - explicitly disabled", ingress: &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationEnabled: "false"}}}, selectionMode: SelectionModeAll, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsEnabled(tt.ingress, tt.selectionMode, tt.classMapping))
		})
	}
}

func TestGetConfig(t *testing.T) {
	tests := []struct {
		name    string
		ingress *networkingv1.Ingress
		opts    ControllerOptions
		want    *Config
		wantErr bool
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
			opts: ControllerOptions{
				DefaultProvider:                   "nginx",
				DefaultEmitter:                    DefaultEmitterName,
				IngressClassToGatewayClassMapping: map[string]string{"nginx-internal": "nginx"},
			},
			want: &Config{
				Provider:         "nginx",
				GatewayName:      "nginx-internal",
				GatewayClass:     "nginx",
				GatewayNamespace: "default",
				RouteNamespace:   "default",
				Emitter:          DefaultEmitterName,
				ManageGateway:    true,
			},
			wantErr: false,
		},
		{
			name: "legacy annotation class with mapping",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress-legacy",
					Namespace: "default",
					Annotations: map[string]string{"kubernetes.io/ingress.class": "nginx"},
				},
			},
			opts: ControllerOptions{DefaultProvider: "ingress-nginx", DefaultEmitter: DefaultEmitterName, IngressClassToGatewayClassMapping: map[string]string{"nginx": "envoy-gateway"}},
			want: &Config{Provider: "ingress-nginx", GatewayName: "nginx", GatewayClass: "envoy-gateway", GatewayNamespace: "default", RouteNamespace: "default", Emitter: DefaultEmitterName, ManageGateway: true},
		},
		{
			name: "target gateway external unmanaged",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-app", Namespace: "gitlab-proj",
					Annotations: map[string]string{"kubernetes.io/ingress.class": "nginx"},
				},
			},
			opts: ControllerOptions{DefaultProvider: "ingress-nginx", DefaultEmitter: DefaultEmitterName, TargetGatewayName: "shared-gateway", TargetGatewayNamespace: "gateway-system", TargetGatewayManage: false},
			want: &Config{Provider: "ingress-nginx", GatewayName: "shared-gateway", GatewayClass: "shared-gateway", GatewayNamespace: "gateway-system", RouteNamespace: "gitlab-proj", Emitter: DefaultEmitterName, ManageGateway: false},
		},
		{
			name: "missing IngressClassName without target gateway",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Annotations: map[string]string{AnnotationEnabled: "true"},
				},
			},
			opts: ControllerOptions{DefaultProvider: "ingress-nginx", IngressClassToGatewayClassMapping: map[string]string{"nginx": "nginx"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetConfig(tt.ingress, tt.opts)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
