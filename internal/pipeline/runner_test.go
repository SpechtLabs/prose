package pipeline

import (
	"context"
	"errors"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	humane "github.com/sierrasoftworks/humane-errors-go"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/spechtlabs/prose/internal/observability"
)

const testFinalizer = "configmap.test/finalizer"

func cmStep(log *[]string, name string, out Outcome, err error) *node[*corev1.ConfigMap] {
	return &node[*corev1.ConfigMap]{
		name: name,
		fn: func(rctx *Context[*corev1.ConfigMap]) (Outcome, error) {
			*log = append(*log, name)
			return out, err
		},
	}
}

func newTestRunner(root, finalize *node[*corev1.ConfigMap], objs ...client.Object) (*runner[*corev1.ConfigMap], *logRecorder, client.Client) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	logger, rec := newRecLogger()
	sink := observability.NewSink(observability.WideEvents(logger))
	r := &runner[*corev1.ConfigMap]{
		client:       fc,
		scheme:       scheme,
		sink:         sink,
		newObject:    func() *corev1.ConfigMap { return &corev1.ConfigMap{} },
		root:         root,
		finalize:     finalize,
		controller:   "configmap",
		finalizer:    testFinalizer,
		fieldOwner:   "prose-configmap",
		baseLogger:   sink.Logger().WithValues("controller", "configmap"),
		conflicts:    newConflictTracker(),
		maxConflicts: maxQuietConflicts,
	}
	return r, rec, fc
}

func reqFor(ns, name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}}
}

func cm(name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"}}
}

