package config

import (
	"fmt"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Annotation keys for Ingress configuration
	AnnotationProvider         = "ingress2gateway.io/provider"
	AnnotationGatewayNamespace = "ingress2gateway.io/gateway-namespace"
	AnnotationRouteNamespace   = "ingress2gateway.io/route-namespace"
	AnnotationEmitter          = "ingress2gateway.io/emitter"
	AnnotationEnabled          = "ingress2gateway.io/enabled"

	// DefaultEmitterName is the emitter used when neither annotation nor flag is set.
	DefaultEmitterName = "standard"

	// NGINX Ingress annotations
	NginxIngressBackendProtocolAnnotation = "nginx.ingress.kubernetes.io/backend-protocol"

	// Annotation keys for created resources (Gateway/HTTPRoute)
	AnnotationSourceIngress  = "ingress2gateway.io/source-ingress"
	AnnotationSourceProvider = "ingress2gateway.io/source-provider"
	AnnotationConvertedAt    = "ingress2gateway.io/converted-at"
)

// Config holds the configuration extracted from Ingress annotations
type Config struct {
	Provider         string
	GatewayName      string
	GatewayClass     string
	GatewayNamespace string
	RouteNamespace   string
	Emitter          string
}

// IsEnabled checks if the Ingress has conversion enabled.
// It returns true if the "ingress2gateway.io/enabled" annotation is set to "true".
func IsEnabled(ingress *networkingv1.Ingress) bool {
	if ingress == nil || ingress.Annotations == nil {
		return false
	}
	return strings.ToLower(ingress.Annotations[AnnotationEnabled]) == "true"
}

// GetConfig extracts and validates the configuration from Ingress annotations.
// defaultProvider is used when no per-ingress provider annotation is set.
// defaultEmitter is used when no per-ingress emitter annotation is set.
// ingressClassToGatewayClassMapping is used to map IngressClass names to GatewayClass names.
func GetConfig(ingress *networkingv1.Ingress, defaultProvider, defaultEmitter string, ingressClassToGatewayClassMapping map[string]string) (*Config, error) {
	if ingress == nil {
		return nil, fmt.Errorf("ingress cannot be nil")
	}
	if ingress.Annotations == nil {
		return nil, fmt.Errorf("no annotations found on Ingress %s/%s", ingress.Namespace, ingress.Name)
	}

	provider := strings.TrimSpace(ingress.Annotations[AnnotationProvider])
	if provider == "" {
		provider = defaultProvider
	}

	emitter := strings.TrimSpace(ingress.Annotations[AnnotationEmitter])
	if emitter == "" {
		emitter = defaultEmitter
	}
	if emitter == "" {
		emitter = DefaultEmitterName
	}

	cfg := &Config{
		Provider:         provider,
		GatewayNamespace: strings.TrimSpace(ingress.Annotations[AnnotationGatewayNamespace]),
		RouteNamespace:   strings.TrimSpace(ingress.Annotations[AnnotationRouteNamespace]),
		Emitter:          emitter,
	}

	// Resolve GatewayName and GatewayClass from the mapping using the IngressClassName
	if ingress.Spec.IngressClassName == nil {
		return nil, fmt.Errorf("ingress %s/%s must have an ingressClassName to determine the target GatewayClass",
			ingress.Namespace, ingress.Name)
	}

	className := *ingress.Spec.IngressClassName
	mappedClass, ok := ingressClassToGatewayClassMapping[className]
	if !ok {
		return nil, fmt.Errorf("ingressClass %q for ingress %s/%s is not in the global mapping. Please add it to the controller configuration",
			className, ingress.Namespace, ingress.Name)
	}
	cfg.GatewayName = className
	cfg.GatewayClass = mappedClass

	// Set defaults
	if cfg.GatewayNamespace == "" {
		cfg.GatewayNamespace = ingress.Namespace
	}
	if cfg.RouteNamespace == "" {
		cfg.RouteNamespace = ingress.Namespace
	}

	// Validate required fields
	if cfg.Provider == "" {
		return nil, fmt.Errorf("provider is required for Ingress %s/%s (via annotation %s or default provider)",
			ingress.Namespace, ingress.Name, AnnotationProvider)
	}
	if cfg.GatewayClass == "" {
		return nil, fmt.Errorf("gateway-class could not be determined for Ingress %s/%s", ingress.Namespace, ingress.Name)
	}

	// Validate provider is supported
	if !IsProviderSupported(cfg.Provider) {
		return nil, fmt.Errorf("unsupported provider %q for Ingress %s/%s. Supported providers: %v",
			cfg.Provider, ingress.Namespace, ingress.Name, GetSupportedProviders())
	}

	return cfg, nil
}

// AddSourceAnnotations adds tracking annotations to the created Gateway/HTTPRoute
func AddSourceAnnotations(obj metav1.Object, ingress *networkingv1.Ingress, provider string) {
	if obj == nil || ingress == nil {
		return
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[AnnotationSourceIngress] = fmt.Sprintf("%s/%s", ingress.Namespace, ingress.Name)
	annotations[AnnotationSourceProvider] = provider
	annotations[AnnotationConvertedAt] = time.Now().UTC().Format(time.RFC3339)
	obj.SetAnnotations(annotations)
}

// IsProviderSupported checks if the provider is supported
func IsProviderSupported(provider string) bool {
	return SupportedProviders[provider]
}

// GetSupportedProviders returns a sorted list of supported providers
func GetSupportedProviders() []string {
	providers := make([]string, 0, len(SupportedProviders))
	for p := range SupportedProviders {
		providers = append(providers, p)
	}
	return providers
}
