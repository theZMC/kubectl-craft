package matchers

import (
	"fmt"
	"reflect"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"

	"github.com/thezmc/kubectl-craft/internal/schema"
)

// HaveValueAt succeeds when a Draft holds a filled value at the Draft-level
// Field Path — and, when an expected data value is given, when the value read
// back carries exactly that data (in the Draft's normalized spelling: int64
// for integers, float64 for numbers).
func HaveValueAt(fieldPath string, expectedData ...any) types.GomegaMatcher {
	return &haveValueAtMatcher{fieldPath: fieldPath, expected: expectedData}
}

type haveValueAtMatcher struct {
	fieldPath string
	expected  []any
}

// Match reads the value back from the Draft and compares its data when an
// expectation was given.
func (matcher *haveValueAtMatcher) Match(actual any) (bool, error) {
	if len(matcher.expected) > 1 {
		return false, fmt.Errorf("HaveValueAt takes at most one expected data value, but got %d", len(matcher.expected))
	}
	draft, isDraft := actual.(*schema.Draft)
	if !isDraft {
		return false, fmt.Errorf("HaveValueAt matches a *schema.Draft, but got%s", format.Object(actual, 1))
	}
	value, filled := draft.ValueAt(matcher.fieldPath)
	if !filled {
		return false, nil
	}
	if len(matcher.expected) == 0 {
		return true, nil
	}
	return reflect.DeepEqual(value.Data, matcher.expected[0]), nil
}

func (matcher *haveValueAtMatcher) FailureMessage(actual any) string {
	if len(matcher.expected) == 0 {
		return format.Message(actual, fmt.Sprintf("to hold a value at Draft-level Field Path %q", matcher.fieldPath))
	}
	return format.Message(actual,
		fmt.Sprintf("to hold exactly this data at Draft-level Field Path %q", matcher.fieldPath), matcher.expected[0])
}

func (matcher *haveValueAtMatcher) NegatedFailureMessage(actual any) string {
	if len(matcher.expected) == 0 {
		return format.Message(actual, fmt.Sprintf("to hold no value at Draft-level Field Path %q", matcher.fieldPath))
	}
	return format.Message(actual,
		fmt.Sprintf("not to hold exactly this data at Draft-level Field Path %q", matcher.fieldPath), matcher.expected[0])
}
