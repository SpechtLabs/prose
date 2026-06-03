package observability

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Fields", func() {
	It("emits fields in insertion order", func() {
		f := NewFields()
		f.Set("controller", "foo")
		f.Set("dependencies.deployment.duration", "190ms")
		f.Set("dependencies.deployment.outcome", "continue")
		f.Set("dependencies.deployment.image", "ghcr.io/x:v2")

		Expect(f.Flatten()).To(Equal([]any{
			"controller", "foo",
			"dependencies.deployment.duration", "190ms",
			"dependencies.deployment.outcome", "continue",
			"dependencies.deployment.image", "ghcr.io/x:v2",
		}))
	})

	It("keeps a key's position when its value is updated", func() {
		f := NewFields()
		f.Set("a", 1)
		f.Set("b", 2)
		f.Set("a", 99)

		Expect(f.Flatten()).To(Equal([]any{"a", 99, "b", 2}))
	})
})

var _ = DescribeTable("Dotted joins path segments, skipping empties",
	func(parts []string, want string) {
		Expect(Dotted(parts...)).To(Equal(want))
	},
	Entry("a group path and a leaf key", []string{"dependencies", "deployment.image"}, "dependencies.deployment.image"),
	Entry("an empty group path (top-level step)", []string{"", "status.nodes"}, "status.nodes"),
	Entry("a reserved key at the root", []string{"status", "duration"}, "status.duration"),
	Entry("several segments", []string{"a", "b", "c"}, "a.b.c"),
	Entry("a single empty segment", []string{""}, ""),
	Entry("a single segment", []string{"only"}, "only"),
	Entry("spaces in a segment become underscores", []string{"open tunnel", "duration"}, "open_tunnel.duration"),
	Entry("a multi-word Context label", []string{"now that both ends exist", "link.session"}, "now_that_both_ends_exist.link.session"),
)

var _ = DescribeTable("ToAttr maps Go values onto span attributes",
	func(key string, value any, wantKey, wantStr string) {
		kv := ToAttr(key, value)
		Expect(string(kv.Key)).To(Equal(wantKey))
		Expect(kv.Value.Emit()).To(Equal(wantStr))
	},
	Entry("string", "s", "hello", "s", "hello"),
	Entry("bool", "b", true, "b", "true"),
	Entry("int", "i", 7, "i", "7"),
	Entry("int32 (k8s replica counts)", "i32", int32(3), "i32", "3"),
	Entry("int64", "i64", int64(9), "i64", "9"),
	Entry("float64", "f", 1.5, "f", "1.5"),
	Entry("string slice", "slice", []string{"a", "b"}, "slice", `["a","b"]`),
	Entry("fallback to %v", "fallback", struct{ X int }{5}, "fallback", "{5}"),
)