var _ = ginkgo.Describe("the runner", func() {
	ginkgo.It("runs the pipeline and emits exactly one wide event on the happy path", func() {
		var log []string
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			{name: "status", fn: func(rctx *Context[*corev1.ConfigMap]) (Outcome, error) {
				log = append(log, "status")
				rctx.Set("status.nodes", 3)
				return Continue, nil
			}},
		}}
		r, rec, _ := newTestRunner(root, nil, cm("widget"))

		res, err := r.Reconcile(context.Background(), reqFor("ns", "widget"))

		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
		Expect(log).To(Equal([]string{"status"}))

		line, ok := rec.line("reconcile")
		Expect(ok).To(BeTrue(), "expected exactly one wide event named 'reconcile'")
		Expect(line.value("controller")).To(Equal("configmap"))
		Expect(line.value("name")).To(Equal("widget"))
		Expect(line.value("result")).To(Equal("continue"))
		Expect(line.value("status.nodes")).To(Equal(3))
		Expect(line.value("status.outcome")).To(Equal("continue"))
	})

	ginkgo.It("treats a vanished object as a no-op with no wide event", func() {
		root := &node[*corev1.ConfigMap]{isGroup: true}
		r, rec, _ := newTestRunner(root, nil)

		res, err := r.Reconcile(context.Background(), reqFor("ns", "ghost"))

		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
		_, ok := rec.line("reconcile")
		Expect(ok).To(BeFalse(), "a vanished object should not emit a wide event")
	})

	ginkgo.It("maps a Requeue outcome to a requeue result", func() {
		var log []string
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			cmStep(&log, "s", Requeue, nil),
		}}
		r, _, _ := newTestRunner(root, nil, cm("widget"))

		res, err := r.Reconcile(context.Background(), reqFor("ns", "widget"))

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Requeue).To(BeTrue())
	})

	ginkgo.It("propagates a step error, stops the pipeline, and still emits", func() {
		var log []string
		boom := humane.Wrap(errors.New("io timeout"), "list pods", "check RBAC")
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			cmStep(&log, "first", Continue, nil),
			cmStep(&log, "second", Requeue, boom),
			cmStep(&log, "third", Continue, nil),
		}}
		r, rec, _ := newTestRunner(root, nil, cm("widget"))

		_, err := r.Reconcile(context.Background(), reqFor("ns", "widget"))

		Expect(err).To(MatchError(boom))
		Expect(log).To(Equal([]string{"first", "second"}))
		line, ok := rec.line("reconcile")
		Expect(ok).To(BeTrue(), "error path must still emit the wide event")
		Expect(line.value("result")).To(Equal("error"))
		Expect(line.value("second.error")).To(Equal("list pods"))
	})

	ginkgo.It("runs error-cleanups before always-cleanups on the failure path", func() {
		var events []string
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			{name: "acquire", fn: func(rctx *Context[*corev1.ConfigMap]) (Outcome, error) {
				rctx.DeferCleanup(func() error { events = append(events, "always"); return nil })
				rctx.DeferErrorCleanup(func() error { events = append(events, "compensate"); return nil })
				return Continue, nil
			}},
			{name: "fail", fn: func(rctx *Context[*corev1.ConfigMap]) (Outcome, error) {
				return Requeue, errors.New("downstream failure")
			}},
		}}
		r, _, _ := newTestRunner(root, nil, cm("widget"))

		_, _ = r.Reconcile(context.Background(), reqFor("ns", "widget"))

		Expect(events).To(Equal([]string{"compensate", "always"}))
	})

	ginkgo.It("skips compensation cleanups on the success path", func() {
		var events []string
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			{name: "acquire", fn: func(rctx *Context[*corev1.ConfigMap]) (Outcome, error) {
				rctx.DeferCleanup(func() error { events = append(events, "always"); return nil })
				rctx.DeferErrorCleanup(func() error { events = append(events, "compensate"); return nil })
				return Continue, nil
			}},
		}}
		r, _, _ := newTestRunner(root, nil, cm("widget"))

		_, _ = r.Reconcile(context.Background(), reqFor("ns", "widget"))

		Expect(events).To(Equal([]string{"always"}))
	})

	ginkgo.It("adds the finalizer and runs the normal pipeline on the non-deletion path", func() {
		var log []string
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			cmStep(&log, "converge", Continue, nil),
		}}
		finalize := &node[*corev1.ConfigMap]{name: "teardown", isGroup: true, children: []*node[*corev1.ConfigMap]{
			cmStep(&log, "release", Continue, nil),
		}}
		r, _, fc := newTestRunner(root, finalize, cm("widget"))

		_, err := r.Reconcile(context.Background(), reqFor("ns", "widget"))
		Expect(err).NotTo(HaveOccurred())

		got := &corev1.ConfigMap{}
		Expect(fc.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "widget"}, got)).To(Succeed())
		Expect(got.Finalizers).To(ContainElement(testFinalizer))
		Expect(log).To(Equal([]string{"converge"}))
	})

	ginkgo.It("runs only the Finalize group and removes the finalizer on the deletion path", func() {
		var log []string
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			cmStep(&log, "converge", Continue, nil),
		}}
		finalize := &node[*corev1.ConfigMap]{name: "teardown", isGroup: true, children: []*node[*corev1.ConfigMap]{
			cmStep(&log, "release", Continue, nil),
		}}

		obj := cm("widget")
		obj.Finalizers = []string{testFinalizer}
		r, _, fc := newTestRunner(root, finalize, obj)
		Expect(fc.Delete(context.Background(), obj)).To(Succeed())

		_, err := r.Reconcile(context.Background(), reqFor("ns", "widget"))
		Expect(err).NotTo(HaveOccurred())

		Expect(log).To(Equal([]string{"release"}))
		err = fc.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "widget"}, &corev1.ConfigMap{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "object should be gone after finalizer removal")
	})

	ginkgo.It("reports a shutdown-canceled step as aborted, not an error", func() {
		// Reproduces a Ctrl-C during an in-flight rctx.Apply: the step returns an
		// error wrapping context.Canceled. The runner must not surface it to
		// controller-runtime (which would log ERROR + a stack trace) and must label
		// the wide event aborted rather than error.
		var ran bool
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			{name: "open-tunnel", fn: func(rctx *Context[*corev1.ConfigMap]) (Outcome, error) {
				ran = true
				return Requeue, humane.Wrap(context.Canceled, "apply tunnel manifest",
					"verify the controller can create ConfigMaps in this namespace")
			}},
		}}
		r, rec, _ := newTestRunner(root, nil, cm("voyager"))

		res, err := r.Reconcile(context.Background(), reqFor("ns", "voyager"))

		Expect(ran).To(BeTrue())
		Expect(err).NotTo(HaveOccurred(), "a shutdown-canceled reconcile must not surface an error")
		Expect(res).To(Equal(ctrl.Result{}))
		line, ok := rec.line("reconcile")
		Expect(ok).To(BeTrue())
		Expect(line.value("result")).To(Equal("aborted"))
		Expect(line.value("open-tunnel.outcome")).To(Equal("aborted"))
		Expect(line.value("open-tunnel.error")).To(BeNil(), "no error field on an aborted step")
	})

	ginkgo.It("swallows a canceled fetch quietly with no wide event", func() {
		scheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
		base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm("widget")).Build()
		canceling := interceptor.NewClient(base, interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return context.Canceled
			},
		})
		logger, rec := newRecLogger()
		sink := observability.NewSink(observability.WideEvents(logger))
		r := &runner[*corev1.ConfigMap]{
			client:       canceling,
			scheme:       scheme,
			sink:         sink,
			newObject:    func() *corev1.ConfigMap { return &corev1.ConfigMap{} },
			root:         &node[*corev1.ConfigMap]{isGroup: true},
			controller:   "configmap",
			finalizer:    testFinalizer,
			fieldOwner:   "prose-configmap",
			baseLogger:   sink.Logger(),
			conflicts:    newConflictTracker(),
			maxConflicts: maxQuietConflicts,
		}

		res, err := r.Reconcile(context.Background(), reqFor("ns", "widget"))

		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
		_, ok := rec.line("reconcile")
		Expect(ok).To(BeFalse(), "a canceled fetch has no transaction to record")
	})
})
