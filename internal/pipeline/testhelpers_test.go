package pipeline

import (
	"context"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// recSpan is a trace.Span that records the attributes, errors, status, and end
// calls it receives. It embeds the no-op span for every method it doesn't care
// about, so it stays a valid trace.Span without reimplementing the whole surface.
type recSpan struct {
	tracenoop.Span
	name   string
	attrs  []attribute.KeyValue
	errs   []error
	status codes.Code
	ended  bool
}

func (s *recSpan) SetAttributes(kv ...attribute.KeyValue)        { s.attrs = append(s.attrs, kv...) }
func (s *recSpan) RecordError(err error, _ ...trace.EventOption) { s.errs = append(s.errs, err) }
func (s *recSpan) SetStatus(c codes.Code, _ string)              { s.status = c }
func (s *recSpan) End(_ ...trace.SpanEndOption)                  { s.ended = true }

func (s *recSpan) attr(key string) (attribute.Value, bool) {
	for _, kv := range s.attrs {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// recTracer hands out recSpans and remembers them in start order.
type recTracer struct {
	tracenoop.Tracer
	spans []*recSpan
}

func (t *recTracer) Start(ctx context.Context, name string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	s := &recSpan{name: name}
	t.spans = append(t.spans, s)
	return ctx, s
}

// logLine is one captured log emission with its accumulated WithValues fields.
type logLine struct {
	msg string
	kv  []any
}

type logRecorder struct {
	lines []logLine
}

// recLogSink is a logr.LogSink that captures Info emissions and the WithValues
// fields layered onto the logger, so a test can assert the single wide event.
type recLogSink struct {
	rec    *logRecorder
	values []any
}

func newRecLogger() (logr.Logger, *logRecorder) {
	rec := &logRecorder{}
	return logr.New(&recLogSink{rec: rec}), rec
}

func (s *recLogSink) Init(logr.RuntimeInfo) {}
func (s *recLogSink) Enabled(int) bool      { return true }
func (s *recLogSink) Info(_ int, msg string, kv ...any) {
	all := append(append([]any{}, s.values...), kv...)
	s.rec.lines = append(s.rec.lines, logLine{msg: msg, kv: all})
}
func (s *recLogSink) Error(_ error, msg string, kv ...any) {
	all := append(append([]any{}, s.values...), kv...)
	s.rec.lines = append(s.rec.lines, logLine{msg: "ERROR:" + msg, kv: all})
}
func (s *recLogSink) WithValues(kv ...any) logr.LogSink {
	return &recLogSink{rec: s.rec, values: append(append([]any{}, s.values...), kv...)}
}
func (s *recLogSink) WithName(string) logr.LogSink { return s }

func (r *logRecorder) line(msg string) (logLine, bool) {
	for _, l := range r.lines {
		if l.msg == msg {
			return l, true
		}
	}
	return logLine{}, false
}

// value returns the value recorded for key in a captured line's key/value pairs,
// or nil if absent.
func (l logLine) value(key string) any {
	for i := 0; i+1 < len(l.kv); i += 2 {
		if k, ok := l.kv[i].(string); ok && k == key {
			return l.kv[i+1]
		}
	}
	return nil
}
