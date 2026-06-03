//go:build envtest

// Package pipeline integration specs run against a real kube-apiserver via
// envtest. They cover behaviors a fake client cannot model: server-side apply
// (owner-ref persistence and idempotency) and finalizer add/remove end to end.
// They reconcile built-in ConfigMaps, so no custom CRD scaffolding is needed.
//
// This file is built only with -tags envtest, so it contributes its BeforeSuite
// (which starts the apiserver) and its specs only when the integration tag is set;
// a plain `go test` run executes the unit specs alone, with no apiserver.
//
// Run with: KUBEBUILDER_ASSETS=<path> go test -tags envtest ./...
package pipeline

import (
	"context"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/spechtlabs/prose/internal/observability"
)

var (
	itEnv    *envtest.Environment
	itClient client.Client
	itScheme *runtime.Scheme
)

var _ = ginkgo.BeforeSuite(func() {
	itEnv = &envtest.Environment{}
	cfg, err := itEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	itScheme = runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(itScheme)).To(Succeed())

	itClient, err = client.New(cfg, client.Options{Scheme: itScheme})
	Expect(err).NotTo(HaveOccurred())
})

var _ = ginkgo.AfterSuite(func() {
	if itEnv != nil {
		Expect(itEnv.Stop()).To(Succeed())
	}
})

func itRunner(root, finalize *node[*corev1.ConfigMap]) *runner[*corev1.ConfigMap] {
	sink := observability.NewSink()
	return &runner[*corev1.ConfigMap]{
		client:       itClient,
		scheme:       itScheme,
		sink:         sink,
		newObject:    func() *corev1.ConfigMap { return &corev1.ConfigMap{} },
		root:         root,
		finalize:     finalize,
		controller:   "configmap",
		finalizer:    testFinalizer,
		fieldOwner:   "prose-configmap",
		baseLogger:   sink.Logger(),
		conflicts:    newConflictTracker(),
		maxConflicts: maxQuietConflicts,
	}
}

func itReq(name string) reconcile.Request {
	return reqFor("default", name)
}

var _ = ginkgo.Describe("integration against a real apiserver", func() {
	ginkgo.It("applies an owned object with a controller reference, idempotently", func() {
		ctx := context.Background()
		owner := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ssa-owner", Namespace: "default"}}
		Expect(itClient.Create(ctx, owner)).To(Succeed())

		applyChild := func(rctx *Context[*corev1.ConfigMap]) (Outcome, error) {
			child := &corev1.ConfigMap{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{Name: "ssa-child", Namespace: "default"},
				Data:       map[string]string{"key": "value"},
			}
			if err := rctx.Apply(child); err != nil {
				return Requeue, err
			}
			return Continue, nil
		}
		root := &node[*corev1.ConfigMap]{isGroup: true, children: []*node[*corev1.ConfigMap]{
			{name: "child", fn: applyChild},
		}}
		r := itRunner(root, nil)

		_, err := r.Reconcile(ctx, itReq("ssa-owner"))
		Expect(err).NotTo(HaveOccurred())

		child := &corev1.ConfigMap{}
		Expect(itClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "ssa-child"}, child)).To(Succeed())
		ref := metav1.GetControllerOf(child)
		Expect(ref).NotTo(BeNil())
		Expect(ref.Name).To(Equal("ssa-owner"))
		Expect(child.Data).To(HaveKeyWithValue("key", "value"))
		rv := child.ResourceVersion

		// Second reconcile must be idempotent: same desired state, no spurious write.
		_, err = r.Reconcile(ctx, itReq("ssa-owner"))
		Expect(err).NotTo(HaveOccurred())

		again := &corev1.ConfigMap{}
		Expect(itClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "ssa-child"}, again)).To(Succeed())
		Expect(again.ResourceVersion).To(Equal(rv), "resourceVersion must not change on an idempotent reconcile")
	})

	ginkgo.It("adds a finalizer, then runs the Finalize group and removes it on delete", func() {
		ctx := context.Background()
		obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "fin", Namespace: "default"}}
		Expect(itClient.Create(ctx, obj)).To(Succeed())

		var finalized bool
		finalize := &node[*corev1.ConfigMap]{name: "teardown", isGroup: true, children: []*node[*corev1.ConfigMap]{
			{name: "release", fn: func(rctx *Context[*corev1.ConfigMap]) (Outcome, error) {
				finalized = true
				return Continue, nil
			}},
		}}
		r := itRunner(&node[*corev1.ConfigMap]{isGroup: true}, finalize)

		_, err := r.Reconcile(ctx, itReq("fin"))
		Expect(err).NotTo(HaveOccurred())

		got := &corev1.ConfigMap{}
		Expect(itClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "fin"}, got)).To(Succeed())
		Expect(got.Finalizers).To(ContainElement(testFinalizer))

		Expect(itClient.Delete(ctx, got)).To(Succeed())
		_, err = r.Reconcile(ctx, itReq("fin"))
		Expect(err).NotTo(HaveOccurred())
		Expect(finalized).To(BeTrue(), "the Finalize group must run on the deletion path")

		Eventually(func() bool {
			err := itClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "fin"}, &corev1.ConfigMap{})
			return err != nil
		}, 5*time.Second, 50*time.Millisecond).Should(BeTrue(), "object should be gone after finalizer removal")
	})
})
