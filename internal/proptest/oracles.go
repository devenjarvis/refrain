package proptest

import "regexp"

// ValidNameRe matches valid agent/session names: must start with an
// alphanumeric character and contain only alphanumerics, underscores, and
// dashes. Mirrors the pattern enforced in internal/agent Manager.Create().
var ValidNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// ValidSlugRe matches valid slug output: must start with a lowercase
// alphanumeric and contain only lowercase alphanumerics and dashes.
var ValidSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// IsValidName reports whether s satisfies the agent name invariant.
func IsValidName(s string) bool {
	return ValidNameRe.MatchString(s)
}

// IsValidSlug reports whether s satisfies the slug output invariant.
func IsValidSlug(s string) bool {
	return ValidSlugRe.MatchString(s)
}
