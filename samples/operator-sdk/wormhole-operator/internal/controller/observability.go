/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type reconcileObservability struct {
	controller string
	logger     logr.Logger
	tracer     trace.Tracer
	root       trace.Span
	current    trace.Span
	fields     *orderedFields
	start      time.Time
}

type orderedFields struct {
	order []string
	vals  map[string]any
}

func newOrderedFields() *orderedFields {
	return &orderedFields{vals: make(map[string]any)}
}

func (f *orderedFields) set(key string, value any) {
	if _, ok := f.vals[key]; !ok {
		f.order = append(f.order, key)
	}
	f.vals[key] = value
}

func (f *orderedFields) flatten() []any {
	out := make([]any, 0, len(f.order)*2)
	for _, key := range f.order {
		out = append(out, key, f.vals[key])
	}
	return out
}

func beginReconcile(ctx context.Context, logger logr.Logger, tracer trace.Tracer, controller string, req reconcile.Request, generation int64) (context.Context, *reconcileObservability) {
	if tracer == nil {
		tracer = tracenoop.NewTracerProvider().Tracer("wormhole-operator")
	}
	logger = logger.WithValues(
		"namespace", req.Namespace,
		"name", req.Name,
		"generation", generation,
	)
	ctx, span := tracer.Start(ctx, "reconcile."+controller,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("controller", controller),
			attribute.String("namespace", req.Namespace),
			attribute.String("name", req.Name),
			attribute.Int64("generation", generation),
		),
	)
	return ctx, &reconcileObservability{
		controller: controller,
		logger:     logger,
		tracer:     tracer,
		root:       span,
		current:    span,
		fields:     newOrderedFields(),
		start:      time.Now(),
	}
}

func (o *reconcileObservability) Set(key string, value any) {
	o.fields.set(key, value)
	o.current.SetAttributes(toAttr(key, value))
}

func (o *reconcileObservability) Step(ctx context.Context, name string, fn func(context.Context) (string, error)) (string, error) {
	ctx, span := o.tracer.Start(ctx, name)
	previous := o.current
	o.current = span
	start := time.Now()

	outcome, err := fn(ctx)
	if outcome == "" {
		outcome = "continue"
	}
	duration := time.Since(start)
	path := dotted(name)
	o.fields.set(path+".duration", duration.String())
	o.fields.set(path+".outcome", outcome)
	span.SetAttributes(
		attribute.String("operator_sdk.outcome", outcome),
		attribute.String("operator_sdk.step", name),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		o.fields.set(path+".error", err.Error())
	}
	span.End()
	o.current = previous
	return outcome, err
}

func (o *reconcileObservability) Finish(outcome string, result ctrl.Result, err error) {
	if outcome == "" {
		outcome = resultOutcome(result, err)
	}
	o.root.SetAttributes(attribute.String("operator_sdk.outcome", outcome))
	if err != nil {
		o.root.RecordError(err)
		o.root.SetStatus(codes.Error, err.Error())
	}
	o.root.End()

	kv := []any{
		"result", outcome,
		"requeue_after", result.RequeueAfter.String(),
		"duration", time.Since(o.start).String(),
	}
	kv = append(kv, o.fields.flatten()...)
	o.logger.Info("reconcile", kv...)
}

func resultOutcome(result ctrl.Result, err error) string {
	switch {
	case err != nil:
		return "error"
	case result.RequeueAfter > 0:
		return "requeue_after"
	case result.Requeue:
		return "requeue"
	default:
		return "done"
	}
}

func dotted(parts ...string) string {
	nonEmpty := parts[:0:0]
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, strings.ReplaceAll(part, " ", "_"))
		}
	}
	return strings.Join(nonEmpty, ".")
}

func toAttr(key string, value any) attribute.KeyValue {
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
