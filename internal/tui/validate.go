package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/validate"
)

const (
	// findingMarker flags a tree row a mappable Validate finding names —
	// visually distinct from the required-but-unset marker, so "the
	// server rejected this" never reads as "you haven't filled this".
	findingMarker = "✘"

	// validateTransitHints is the hint bar while a Validate's dry-run is
	// in flight: cancelling returns to composing with the Draft
	// untouched — the same loading grammar a version switch uses.
	validateTransitHints = "esc/q cancel the Validate · ctrl+c quit"

	// resultsPaneHints is the hint bar while the Validate results pane
	// is open, in the modal grammar.
	resultsPaneHints = "esc dismiss"
)

// validateRequestedMsg asks the Session shell to run the manual Validate:
// it carries the emitted Draft — by construction exactly the bytes the
// exit ramp would print, pure emission through Draft.Emit — for the shell
// to resolve the namespace against and POST as a server-side dry-run.
type validateRequestedMsg struct {
	manifest []byte
}

// validateOutcomeMsg delivers one Validate flight's Outcome back to the
// shell. seq is the flight's number: an outcome landing with a stale
// sequence — Esc cancelled the wait, or a newer `v` superseded it — is
// dropped, never rendered.
type validateOutcomeMsg struct {
	seq     int
	outcome data.Outcome
}

// validationState is the compose view's rendered view of one Validate
// outcome: exactly one of clean, unavailable, or the Invalid triple
// (summary + mapped + unmappable) is meaningful.
type validationState struct {
	// clean marks a dry-run the server accepted — the positive
	// confirmation the status line renders.
	clean bool

	// unavailable carries the Unavailable reason — Validate could not
	// run or could not be trusted; it renders in the results pane,
	// visually and verbally distinct, and never marks tree nodes.
	unavailable string

	// summary is the Invalid outcome's Status-level failure, verbatim.
	summary validate.Summary

	// mapped lists the mappable findings' Draft-level Field Paths in
	// server order (first appearance), the order `n` cycles through;
	// messages carries each path's finding messages for the marker and
	// the detail pane.
	mapped   []string
	messages map[string][]string

	// unmappable lists the findings the tree cannot pin — no Field Path,
	// or a path the open Kind's tree doesn't resolve — in server order,
	// for the results pane.
	unmappable []validate.Finding

	// stale marks results the Draft has mutated out from under: the
	// markers stay put as a to-do list, flagged stale until the next
	// Validate replaces them (the documented marker lifecycle).
	stale bool

	// next indexes mapped for the jump key's cycle.
	next int
}

// findingCount is how many findings the Invalid outcome carried, mapped
// and unmappable together.
func (v *validationState) findingCount() int {
	count := len(v.unmappable)
	for _, messages := range v.messages {
		count += len(messages)
	}
	return count
}

// gatePrompt is `v`'s open required-to-Validate prompt: the metadata Field
// Paths still to confirm (in prompt order), the type-to-enter buffer, and
// the last confirm's inline rejection. Enter confirms the typed value into
// the Draft at the real Field Path; Esc cancels the whole Validate.
type gatePrompt struct {
	missing   []string
	input     string
	rejection string
}

// path is the Field Path the prompt is currently asking for.
func (p gatePrompt) path() string {
	return p.missing[0]
}

// prompt is the gate's footer line, in the existing prompt grammar.
func (p gatePrompt) prompt() string {
	line := "Validate needs " + p.path() + " > " + p.input + "▏ · enter confirm · esc cancel the Validate"
	if p.rejection != "" {
		line += " — " + p.rejection
	}
	return line
}

// validateCommand routes navigate mode's Validate keys: `v` runs the gate
// and the dry-run request, `n` jumps through the mapped findings, and `r`
// opens the results pane.
func (c compose) validateCommand(key string) (compose, tea.Cmd) {
	switch key {
	case "v":
		return c.pressValidate()
	case "n":
		return c.pressJumpToFinding(), nil
	default:
		return c.pressResults(), nil
	}
}

