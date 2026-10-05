package pipeline

import (
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// newOfflineManager builds a manager that is never started, so Complete can
// register controllers with it without an apiserver.
func newOfflineManager(scheme *runtime.Scheme) manager.Manager {
	mgr, err := manager.New(&rest.Config{Host: "http://127.0.0.1:0"}, manager.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	Expect(err).NotTo(HaveOccurred())
	return mgr
}

func builtinScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	return scheme
}

var _ = ginkgo.Describe("Complete", func() {
	ginkgo.It("registers the pipeline as a controller with the manager", func() {
		mgr := newOfflineManager(builtinScheme())

		crb, err := For[*corev1.ConfigMap](mgr).
			WithObservability().
			WithFinalizer("example.com/teardown").
			WithPredicates(IgnoreStatusOnlyUpdates()).
			Owns(&corev1.Pod{}).
			Step("converge", func(*Context[*corev1.ConfigMap]) (Outcome, error) { return Continue, nil }).
			Complete()

		Expect(err).NotTo(HaveOccurred())
		Expect(crb).NotTo(BeNil())
	})

	ginkgo.It("refuses a type parameter that isn't a pointer to a struct", func() {
		_, err := For[client.Object](newOfflineManager(builtinScheme())).Complete()

		Expect(err).To(MatchError(ContainSubstring("must be a pointer to a struct implementing client.Object")))
	})

	ginkgo.It("says what to do when the scheme doesn't know the type", func() {
		_, err := For[*corev1.Secret](newOfflineManager(runtime.NewScheme())).Complete()

		Expect(err).To(MatchError(ContainSubstring("prose: cannot find the GroupVersionKind of *v1.Secret")))
	})

	ginkgo.It("says what to do when controller-runtime refuses the controller", func() {
		mgr := newOfflineManager(builtinScheme())
		step := func(*Context[*corev1.Service]) (Outcome, error) { return Continue, nil }

		_, err := For[*corev1.Service](mgr).Step("a", step).Complete()
		Expect(err).NotTo(HaveOccurred())
		_, err = For[*corev1.Service](mgr).Step("b", step).Complete()

		Expect(err).To(MatchError(ContainSubstring("prose: cannot register the service controller with the manager")))
	})
})

var _ = ginkgo.DescribeTable("deriveFinalizer qualifies the kind with its API group",
	func(kind, group, want string) {
		Expect(deriveFinalizer(kind, group)).To(Equal(want))
	},
	ginkgo.Entry("a custom resource", "Memcached", "cache.example.com", "memcached.cache.example.com/finalizer"),
	ginkgo.Entry("a core type", "ConfigMap", "", "configmap/finalizer"),
)
