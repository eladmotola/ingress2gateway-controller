package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ConversionsTotal tracks total number of conversions
	ConversionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingress2gateway_conversions_total",
			Help: "Total number of Ingress to Gateway conversions",
		},
		[]string{"provider", "status"},
	)

	// ConversionErrorsTotal tracks conversion errors
	ConversionErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingress2gateway_conversion_errors_total",
			Help: "Total number of conversion errors",
		},
		[]string{"provider", "error_type"},
	)

	// ConversionDuration tracks conversion duration
	ConversionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ingress2gateway_conversion_duration_seconds",
			Help:    "Duration of Ingress to Gateway conversions",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"provider"},
	)

	// ResourcesCreated tracks number of resources created
	ResourcesCreated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingress2gateway_resources_created_total",
			Help: "Total number of Gateway API resources created",
		},
		[]string{"resource_type", "provider"},
	)
)

func init() {
	//Register custom metrics with controller-runtime's metrics registry
	metrics.Registry.MustRegister(
		ConversionsTotal,
		ConversionErrorsTotal,
		ConversionDuration,
		ResourcesCreated,
	)
}
