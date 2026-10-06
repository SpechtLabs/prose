package observability

import (
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

// Sink holds the three observability destinations plus the shared step metric. It
// carries no type parameter: telemetry primitives don't need the reconciled type,
// only the builder chain does. Tracer and logger always default to working no-ops
// so the runner never branches on their presence; recorder may be nil because
// events are opt-in per step and there is no universal no-op recorder.
type Sink struct {
	tracer   trace.Tracer
	recorder record.EventRecorder
	metrics  *StepMetrics
	// now is the clock step and reconcile durations are measured with.
	now    func() time.Time
	logger logr.Logger
}

// NewSink returns a sink with no-op tracing and logging, no event recorder and
// the process-wide step histogram, configured further by opts.
func NewSink(opts ...Option) *Sink {
	s := &Sink{
		tracer:  tracenoop.NewTracerProvider().Tracer("prose"),
		logger:  logr.Discard(),
		metrics: GlobalStepMetrics(),
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Tracer returns the configured tracer (a no-op if Otel was not supplied).
func (s *Sink) Tracer() trace.Tracer { return s.tracer }

// Logger returns the configured wide-event logger (logr.Discard if WideEvents was
// not supplied).
func (s *Sink) Logger() logr.Logger { return s.logger }

// Now reads the sink's clock (the wall clock unless Clock replaced it).
func (s *Sink) Now() time.Time { return s.now() }

// Observe records a step's duration into the per-step histogram.
func (s *Sink) Observe(controller, step, outcome string, d time.Duration) {
	s.metrics.Observe(controller, step, outcome, d)
}

// Event records a Kubernetes event against obj, formatting the message. It no-ops
// when no Recorder was configured — the single place recorder-absence is handled.
func (s *Sink) Event(obj runtime.Object, eventtype, reason, msgFmt string, args ...any) {
	if s.recorder == nil {
		return
	}
	s.recorder.Eventf(obj, eventtype, reason, msgFmt, args...)
}
