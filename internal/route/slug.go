// Slug derivation and validation for routes. Slugs are how an ingested route is
// addressed once persisted, so the rules for what makes a well-formed one live
// beside the payload validation that enforces them — but in their own file,
// since they are useful independently of any particular payload.
package route

import "strings"

// Slugify derives a URL slug from a display name: lowercase, with every run of
// non-alphanumeric characters collapsed to a single hyphen and no leading or
// trailing hyphen. It returns "" for a name with no alphanumeric content, which
// callers must treat as "no slug could be derived".
//
// Slugify's output always satisfies IsValidSlug.
func Slugify(name string) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(name) {
		if isSlugRune(r) {
			// Emit a separator only once we know a real character follows, so
			// the result never starts or ends with a hyphen.
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
			continue
		}
		pendingHyphen = true
	}
	return b.String()
}

// IsValidSlug reports whether s is already in canonical slug form: one or more
// lowercase-alphanumeric words joined by single hyphens.
func IsValidSlug(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, "-") {
		if part == "" { // leading, trailing, or doubled hyphen
			return false
		}
		for _, r := range part {
			if !isSlugRune(r) {
				return false
			}
		}
	}
	return true
}

// isSlugRune reports whether r may appear inside a slug word. ASCII digits and
// lowercase letters only: admitting accented or non-Latin characters would
// produce slugs that do not survive a round trip through a URL path cleanly.
func isSlugRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
