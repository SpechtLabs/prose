package prose

import (
	"testing"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Ginkgo is imported qualified here because prose re-exports a Context[T] type,
// which would collide with Ginkgo's dot-imported Context container.
func TestProse(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Prose Suite")
}
