// Package md renders user-supplied markdown (task descriptions, comments) to
// HTML that is safe to embed: raw HTML in the source is never passed through
// and dangerous URL schemes are dropped by the renderer.
package md

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var engine = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify, extension.TaskList),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
		// html.WithUnsafe is deliberately NOT set: raw HTML and javascript:/data: links are neutralised.
	),
)

var rawHTMLOmitted = regexp.MustCompile(`<!-- raw HTML omitted -->`)

// Render converts markdown to sanitised HTML. Raw HTML in the input is shown as
// literal text (escaped), never interpreted.
func Render(src string) string {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := engine.Convert([]byte(escapeAngle(src)), &buf); err != nil {
		return "<p>" + escapeText(src) + "</p>"
	}
	return rawHTMLOmitted.ReplaceAllString(buf.String(), "")
}

// escapeAngle escapes "<" outside fenced code so inline HTML becomes visible
// text instead of markup. Autolinks written as <https://…> are preserved.
func escapeAngle(s string) string {
	lines := strings.Split(s, "\n")
	inFence := false
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.Contains(ln, "<") {
			continue
		}
		lines[i] = escapeLine(ln)
	}
	return strings.Join(lines, "\n")
}

var autolink = regexp.MustCompile(`<(https?://[^\s<>]+)>`)

func escapeLine(ln string) string {
	// inline code spans are literal: leave their content alone
	parts := strings.Split(ln, "`")
	if len(parts) > 1 && len(parts)%2 == 1 {
		for i := 0; i < len(parts); i += 2 {
			parts[i] = escapeSegment(parts[i])
		}
		return strings.Join(parts, "`")
	}
	return escapeSegment(ln)
}

func escapeSegment(ln string) string {
	// protect autolinks
	var keep []string
	ln = autolink.ReplaceAllStringFunc(ln, func(m string) string {
		keep = append(keep, m)
		return "\x00" + strconv.Itoa(len(keep)-1) + "\x00"
	})
	ln = strings.ReplaceAll(ln, "<", "&lt;")
	for i, m := range keep {
		ln = strings.Replace(ln, "\x00"+strconv.Itoa(i)+"\x00", m, 1)
	}
	return ln
}

func escapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
