package validate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// nilField is how a nil *field.Path renders on the server side; it names no
// position, so a cause spelling it is unmappable.
const nilField = "<nil>"

// normalizeFieldPath rewrites a Status cause's field — the server's
// error-cause spelling — as a Draft-level Field Path in the one canonical
// spelling the Draft and the TUI already speak (requiredness.go's grammar):
// dotted fields, [0] item indices, and double-quoted map keys. It reports
// ok=false for the spellings that name no position at all: an empty field,
// the rendered nil path ("<nil>"), a path of nothing but dots, and anything
// the error-cause grammar cannot parse.
func normalizeFieldPath(field string) (string, bool) {
	// Servers sometimes prefix the root: ".spec.x" means "spec.x".
	rest := strings.TrimLeft(field, ".")
	if rest == "" || rest == nilField {
		return "", false
	}
	path, err := respellCausePath(rest)
	if err != nil {
		return "", false
	}
	// The shared parser is the grammar contract: a normalized Field Path
	// must parse as a Draft-level Field Path, or the finding cannot claim
	// to be mappable. By construction this never fails; it is the seam
	// that keeps this normalizer and internal/schema on one grammar.
	if err := schema.ParseDraftPath(path); err != nil {
		return "", false
	}
	return path, true
}

// respellCausePath respells one error-cause path in the Draft bracket
// grammar. Dotted fields carry over unchanged, [0] selectors stay item
// indices, and any other bracket content is a map key the server spells
// unquoted (limits[cpu]) and the Draft grammar spells double-quoted
// (limits["cpu"]).
func respellCausePath(causePath string) (string, error) {
	name, rest, err := cutCauseField(causePath)
	if err != nil {
		return "", err
	}
	path := name
	for rest != "" {
		var segment string
		switch rest[0] {
		case '.':
			name, rest, err = cutCauseField(rest[1:])
			segment = "." + name
		case '[':
			segment, rest, err = respellSelector(rest[1:])
		default:
			err = fmt.Errorf("expected '.' or '[' after a selector, not %q", string(rest[0]))
		}
		if err != nil {
			return "", err
		}
		path += segment
	}
	return path, nil
}

// cutCauseField reads one dotted field-name segment of an error-cause path:
// everything up to the next '.' or '['. Dots address fields, so the name
// must be non-empty.
func cutCauseField(s string) (string, string, error) {
	end := strings.IndexAny(s, ".[")
	if end == -1 {
		end = len(s)
	}
	if end == 0 {
		return "", "", errors.New("expected a field name")
	}
	return s[:end], s[end:], nil
}

// respellSelector respells the bracket selector opening s (the leading '['
// already cut) and returns the respelled selector and the remainder after
// its ']'. All-digit content is an item index and carries over verbatim;
// anything else is a map key, re-spelled double-quoted.
func respellSelector(s string) (string, string, error) {
	end := strings.IndexByte(s, ']')
	if end == -1 {
		return "", "", errors.New("a selector is missing its closing ']'")
	}
	content, rest := s[:end], s[end+1:]
	if content == "" {
		return "", "", errors.New("a selector cannot be empty")
	}
	if isIndex(content) {
		return "[" + content + "]", rest, nil
	}
	return "[" + strconv.Quote(content) + "]", rest, nil
}

// isIndex reports whether selector content is an item index — all digits,
// the only spelling the server's field paths use for array items.
func isIndex(content string) bool {
	for i := 0; i < len(content); i++ {
		if content[i] < '0' || content[i] > '9' {
			return false
		}
	}
	return content != ""
}