// pressValidate is `v` in navigate mode: the required-to-Validate gate
// runs first — metadata the dry-run cannot run without prompts inline
// instead of surfacing a raw server error (DESIGN.md — Output) — and a
// satisfied gate emits the Draft and requests the dry-run.
func (c compose) pressValidate() (compose, tea.Cmd) {
	if missing := c.missingValidateMetadata(); len(missing) > 0 {
		c.gate = &gatePrompt{missing: missing}
		return c, nil
	}
	return c.requestValidate()
}

// missingValidateMetadata is the required-to-Validate gate's current
// answer: metadata.name always, plus metadata.namespace for a namespaced
// Kind when the shell's default namespace doesn't resolve either. It backs
// both the gate prompt and the status line's from-session-start flag —
// distinct from the schema-required count on purpose.
func (c compose) missingValidateMetadata() []string {
	var missing []string
	if _, filled := c.draft.ValueAt("metadata.name"); !filled {
		missing = append(missing, "metadata.name")
	}
	if c.kind.Namespaced && c.defaultNamespace == "" {
		if _, filled := c.draft.ValueAt("metadata.namespace"); !filled {
			missing = append(missing, "metadata.namespace")
		}
	}
	return missing
}

// requestValidate emits the Draft — the same pure emission the exit ramp
// prints, so the dry-run checks exactly the Manifest the Session would
// Emit — and hands the bytes to the shell as a typed message. A failed
// emission is a non-fatal notice, never a POST.
func (c compose) requestValidate() (compose, tea.Cmd) {
	manifest, err := c.draft.Emit()
	if err != nil {
		c.notice = "emitting the Draft for Validate failed: " + err.Error()
		return c, nil
	}
	return c, func() tea.Msg { return validateRequestedMsg{manifest: manifest} }
}

// updateGatePrompt routes one key press into the open gate prompt, in the
// existing prompt grammar: Enter confirms the typed value into the Draft
// at the real Field Path, Esc cancels the whole Validate, printable keys
// type — any typing clears a lingering rejection.
func (c compose) updateGatePrompt(key tea.KeyMsg) (compose, tea.Cmd) {
	prompt := *c.gate
	switch {
	case key.String() == "esc":
		c.gate = nil
		return c, nil
	case key.String() == "enter":
		return c.confirmGatePrompt()
	case key.String() == "backspace":
		prompt.rejection = ""
		if prompt.input != "" {
			runes := []rune(prompt.input)
			prompt.input = string(runes[:len(runes)-1])
		}
	case key.Type == tea.KeySpace:
		prompt.rejection = ""
		prompt.input += " "
	case key.Type == tea.KeyRunes && !key.Alt:
		prompt.rejection = ""
		prompt.input += string(key.Runes)
	}
	c.gate = &prompt
	return c, nil
}

// confirmGatePrompt confirms the typed value into the Draft. A rejection —
// an empty value, or the Draft's own schema-local check — renders inline
// and commits nothing; a confirmed value moves to the next missing gate
// field, and the last one requests the Validate the gate was holding.
func (c compose) confirmGatePrompt() (compose, tea.Cmd) {
	prompt := *c.gate
	if prompt.input == "" {
		prompt.rejection = "type a value — or Esc cancels the Validate"
		c.gate = &prompt
		return c, nil
	}
	if err := c.draft.Set(prompt.path(), prompt.input); err != nil {
		prompt.rejection = err.Error()
		c.gate = &prompt
		return c, nil
	}
	c.refreshCompleteness()

	if len(prompt.missing) > 1 {
		c.gate = &gatePrompt{missing: prompt.missing[1:]}
		return c, nil
	}
	c.gate = nil
	return c.requestValidate()
}

