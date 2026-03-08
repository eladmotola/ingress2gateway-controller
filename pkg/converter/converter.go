package converter

import (
	"context"
	"fmt"
	"strings"

	"github.com/eladm/ingress2gateway-controller/pkg/config"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/emitters/common_emitter"
	_ "github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/emitters/standard"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	_ "github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/apisix"
	_ "github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/cilium"
	_ "github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/gce"
	_ "github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/ingressnginx"
	_ "github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/istio"
	_ "github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/kong"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Converter handles the conversion from Ingress to Gateway API resources.
// It leverages the upstream ingress2gateway library providers and emitters.
type Converter struct {
	client client.Client
	scheme *runtime.Scheme
}

// New creates a new Converter.
func New(client client.Client, scheme *runtime.Scheme) *Converter {
	return &Converter{
		client: client,
		scheme: scheme,
	}
}

// ConversionResult holds the final Gateway API resources produced by the conversion.
type ConversionResult struct {
	Gateway    *gatewayv1.Gateway
	HTTPRoutes []*gatewayv1.HTTPRoute
	GRPCRoutes []*gatewayv1.GRPCRoute
}

// Convert converts a set of Ingresses to Gateway API resources.
// It bypasses the CLI by using a fake client pre-loaded with the in-memory Ingresses,
// allowing the provider to use its full resource-reading and IR-generation pipeline.
func (c *Converter) Convert(ctx context.Context, ingresses []networkingv1.Ingress, cfg *config.Config) (*ConversionResult, error) {
	if len(ingresses) == 0 {
		return nil, fmt.Errorf("at least one ingress is required")
	}
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	logger := log.FromContext(ctx)
	logger.Info("Converting Ingresses to Gateway API using ingress2gateway library",
		"count", len(ingresses),
		"provider", cfg.Provider)

	// ------------------------------------------------------------------
	// 1. Prepare the Fake Client with Ingress and related Service objects.
	// ------------------------------------------------------------------
	objects, err := c.buildFakeClientObjects(ctx, ingresses)
	if err != nil {
		return nil, fmt.Errorf("failed to build fake client objects: %w", err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(c.scheme).WithObjects(objects...).Build()

	// ------------------------------------------------------------------
	// 2. Instantiate the Provider.
	// ------------------------------------------------------------------
	providerName := cfg.Provider
	providerCtor, ok := i2gw.ProviderConstructorByName[i2gw.ProviderName(providerName)]
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered; supported: %v", providerName, i2gw.GetSupportedProviders())
	}

	// ------------------------------------------------------------------
	// 3. Build Provider Flags.
	//    We dynamically set the ingress-class flag to match what's on the
	//    actual Ingress objects so they aren't filtered out by the reader.
	// ------------------------------------------------------------------
	report := notifications.NewReport(true)
	providerConf := &i2gw.ProviderConf{
		Client:                fakeClient,
		ProviderSpecificFlags: buildProviderSpecificFlags(ingresses, providerName),
		Report:                report,
	}
	provider := providerCtor(providerConf)

	// ------------------------------------------------------------------
	// 4. Read resources using the provider's pipeline.
	//    Even though the client is fake, this invokes the full parsing logic.
	// ------------------------------------------------------------------
	if err := provider.ReadResourcesFromCluster(ctx); err != nil {
		return nil, fmt.Errorf("provider failed to read resources from fake client: %w", err)
	}

	// ------------------------------------------------------------------
	// 5. Generate Intermediate Representation (IR).
	// ------------------------------------------------------------------
	ir, errs := provider.ToIR()
	if len(errs) > 0 {
		return nil, fmt.Errorf("failed to generate IR: %v", errs)
	}

	// ------------------------------------------------------------------
	// 6. Chain the common emitter (processes IR transformations).
	// ------------------------------------------------------------------
	commonEmitter := common_emitter.NewEmitter(&common_emitter.EmitterConf{
		AllowExperimentalGatewayAPI: true,
		Report:                      report,
	})
	eIR, errs := commonEmitter.Emit(ir)
	if len(errs) > 0 {
		return nil, fmt.Errorf("common emitter failed: %v", errs)
	}

	// ------------------------------------------------------------------
	// 7. Instantiate the Standard Emitter (produces final Gateway API objects).
	// ------------------------------------------------------------------
	emitterName := cfg.Emitter
	if emitterName == "" {
		emitterName = config.DefaultEmitterName
	}
	emitterCtor, ok := i2gw.EmitterConstructorByName[i2gw.EmitterName(emitterName)]
	if !ok {
		return nil, fmt.Errorf("emitter %q is not registered; supported: %v", emitterName, i2gw.GetSupportedEmitters())
	}
	emitter := emitterCtor(&i2gw.EmitterConf{
		AllowExperimentalGatewayAPI: true,
		Report:                      report,
	})
	gatewayResources, errs := emitter.Emit(eIR)
	if len(errs) > 0 {
		logger.Info("Emitter produced warnings", "emitter", emitterName, "warnings", errs.ToAggregate())
	}

	// ------------------------------------------------------------------
	// 8. Post-Process the created resources.
	//    We override namespaces to match cfg.GatewayNamespace and
	//    cfg.RouteNamespace, and add our tracking annotations.
	// ------------------------------------------------------------------
	result := &ConversionResult{
		HTTPRoutes: make([]*gatewayv1.HTTPRoute, 0),
		GRPCRoutes: make([]*gatewayv1.GRPCRoute, 0),
	}

	// Extract and fix up the Gateways
	for _, gw := range gatewayResources.Gateways {
		// We use the GatewayName derived from the IngressClass as the physical Gateway name.
		// The GatewayClass is used for the underlying implementation class.
		gw := gw // capture loop variable
		gw.Name = cfg.GatewayName
		gw.Namespace = cfg.GatewayNamespace
		gw.Spec.GatewayClassName = gatewayv1.ObjectName(cfg.GatewayClass)
		result.Gateway = &gw
		break // We support one managed Gateway per reconciliation group
	}

	// Fix up each HTTPRoute: namespace, ParentRefs, and source annotations.
	for _, route := range gatewayResources.HTTPRoutes {
		route := route // capture loop variable
		route.Namespace = cfg.RouteNamespace
		c.setParentRef(&route.Spec.CommonRouteSpec, result.Gateway)
		config.AddSourceAnnotations(&route, c.findSourceIngress(ingresses, route.Name), cfg.Provider)
		result.HTTPRoutes = append(result.HTTPRoutes, &route)
	}

	// Fix up each GRPCRoute: namespace, ParentRefs, and source annotations.
	for _, route := range gatewayResources.GRPCRoutes {
		route := route // capture loop variable
		route.Namespace = cfg.RouteNamespace
		c.setParentRef(&route.Spec.CommonRouteSpec, result.Gateway)
		config.AddSourceAnnotations(&route, c.findSourceIngress(ingresses, route.Name), cfg.Provider)
		result.GRPCRoutes = append(result.GRPCRoutes, &route)
	}

	logger.Info("Conversion complete",
		"gateways", len(gatewayResources.Gateways),
		"httpRoutes", len(gatewayResources.HTTPRoutes),
		"grpcRoutes", len(gatewayResources.GRPCRoutes))

	return result, nil
}

// setParentRef ensures the route points to our generated Gateway.
func (c *Converter) setParentRef(spec *gatewayv1.CommonRouteSpec, gateway *gatewayv1.Gateway) {
	if gateway == nil {
		return
	}
	ns := gatewayv1.Namespace(gateway.Namespace)

	spec.ParentRefs = []gatewayv1.ParentReference{
		{
			Name:      gatewayv1.ObjectName(gateway.Name),
			Namespace: &ns,
		},
	}
}

// findSourceIngress attempts to find the Ingress that generated a specific route.
// It matches by checking if the Ingress name is a substring of the route name (library convention).
func (c *Converter) findSourceIngress(ingresses []networkingv1.Ingress, routeName string) *networkingv1.Ingress {
	if len(ingresses) == 1 {
		return &ingresses[0]
	}
	for i := range ingresses {
		if strings.Contains(routeName, ingresses[i].Name) {
			return &ingresses[i]
		}
	}
	return &ingresses[0] // fallback
}

// buildFakeClientObjects gathers the Ingresses and any referenced Services to populate the fake client.
func (c *Converter) buildFakeClientObjects(ctx context.Context, ingresses []networkingv1.Ingress) ([]client.Object, error) {
	objects := c.ingressesToClientObjects(ingresses)

	// Fetch referenced services for named port resolution
	visitedSvc := make(map[types.NamespacedName]bool)
	for _, ing := range ingresses {
		backends := getIngressServiceBackends(&ing)
		for _, backend := range backends {
			nn := types.NamespacedName{Namespace: ing.Namespace, Name: backend.Name}
			if visitedSvc[nn] {
				continue
			}
			visitedSvc[nn] = true
			svc := &corev1.Service{}
			if err := c.client.Get(ctx, nn, svc); err == nil {
				objects = append(objects, svc)
			}
		}
	}
	return objects, nil
}

func (c *Converter) ingressesToClientObjects(ingresses []networkingv1.Ingress) []client.Object {
	objs := make([]client.Object, len(ingresses))
	for i := range ingresses {
		objs[i] = &ingresses[i]
	}
	return objs
}

// getIngressServiceBackends extracts all unique service backends from an Ingress.
func getIngressServiceBackends(ing *networkingv1.Ingress) []networkingv1.IngressServiceBackend {
	var backends []networkingv1.IngressServiceBackend
	if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
		backends = append(backends, *ing.Spec.DefaultBackend.Service)
	}
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service != nil {
					backends = append(backends, *path.Backend.Service)
				}
			}
		}
	}
	return backends
}

// buildProviderSpecificFlags sets flags like "ingress-class" to ensure the provider picks up our resources.
func buildProviderSpecificFlags(ingresses []networkingv1.Ingress, providerName string) map[string]map[string]string {
	flags := make(map[string]map[string]string)
	if providerName == "ingress-nginx" || providerName == "nginx" {
		if len(ingresses) > 0 {
			flags[providerName] = map[string]string{
				"ingress-class": getIngressClass(&ingresses[0]),
			}
		}
	}
	return flags
}

// getIngressClass determines the Ingress class from annotations or spec.
func getIngressClass(ingress *networkingv1.Ingress) string {
	if ingress.Spec.IngressClassName != nil {
		return *ingress.Spec.IngressClassName
	}
	if class, ok := ingress.Annotations["kubernetes.io/ingress.class"]; ok {
		return class
	}
	return "nginx" // Default fallback
}
