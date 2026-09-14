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
	AnnotationGatewayName      = "ingress2gateway.io/gateway-name"
	AnnotationGatewayNamespace = "ingress2gateway.io/gateway-namespace"
	AnnotationRouteNamespace   = "ingress2gateway.io/route-namespace"
	AnnotationEmitter          = "ingress2gateway.io/emitter"
	AnnotationEnabled          = "ingress2gateway.io/enabled"
	AnnotationManageGateway    = "ingress2gateway.io/manage-gateway"

	// Selection modes
	SelectionModeAnnotation = "annotation"
	SelectionModeMapped     = "mapped"
	SelectionModeAll        = "all"

	// DefaultEmitterName is the emitter used when neither annotation nor flag is set.
	DefaultEmitterName = "standard"

	// NGINX Ingress annotations
	NginxIngressBackendProtocolAnnotation = "nginx.ingress.kubernetes.io/backend-protocol"

	// Annotation keys for created resources (Gateway/HTTPRoute)
	AnnotationSourceIngress  = "ingress2gateway.io/source-ingress"
	AnnotationSourceProvider = "ingress2gateway.io/source-provider"
	AnnotationConvertedAt    = "ingress2gateway.io/converted-at"
)

// ControllerOptions holds global configuration options for Ingress selection and target Gateway behavior.
type ControllerOptions struct {
	DefaultProvider                   string
	DefaultEmitter                    string
	IngressSelection                  string
	TargetGatewayName                 string
	TargetGatewayNamespace            string
	TargetGatewayManage               bool
	IngressClassToGatewayClassMapping map[string]string
}

// Config holds the configuration extracted from Ingress annotations and global options.
type Config struct {
	Provider         string
	GatewayName      string
	GatewayClass     string
	GatewayNamespace string
	RouteNamespace   string
	Emitter          string
	ManageGateway    bool
}

// GetIngressClass extracts the Ingress class from spec.ingressClassName or kubernetes.io/ingress.class annotation.
func GetIngressClass(ingress *networkingv1.Ingress) string {
	if ingress == nil {
		return ""
	}
	if ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName != "" {
		return *ingress.Spec.IngressClassName
	}
	if ingress.Annotations != nil {
		if class, ok := ingress.Annotations["kubernetes.io/ingress.class"]; ok && strings.TrimSpace(class) != "" {
			return strings.TrimSpace(class)
		}
	}
	return ""
}

// IsEnabled checks if the Ingress has conversion enabled.
// Explicit ingress2gateway.io/enabled annotations take highest precedence ("true" or "false").
// When absent, selectionMode determines conversion: "all" converts every Ingress, "mapped" converts any Ingress with a mapped class.
func IsEnabled(ingress *networkingv1.Ingress, selectionMode string, classMapping map[string]string) bool {
	if ingress == nil {
		return false
	}
	if ingress.Annotations != nil {
		if value, ok := ingress.Annotations[AnnotationEnabled]; ok {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "false":
				return false
			case "true":
				return true
			}
		}
	}
	switch selectionMode {
	case SelectionModeAll:
		return true
	case SelectionModeMapped:
		className := GetIngressClass(ingress)
		_, ok := classMapping[className]
		return className != "" && ok
	default:
		return false
	}
}

// GetConfig extracts and validates the configuration from Ingress annotations and controller options.
func GetConfig(ingress *networkingv1.Ingress, opts ControllerOptions) (*Config, error) {
	if ingress == nil {
		return nil, fmt.Errorf("ingress cannot be nil")
	}

	annotations := ingress.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}

	provider := strings.TrimSpace(annotations[AnnotationProvider])
	if provider == "" {
		provider = opts.DefaultProvider
	}
	if provider == "" {
		provider = "ingress-nginx"
	}

	emitter := strings.TrimSpace(annotations[AnnotationEmitter])
	if emitter == "" {
		emitter = opts.DefaultEmitter
	}
	if emitter == "" {
		emitter = DefaultEmitterName
	}

	cfg := &Config{
		Provider:       provider,
		Emitter:        emitter,
		RouteNamespace: strings.TrimSpace(annotations[AnnotationRouteNamespace]),
		ManageGateway:  true,
	}

	if cfg.RouteNamespace == "" {
		cfg.RouteNamespace = ingress.Namespace
	}

	gwName := strings.TrimSpace(annotations[AnnotationGatewayName])
	if gwName != "" {
		cfg.GatewayName = gwName
	} else if opts.TargetGatewayName != "" {
		cfg.GatewayName = opts.TargetGatewayName
	}

	gwNamespace := strings.TrimSpace(annotations[AnnotationGatewayNamespace])
	if gwNamespace != "" {
		cfg.GatewayNamespace = gwNamespace
	} else if opts.TargetGatewayNamespace != "" {
		cfg.GatewayNamespace = opts.TargetGatewayNamespace
	} else {
		cfg.GatewayNamespace = ingress.Namespace
	}

	if manageGateway, ok := annotations[AnnotationManageGateway]; ok && strings.TrimSpace(manageGateway) != "" {
		cfg.ManageGateway = strings.EqualFold(strings.TrimSpace(manageGateway), "true")
	} else if opts.TargetGatewayName != "" {
		cfg.ManageGateway = opts.TargetGatewayManage
	}

	className := GetIngressClass(ingress)
	if className != "" && opts.IngressClassToGatewayClassMapping != nil {
		cfg.GatewayClass = opts.IngressClassToGatewayClassMapping[className]
	}
	if cfg.GatewayName == "" {
		if className != "" {
			cfg.GatewayName = className
		} else {
			cfg.GatewayName = "default"
		}
	}

	if cfg.ManageGateway {
		if cfg.GatewayClass == "" {
			if className == "" {
				return nil, fmt.Errorf("ingress %s/%s must have an IngressClass (via spec.ingressClassName or annotation %q) to determine target GatewayClass", ingress.Namespace, ingress.Name, "kubernetes.io/ingress.class")
			}
			return nil, fmt.Errorf("ingressClass %q for ingress %s/%s is not in the global mapping. Please add it to the controller configuration", className, ingress.Namespace, ingress.Name)
		}
	} else if cfg.GatewayClass == "" {
		cfg.GatewayClass = cfg.GatewayName
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
