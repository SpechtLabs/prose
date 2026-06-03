package observability

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// StepMetrics is the narrow, bounded door to Prometheus. Its only label source is
// the runner's own (controller, step, outcome) triple — never rctx.Set — so an
// arbitrary field key can never explode metric cardinality. The two doors (wide
// event vs metric) share no type and no call path, which is the separation by
// design.
type StepMetrics struct {
	duration *prometheus.HistogramVec
}

func NewStepMetrics(reg prometheus.Registerer) *StepMetrics {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "prose_step_duration_seconds",
		Help:    "Duration of each prose reconcile step, labelled by controller, step, and outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"controller", "step", "outcome"})
	reg.MustRegister(h)
	return &StepMetrics{duration: h}
}

func (m *StepMetrics) Observe(controller, step, outcome string, d time.Duration) {
	m.duration.WithLabelValues(controller, step, outcome).Observe(d.Seconds())
}

var (
	globalMetricsOnce sync.Once
	globalMetrics     *StepMetrics
)

// GlobalStepMetrics returns the process-wide step histogram, registered exactly
// once against controller-runtime's registry so it is served on the manager's
// existing /metrics endpoint with no extra wiring. The sync.Once guards against
// duplicate-registration panics when several controllers build pipelines in one
// process.
func GlobalStepMetrics() *StepMetrics {
	globalMetricsOnce.Do(func() {
		globalMetrics = NewStepMetrics(crmetrics.Registry)
	})
	return globalMetrics
}
