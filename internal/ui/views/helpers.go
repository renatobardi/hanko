package views

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// initial returns the first letter of s, upper-cased.
func initial(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "?"
	}
	r, _ := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r))
}

// Initials returns up to two initials for a display name.
func Initials(name string) string {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "?"
	case 1:
		return initial(parts[0])
	}
	return initial(parts[0]) + initial(parts[len(parts)-1])
}

func prioIcon(p string) string {
	switch p {
	case "critical":
		return "alert"
	case "high":
		return "arrow-up"
	case "low":
		return "chevron-down"
	}
	return "square"
}

func pct(n int) string {
	if n < 0 {
		n = 0
	}
	if n > 100 {
		n = 100
	}
	return "width:" + itoa(n) + "%"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
