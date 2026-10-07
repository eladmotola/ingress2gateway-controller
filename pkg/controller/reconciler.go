package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/eladm/ingress2gateway-controller/pkg/config"
	"github.com/eladm/ingress2gateway-controller/pkg/converter"
	custmetrics "github.com/eladm/ingress2gateway-controller/pkg/metrics"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
)

const (
	finalizerName = "ingress2gateway.io/finalizer"
)

// IngressReconciler reconciles Ingress resources
type IngressReconciler struct {
	client.Client
	Scheme                            *runtime.Scheme
	Recorder                          record.EventRecorder
	Converter                         *converter.Converter
	DefaultProvider                   string
	DefaultEmitter                    string
	IngressClassToGatewayClassMapping map[string]string
}

// Reconcile handles the reconciliation of Ingress resources
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	startTime := time.Now()

	// Fetch the Ingress
	var ingress networkingv1.Ingress
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Ingress")
		return ctrl.Result{}, err
	}

	// Check if the Ingress is being deleted
	if !ingress.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &ingress)
	}

	// Check if conversion is enabled
	if !config.IsEnabled(&ingress) {
		// If it's disabled but still has our finalizer, it means it was previously enabled.
		// We should clean up resources before removing the finalizer.
		if controllerutil.ContainsFinalizer(&ingress, finalizerName) {
			logger.Info("Ingress conversion disabled, cleaning up resources", "ingress", ingress.Name)
			return r.handleDeletion(ctx, &ingress)
		}
		return ctrl.Result{}, nil
	}

	logger.Info("IngressClassToGatewayClassMapping", "mapping", r.IngressClassToGatewayClassMapping)
	// Get and validate configuration
	cfg, err := config.GetConfig(&ingress, r.DefaultProvider, r.DefaultEmitter, r.IngressClassToGatewayClassMapping)
	if err != nil {
		logger.Error(err, "Invalid configuration", "ingress", ingress.Name)
		custmetrics.ConversionErrorsTotal.WithLabelValues("unknown", "invalid_config").Inc()
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling Ingress",
		"ingress", ingress.Name,
		"namespace", ingress.Namespace,
		"provider", cfg.Provider)

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&ingress, finalizerName) {
		controllerutil.AddFinalizer(&ingress, finalizerName)
		if err := r.Update(ctx, &ingress); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Find all Ingresses that target the SAME Gateway (merged conversion)
	// We list cluster-wide because a shared Gateway can be targeted by Ingresses in multiple namespaces.
	var ingressList networkingv1.IngressList
	if err := r.List(ctx, &ingressList); err != nil {
		logger.Error(err, "Failed to list cluster-wide Ingresses for merged conversion")
		return ctrl.Result{}, err
	}

	var sharedIngresses []networkingv1.Ingress
	for _, ing := range ingressList.Items {
		if !config.IsEnabled(&ing) {
			continue
		}
		ingCfg, err := config.GetConfig(&ing, r.DefaultProvider, r.DefaultEmitter, r.IngressClassToGatewayClassMapping)
		if err != nil {
			continue
		}
		// If it targets the same Gateway (Name and Namespace), it must be included in the merge.
		if ingCfg.GatewayName == cfg.GatewayName && ingCfg.GatewayNamespace == cfg.GatewayNamespace {
			sharedIngresses = append(sharedIngresses, ing)
		}
	}

	// Perform conversion with all shared Ingresses
	result, err := r.Converter.Convert(ctx, sharedIngresses, cfg)
	if err != nil {
		logger.Error(err, "Conversion failed", "ingress", ingress.Name)
		custmetrics.ConversionErrorsTotal.WithLabelValues(cfg.Provider, "conversion_failed").Inc()
		custmetrics.ConversionsTotal.WithLabelValues(cfg.Provider, "error").Inc()
		return ctrl.Result{}, err
	}

	// Record warnings/notifications from the conversion report
	if result.Report != nil {
		for source, notifs := range result.Report.Notifications() {
			for _, n := range notifs {
				logger.Info("Conversion notification",
					"ingress", ingress.Name,
					"source", source,
					"type", string(n.Type),
					"message", n.Message)

				if r.Recorder != nil {
					eventType := corev1.EventTypeWarning
					if n.Type == notifications.InfoNotification {
						eventType = corev1.EventTypeNormal
					}
					r.Recorder.Eventf(&ingress, eventType, "ConversionNotification", "[%s] %s", source, n.Message)
				}
			}
		}
		if rendered := result.Report.Render(); strings.TrimSpace(rendered) != "" {
			fmt.Printf("Conversion warnings for Ingress %s/%s:\n%s\n", ingress.Namespace, ingress.Name, rendered)
		}
	}

	// Update Gateway if needed
	if result.Gateway != nil {
		if err := r.createOrUpdateGateway(ctx, result.Gateway, &ingress); err != nil {
			logger.Error(err, "Failed to create/update Gateway")
			custmetrics.ConversionErrorsTotal.WithLabelValues(cfg.Provider, "gateway_creation_failed").Inc()
			return ctrl.Result{}, err
		}
		custmetrics.ResourcesCreated.WithLabelValues("Gateway", cfg.Provider).Inc()
	}

	// Create or update HTTPRoutes
	for _, httpRoute := range result.HTTPRoutes {
		if err := r.createOrUpdateHTTPRoute(ctx, httpRoute, &ingress); err != nil {
			logger.Error(err, "Failed to create/update HTTPRoute", "httpRoute", httpRoute.Name)
			custmetrics.ConversionErrorsTotal.WithLabelValues(cfg.Provider, "httproute_creation_failed").Inc()
			return ctrl.Result{}, err
		}
		custmetrics.ResourcesCreated.WithLabelValues("HTTPRoute", cfg.Provider).Inc()
	}

	// Create or update GRPCRoutes
	for _, grpcRoute := range result.GRPCRoutes {
		if err := r.createOrUpdateGRPCRoute(ctx, grpcRoute, &ingress); err != nil {
			logger.Error(err, "Failed to create/update GRPCRoute", "grpcRoute", grpcRoute.Name)
			custmetrics.ConversionErrorsTotal.WithLabelValues(cfg.Provider, "grpcroute_creation_failed").Inc()
			return ctrl.Result{}, err
		}
		custmetrics.ResourcesCreated.WithLabelValues("GRPCRoute", cfg.Provider).Inc()
	}

	// Create or update TLSRoutes
	for _, tlsRoute := range result.TLSRoutes {
		if err := r.createOrUpdateTLSRoute(ctx, tlsRoute, &ingress); err != nil {
			logger.Error(err, "Failed to create/update TLSRoute", "tlsRoute", tlsRoute.Name)
			custmetrics.ConversionErrorsTotal.WithLabelValues(cfg.Provider, "tlsroute_creation_failed").Inc()
			return ctrl.Result{}, err
		}
		custmetrics.ResourcesCreated.WithLabelValues("TLSRoute", cfg.Provider).Inc()
	}

	// Create or update BackendTLSPolicies
	for _, policy := range result.BackendTLSPolicies {
		if err := r.createOrUpdateBackendTLSPolicy(ctx, policy, &ingress); err != nil {
			logger.Error(err, "Failed to create/update BackendTLSPolicy", "backendTLSPolicy", policy.Name)
			custmetrics.ConversionErrorsTotal.WithLabelValues(cfg.Provider, "backendtlspolicy_creation_failed").Inc()
			return ctrl.Result{}, err
		}
		custmetrics.ResourcesCreated.WithLabelValues("BackendTLSPolicy", cfg.Provider).Inc()
	}

	// Record success metrics
	duration := time.Since(startTime).Seconds()
	custmetrics.ConversionDuration.WithLabelValues(cfg.Provider).Observe(duration)
	custmetrics.ConversionsTotal.WithLabelValues(cfg.Provider, "success").Inc()

	logger.Info("Successfully converted Ingress",
		"ingress", ingress.Name,
		"duration", duration)

	return ctrl.Result{}, nil
}

