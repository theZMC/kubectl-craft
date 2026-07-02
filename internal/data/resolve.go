package data

import (
	"fmt"
	"slices"
	"strings"
)

// ResolveKindToken resolves a deep-link kind token against the browsable
// Kind list with kubectl explain's tolerance: the token may be the Kind
// name, the resource's plural, or one of its short names, matched
// case-insensitively. A token naming exactly one Kind lands on its
// Preferred Version (CONTEXT.md); a token matching Kinds in more than one
// group is ambiguous and errors naming the candidates rather than guessing.
func ResolveKindToken(kinds []Kind, token string) (Kind, error) {
	var matched []Kind
	for _, kind := range kinds {
		if token != "" && kindMatchesToken(kind, token) {
			matched = append(matched, kind)
		}
	}

	if len(matched) == 0 {
		return Kind{}, fmt.Errorf(
			"unknown kind %q: no browsable Kind name, plural, or short name matches it", token,
		)
	}
	if groupKinds := distinctGroupKinds(matched); len(groupKinds) > 1 {
		return Kind{}, fmt.Errorf(
			"ambiguous kind %q: it matches %s", token, strings.Join(groupKinds, " and "),
		)
	}

	return preferredVersionOf(matched), nil
}

// kindMatchesToken reports whether the token names this Kind: its Kind
// name, its plural, or any of its short names, case-insensitively.
func kindMatchesToken(kind Kind, token string) bool {
	if strings.EqualFold(token, kind.GVK.Kind) || strings.EqualFold(token, kind.Plural) {
		return true
	}
	return slices.ContainsFunc(kind.ShortNames, func(short string) bool {
		return strings.EqualFold(token, short)
	})
}

// distinctGroupKinds projects the matched Kinds onto their distinct
// kind.group names, in match order — more than one means the token is
// ambiguous across groups.
func distinctGroupKinds(matched []Kind) []string {
	var names []string
	for _, kind := range matched {
		name := kind.GVK.GroupKind().String()
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// preferredVersionOf picks the Preferred Version among one Kind's matched
// versions — the version the deep link lands on — falling back to the first
// match when the group reports no preference.
func preferredVersionOf(matched []Kind) Kind {
	for _, kind := range matched {
		if kind.Preferred {
			return kind
		}
	}
	return matched[0]
}