// pressJumpToFinding is the jump-to-first-error key, `n` in navigate mode:
// it focuses the first mapped finding's node — ancestors expanded, the
// deep-link landing machinery's discipline — and subsequent presses cycle
// through the findings in server order. With nothing to jump to it says why
// instead of staying silent, mirroring `r`'s explanatory notices.
func (c compose) pressJumpToFinding() compose {
	if c.validation == nil {
		c.notice = "no Validate findings yet — v validates the Draft against the live cluster"
		return c
	}
	if len(c.validation.mapped) == 0 {
		c.notice = c.noFindingsNotice()
		return c
	}
	state := *c.validation
	path := state.mapped[state.next]
	state.next = (state.next + 1) % len(state.mapped)
	c.validation = &state

	row := c.rowForDraftPath(path, true)
	if row == nil {
		c.notice = path + " no longer resolves in the tree — the finding is stale, v revalidates"
		return c
	}
	c.rebuildRows()
	c.focusRow(row)
	return c
}

// noFindingsNotice says why the jump key has nothing to jump to when a
// Validate outcome exists but mapped no finding onto the tree.
func (c compose) noFindingsNotice() string {
	switch {
	case c.validation.clean:
		return "the last Validate passed — no findings to jump to"
	case c.validation.unavailable != "":
		return "the last Validate did not run — r shows why"
	default:
		return "no findings mark the tree — r opens the Validate results pane"
	}
}

// pressResults is `r` in navigate mode: it reopens the results pane over
// the last Validate's unmappable findings, Status summary, or
// unavailability — and says why when there is nothing to show.
func (c compose) pressResults() compose {
	switch {
	case c.validation == nil:
		c.notice = "no Validate results yet — v validates the Draft against the live cluster"
	case c.validation.clean:
		c.notice = "the last Validate passed — the results pane has nothing to show"
	default:
		c.resultsOpen = true
	}
	return c
}

// updateResultsPane applies one key press to the open results pane, in the
// modal grammar: Esc dismisses back to navigate mode, and everything else
// is inert (Ctrl-c never reaches here — it quits from anywhere first).
func (c compose) updateResultsPane(key tea.KeyMsg) compose {
	if key.String() == "esc" {
		c.resultsOpen = false
	}
	return c
}

// applyValidateOutcome renders one Validate flight's Outcome into the
// compose view: a clean pass replaces any prior findings with the positive
// confirmation, an Invalid maps its findings over the tree, and an
// Unavailable opens the results pane without touching a single tree node.
func (c compose) applyValidateOutcome(outcome data.Outcome) compose {
	switch outcome := outcome.(type) {
	case data.Clean:
		c.validation = &validationState{clean: true}
	case data.Invalid:
		c = c.applyReport(validate.MapStatus(outcome.Status))
	case data.Unavailable:
		c.validation = &validationState{unavailable: outcome.Reason}
		c.resultsOpen = true
	}
	return c
}

// applyReport pins an Invalid outcome's Report onto the compose view:
// mappable findings whose paths resolve in the open Kind's tree mark those
// rows, everything else — no path, or a path the tree can't reach —
// degrades to the results pane. The pane opens by itself when it holds
// anything the tree doesn't show (a cause-less Status included, so a 404's
// summary is never an empty findings list).
func (c compose) applyReport(report validate.Report) compose {
	state := &validationState{summary: report.Summary, messages: map[string][]string{}}
	for _, finding := range report.Findings {
		if !finding.Mappable() || c.rowForDraftPath(finding.FieldPath, false) == nil {
			state.unmappable = append(state.unmappable, finding)
			continue
		}
		if _, seen := state.messages[finding.FieldPath]; !seen {
			state.mapped = append(state.mapped, finding.FieldPath)
		}
		state.messages[finding.FieldPath] = append(state.messages[finding.FieldPath], finding.Message)
	}
	c.validation = state
	if len(state.unmappable) > 0 || len(state.mapped) == 0 {
		c.resultsOpen = true
	}
	return c
}

