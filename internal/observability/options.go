package observability

import (
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/tools/record"
)

// Option configures the observability Sink. Options are non-generic because the
// Sink is non-generic; this also lets the public Otel/WideEvents/Recorder be
// called without a type argument.
type Option func(*Sink)

// Otel sends per-step and per-group spans to the given tracer. Each step becomes a
// child span and each group a parent span; durations and errors are recorded by
// the framework, without a tracing call in your business logic.
func Otel(tracer trace.Tracer) Option {
	return func(s *Sink) {
		if tracer != nil {
			s.tracer = tracer
		}
	}
}

// WideEvents emits exactly one canonical structured log line per reconcile to the
// given logger, with every accumulated field flattened into dotted keys.
func WideEvents(logger logr.Logger) Option {
	return func(s *Sink) {
		s.logger = logger
	}
}

// Recorder enables kubectl-visible Kubernetes events via rctx.Event. Without it,
// rctx.Event is a no-op.
func Recorder(rec record.EventRecorder) Option {
	return func(s *Sink) {
		s.recorder = rec
	}
}
