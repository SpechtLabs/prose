package pipeline

import (
	"errors"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

var errBoom = errors.New("boom")

var _ = ginkgo.Describe("Outcome", func() {
	ginkgo.It("is comparable with Continue as its zero value", func() {
		var zero Outcome
		Expect(zero).To(Equal(Continue))
		Expect(Continue).NotTo(Equal(Requeue))
	})

	ginkgo.DescribeTable("label is a closed set of strings",
		func(o Outcome, want string) {
			Expect(o.label()).To(Equal(want))
		},
		ginkgo.Entry("continue", Continue, "continue"),
		ginkgo.Entry("requeue", Requeue, "requeue"),
		ginkgo.Entry("requeue-after", RequeueAfter(time.Second), "requeue_after"),
		ginkgo.Entry("done", Done, "done"),
	)

	ginkgo.DescribeTable("terminalSuccess distinguishes converged ends from requeues",
		func(o Outcome, want bool) {
			Expect(terminalSuccess(o)).To(Equal(want))
		},
		ginkgo.Entry("continue is a success", Continue, true),
		ginkgo.Entry("done is a success", Done, true),
		ginkgo.Entry("requeue is not", Requeue, false),
		ginkgo.Entry("requeue-after is not", RequeueAfter(time.Second), false),
	)
})

var _ = ginkgo.DescribeTable("translate maps an outcome and error to a controller-runtime result",
	func(o Outcome, inErr error, wantResult ctrl.Result, wantErr error) {
		gotResult, gotErr := translate(o, inErr)
		Expect(gotResult).To(Equal(wantResult))
		if wantErr == nil {
			Expect(gotErr).NotTo(HaveOccurred())
		} else {
			Expect(gotErr).To(MatchError(wantErr))
		}
	},
	ginkgo.Entry("continue, no error, is a converged success", Continue, nil, ctrl.Result{}, nil),
	ginkgo.Entry("done, no error, stops successfully", Done, nil, ctrl.Result{}, nil),
	ginkgo.Entry("requeue, no error, comes back immediately", Requeue, nil, ctrl.Result{Requeue: true}, nil),
	ginkgo.Entry("requeue-after, no error, carries the duration", RequeueAfter(30*time.Second), nil, ctrl.Result{RequeueAfter: 30 * time.Second}, nil),
	ginkgo.Entry("error with continue clears the result", Continue, errBoom, ctrl.Result{}, errBoom),
	ginkgo.Entry("error paired with requeue-after honors the duration", RequeueAfter(5*time.Second), errBoom, ctrl.Result{RequeueAfter: 5 * time.Second}, errBoom),
	ginkgo.Entry("error with requeue uses no deprecated requeue bool", Requeue, errBoom, ctrl.Result{}, errBoom),
	ginkgo.Entry("aborted (shutdown) stops cleanly with no error", aborted, nil, ctrl.Result{}, nil),
)
