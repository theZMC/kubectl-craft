package tui_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/thezmc/kubectl-craft/internal/tui"
)

// syncExecEditor swaps the pop-out's Bubble Tea terminal handoff for a
// synchronous fake: the editor command runs inline and its exit re-enters
// Update as the finished message — the whole round trip drives through
// Update, hermetically, with no TTY.
func syncExecEditor() {
	GinkgoHelper()
	DeferCleanup(tui.SwapExecEditor(func(command *exec.Cmd, callback tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return callback(command.Run()) }
	}))
}

// stubEditor writes a hermetic stand-in editor: a shell script that records
// the file it was handed (path and content) and, when rewrite is non-empty,
// rewrites it — one deterministic "editor session", no real editor. It
// returns the script and the two record files.
func stubEditor(rewrite string) (script, pathRecord, contentRecord string) {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	pathRecord = filepath.Join(dir, "handed-path")
	contentRecord = filepath.Join(dir, "handed-content")
	script = filepath.Join(dir, "stub-editor")

	body := "#!/bin/sh\n" +
		`printf '%s' "$1" > ` + pathRecord + "\n" +
		`cat "$1" > ` + contentRecord + "\n"
	if rewrite != "" {
		body += `cat > "$1" <<'STUB_EOF'` + "\n" + rewrite + "STUB_EOF\n"
	}
	Expect(os.WriteFile(script, []byte(body), 0o755)).To(Succeed())
	return script, pathRecord, contentRecord
}

// popOutTuning presses e on Gadget's focused spec.tuning node and drives the
// editor round trip: the handoff command runs the stub editor synchronously
// and its finished message re-enters Update.
func popOutTuning(model tui.Model) tui.Model {
	GinkgoHelper()
	model, cmd := press(model, keyRune('e'))
	Expect(cmd).NotTo(BeNil(), "e on a schema-blind node must hand off to $EDITOR")
	model, _ = press(model, cmd())
	return model
}

// recordedTempFile reads the temp file path the stub editor was handed.
func recordedTempFile(pathRecord string) string {
	GinkgoHelper()
	handed, err := os.ReadFile(pathRecord)
	Expect(err).NotTo(HaveOccurred())
	return string(handed)
}

// recordStdout mirrors root_test's recorder pattern: swap os.Stdout for a
// pipe around the round trip and return everything written to it — the
// clean-stdout contract reserves stdout for the Emitted Manifest, so the
// editor flow must leave it empty.
func recordStdout(run func()) string {
	GinkgoHelper()
	original := os.Stdout
	read, write, pipeErr := os.Pipe()
	Expect(pipeErr).NotTo(HaveOccurred())
	os.Stdout = write
	defer func() { os.Stdout = original }()

	run()

	os.Stdout = original
	Expect(write.Close()).To(Succeed())
	captured, readErr := io.ReadAll(read)
	Expect(readErr).NotTo(HaveOccurred())
	Expect(read.Close()).To(Succeed())
	return string(captured)
}

