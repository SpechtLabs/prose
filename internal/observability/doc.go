// Package observability holds prose's non-generic telemetry primitives: the
// observability Sink and its functional options, the wide-event field
// accumulator, the per-step Prometheus metric, and humane-error folding. It is
// consumed by internal/pipeline and carries no dependency on the generic DSL.
package observability
