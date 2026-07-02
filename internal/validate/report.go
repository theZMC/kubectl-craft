package validate

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Report is one Validate outcome, mapped from the metav1.Status a failed
// server-side dry-run returns. The TUI pins the mappable findings to tree
// nodes, routes the unmappable ones to the validation results pane, and
// summarizes the Status itself (DESIGN.md — Output).
type Report struct {
	// Summary is the Status-level failure the findings elaborate.
	Summary Summary
	// Findings preserves the server's cause order exactly — mappable and
	// unmappable findings interleave however the server interleaved them.
	Findings []Finding
}

// Summary is the Status-level view of the failure: the server's reason,
// message, and HTTP code, verbatim.
type Summary struct {
	Reason  string
	Message string
	Code    int32
}

// Finding is one Validate finding, mapped from a Status cause (or, for a
// cause-less Status, from the Status message itself).
type Finding struct {
	// FieldPath names the offending position as a Draft-level Field Path,
	// normalized from the server's error-cause spelling. It is empty when
	// the finding is unmappable: the cause named no position (empty,
	// "<nil>", or unparseable field) or the whole Status was a freeform
	// failure (a webhook denial, a top-level error). Whether the path
	// exists in the open Kind's tree is the TUI's business — normalization
	// only speaks the grammar.
	FieldPath string
	// Field is the server's raw spelling of the cause's field, verbatim —
	// provenance for the results pane, where even an unmappable cause may
	// carry a hint worth showing.
	Field string
	// Message is the server's text for this finding, verbatim.
	Message string
}

// Mappable reports whether the finding carries a Field Path the TUI can pin
// to a tree node; everything else belongs in the validation results pane.
func (f Finding) Mappable() bool {
	return f.FieldPath != ""
}

// MapStatus maps a failed dry-run's Status into the Validate Report: one
// finding per cause, in server order, plus the Status-level summary.
func MapStatus(status metav1.Status) Report {
	return Report{
		Summary: Summary{
			Reason:  string(status.Reason),
			Message: status.Message,
			Code:    status.Code,
		},
		Findings: mapCauses(status),
	}
}

// mapCauses maps the Status causes to findings, preserving server order. A
// Status carrying no causes at all — a freeform webhook denial, a top-level
// failure — yields its message as one unmappable finding, so the results
// pane always has the denial's text to show.
func mapCauses(status metav1.Status) []Finding {
	if status.Details == nil || len(status.Details.Causes) == 0 {
		if status.Message == "" {
			return nil
		}
		return []Finding{{Message: status.Message}}
	}
	findings := make([]Finding, 0, len(status.Details.Causes))
	for _, cause := range status.Details.Causes {
		findings = append(findings, mapCause(cause))
	}
	return findings
}

// mapCause maps one Status cause: its field is normalized to a Draft-level
// Field Path when the error-cause grammar allows, and stays an unmappable
// finding (raw field and message intact) when it doesn't.
func mapCause(cause metav1.StatusCause) Finding {
	finding := Finding{Field: cause.Field, Message: cause.Message}
	if path, ok := normalizeFieldPath(cause.Field); ok {
		finding.FieldPath = path
	}
	return finding
}
