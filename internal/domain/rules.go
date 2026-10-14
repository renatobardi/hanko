package domain

import (
	"fmt"
	"regexp"
	"strings"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidSlug reports whether s is a lowercase URL-safe slug.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// Slugify derives a slug from a display name.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == 'á' || r == 'à' || r == 'â' || r == 'ã':
			b.WriteRune('a')
			dash = false
		case r == 'é' || r == 'ê':
			b.WriteRune('e')
			dash = false
		case r == 'í':
			b.WriteRune('i')
			dash = false
		case r == 'ó' || r == 'ô' || r == 'õ':
			b.WriteRune('o')
			dash = false
		case r == 'ú' || r == 'ü':
			b.WriteRune('u')
			dash = false
		case r == 'ç':
			b.WriteRune('c')
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteRune('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	return s
}

// TaskKey formats the human key of a task, e.g. HNK-42.
func TaskKey(prefix string, number int64) string { return fmt.Sprintf("%s-%d", prefix, number) }

var keyRe = regexp.MustCompile(`^([A-Z][A-Z0-9]{0,9})-(\d+)$`)

// ParseTaskKey splits "HNK-42" into prefix and number.
func ParseTaskKey(key string) (string, int64, bool) {
	m := keyRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(key)))
	if m == nil {
		return "", 0, false
	}
	var n int64
	_, err := fmt.Sscan(m[2], &n)
	return m[1], n, err == nil
}

// CanTransition reports whether a task may move from s to t.
// Done/cancelled tasks can only be reopened to todo.
func (s TaskStatus) CanTransition(t TaskStatus) bool {
	if !t.Valid() || s == t {
		return false
	}
	if !s.Open() {
		return t == TaskTodo
	}
	return true
}

// Agents supported by the adapter layer in v0.1.
var KnownAgents = []string{"claude", "codex", "fake"}

func ValidAgent(a string) bool {
	for _, k := range KnownAgents {
		if a == k {
			return true
		}
	}
	return false
}
