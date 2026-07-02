package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The capture tool's cluster-facing path runs only when the program is
// invoked explicitly (mise run fixtures:capture); this suite covers its
// hermetic pieces, so the fast loop stays Docker-free.
func TestCaptureFixtures(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Capture Fixtures Suite")
}
