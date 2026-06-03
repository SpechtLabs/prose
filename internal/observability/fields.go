package observability

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// Fields is an insertion-ordered key/value accumulator. It is the in-memory form
// of the wide event: every rctx.Set and every framework-recorded duration/outcome
// lands here, keyed by its fully-qualified dotted path, and is emitted once as a
// single structured log line at the end of the reconcile.
//
// A reconcile runs on a single goroutine (controller-runtime serializes work per
// object key), so Fields needs no locking.
type Fields struct {
	order []string
	vals  map[string]any
}

func NewFields() *Fields {
	return &Fields{vals: make(map[string]any)}
}

// Set records key=value. Re-setting an existing key updates the value in place
// and keeps the key's original position in the emitted line.
func (f *Fields) Set(key string, value any) {
	if _, ok := f.vals[key]; !ok {
		f.order = append(f.order, key)
	}
	f.vals[key] = value
}

// Value returns the value recorded for key, if any.
func (f *Fields) Value(key string) (any, bool) {
	v, ok := f.vals[key]
	return v, ok
}

// Has reports whether key has been recorded.
func (f *Fields) Has(key string) bool {
	_, ok := f.vals[key]
	return ok
}

// Flatten returns the accumulated fields as a logr-style alternating
// key, value, key, value slice in insertion order.
func (f *Fields) Flatten() []any {
	out := make([]any, 0, len(f.order)*2)
	for _, k := range f.order {
		out = append(out, k, f.vals[k])
	}
	return out
}

// Dotted joins path segments with ".", skipping empty segments. The empty group
// path (a top-level step) therefore contributes no leading dot, so a step "status"
// at the root flattens to "status.duration", not ".status.duration".
//
// Spaces within a segment are normalized to underscores so that natural-language
// group labels (e.g. a Context("now that both ends exist")) produce queryable
// telemetry keys ("now_that_both_ends_exist.…") rather than keys with spaces. The
// human-readable label is still used verbatim for the span name; only the dotted
// key derived from it is normalized.
func Dotted(parts ...string) string {
	nonEmpty := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, strings.ReplaceAll(p, " ", "_"))
		}
	}
	return strings.Join(nonEmpty, ".")
}

// ToAttr maps a Go value onto an OpenTelemetry attribute, so the same field that
// feeds the wide event also annotates the current span. Kubernetes counts are
// commonly int32 (e.g. replica counts), hence the explicit case.
func ToAttr(key string, value any) attribute.KeyValue {
	switch v := value.(type) {
	case string:
		return attribute.String(key, v)
	case bool:
		return attribute.Bool(key, v)
	case int:
		return attribute.Int(key, v)
	case int32:
		return attribute.Int64(key, int64(v))
	case int64:
		return attribute.Int64(key, v)
	case float64:
		return attribute.Float64(key, v)
	case []string:
		return attribute.StringSlice(key, v)
	default:
		return attribute.String(key, fmt.Sprintf("%v", v))
	}
}
