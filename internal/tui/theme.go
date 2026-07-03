package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// This file is the owned adaptive palette's single home (ADR-0007): five
// tokens, one meaning each, resolved here and nowhere else. Render sites
// take finished styles from the theme — no lipgloss color or attribute
// styles are constructed inline anywhere else in the package.

// token names one meaning in the owned palette: every color carries exactly
// one meaning, and one meaning surfaces in many places (ADR-0007).
type token int

const (
	// tokenNeedsFixing marks what blocks or rejects: the missing-required
	// marker, a Validate finding, an editor rejection.
	tokenNeedsFixing token = iota
	// tokenSet marks what is filled and well: set values, the ✔ all-clear.
	tokenSet
	// tokenAsk marks what awaits a decision: confirms, unset and gate
	// prompts, pending choices.
	tokenAsk
	// tokenStructure marks what the eye navigates by: the breadcrumb, the
	// focused row, overlay borders and titles.
	tokenStructure
	// tokenMeta marks what merely informs: hints, defaults, placeholders,
	// row metadata.
	tokenMeta
)

// colorPair is one token's adaptive pair: the dark half renders on dark
// terminal backgrounds, the light half on light ones.
type colorPair struct {
	dark  color.Color
	light color.Color
}

// tokenPairs is the palette itself — each colored token's pair, in the
// soft-conventional direction the token table documents (DESIGN.md —
// Palette): soft red, sage green, warm amber, cool blue. Meta carries no
// pair by design: its treatment is the terminal's own faint rendering,
// neutral so metadata never competes with meaning.
var tokenPairs = [...]colorPair{
	tokenNeedsFixing: {dark: lipgloss.Color("#e06c75"), light: lipgloss.Color("#c94f4f")},
	tokenSet:         {dark: lipgloss.Color("#98c379"), light: lipgloss.Color("#4e9a06")},
	tokenAsk:         {dark: lipgloss.Color("#e5c07b"), light: lipgloss.Color("#b58900")},
	tokenStructure:   {dark: lipgloss.Color("#61afef"), light: lipgloss.Color("#2472c8")},
}

// theme resolves the owned palette for one Session against the terminal
// background the shell queried (tea.BackgroundColorMsg — the program's own
// I/O, never environment sniffing). The zero value is the dark palette,
// unmuted: the Session's look until the query answers, and the
// deterministic default every spec starts from.
type theme struct {
	// light records that the queried terminal background answered light,
	// flipping every pair to its light half.
	light bool

	// muted folds every token into the Meta treatment — the Muted variant.
	muted bool

	// bar adds the chrome bar's reverse-video treatment to every token —
	// the Bar variant.
	bar bool
}

// withBackground re-resolves the palette against the queried terminal
// background — the answer to the shell's RequestBackgroundColor.
func (t theme) withBackground(isDark bool) theme {
	t.light = !isDark
	return t
}

// Muted is the variant a floating overlay's backdrop will consume: every
// token resolves to the Meta treatment, so a dimmed background carries no
// meaning of its own.
func (t theme) Muted() theme {
	t.muted = true
	return t
}

// Bar is the chrome variant the frame's bottom status line and hint bar
// consume: every token keeps its meaning and gains the bar's reverse-video
// treatment, so the two lines read as one full-width bar anchoring the
// frame. The chrome is attribute-only — reverse video, no hue of its own —
// so the bar never grows a sixth meaning (ADR-0007).
func (t theme) Bar() theme {
	t.bar = true
	return t
}

// Chrome styles the bar's own surface — the full-width fill and any
// tokenless text riding it (a notice keeps carrying no token: the reverse
// belongs to the bar, never to the notice's words). Off the Bar variant it
// is the unstyled default, because chrome exists only where the bar does.
func (t theme) Chrome() lipgloss.Style {
	if !t.bar {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Reverse(true)
}

// NeedsFixing styles what blocks or rejects — the missing-required marker,
// a Validate finding, an editor rejection. Soft red: the one broken-prior
// hue (ADR-0007 rejected splitting it three ways).
func (t theme) NeedsFixing() lipgloss.Style { return t.style(tokenNeedsFixing) }

// Set styles what is filled and well — set values, the ✔ all-clear.
func (t theme) Set() lipgloss.Style { return t.style(tokenSet) }

// Ask styles what awaits a decision — confirms, unset and gate prompts,
// pending choices.
func (t theme) Ask() lipgloss.Style { return t.style(tokenAsk) }

// Structure styles what the eye navigates by — the breadcrumb, the focused
// row, overlay borders and titles. Focus emphasis is bold today and the
// token carries it, so render sites never add attributes of their own.
func (t theme) Structure() lipgloss.Style { return t.style(tokenStructure) }

// Meta styles what merely informs — hints, defaults, placeholders, row
// metadata: the terminal's own faint rendering, no owned hue.
func (t theme) Meta() lipgloss.Style { return t.style(tokenMeta) }

// style is the palette's single resolution point — every render site's
// style arrives from here through the token accessors. The Muted variant
// folds every token into the Meta treatment; Meta renders neutral faint;
// every other token picks its pair against the queried background; the Bar
// variant adds the chrome bar's reverse on top of whatever the token
// resolved to.
func (t theme) style(tk token) lipgloss.Style {
	style := t.Chrome()
	if t.muted || tk == tokenMeta {
		return style.Faint(true)
	}

	pair := tokenPairs[tk]
	style = style.Foreground(lipgloss.LightDark(!t.light)(pair.light, pair.dark))
	if tk == tokenStructure {
		style = style.Bold(true)
	}
	return style
}