// handleDeletion handles cleanup when Ingress is deleted or conversion is disabled
func (r *IngressReconciler) handleDeletion(ctx context.Context, ingress *networkingv1.Ingress) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(ingress, finalizerName) {
		return ctrl.Result{}, nil
	}

	logger.Info("Cleaning up resources for Ingress", "ingress", ingress.Name)

	// Manually delete owned resources which won't be GC'd if parent Ingress still exists
	if err := r.cleanupManagedResources(ctx, ingress); err != nil {
		logger.Error(err, "Failed to cleanup managed resources")
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(ingress, finalizerName)
	if err := r.Update(ctx, ingress); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Finalizer removed", "ingress", ingress.Name)
	return ctrl.Result{}, nil
}

// cleanupManagedResources finds and deletes resources with our tracking annotation
func (r *IngressReconciler) cleanupManagedResources(ctx context.Context, ingress *networkingv1.Ingress) error {
	sourceVal := fmt.Sprintf("%s/%s", ingress.Namespace, ingress.Name)

	// 1. Clean up HTTPRoutes & GRPCRoutes (Manual delete)
	// We delete these because they are typically 1-to-1 with our Ingress conversion.
	if err := r.deleteOrphanRoutes(ctx, ingress.Namespace, sourceVal); err != nil {
		return err
	}

	// 2. Clean up Gateway owner reference
	// Gateways are shared. If we just delete it based on an annotation, we might break other Ingresses.
	// We find any Gateway that has this specific Ingress listed as an OwnerReference.
	gateways := &gatewayv1.GatewayList{}
	if err := r.List(ctx, gateways); err == nil {
		for _, gw := range gateways.Items {
			// Check if this Ingress is an owner
			isOwner := false
			for _, ref := range gw.GetOwnerReferences() {
				if ref.UID == ingress.UID {
					isOwner = true
					break
				}
			}

			if isOwner {
				// Remove our owner reference. This function will only delete the Gateway
				// if no other owners (Ingresses) are left.
				if err := r.removeOwnerReference(ctx, &gw, ingress); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (r *IngressReconciler) deleteOrphanRoutes(ctx context.Context, namespace, sourceVal string) error {
	logger := log.FromContext(ctx)

	// HTTPRoutes
	httpRoutes := &gatewayv1.HTTPRouteList{}
	if err := r.List(ctx, httpRoutes, client.InNamespace(namespace)); err == nil {
		for _, route := range httpRoutes.Items {
			if route.Annotations != nil && route.Annotations[config.AnnotationSourceIngress] == sourceVal {
				logger.Info("Deleting orphan HTTPRoute", "name", route.Name)
				if err := r.Delete(ctx, &route); err != nil {
					return fmt.Errorf("failed to delete HTTPRoute %s: %w", route.Name, err)
				}
			}
		}
	}

	// GRPCRoutes
	grpcRoutes := &gatewayv1.GRPCRouteList{}
	if err := r.List(ctx, grpcRoutes, client.InNamespace(namespace)); err == nil {
		for _, route := range grpcRoutes.Items {
			if route.Annotations != nil && route.Annotations[config.AnnotationSourceIngress] == sourceVal {
				logger.Info("Deleting orphan GRPCRoute", "name", route.Name)
				if err := r.Delete(ctx, &route); err != nil {
					return fmt.Errorf("failed to delete GRPCRoute %s: %w", route.Name, err)
				}
			}
		}
	}

	// TLSRoutes
	tlsRoutes := &gatewayv1.TLSRouteList{}
	if err := r.List(ctx, tlsRoutes, client.InNamespace(namespace)); err == nil {
		for _, route := range tlsRoutes.Items {
			if route.Annotations != nil && route.Annotations[config.AnnotationSourceIngress] == sourceVal {
				logger.Info("Deleting orphan TLSRoute", "name", route.Name)
				if err := r.Delete(ctx, &route); err != nil {
					return fmt.Errorf("failed to delete TLSRoute %s: %w", route.Name, err)
				}
			}
		}
	}

	// BackendTLSPolicies
	policies := &gatewayv1.BackendTLSPolicyList{}
	if err := r.List(ctx, policies, client.InNamespace(namespace)); err == nil {
		for _, policy := range policies.Items {
			if policy.Annotations != nil && policy.Annotations[config.AnnotationSourceIngress] == sourceVal {
				logger.Info("Deleting orphan BackendTLSPolicy", "name", policy.Name)
				if err := r.Delete(ctx, &policy); err != nil {
					return fmt.Errorf("failed to delete BackendTLSPolicy %s: %w", policy.Name, err)
				}
			}
		}
	}
	return nil
}

func (r *IngressReconciler) removeOwnerReference(ctx context.Context, obj client.Object, owner *networkingv1.Ingress) error {
	logger := log.FromContext(ctx)
	refs := obj.GetOwnerReferences()
	newRefs := []metav1.OwnerReference{}
	found := false

	for _, ref := range refs {
		if ref.UID == owner.UID {
			found = true
			continue
		}
		newRefs = append(newRefs, ref)
	}

	if !found {
		return nil
	}

	if len(newRefs) == 0 {
		// If we were the only owner, just delete the resource
		logger.Info("Deleting resource as we were the last owner", "kind", obj.GetObjectKind().GroupVersionKind().Kind, "name", obj.GetName())
		return r.Delete(ctx, obj)
	}

	logger.Info("Removing owner reference from resource", "kind", obj.GetObjectKind().GroupVersionKind().Kind, "name", obj.GetName())
	obj.SetOwnerReferences(newRefs)
	return r.Update(ctx, obj)
}

// createOrUpdateGateway creates or updates a Gateway resource
func (r *IngressReconciler) createOrUpdateGateway(ctx context.Context, gateway *gatewayv1.Gateway, owner *networkingv1.Ingress) error {
	logger := log.FromContext(ctx)

	// Set owner reference
	// We use SetOwnerReference instead of SetControllerReference because multiple Ingresses
	// may share the same Gateway. Only one can be the "Controller", but many can be "Owners".
	if err := controllerutil.SetOwnerReference(owner, gateway, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Try to get existing Gateway
	existing := &gatewayv1.Gateway{}
	err := r.Get(ctx, client.ObjectKey{Name: gateway.Name, Namespace: gateway.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new Gateway
			logger.Info("Creating Gateway", "gateway", gateway.Name, "namespace", gateway.Namespace)
			return r.Create(ctx, gateway)
		}
		return err
	}

	// Check for configuration conflicts
	if existing.Spec.GatewayClassName != gateway.Spec.GatewayClassName {
		return fmt.Errorf("conflict: Gateway %s/%s already exists but is using a different GatewayClassName %q (requested %q). Ensure all Ingresses targeting the same Gateway share the same gateway-class annotation",
			existing.Namespace, existing.Name, string(existing.Spec.GatewayClassName), string(gateway.Spec.GatewayClassName))
	}

	// Prepare for update while avoiding clobbering other owners
	gateway.ResourceVersion = existing.ResourceVersion
	gateway.OwnerReferences = existing.OwnerReferences
	if err := controllerutil.SetOwnerReference(owner, gateway, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Now check if an update is actually needed (ignoring the timestamp)
	if r.isResourceEqual(existing.ObjectMeta, gateway.ObjectMeta, existing.Spec, gateway.Spec) {
		return nil
	}

	logger.Info("Updating Gateway", "gateway", gateway.Name, "namespace", gateway.Namespace)
	return r.Update(ctx, gateway)
}

// createOrUpdateHTTPRoute creates or updates an HTTPRoute resource
func (r *IngressReconciler) createOrUpdateHTTPRoute(ctx context.Context, httpRoute *gatewayv1.HTTPRoute, owner *networkingv1.Ingress) error {
	logger := log.FromContext(ctx)

	// Set owner reference
	if err := controllerutil.SetOwnerReference(owner, httpRoute, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Try to get existing HTTPRoute
	existing := &gatewayv1.HTTPRoute{}
	err := r.Get(ctx, client.ObjectKey{Name: httpRoute.Name, Namespace: httpRoute.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new HTTPRoute
			logger.Info("Creating HTTPRoute", "httpRoute", httpRoute.Name, "namespace", httpRoute.Namespace)
			return r.Create(ctx, httpRoute)
		}
		return err
	}

	// Prepare for update
	httpRoute.ResourceVersion = existing.ResourceVersion
	httpRoute.OwnerReferences = existing.OwnerReferences
	if err := controllerutil.SetOwnerReference(owner, httpRoute, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Update existing HTTPRoute
	if r.isResourceEqual(existing.ObjectMeta, httpRoute.ObjectMeta, existing.Spec, httpRoute.Spec) {
		return nil
	}

	logger.Info("Updating HTTPRoute", "httpRoute", httpRoute.Name, "namespace", httpRoute.Namespace)
	return r.Update(ctx, httpRoute)
}

// createOrUpdateGRPCRoute creates or updates a GRPCRoute resource
func (r *IngressReconciler) createOrUpdateGRPCRoute(ctx context.Context, grpcRoute *gatewayv1.GRPCRoute, owner *networkingv1.Ingress) error {
	logger := log.FromContext(ctx)

	// Set owner reference
	if err := controllerutil.SetOwnerReference(owner, grpcRoute, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Try to get existing GRPCRoute
	existing := &gatewayv1.GRPCRoute{}
	err := r.Get(ctx, client.ObjectKey{Name: grpcRoute.Name, Namespace: grpcRoute.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new GRPCRoute
			logger.Info("Creating GRPCRoute", "grpcRoute", grpcRoute.Name, "namespace", grpcRoute.Namespace)
			return r.Create(ctx, grpcRoute)
		}
		return err
	}

	// Prepare for update
	grpcRoute.ResourceVersion = existing.ResourceVersion
	grpcRoute.OwnerReferences = existing.OwnerReferences
	if err := controllerutil.SetOwnerReference(owner, grpcRoute, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Update existing GRPCRoute
	if r.isResourceEqual(existing.ObjectMeta, grpcRoute.ObjectMeta, existing.Spec, grpcRoute.Spec) {
		return nil
	}

	logger.Info("Updating GRPCRoute", "grpcRoute", grpcRoute.Name, "namespace", grpcRoute.Namespace)
	return r.Update(ctx, grpcRoute)
}

// createOrUpdateTLSRoute creates or updates a TLSRoute resource
func (r *IngressReconciler) createOrUpdateTLSRoute(ctx context.Context, tlsRoute *gatewayv1.TLSRoute, owner *networkingv1.Ingress) error {
	logger := log.FromContext(ctx)

	// Set owner reference
	if err := controllerutil.SetOwnerReference(owner, tlsRoute, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Try to get existing TLSRoute
	existing := &gatewayv1.TLSRoute{}
	err := r.Get(ctx, client.ObjectKey{Name: tlsRoute.Name, Namespace: tlsRoute.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new TLSRoute
			logger.Info("Creating TLSRoute", "tlsRoute", tlsRoute.Name, "namespace", tlsRoute.Namespace)
			return r.Create(ctx, tlsRoute)
		}
		return err
	}

	// Prepare for update
	tlsRoute.ResourceVersion = existing.ResourceVersion
	tlsRoute.OwnerReferences = existing.OwnerReferences
	if err := controllerutil.SetOwnerReference(owner, tlsRoute, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Update existing TLSRoute
	if r.isResourceEqual(existing.ObjectMeta, tlsRoute.ObjectMeta, existing.Spec, tlsRoute.Spec) {
		return nil
	}

	logger.Info("Updating TLSRoute", "tlsRoute", tlsRoute.Name, "namespace", tlsRoute.Namespace)
	return r.Update(ctx, tlsRoute)
}

// createOrUpdateBackendTLSPolicy creates or updates a BackendTLSPolicy resource
func (r *IngressReconciler) createOrUpdateBackendTLSPolicy(ctx context.Context, policy *gatewayv1.BackendTLSPolicy, owner *networkingv1.Ingress) error {
	logger := log.FromContext(ctx)

	// Set owner reference
	if err := controllerutil.SetOwnerReference(owner, policy, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Try to get existing BackendTLSPolicy
	existing := &gatewayv1.BackendTLSPolicy{}
	err := r.Get(ctx, client.ObjectKey{Name: policy.Name, Namespace: policy.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new BackendTLSPolicy
			logger.Info("Creating BackendTLSPolicy", "backendTLSPolicy", policy.Name, "namespace", policy.Namespace)
			return r.Create(ctx, policy)
		}
		return err
	}

	// Prepare for update
	policy.ResourceVersion = existing.ResourceVersion
	policy.OwnerReferences = existing.OwnerReferences
	if err := controllerutil.SetOwnerReference(owner, policy, r.Scheme); err != nil {
		logger.V(1).Info("Failed to set owner reference", "error", err)
	}

	// Update existing BackendTLSPolicy
	if r.isResourceEqual(existing.ObjectMeta, policy.ObjectMeta, existing.Spec, policy.Spec) {
		return nil
	}

	logger.Info("Updating BackendTLSPolicy", "backendTLSPolicy", policy.Name, "namespace", policy.Namespace)
	return r.Update(ctx, policy)
}

// isResourceEqual compares two resources ignoring the dynamic timestamp annotation
func (r *IngressReconciler) isResourceEqual(oldMeta, newMeta metav1.ObjectMeta, oldSpec, newSpec interface{}) bool {
	if !apiequality.Semantic.DeepEqual(oldSpec, newSpec) {
		return false
	}
	if !apiequality.Semantic.DeepEqual(oldMeta.Labels, newMeta.Labels) {
		return false
	}
	if !apiequality.Semantic.DeepEqual(oldMeta.OwnerReferences, newMeta.OwnerReferences) {
		return false
	}

	// Compare annotations but ignore the timestamp
	oldAnn := filterAnnotations(oldMeta.Annotations)
	newAnn := filterAnnotations(newMeta.Annotations)

	return apiequality.Semantic.DeepEqual(oldAnn, newAnn)
}

func filterAnnotations(ann map[string]string) map[string]string {
	if ann == nil {
		return nil
	}
	filtered := make(map[string]string)
	for k, v := range ann {
		if k != config.AnnotationConvertedAt {
			filtered[k] = v
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// SetupWithManager sets up the controller with the Manager
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Owns(&gatewayv1.Gateway{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Owns(&gatewayv1.GRPCRoute{}).
		Owns(&gatewayv1.TLSRoute{}).
		Owns(&gatewayv1.BackendTLSPolicy{}).
		Complete(r)
}
