package pipeline

import (
	"context"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/spechtlabs/prose/internal/observability"
)

var _ = ginkgo.Describe("the runner trace", func() {
	ginkgo.It("wraps the whole reconcile in a single root span so groups and steps nest under it", func() {
		recorder := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		tracer := tp.Tracer("test")

		scheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
		fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm("widget")).Build()

		var log []string
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			{name: "deps", isGroup: true, children: []*node[*corev1.ConfigMap]{
				cmStep(&log, "child", Continue, nil),
			}},
			cmStep(&log, "status", Continue, nil),
		}}

		r := &runner[*corev1.ConfigMap]{
			client:       fc,
			scheme:       scheme,
			sink:         observability.NewSink(observability.Otel(tracer)),
			newObject:    func() *corev1.ConfigMap { return &corev1.ConfigMap{} },
			root:         root,
			controller:   "configmap",
			finalizer:    testFinalizer,
			fieldOwner:   "prose-configmap",
			baseLogger:   observability.NewSink().Logger(),
			conflicts:    newConflictTracker(),
			maxConflicts: maxQuietConflicts,
		}

		_, err := r.Reconcile(context.Background(), reqFor("ns", "widget"))
		Expect(err).NotTo(HaveOccurred())

		spans := recorder.Ended()
		byName := map[string]sdktrace.ReadOnlySpan{}
		for _, s := range spans {
			byName[s.Name()] = s
		}
		Expect(byName).To(HaveKey("reconcile"))
		Expect(byName).To(HaveKey("deps"))
		Expect(byName).To(HaveKey("child"))
		Expect(byName).To(HaveKey("status"))

		recon := byName["reconcile"]
		Expect(recon.Parent().IsValid()).To(BeFalse(), "the reconcile span must be the trace root")
		Expect(recon.SpanKind()).To(Equal(oteltrace.SpanKindServer), "the reconcile span must be SERVER so it feeds per-service RED span metrics")

		// Every other span shares the root's trace and chains up to it.
		traceID := recon.SpanContext().TraceID()
		for name, s := range byName {
			Expect(s.SpanContext().TraceID()).To(Equal(traceID), "span %q must be in the same trace", name)
		}
		Expect(byName["deps"].Parent().SpanID()).To(Equal(recon.SpanContext().SpanID()), "a top-level group nests under reconcile")
		Expect(byName["status"].Parent().SpanID()).To(Equal(recon.SpanContext().SpanID()), "a top-level step nests under reconcile")
		Expect(byName["child"].Parent().SpanID()).To(Equal(byName["deps"].SpanContext().SpanID()), "a step nests under its group")

		// Every span carries its outcome as an attribute (parity with the wide event).
		outcomeOf := func(s sdktrace.ReadOnlySpan) string {
			for _, kv := range s.Attributes() {
				if string(kv.Key) == "prose.outcome" {
					return kv.Value.AsString()
				}
			}
			return ""
		}
		Expect(outcomeOf(recon)).To(Equal("continue"), "the reconcile root carries the overall outcome")
		Expect(outcomeOf(byName["deps"])).To(Equal("continue"), "a group carries its outcome")
		Expect(outcomeOf(byName["status"])).To(Equal("continue"), "a step carries its outcome")
	})
})
