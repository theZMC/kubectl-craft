package validate_test

import (
	"github.com/thezmc/kubectl-craft/internal/validate"
)

// expectedReports pins the full findings partition of every recorded Status
// fixture in testdata/: which causes map to Draft-level Field Paths, which
// stay unmappable, and the Status-level summary — messages verbatim from the
// recorded server bytes (regenerate with `mise run fixtures:capture-status`).
var expectedReports = map[string]validate.Report{
	// apps/v1 Deployment POSTed without spec.selector: dotted paths from
	// the server's native validation, every cause mappable.
	"deployment_required.json": {
		Summary: validate.Summary{
			Reason: "Invalid",
			Message: `Deployment.apps "craft-capture-required" is invalid: ` +
				"[spec.selector: Required value, spec.template.metadata.labels: " +
				"Invalid value: {\"app\":\"craft\"}: `selector` does not match template `labels`]",
			Code: 422,
		},
		Findings: []validate.Finding{
			{
				FieldPath: "spec.selector",
				Field:     "spec.selector",
				Message:   "Required value",
			},
			{
				FieldPath: "spec.template.metadata.labels",
				Field:     "spec.template.metadata.labels",
				Message:   "Invalid value: {\"app\":\"craft\"}: `selector` does not match template `labels`",
			},
		},
	},

	// craft.example.com/v1 Gadget violating both corpus CEL rules: the
	// object-level rule lands on spec itself, the scalar rule on its leaf.
	"gadget_cel.json": {
		Summary: validate.Summary{
			Reason: "Invalid",
			Message: `Gadget.craft.example.com "craft-capture-cel" is invalid: ` +
				`[spec: Invalid value: minReplicas must not exceed maxReplicas, ` +
				`spec.profile: Invalid value: "turbo": profile must be economy, balanced, or performance]`,
			Code: 422,
		},
		Findings: []validate.Finding{
			{
				FieldPath: "spec",
				Field:     "spec",
				Message:   "Invalid value: minReplicas must not exceed maxReplicas",
			},
			{
				FieldPath: "spec.profile",
				Field:     "spec.profile",
				Message:   `Invalid value: "turbo": profile must be economy, balanced, or performance`,
			},
		},
	},

	// v1 Pod with an unsupported imagePullPolicy and a bogus resource
	// name: an enum violation on an indexed path, and map keys the server
	// spells unquoted (limits[bogus]) normalized to the quoted Draft
	// spelling (limits["bogus"]).
	"pod_enum.json": {
		Summary: validate.Summary{
			Reason: "Invalid",
			Message: `Pod "craft-capture-enum" is invalid: ` +
				`[spec.containers[0].imagePullPolicy: Unsupported value: "Sometimes": ` +
				`supported values: "Always", "IfNotPresent", "Never", ` +
				`spec.containers[0].resources.limits[bogus]: Invalid value: "bogus": ` +
				`must be a standard resource type or fully qualified, ` +
				`spec.containers[0].resources.limits[bogus]: Invalid value: "bogus": ` +
				`must be a standard resource for containers, ` +
				`spec.containers[0].resources.requests[bogus]: Invalid value: "bogus": ` +
				`must be a standard resource type or fully qualified, ` +
				`spec.containers[0].resources.requests[bogus]: Invalid value: "bogus": ` +
				`must be a standard resource for containers]`,
			Code: 422,
		},
		Findings: []validate.Finding{
			{
				FieldPath: "spec.containers[0].imagePullPolicy",
				Field:     "spec.containers[0].imagePullPolicy",
				Message:   `Unsupported value: "Sometimes": supported values: "Always", "IfNotPresent", "Never"`,
			},
			{
				FieldPath: `spec.containers[0].resources.limits["bogus"]`,
				Field:     "spec.containers[0].resources.limits[bogus]",
				Message:   `Invalid value: "bogus": must be a standard resource type or fully qualified`,
			},
			{
				FieldPath: `spec.containers[0].resources.limits["bogus"]`,
				Field:     "spec.containers[0].resources.limits[bogus]",
				Message:   `Invalid value: "bogus": must be a standard resource for containers`,
			},
			{
				FieldPath: `spec.containers[0].resources.requests["bogus"]`,
				Field:     "spec.containers[0].resources.requests[bogus]",
				Message:   `Invalid value: "bogus": must be a standard resource type or fully qualified`,
			},
			{
				FieldPath: `spec.containers[0].resources.requests["bogus"]`,
				Field:     "spec.containers[0].resources.requests[bogus]",
				Message:   `Invalid value: "bogus": must be a standard resource for containers`,
			},
		},
	},

	// v1 ConfigMap denied by the always-deny admission webhook: no causes,
	// no field paths — the freeform message is the one unmappable finding
	// the results pane shows.
	"configmap_webhook_denial.json": {
		Summary: validate.Summary{
			Message: `admission webhook "deny.craft.example.com" denied the request: ` +
				`the kubectl-craft capture webhook denies every matching Manifest`,
			Code: 400,
		},
		Findings: []validate.Finding{
			{
				Message: `admission webhook "deny.craft.example.com" denied the request: ` +
					`the kubectl-craft capture webhook denies every matching Manifest`,
			},
		},
	},
}
