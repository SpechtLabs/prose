package pipeline

import (
	"testing"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Ginkgo is imported qualified (not dot-imported) in this package because Ginkgo
// exports a Context container that would collide with prose's own Context[T] type.
// Gomega is dot-imported as usual.
func TestPipeline(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Pipeline Suite")
}
