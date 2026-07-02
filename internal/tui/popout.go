package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// execEditor hands the terminal to the pop-out editor through Bubble Tea's
// process handoff: the program releases the TTY it renders on, the editor
// runs on it, and the callback's message re-enters Update when it exits —
// stdout carries nothing throughout, keeping the clean-stdout contract
// (DESIGN.md — Output). Specs swap in a synchronous fake, so the flow stays
// hermetic and state-first.
var execEditor = tea.ExecProcess

// editorFinishedMsg re-enters Update when the popped-out $EDITOR exits: err
// carries the handoff's failure, and the pop-out's temp file holds whatever
// the editor saved.
type editorFinishedMsg struct {
	err error
}

// popOut is `e`'s in-flight $EDITOR pop-out (DESIGN.md — Flow §4: the
// escape hatch's heavy-editing keybinding): the schema-blind row the edited
// subtree grafts back onto, its Draft-level Field Path, and the temp file
// the editor edits — holding exactly the one subtree, consumed and removed
// when the editor exits.
type popOut struct {
	row  *treeRow
	path string
	file string
}

// pressPopOut is `e` in navigate mode (DESIGN.md — Keybindings: pop
// node/subtree to $EDITOR): on an addressable schema-blind node it writes
// the subtree to a temp file and hands the terminal to $EDITOR; anywhere
// else it is a no-op with a hint-bar flash. Only schema-blind subtrees pop
// out in MVP — the Type Schema's own fields keep their typed widgets, so
// nothing bypasses the Draft's schema-local checks.
func (c compose) pressPopOut() (compose, tea.Cmd) {
	row := c.focused()
	if row == nil {
		return c, nil
	}
	meta, err := row.node.Metadata()
	if err != nil {
		// The detail pane already surfaces the resolution error.
		return c, nil
	}
	if !meta.SchemaBlind {
		c.notice = "e pops a schema-blind subtree out to $EDITOR — the Type Schema describes " +
			rowDisplayName(row) + ", so its typed widgets compose it"
		return c, nil
	}
	path, addressable := row.draftFieldPath()
	if !addressable {
		c.notice = row.node.FieldPath() + " sits under an uninstantiated collection — " +
			"press a on the collection node to add its first item or key"
		return c, nil
	}

	editor, err := resolveEditor()
	if err != nil {
		c.notice = err.Error()
		return c, nil
	}
	file, err := c.writePopOutFile(path)
	if err != nil {
		c.notice = "writing the $EDITOR temp file failed: " + err.Error()
		return c, nil
	}

	c.popOut = &popOut{row: row, path: path, file: file}
	command := exec.Command(editor[0], append(editor[1:], file)...)
	return c, execEditor(command, func(err error) tea.Msg {
		return editorFinishedMsg{err: err}
	})
}

// resolveEditor resolves the pop-out's editor command line: $EDITOR as
// spelled, arguments included, falling back to vi when it is unset — and a
// binary that cannot run is an in-TUI notice, never a crash.
func resolveEditor() ([]string, error) {
	spelled := strings.TrimSpace(os.Getenv("EDITOR"))
	if spelled == "" {
		spelled = "vi"
	}
	command := strings.Fields(spelled)
	if _, err := exec.LookPath(command[0]); err != nil {
		return nil, fmt.Errorf("no editor to pop out to: %q is not runnable — set $EDITOR to one that is", command[0])
	}
	return command, nil
}

// writePopOutFile writes exactly the one subtree the editor is popping out —
// the current graft's canonical YAML, or a skeleton comment when the Draft
// holds nothing there yet — to a fresh temp file.
func (c compose) writePopOutFile(path string) (string, error) {
	content := popOutSkeleton(path)
	if value, filled := c.draft.ValueAt(path); filled && value.Type == schema.TypeRawYAML {
		content = graftYAMLText(value.Data)
	}

	file, err := os.CreateTemp("", "kubectl-craft-*.yaml")
	if err != nil {
		return "", fmt.Errorf("creating it: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("writing the subtree into %s: %w", file.Name(), err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("closing %s: %w", file.Name(), err)
	}
	return file.Name(), nil
}

// popOutSkeleton is the placeholder an empty schema-blind subtree pops out
// as: comments only, so an editor session that saves nothing new grafts
// nothing.
func popOutSkeleton(path string) string {
	return "# " + path + " — the Type Schema is blind here.\n" +
		"# Compose the subtree as raw YAML; what this file holds on exit grafts into the Draft.\n"
}

// editorFinished consumes the popped-out editor's exit: read what it saved,
// parse it, and graft it at the subtree's Draft-level Field Path. A file
// holding no YAML value — the skeleton untouched, or emptied out — leaves
// the Draft alone; a parse or graft rejection reopens the raw-YAML text
// area over the saved content with the rejection inline, the same error
// path an in-TUI confirm takes. The temp file is removed on every path.
func (c compose) editorFinished(msg editorFinishedMsg) compose {
	pop := c.popOut
	if pop == nil {
		return c
	}
	c.popOut = nil
	defer func() { _ = os.Remove(pop.file) }()

	if msg.err != nil {
		c.notice = "the $EDITOR pop-out failed: " + msg.err.Error()
		return c
	}
	content, err := os.ReadFile(pop.file)
	if err != nil {
		c.notice = "reading the edited subtree back failed: " + err.Error()
		return c
	}

	var probe any
	if yaml.Unmarshal(content, &probe) == nil && probe == nil {
		c.notice = "the editor saved no YAML value for " + pop.path + " — the Draft is untouched"
		return c
	}
	if err := c.draft.GraftYAML(pop.path, string(content)); err != nil {
		return c.reopenRawYAML(pop, string(content), err)
	}
	c.refreshCompleteness()
	return c
}

// reopenRawYAML reopens the raw-YAML text area over what the editor saved,
// the rejection inline — nothing typed out in $EDITOR is lost to a typo.
func (c compose) reopenRawYAML(pop *popOut, content string, rejection error) compose {
	meta, err := pop.row.node.Metadata()
	if err != nil {
		c.notice = rejection.Error()
		return c
	}
	c = c.openRawYAMLEditor(pop.row, pop.path, meta)
	edited := *c.editor
	edited.input = strings.TrimRight(content, "\n")
	edited.rejection = rejection.Error()
	c.editor = &edited
	return c
}
