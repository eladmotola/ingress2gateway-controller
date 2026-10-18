package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetricsRegistrationAndTracking(t *testing.T) {
	assert.NotNil(t, ConversionsTotal)
	assert.NotNil(t, ConversionErrorsTotal)
	assert.NotNil(t, ConversionDuration)
	assert.NotNil(t, ResourcesCreated)

	// Verify that metric instruments can be incremented and observed without panic
	assert.NotPanics(t, func() {
		ConversionsTotal.WithLabelValues("nginx", "success").Inc()
		ConversionErrorsTotal.WithLabelValues("nginx", "conversion_failed").Inc()
		ConversionDuration.WithLabelValues("nginx").Observe(0.123)
		ResourcesCreated.WithLabelValues("HTTPRoute", "nginx").Add(2)

		start := time.Now()
		time.Sleep(1 * time.Millisecond)
		ConversionDuration.WithLabelValues("nginx").Observe(time.Since(start).Seconds())
	})
}
