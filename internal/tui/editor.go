package tui

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// editorKind names the widget flavor a leaf's Metadata() picks — the
// type-appropriate, HTML-form-feel widgets of DESIGN.md Flow §4.
type editorKind int

const (
	// editorText is the text input: strings, and int-or-string fields in
	// either spelling.
	editorText editorKind = iota
	// editorNumeric is the numeric input for integers and numbers,
	// validated on confirm — parse failures and schema bounds render
	// inline.
	editorNumeric
	// editorToggle is the boolean toggle.
	editorToggle
	// editorSelect is the enum select list: ↑/↓ choose among the Type
	// Schema's admissible values, Enter confirms.
	editorSelect
	// editorRawYAML is the raw-YAML escape hatch's multiline text area
	// (DESIGN.md — Flow §4): the widget a schema-blind leaf opens instead
	// of a typed one. Enter types a newline, so Ctrl-s confirms — the
	// buffer parses on confirm and grafts into the Draft at the leaf's
	// Field Path; a parse rejection renders inline and commits nothing.
	editorRawYAML
)

// widgetFor picks the widget for a leaf from its Metadata(): an enum takes
// the select list whatever its underlying type; everything else routes by
// display type. A leaf no widget serves (a bare untypeable position) reports
// false, and Enter stays inert on it.
func widgetFor(meta schema.Metadata) (editorKind, bool) {
	if len(meta.Enum) > 0 {
		return editorSelect, true
	}
	switch meta.Type {
	case "boolean":
		return editorToggle, true
	case "integer", "number":
		return editorNumeric, true
	case "string", "int-or-string":
		return editorText, true
	}
	return 0, false
}

// fieldEditor is edit mode (DESIGN.md — Keybindings): the value widget Enter
// opens on a leaf. Enter confirms the value into the Draft — a schema
// rejection renders inline and commits nothing — and Esc cancels back to
// navigate mode without mutating; every navigate key is inert meanwhile.
type fieldEditor struct {
	row  *treeRow
	meta schema.Metadata
	kind editorKind

	// path is the row's Draft-level Field Path — bracket selectors
	// included on an instantiated item or key leaf — the position the
	// confirmed value Sets.
	path string

	// input is the text and numeric widgets' buffer.
	input string

	// toggle is the boolean widget's state.
	toggle bool

	// cursor is the select widget's highlighted index into meta.Enum.
	cursor int

	// rejection is the last confirm's rejection — a parse failure or the
	// Draft's schema-local check — rendered inline in the widget.
	rejection string
}

// newFieldEditor opens the widget over the leaf's current state: a value the
// Draft already holds prefills the widget; otherwise a boolean or enum
// widget starts on the schema default when one is declared, and the text
// widgets start empty — the dimmed placeholder stays a placeholder.
func newFieldEditor(row *treeRow, path string, meta schema.Metadata, kind editorKind, value schema.Value, filled bool) fieldEditor {
	editor := fieldEditor{row: row, path: path, meta: meta, kind: kind}
	switch kind {
	case editorToggle:
		if filled {
			editor.toggle, _ = value.Data.(bool)
		} else if fallback, isBool := meta.Default.(bool); isBool {
			editor.toggle = fallback
		}
	case editorSelect:
		spelled := ""
		if filled {
			spelled = renderScalar(value.Data)
		} else if meta.Default != nil {
			spelled = renderScalar(meta.Default)
		}
		if index := slices.Index(meta.Enum, spelled); index >= 0 {
			editor.cursor = index
		}
	default:
		if filled {
			editor.input = renderScalar(value.Data)
		}
	}
	return editor
}

// update applies one edit-mode key press to the widget. Enter and Esc are
// the caller's business (confirm and cancel); everything else routes by
// widget flavor, and any key clears a lingering rejection — the message
// belongs to the confirm it answered.
func (e fieldEditor) update(key tea.KeyMsg) fieldEditor {
	e.rejection = ""
	switch e.kind {
	case editorToggle:
		return e.updateToggle(key)
	case editorSelect:
		return e.updateSelect(key)
	case editorRawYAML:
		return e.updateRawYAML(key)
	default:
		return e.updateText(key)
	}
}

