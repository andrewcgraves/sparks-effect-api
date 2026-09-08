package route

import "strings"

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

func isSlugRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
