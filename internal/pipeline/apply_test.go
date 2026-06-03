package pipeline

import (
	"context"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/spechtlabs/prose/internal/observability"
)

var _ = ginkgo.Describe("Context.Apply", func() {
	// Full server-side-apply persistence is verified in the envtest suite (fake
	// clients do not implement SSA create). Here we verify Apply's wiring: it
	// stamps the controller owner reference and issues an apply patch with a field
	// owner.
	ginkgo.It("stamps the owner reference and issues a forced apply patch", func() {
		scheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
		owner := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "ns", UID: "uid-123"},
		}

		var (
			gotPatchType  types.PatchType
			gotFieldOwner string
			gotForce      bool
		)
		base := fake.NewClientBuilder().WithScheme(scheme).Build()
		capturing := interceptor.NewClient(base, interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				gotPatchType = patch.Type()
				po := &client.PatchOptions{}
				po.ApplyOptions(opts)
				gotFieldOwner = po.FieldManager
				gotForce = po.Force != nil && *po.Force
				return nil
			},
		})

		rctx := newContext[*corev1.ConfigMap](context.Background(), capturing, scheme, observability.NewSink(), "configmap", "prose", owner)

		dep := &appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "ns"},
		}
		Expect(rctx.Apply(dep)).To(Succeed())

		ref := metav1.GetControllerOf(dep)
		Expect(ref).NotTo(BeNil())
		Expect(ref.Name).To(Equal("owner"))
		Expect(ref.UID).To(Equal(types.UID("uid-123")))
		Expect(gotPatchType).To(Equal(types.ApplyPatchType))
		Expect(gotFieldOwner).To(Equal("prose"))
		Expect(gotForce).To(BeTrue(), "apply must force ownership")
	})
})