// updateRawYAML types into the raw-YAML text area: Enter breaks the line
// (Ctrl-s is the confirm, the caller's business) and Tab types the two-space
// indent step — YAML forbids tab indentation, so a literal tab could only
// ever be a parse rejection. Everything else is the text widget's grammar.
func (e fieldEditor) updateRawYAML(key tea.KeyMsg) fieldEditor {
	switch key.Type {
	case tea.KeyEnter:
		e.input += "\n"
		return e
	case tea.KeyTab:
		e.input += "  "
		return e
	default:
		return e.updateText(key)
	}
}

// updateText types into the text and numeric widgets' buffer: printable keys
// append, backspace erases one rune, and everything else is inert.
func (e fieldEditor) updateText(key tea.KeyMsg) fieldEditor {
	switch {
	case key.String() == "backspace":
		if e.input != "" {
			runes := []rune(e.input)
			e.input = string(runes[:len(runes)-1])
		}
	case key.Type == tea.KeySpace:
		e.input += " "
	case key.Type == tea.KeyRunes && !key.Alt:
		e.input += string(key.Runes)
	}
	return e
}

// updateToggle flips the boolean widget on space and the horizontal keys —
// a two-state toggle has no direction to move, only a flip.
func (e fieldEditor) updateToggle(key tea.KeyMsg) fieldEditor {
	switch key.String() {
	case " ", "left", "right", "h", "l":
		e.toggle = !e.toggle
	}
	return e
}

// updateSelect moves the enum select's highlight with ↑/↓ and Ctrl-j/k,
// clamping at the list's edges — the admissible values are fixed, so
// printable keys are inert: nothing outside the enum can be spelled.
func (e fieldEditor) updateSelect(key tea.KeyMsg) fieldEditor {
	switch key.String() {
	case "up", "ctrl+k":
		e.cursor = max(e.cursor-1, 0)
	case "down", "ctrl+j":
		e.cursor = min(e.cursor+1, len(e.meta.Enum)-1)
	}
	return e
}

// confirmValue converts the widget's state into the Go value Draft.Set
// checks: a toggle confirms its bool, a select confirms the highlighted enum
// value in the field's own type, the numeric widget parses its buffer (a
// failure rejects inline, before the Draft is asked), and the text widget
// confirms its string — an int-or-string buffer confirms the integer
// spelling when it parses as one.
func (e fieldEditor) confirmValue() (any, error) {
	switch e.kind {
	case editorToggle:
		return e.toggle, nil
	case editorSelect:
		return typedEnumValue(e.meta.Enum[e.cursor], e.meta.Type), nil
	case editorNumeric:
		return e.parseNumeric()
	default:
		if e.meta.Type == "int-or-string" {
			if count, err := strconv.ParseInt(e.input, 10, 64); err == nil {
				return count, nil
			}
		}
		return e.input, nil
	}
}

// parseNumeric parses the numeric widget's buffer by display type; what does
// not parse never reaches the Draft.
func (e fieldEditor) parseNumeric() (any, error) {
	if e.meta.Type == "integer" {
		parsed, err := strconv.ParseInt(e.input, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer — the Type Schema wants a whole number here", e.input)
		}
		return parsed, nil
	}
	parsed, err := strconv.ParseFloat(e.input, 64)
	if err != nil {
		return nil, fmt.Errorf("%q is not a number — the Type Schema wants a numeric value here", e.input)
	}
	return parsed, nil
}

// typedEnumValue converts one enum value from its display spelling back to
// the field's own type, so an integer enum confirms an integer. A spelling
// that resists conversion confirms as written and lets the Draft's
// schema-local check speak.
func typedEnumValue(spelled, displayType string) any {
	switch displayType {
	case "integer", "int-or-string":
		if parsed, err := strconv.ParseInt(spelled, 10, 64); err == nil {
			return parsed
		}
	case "number":
		if parsed, err := strconv.ParseFloat(spelled, 64); err == nil {
			return parsed
		}
	case "boolean":
		if parsed, err := strconv.ParseBool(spelled); err == nil {
			return parsed
		}
	}
	return spelled
}

