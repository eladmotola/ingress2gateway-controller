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
	AnnotationEmitter          = "ingress2gateway.io/emitter"
	AnnotationEnabled          = "ingress2gateway.io/enabled"

	// DefaultEmitterName is the emitter used when neither annotation nor flag is set.
	DefaultEmitterName = "standard"

	// IngressSelectionMode defines how ingresses are selected for conversion.
	IngressSelectionModeAnnotation   = "annotation"
	IngressSelectionModeIngressClass = "ingressclass"

	// NGINX Ingress annotations
	NginxIngressBackendProtocolAnnotation = "nginx.ingress.kubernetes.io/backend-protocol"

	// Annotation keys for created resources (Gateway/HTTPRoute)
	AnnotationSourceIngress  = "ingress2gateway.io/source-ingress"
	AnnotationSourceProvider = "ingress2gateway.io/source-provider"
	AnnotationConvertedAt    = "ingress2gateway.io/converted-at"

	LegacyIngressClassAnnotation = "kubernetes.io/ingress.class"
)

// ExternalGateway defines a pre-existing Gateway that the controller must not manage.
type ExternalGateway struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ParseExternalGatewayMapping parses a single --ingress-class-to-external-gateway-mapping flag value: <ingressClass>=<namespace>/<name>
func ParseExternalGatewayMapping(val string) (string, ExternalGateway, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", ExternalGateway{}, fmt.Errorf("external gateway mapping cannot be empty")
	}

	parts := strings.SplitN(val, "=", 2)
	if len(parts) != 2 {
		return "", ExternalGateway{}, fmt.Errorf("invalid external gateway mapping format %q: expected <ingressClass>=<namespace>/<name>", val)
	}

	className := strings.TrimSpace(parts[0])
	gwPart := strings.TrimSpace(parts[1])

	if className == "" {
		return "", ExternalGateway{}, fmt.Errorf("invalid external gateway mapping %q: ingressClass must not be empty", val)
	}

	nsName := strings.Split(gwPart, "/")
	if len(nsName) != 2 {
		return "", ExternalGateway{}, fmt.Errorf("invalid external gateway target format %q: expected <namespace>/<name>", gwPart)
	}

	namespace := strings.TrimSpace(nsName[0])
	name := strings.TrimSpace(nsName[1])
	if namespace == "" || name == "" {
		return "", ExternalGateway{}, fmt.Errorf("invalid external gateway target %q: namespace and name must both be non-empty", gwPart)
	}

	return className, ExternalGateway{
		Namespace: namespace,
		Name:      name,
	}, nil
}

// ValidateIngressSelectionMode validates the ingress selection mode.
func ValidateIngressSelectionMode(mode string) error {
	switch mode {
	case IngressSelectionModeAnnotation, IngressSelectionModeIngressClass:
		return nil
	default:
		return fmt.Errorf("invalid ingress selection mode %q: allowed values are %q or %q",
			mode, IngressSelectionModeAnnotation, IngressSelectionModeIngressClass)
	}
}

// Config holds the configuration extracted from Ingress annotations
type Config struct {
	Provider         string
	GatewayName      string
	GatewayClass     string
	GatewayNamespace string
	Emitter          string
	ExternalGateway  bool
}

// IsEnabled checks if the Ingress has conversion enabled based on the selection mode and classes.
// In annotation mode: requires ingress2gateway.io/enabled: "true".
// In ingressclass mode: selected if its class is in classes (keys of ingressClassToGatewayClassMapping or ingressClassToExternalGatewayMapping),
// unless opted out via ingress2gateway.io/enabled: "false". An enabled: "true" annotation does not select an unmapped class.
func IsEnabled(ingress *networkingv1.Ingress, mode string, classes []string) bool {
	if ingress == nil {
		return false
	}

	// In both modes, enabled: "false" explicitly opts out
	if ingress.Annotations != nil {
		if enabledVal, ok := ingress.Annotations[AnnotationEnabled]; ok {
			if strings.ToLower(strings.TrimSpace(enabledVal)) == "false" {
				return false
			}
		}
	}

	switch mode {
	case IngressSelectionModeIngressClass:
		cls := GetIngressClass(ingress)
		if cls == "" {
			return false
		}
		for _, c := range classes {
			if c == cls {
				return true
			}
		}
		return false

	case IngressSelectionModeAnnotation, "":
		if ingress.Annotations != nil {
			if enabledVal, ok := ingress.Annotations[AnnotationEnabled]; ok {
				return strings.ToLower(strings.TrimSpace(enabledVal)) == "true"
			}
		}
		return false

	default:
		return false
	}
}

// GetConfig extracts and validates the configuration from Ingress annotations and external gateways.
// defaultProvider is used when no per-ingress provider annotation is set.
// defaultEmitter is used when no per-ingress emitter annotation is set.
// ingressClassToGatewayClassMapping is used to map IngressClass names to GatewayClass names.
// ingressClassToExternalGatewayMapping is used to map IngressClass names to external Gateways.
func GetConfig(
	ingress *networkingv1.Ingress,
	defaultProvider, defaultEmitter string,
	ingressClassToGatewayClassMapping map[string]string,
	ingressClassToExternalGatewayMapping map[string]ExternalGateway,
) (*Config, error) {
	if ingress == nil {
		return nil, fmt.Errorf("ingress cannot be nil")
	}

	var provider, emitter, gwNamespace string
	if ingress.Annotations != nil {
		provider = strings.TrimSpace(ingress.Annotations[AnnotationProvider])
		emitter = strings.TrimSpace(ingress.Annotations[AnnotationEmitter])
		gwNamespace = strings.TrimSpace(ingress.Annotations[AnnotationGatewayNamespace])
	}

	if provider == "" {
		provider = defaultProvider
	}

	if emitter == "" {
		emitter = defaultEmitter
	}
	if emitter == "" {
		emitter = DefaultEmitterName
	}

	cfg := &Config{
		Provider:         provider,
		GatewayNamespace: gwNamespace,
		Emitter:          emitter,
	}

	className := GetIngressClass(ingress)

	// Check if this Ingress targets an external Gateway via ingressClassToExternalGatewayMapping
	if className != "" {
		if matchedExternal, ok := ingressClassToExternalGatewayMapping[className]; ok {
			cfg.ExternalGateway = true
			cfg.GatewayName = matchedExternal.Name
			cfg.GatewayNamespace = matchedExternal.Namespace
			if mappedClass, ok := ingressClassToGatewayClassMapping[className]; ok {
				cfg.GatewayClass = mappedClass
			}
		}
	}

	if !cfg.ExternalGateway {
		// Non-external gateway
		if className == "" {
			return nil, fmt.Errorf("ingress %s/%s must have an ingressClassName to determine the target GatewayClass",
				ingress.Namespace, ingress.Name)
		}
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

		if cfg.GatewayClass == "" {
			return nil, fmt.Errorf("gateway-class could not be determined for Ingress %s/%s", ingress.Namespace, ingress.Name)
		}
	}

	// Validate required fields
	if cfg.Provider == "" {
		return nil, fmt.Errorf("provider is required for Ingress %s/%s (via annotation %s or default provider)",
			ingress.Namespace, ingress.Name, AnnotationProvider)
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

func GetIngressClass(ingress *networkingv1.Ingress) string {
	if ingress == nil {
		return ""
	}
	if ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName != "" {
		return *ingress.Spec.IngressClassName
	}
	if ingress.Annotations != nil {
		if class, ok := ingress.Annotations[LegacyIngressClassAnnotation]; ok && strings.TrimSpace(class) != "" {
			return strings.TrimSpace(class)
		}
	}
	return ""
}
