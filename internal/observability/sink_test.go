package observability

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

var _ = Describe("Sink", func() {
	Describe("the zero-option sink", func() {
		It("defaults to working no-ops and a nil recorder", func() {
			s := NewSink()
			Expect(s.tracer).NotTo(BeNil(), "tracer must default to a no-op, not nil")
			Expect(s.recorder).To(BeNil(), "recorder must be nil; events are opt-in")
			Expect(s.metrics).NotTo(BeNil(), "metrics must always be present")
			Expect(func() { s.logger.Info("noop") }).NotTo(Panic())
		})
	})

	Describe("options", func() {
		It("set the tracer and recorder", func() {
			tr := tracenoop.NewTracerProvider().Tracer("test")
			rec := record.NewFakeRecorder(10)
			s := NewSink(Otel(tr), WideEvents(logr.Discard()), Recorder(rec))

			Expect(s.tracer).To(Equal(tr))
			Expect(s.recorder).To(Equal(rec))
		})

		It("replace the clock, and keep the wall clock when given none", func() {
			fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

			Expect(NewSink(Clock(func() time.Time { return fixed })).Now()).To(Equal(fixed))
			Expect(NewSink(Clock(nil)).Now()).To(BeTemporally("~", time.Now(), time.Minute))
		})
	})

	Describe("Event", func() {
		It("no-ops without a recorder", func() {
			s := NewSink()
			pod := &corev1.Pod{Name: "foo"}
			Expect(func() {
				s.Event(pod, corev1.EventTypeNormal, "Reason", "msg %d", 1)
			}).NotTo(Panic())
		})

		It("dispatches a formatted event to the recorder", func() {
			rec := record.NewFakeRecorder(10)
			s := NewSink(Recorder(rec))
			pod := &corev1.Pod{Name: "foo"}

			s.Event(pod, corev1.EventTypeNormal, "Scaled", "scaled to %d", 3)

			var got string
			Expect(rec.Events).To(Receive(&got))
			Expect(got).To(ContainSubstring("Scaled"))
			Expect(got).To(ContainSubstring("scaled to 3"))
		})
	})
})