// inlineValue is the being-edited value the tree row renders inline at the
// node — the widget's live state, cursor mark included for the typed
// buffers.
func (e fieldEditor) inlineValue() string {
	switch e.kind {
	case editorToggle:
		return strconv.FormatBool(e.toggle)
	case editorSelect:
		return e.meta.Enum[e.cursor]
	case editorRawYAML:
		// The multiline buffer lives in the detail pane; the tree row
		// just says the escape hatch is composing here.
		return "raw YAML — editing"
	default:
		return e.input + "▏"
	}
}

// hints is the edit-mode hint bar: the widget's own keys plus the modal
// grammar — Enter confirms, Esc cancels (DESIGN.md — Keybindings).
func (e fieldEditor) hints() string {
	switch e.kind {
	case editorToggle:
		return "space/←/→ toggle · enter confirm · esc cancel"
	case editorSelect:
		return "↑/↓ choose · enter confirm · esc cancel"
	case editorRawYAML:
		return "type raw YAML · enter newline · ctrl+s confirm · esc cancel"
	default:
		return "type a value · enter confirm · esc cancel"
	}
}

// viewLines renders the open widget for the detail pane: the field, the
// widget itself, and the inline rejection when the last confirm was turned
// away.
func (e fieldEditor) viewLines() []string {
	editing := "editing — " + e.meta.Type
	if e.kind == editorRawYAML {
		editing = "composing raw YAML — the Type Schema is blind here"
	}
	lines := []string{highlightedStyle.Render(e.row.label), editing, ""}
	lines = append(lines, e.widgetLines()...)
	if e.rejection != "" {
		lines = append(lines, "", highlightedStyle.Render(e.rejection))
	}
	return lines
}

// widgetLines renders the widget's body by flavor.
func (e fieldEditor) widgetLines() []string {
	switch e.kind {
	case editorToggle:
		return []string{radioLine(e.toggle)}
	case editorSelect:
		lines := make([]string, 0, len(e.meta.Enum))
		for index, value := range e.meta.Enum {
			cursor := "  "
			if index == e.cursor {
				cursor = "> "
				value = highlightedStyle.Render(value)
			}
			lines = append(lines, cursor+value)
		}
		return lines
	case editorRawYAML:
		// The multiline buffer, cursor on its last line.
		return strings.Split(e.input+"▏", "\n")
	default:
		return []string{"> " + e.input + "▏"}
	}
}

// radioLine renders the boolean toggle as a two-option radio row.
func radioLine(on bool) string {
	trueMark, falseMark := "( )", "(•)"
	if on {
		trueMark, falseMark = "(•)", "( )"
	}
	return trueMark + " true   " + falseMark + " false"
}

// graftYAMLText spells a raw-YAML graft's parsed value back as canonical
// two-space YAML — the text the escape hatch reopens on and the $EDITOR
// pop-out writes out. The Draft stores the parsed value, so content
// round-trips while spelling normalizes (mirroring Emit's contract).
func graftYAMLText(data any) string {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(data); err != nil {
		return fmt.Sprint(data)
	}
	_ = encoder.Close()
	return buffer.String()
}

// graftLines splits a graft's canonical YAML spelling into display lines.
func graftLines(data any) []string {
	return strings.Split(strings.TrimRight(graftYAMLText(data), "\n"), "\n")
}

// graftSummary is a grafted subtree's distinct one-line rendering — "raw
// YAML (N lines)" — for the tree row, the detail pane, and the unset
// confirm: a graft is opaque to the Type Schema, so its size is what there
// is to say about it.
func graftSummary(data any) string {
	count := len(graftLines(data))
	noun := "lines"
	if count == 1 {
		noun = "line"
	}
	return fmt.Sprintf("raw YAML (%d %s)", count, noun)
}
