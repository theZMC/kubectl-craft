package tui_test

import (
	"context"
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

var _ = Describe("launching the Session shell", func() {
	When("no controlling terminal exists", func() {
		// A spec process has a controlling terminal, so the failure drives
		// through the export_test seam; the message is load-bearing — it is
		// the whole no-TTY user experience (DESIGN.md — Output: the caller
		// surfaces it on stderr as a non-zero exit).
		It("returns before any program starts, saying what is needed and keeping the cause", func() {
			restore := tui.SwapOpenTTY(func() (*os.File, error) {
				return nil, errors.New("open /dev/tty: no such device or address")
			})
			defer restore()

			result, err := tui.Run(context.Background(), nil, nil, nil, nil, "", nil)

			Expect(err).To(MatchError(ContainSubstring(
				"kubectl craft is interactive and needs a controlling terminal",
			)),
				"the message says what kubectl craft needs — a terminal to run in")
			Expect(err).To(MatchError(ContainSubstring("no such device")),
				"the underlying open's own words stay attached for debugging")
			Expect(result.Emitted).To(BeFalse(), "a Session that never started emits nothing")
		})
	})
})