// markValidationOutdated is the documented marker lifecycle (DESIGN.md —
// Output; `?` help): a confirmed Draft mutation — set, unset, append, add
// key, graft — marks the last Validate's findings stale rather than
// clearing them, so they stay browsable as a to-do list until `v`
// revalidates; an outcome without findings (a clean pass, an
// unavailability) is simply dropped, because the mutation made it moot.
// A version switch goes further and drops findings entirely — the compose
// view rebuilds from scratch, and the old paths may no longer exist.
func (c *compose) markValidationOutdated() {
	if c.validation == nil {
		return
	}
	if len(c.validation.mapped) == 0 && len(c.validation.unmappable) == 0 {
		c.validation = nil
		return
	}
	state := *c.validation
	state.stale = true
	c.validation = &state
}

// rowForDraftPath resolves a Draft-level Field Path to its tree row,
// loading rows lazily along the way; expand additionally expands every
// ancestor, so the resolved row lands visible (the deep-link landing
// discipline). Nil when the tree cannot reach the path — a schema field
// the Kind doesn't define, or an item or key the Draft never instantiated.
func (c compose) rowForDraftPath(path string, expand bool) *treeRow {
	if path == "" {
		return nil
	}
	row := c.root
	for {
		if rowPath, addressable := row.draftFieldPath(); addressable && rowPath == path {
			return row
		}
		next := c.childToward(row, path)
		if next == nil {
			return nil
		}
		if expand {
			c.expandRow(row)
		}
		row = next
	}
}

// childToward is one step of rowForDraftPath's walk: the child whose
// Draft-level Field Path is the target path or a segment-boundary prefix
// of it — '.' opens a field segment, '[' a selector — so spelled prefixes
// never match across segment boundaries.
func (c compose) childToward(row *treeRow, path string) *treeRow {
	if !c.loadRow(row) {
		return nil
	}
	for _, child := range row.children {
		childPath, addressable := child.draftFieldPath()
		if !addressable || childPath == "" {
			continue
		}
		if childPath == path || strings.HasPrefix(path, childPath+".") || strings.HasPrefix(path, childPath+"[") {
			return child
		}
	}
	return nil
}

// rowHasFindings reports whether the last Validate mapped a finding onto
// this row — the tree pane's error marker.
func (c compose) rowHasFindings(row *treeRow) bool {
	if c.validation == nil {
		return false
	}
	path, addressable := row.draftFieldPath()
	if !addressable || path == "" {
		return false
	}
	return len(c.validation.messages[path]) > 0
}

// findingDetailLines renders the focused row's Validate finding messages
// for the detail pane — the server's words, verbatim, flagged stale once
// the Draft has changed since the Validate.
func (c compose) findingDetailLines(row *treeRow) []string {
	if c.validation == nil {
		return nil
	}
	path, addressable := row.draftFieldPath()
	if !addressable {
		return nil
	}
	messages := c.validation.messages[path]
	if len(messages) == 0 {
		return nil
	}

	header := "Validate " + findingNoun(len(messages))
	if c.validation.stale {
		header += " (stale — the Draft changed since this Validate)"
	}
	lines := []string{"", highlightedStyle.Render(header + ":")}
	for _, message := range messages {
		lines = append(lines, "  "+message)
	}
	return lines
}

// validateGateSegment is the status line's required-to-Validate flag: the
// metadata the dry-run cannot run without, shown from session start and
// spelled apart from the schema-required count (DESIGN.md — Output).
func (c compose) validateGateSegment() string {
	missing := c.missingValidateMetadata()
	if len(missing) == 0 {
		return ""
	}
	return highlightedStyle.Render("Validate needs " + strings.Join(missing, " and "))
}

