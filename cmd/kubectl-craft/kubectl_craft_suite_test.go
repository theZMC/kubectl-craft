package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

func TestKubectlCraft(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "KubectlCraft Suite")
}

var _ = Describe("the kubectl-craft binary", func() {
	It("reports the build-time version via --version without starting a Session", func() {
		out := &bytes.Buffer{}
		launched := false
		cmd := newRootCommand(func(context.Context, []data.Kind, data.Fetcher, []data.GroupVersion, *tui.DeepLink) (tui.Result, error) {
			launched = true
			return tui.Result{}, nil
		})
		cmd.SetOut(out)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{"--version"})

		Expect(cmd.Execute()).To(Succeed())
		Expect(out.String()).To(ContainSubstring(version))
		Expect(launched).To(BeFalse(), "--version must answer without a Session shell")
	})
})
