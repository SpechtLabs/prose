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
	logger   logr.Logger
	recorder record.EventRecorder
	metrics  *StepMetrics
}

func NewSink(opts ...Option) *Sink {
	s := &Sink{
		tracer:  tracenoop.NewTracerProvider().Tracer("prose"),
		logger:  logr.Discard(),
		metrics: GlobalStepMetrics(),
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