// validateStateSegment is the status line's Validate half: the clean
// pass's positive confirmation, or the finding count — flagged stale once
// the Draft has mutated past it. Unavailability renders in the results
// pane instead, never as manifest state.
func (c compose) validateStateSegment() string {
	state := c.validation
	switch {
	case state == nil, state.unavailable != "":
		return ""
	case state.clean:
		return dimmedStyle.Render("✔ Validate passed")
	case state.stale:
		return highlightedStyle.Render(fmt.Sprintf("%s %d Validate %s — stale, v revalidates",
			findingMarker, state.findingCount(), findingNoun(state.findingCount())))
	default:
		return highlightedStyle.Render(fmt.Sprintf("%s %d Validate %s",
			findingMarker, state.findingCount(), findingNoun(state.findingCount())))
	}
}

// validationVerbs spells the hint-bar verbs the last Validate's results
// serve right now: `n` while mapped findings exist, `r` while the results
// pane has anything to reopen.
func (c compose) validationVerbs() string {
	if c.validation == nil {
		return ""
	}
	verbs := ""
	if len(c.validation.mapped) > 0 {
		verbs += "n next finding · "
	}
	if !c.validation.clean {
		verbs += "r results · "
	}
	return verbs
}

// resultsView renders the results pane as the body overlay: the
// unavailability notice — visually and verbally distinct from manifest
// errors — or the Invalid outcome's Status summary and unmappable
// findings.
func (c compose) resultsView() string {
	state := c.validation
	if state == nil {
		return ""
	}
	if state.unavailable != "" {
		return strings.Join([]string{
			highlightedStyle.Render("Validate unavailable: " + state.unavailable),
			"",
			dimmedStyle.Render("The cluster could not run the Validate — this says nothing " +
				"about the Manifest, and no tree node is marked."),
		}, "\n")
	}

	lines := []string{
		highlightedStyle.Render("Validate results — the server rejected the Manifest"),
		summaryLine(state.summary),
	}
	if state.stale {
		lines = append(lines, dimmedStyle.Render("stale — the Draft changed since this Validate; v revalidates"))
	}
	if len(state.unmappable) > 0 {
		lines = append(lines, "", "findings without a tree position:")
		for _, finding := range state.unmappable {
			lines = append(lines, "  "+spellUnmappable(finding))
		}
	}
	if len(state.mapped) > 0 {
		lines = append(lines, "", dimmedStyle.Render(fmt.Sprintf(
			"%d mapped %s mark their tree nodes — n jumps through them",
			len(state.mapped), findingNoun(len(state.mapped)),
		)))
	}
	return strings.Join(lines, "\n")
}

// summaryLine spells the Invalid outcome's Status-level summary: the
// server's reason, HTTP code, and message, verbatim.
func summaryLine(summary validate.Summary) string {
	line := summary.Reason
	if line == "" {
		line = "Failure"
	}
	if summary.Code != 0 {
		line = fmt.Sprintf("%s (HTTP %d)", line, summary.Code)
	}
	if summary.Message != "" {
		line += ": " + summary.Message
	}
	return line
}

// spellUnmappable renders one unmappable finding for the results pane,
// keeping the server's raw field spelling as provenance when it sent one.
func spellUnmappable(finding validate.Finding) string {
	if finding.Field != "" {
		return finding.Field + " — " + finding.Message
	}
	return finding.Message
}

// findingNoun pluralizes "finding" for the status line and panes.
func findingNoun(count int) string {
	if count == 1 {
		return "finding"
	}
	return "findings"
}

// startValidate consumes the compose view's Validate request: resolve the
// namespace the way kubectl does — the Manifest's metadata.namespace, else
// the Session default (data.ResolveNamespace) — and POST the dry-run as a
// command, entering the in-flight loading state with the compose view
// (Draft included) intact underneath.
func (m Model) startValidate(manifest []byte) (tea.Model, tea.Cmd) {
	if m.view != composing {
		return m, nil
	}
	namespace := data.ResolveNamespace(manifest, m.defaultNamespace)
	m.validateSeq++
	m.view = validatingDraft

	ctx, validator, kind, seq := m.ctx, m.validator, m.kind, m.validateSeq
	return m, func() tea.Msg {
		return validateOutcomeMsg{seq: seq, outcome: validator.Validate(ctx, kind, manifest, namespace)}
	}
}