var _ = Describe("the $EDITOR pop-out", func() {
	When("e pops a schema-blind subtree out to $EDITOR", func() {
		It("hands the editor one skeleton subtree, grafts what it saves, and keeps stdout clean", func() {
			syncExecEditor()
			script, pathRecord, contentRecord := stubEditor("knobs:\n  gain: 3\n")
			GinkgoT().Setenv("EDITOR", script)
			model := focusField(composeGadget(), "spec.tuning")

			out := recordStdout(func() { model = popOutTuning(model) })

			Expect(out).To(BeEmpty(),
				"the round trip must write nothing to stdout — it is reserved for the Emitted Manifest")
			handed, err := os.ReadFile(contentRecord)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(handed)).To(HavePrefix("# spec.tuning"),
				"an empty subtree pops out as a skeleton comment naming its Field Path")
			for _, line := range strings.Split(strings.TrimRight(string(handed), "\n"), "\n") {
				Expect(line).To(HavePrefix("#"), "the skeleton is comments only — nothing but the one subtree")
			}
			Expect(draftValue(model, "spec.tuning")).To(Equal(tuningGraft()),
				"what the editor saved parses and grafts at the popped-out Field Path")
			_, statErr := os.Stat(recordedTempFile(pathRecord))
			Expect(os.IsNotExist(statErr)).To(BeTrue(), "the temp file is removed once consumed")
		})

		It("pops an existing graft out in its canonical YAML spelling", func() {
			syncExecEditor()
			script, pathRecord, contentRecord := stubEditor("mode: turbo\n")
			GinkgoT().Setenv("EDITOR", script)
			model := focusField(graftTuning(composeGadget()), "spec.tuning")

			model = popOutTuning(model)

			handed, err := os.ReadFile(contentRecord)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(handed)).To(Equal("knobs:\n  gain: 3\n"),
				"the editor gets the current subtree, and only it, to edit")
			Expect(draftValue(model, "spec.tuning")).To(Equal(map[string]any{"mode": "turbo"}),
				"the saved YAML replaces the graft")
			_, statErr := os.Stat(recordedTempFile(pathRecord))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})

		It("falls back to vi when $EDITOR is unset", func() {
			syncExecEditor()
			dir := GinkgoT().TempDir()
			script, _, _ := stubEditor("knobs:\n  gain: 3\n")
			Expect(os.Rename(script, filepath.Join(dir, "vi"))).To(Succeed())
			GinkgoT().Setenv("EDITOR", "")
			GinkgoT().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			model := focusField(composeGadget(), "spec.tuning")

			model = popOutTuning(model)

			Expect(draftValue(model, "spec.tuning")).To(Equal(tuningGraft()),
				"an unset $EDITOR falls back to vi — resolved on PATH like any editor")
		})

		It("notices a missing editor instead of crashing", func() {
			GinkgoT().Setenv("EDITOR", "kubectl-craft-no-such-editor")
			model := focusField(composeGadget(), "spec.tuning")

			model, cmd := press(model, keyRune('e'))

			Expect(cmd).To(BeNil(), "nothing runnable means nothing to hand the terminal to")
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue())
			Expect(notice).To(ContainSubstring("set $EDITOR"),
				"a missing editor is an in-TUI notice, never a crash")
			Expect(model.ComposeOpen()).To(BeTrue())
		})

		It("reopens the text area inline when the editor saves malformed YAML", func() {
			syncExecEditor()
			script, pathRecord, _ := stubEditor("knobs: [\n")
			GinkgoT().Setenv("EDITOR", script)
			model := focusField(composeGadget(), "spec.tuning")

			model = popOutTuning(model)

			Expect(model.Editing()).To(BeTrue(),
				"the saved content reopens in the raw-YAML text area — nothing typed in $EDITOR is lost")
			Expect(render(model)).To(ContainSubstring("parsing the raw YAML grafted"),
				"the rejection renders inline, the same error path an in-TUI confirm takes")
			Expect(render(model)).To(ContainSubstring("knobs: ["))
			_, filled := model.DraftValueAt("spec.tuning")
			Expect(filled).To(BeFalse(), "malformed YAML never reaches the Draft")
			_, statErr := os.Stat(recordedTempFile(pathRecord))
			Expect(os.IsNotExist(statErr)).To(BeTrue(), "the temp file is removed on the error path too")
		})

		It("leaves the Draft untouched when the editor saves no YAML value", func() {
			syncExecEditor()
			script, pathRecord, _ := stubEditor("")
			GinkgoT().Setenv("EDITOR", script)
			model := focusField(composeGadget(), "spec.tuning")

			model = popOutTuning(model)

			Expect(model.Editing()).To(BeFalse())
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue())
			Expect(notice).To(ContainSubstring("the Draft is untouched"),
				"quitting the editor over the skeleton composes nothing — not a rejection")
			_, filled := model.DraftValueAt("spec.tuning")
			Expect(filled).To(BeFalse())
			_, statErr := os.Stat(recordedTempFile(pathRecord))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})

	When("e does not serve the focused node", func() {
		It("is a no-op with a hint on a schema-described node", func() {
			model := focusField(composeGadget(), "spec.nickname")

			model, cmd := press(model, keyRune('e'))

			Expect(cmd).To(BeNil())
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue(), "an inert e says why — not an error state")
			Expect(notice).To(ContainSubstring("typed widgets compose it"),
				"only schema-blind subtrees pop out; described fields keep their widgets")
		})

		It("is a no-op with a hint on a schema-blind node inside an uninstantiated placeholder subtree", func() {
			model := expandField(widen(composeDeployment()), "metadata")
			model = expandField(model, "metadata.managedFields")
			model, _ = press(model, keyRune('j')) // the [items] row shares its parent's Field Path
			model, _ = press(model, keyRune('l')) // expand the item schema's fields
			model = focusField(model, "metadata.managedFields.fieldsV1")

			model, cmd := press(model, keyRune('e'))

			Expect(cmd).To(BeNil())
			notice, showing := model.Notice()
			Expect(showing).To(BeTrue())
			Expect(notice).To(ContainSubstring("uninstantiated collection"),
				"nothing under a placeholder is Draft-addressable — grafting needs an item or key first")
		})

		It("documents e in the ? help overlay", func() {
			model, _ := press(composeGadget(), keyRune('?'))

			Expect(render(model)).To(ContainSubstring("pop a schema-blind subtree out to $EDITOR"))
		})
	})
})
