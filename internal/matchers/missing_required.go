package matchers

import (
	"fmt"
	"slices"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"
)

// BeMissingRequired succeeds when the computed missing required Draft-level
// Field Paths are exactly the expected ones, in tree order — the order
// contextual requiredness guarantees. With no arguments it succeeds when the
// Draft is missing nothing required.
func BeMissingRequired(fieldPaths ...string) types.GomegaMatcher {
	return &beMissingRequiredMatcher{expected: fieldPaths}
}

type beMissingRequiredMatcher struct {
	expected []string
}

// Match compares the actual missing required Field Paths — the []string
// contextual requiredness computes — against the expected ones, in order.
func (matcher *beMissingRequiredMatcher) Match(actual any) (bool, error) {
	missing, isFieldPaths := actual.([]string)
	if !isFieldPaths {
		return false, fmt.Errorf(
			"BeMissingRequired matches the []string of missing required Draft-level Field Paths, but got%s",
			format.Object(actual, 1),
		)
	}
	if len(matcher.expected) == 0 {
		return len(missing) == 0, nil
	}
	return slices.Equal(missing, matcher.expected), nil
}

func (matcher *beMissingRequiredMatcher) FailureMessage(actual any) string {
	if len(matcher.expected) == 0 {
		return format.Message(actual, "to be missing nothing required")
	}
	return format.Message(actual, "to be missing exactly these required Field Paths, in tree order", matcher.expected)
}

func (matcher *beMissingRequiredMatcher) NegatedFailureMessage(actual any) string {
	if len(matcher.expected) == 0 {
		return format.Message(actual, "to be missing something required")
	}
	return format.Message(actual, "not to be missing exactly these required Field Paths", matcher.expected)
}
