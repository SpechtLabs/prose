package observability

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var _ = Describe("StepMetrics", func() {
	It("observes step durations keyed by controller, step, and outcome", func() {
		reg := prometheus.NewRegistry()
		m := NewStepMetrics(reg)

		m.Observe("wormhole", "open-tunnel", "continue", 5*time.Millisecond)
		m.Observe("wormhole", "open-tunnel", "continue", 7*time.Millisecond)
		m.Observe("wormhole", "route-traffic", "requeue", time.Millisecond)

		mfs, err := reg.Gather()
		Expect(err).NotTo(HaveOccurred())

		var fam *dto.MetricFamily
		for _, mf := range mfs {
			if mf.GetName() == "prose_step_duration_seconds" {
				fam = mf
			}
		}
		Expect(fam).NotTo(BeNil(), "prose_step_duration_seconds must be registered")

		counts := map[string]uint64{}
		for _, met := range fam.GetMetric() {
			key := ""
			for _, l := range met.GetLabel() {
				key += l.GetName() + "=" + l.GetValue() + " "
			}
			counts[key] = met.GetHistogram().GetSampleCount()
		}

		Expect(counts["controller=wormhole outcome=continue step=open-tunnel "]).To(Equal(uint64(2)))
		Expect(counts["controller=wormhole outcome=requeue step=route-traffic "]).To(Equal(uint64(1)))
	})
})
