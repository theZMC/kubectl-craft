package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The capture tool's cluster-facing path runs only when the program is
// invoked explicitly (mise run fixtures:capture-status); this suite covers
// its hermetic pieces, so the fast loop stays Docker-free.
func TestCaptureStatusFixtures(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Capture Status Fixtures Suite")
}