// validateResolved lands one Validate flight's Outcome: a stale flight —
// cancelled or superseded — is dropped, and a Session whose own context
// has ended returns quietly to composing (its "context canceled" arrives
// dressed as Unavailable, and rendering that would blame the cluster for
// the user's own cancel). Everything else renders through the compose
// view.
func (m Model) validateResolved(msg validateOutcomeMsg) Model {
	if m.view != validatingDraft || msg.seq != m.validateSeq {
		return m
	}
	m.view = composing
	if m.ctx.Err() != nil {
		return m
	}
	m.compose = m.compose.applyValidateOutcome(msg.outcome)
	return m
}

// validateTransitKey is the key grammar while a Validate's dry-run is in
// flight: Esc and `q` cancel the wait and return to composing — the Draft
// untouched, the late outcome dropped as stale — while Ctrl-c remains the
// immediate escape hatch.
func (m Model) validateTransitKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.validateSeq++
		m.view = composing
	}
	return m, nil
}

// Validating reports whether a Validate's dry-run is in flight — the
// loading state between `v` and its Outcome.
func (m Model) Validating() bool {
	return m.view == validatingDraft
}

// PromptingForValidateMetadata returns the Field Path the
// required-to-Validate gate is prompting for, and false when the gate
// prompt is not open.
func (m Model) PromptingForValidateMetadata() (string, bool) {
	if m.view != composing || m.compose.gate == nil {
		return "", false
	}
	return m.compose.gate.path(), true
}

// ResultsPaneOpen reports whether the Validate results pane is open over
// the compose view.
func (m Model) ResultsPaneOpen() bool {
	return m.view == composing && m.compose.resultsOpen
}

// ValidatePassed reports whether the last Validate was a clean pass — the
// server accepted the dry-run — and no mutation has outdated it since.
func (m Model) ValidatePassed() bool {
	return m.view == composing && m.compose.validation != nil && m.compose.validation.clean
}

// ValidateFindingPaths returns the mapped findings' Draft-level Field
// Paths in server order — the order the jump key cycles through — and nil
// when no findings are mapped.
func (m Model) ValidateFindingPaths() []string {
	if m.view != composing || m.compose.validation == nil {
		return nil
	}
	return slices.Clone(m.compose.validation.mapped)
}

// ValidateFindingMessages returns the finding messages the last Validate
// mapped onto a Draft-level Field Path, in server order.
func (m Model) ValidateFindingMessages(fieldPath string) []string {
	if m.view != composing || m.compose.validation == nil {
		return nil
	}
	return slices.Clone(m.compose.validation.messages[fieldPath])
}

// UnmappableFindings returns the last Validate's unmappable findings as
// the results pane spells them, in server order.
func (m Model) UnmappableFindings() []string {
	if m.view != composing || m.compose.validation == nil {
		return nil
	}
	spelled := make([]string, 0, len(m.compose.validation.unmappable))
	for _, finding := range m.compose.validation.unmappable {
		spelled = append(spelled, spellUnmappable(finding))
	}
	return spelled
}

// ValidateUnavailable returns the last Validate's unavailability reason,
// and false when the last Validate ran (or none has).
func (m Model) ValidateUnavailable() (string, bool) {
	if m.view != composing || m.compose.validation == nil || m.compose.validation.unavailable == "" {
		return "", false
	}
	return m.compose.validation.unavailable, true
}

// ValidateStale reports whether the Draft has mutated since the last
// Validate's findings landed — the documented marker lifecycle.
func (m Model) ValidateStale() bool {
	return m.view == composing && m.compose.validation != nil && m.compose.validation.stale
}
