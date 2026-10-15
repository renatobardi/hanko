package md

import (
	"strings"
	"testing"
)

func TestRenderEscapesHTML(t *testing.T) {
	cases := []struct {
		name, in string
		banned   []string
		want     []string
	}{
		{"script", "oi <script>alert(1)</script> fim", []string{"<script", "</script>"}, []string{"&lt;script"}},
		{"img onerror", "<img src=x onerror=alert(1)>", []string{"<img"}, []string{"&lt;img"}},
		{"block html", "<div onclick=\"x()\">\nhello\n</div>", []string{"<div"}, nil},
		{"js link", "[x](javascript:alert(1))", []string{"javascript:"}, nil},
		{"data link", "[x](data:text/html;base64,AAAA)", []string{"data:text/html"}, nil},
		{"iframe", "<iframe src=//evil></iframe>", []string{"<iframe"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Render(c.in)
			for _, b := range c.banned {
				if strings.Contains(strings.ToLower(got), b) {
					t.Errorf("output contains %q: %s", b, got)
				}
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q: %s", w, got)
				}
			}
		})
	}
}

func TestRenderMarkdown(t *testing.T) {
	got := Render("# Título\n\n- a\n- b\n\n`code` e **negrito** e <https://example.com>")
	for _, w := range []string{"<h1>", "<li>a</li>", "<code>code</code>", "<strong>negrito</strong>", `href="https://example.com"`} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in %s", w, got)
		}
	}
	if Render("   ") != "" {
		t.Error("blank input must render empty")
	}
}

func TestFencedCodeKeepsAngle(t *testing.T) {
	got := Render("```html\n<b>x</b>\n```")
	if strings.Contains(got, "<b>x") || !strings.Contains(got, "&lt;b&gt;x") {
		t.Errorf("fence not escaped: %s", got)
	}
}

func TestInlineCodeKeepsAngle(t *testing.T) {
	got := Render("use `<b>` aqui <b>x</b>")
	if !strings.Contains(got, "<code>&lt;b&gt;</code>") || strings.Contains(got, "<b>x") {
		t.Errorf("unexpected: %s", got)
	}
}

func TestRenderArtifactNoRemoteImages(t *testing.T) {
	got := RenderArtifact("![logo](https://track.example/p.gif?u=1)\n\n![](//cdn.example/x.png)\n\n![local](/api/x.png)")
	for _, bad := range []string{`src="https://track.example`, `src="//cdn.example`} {
		if strings.Contains(got, bad) {
			t.Errorf("imagem externa carregada (%s): %s", bad, got)
		}
	}
	if !strings.Contains(got, `<a href="https://track.example/p.gif?u=1" rel="noopener noreferrer nofollow" target="_blank">logo</a>`) ||
		!strings.Contains(got, `>//cdn.example/x.png</a>`) {
		t.Errorf("imagem externa deveria virar link: %s", got)
	}
	if !strings.Contains(got, `loading="lazy" referrerpolicy="no-referrer" src="/api/x.png"`) {
		t.Errorf("imagem local sem lazy/no-referrer: %s", got)
	}
	// Render comum não muda
	if !strings.Contains(Render("![a](https://x.example/p.gif)"), `<img src="https://x.example/p.gif"`) {
		t.Error("Render comum deveria manter a imagem")
	}
}
