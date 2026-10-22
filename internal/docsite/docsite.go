// Package docsite gera o site estático de documentação (docs/ em pt-BR e docs/en em inglês) e a página do feature-map.
// É só um gerador: lê um fs.FS com o repositório e devolve as páginas em memória; nada é publicado (ADR-0060).
package docsite

import (
	"fmt"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/renatobardi/hanko/internal/ui/md"
)

// excluded são arquivos de trabalho do projeto, que não fazem parte da documentação publicada.
var excluded = map[string]bool{"SESSION.md": true, "PROGRESSO.md": true, "VALIDACAO-MANHA.md": true, "PLANO-DECISOES.md": true}

// Lang é um dos dois idiomas do site.
type Lang string

// Idiomas.
const (
	PT Lang = "pt-BR"
	EN Lang = "en"
)

// Options configura a geração.
type Options struct {
	// Version aparece no rodapé ("dev" quando vazia).
	Version string
}

// Site é o resultado: caminho (relativo à raiz do site) → conteúdo.
type Site struct {
	Pages map[string][]byte
	// Warnings são links entre documentos que não resolvem.
	Warnings []string
	// DocPages são as páginas de documentação geradas (sem índices nem feature-map), para conferência.
	DocPages []string
}

type doc struct {
	src   string // caminho do .md relativo à raiz do repositório
	out   string // caminho da página gerada
	title string
	lang  Lang
	body  string // HTML
}

var h1Re = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

// collect lê os .md de um diretório (não recursivo) e, se existir, de dir/adr.
func collect(root fs.FS, dir string, lang Lang, outPrefix string) ([]doc, error) {
	var docs []doc
	for _, sub := range []string{"", "adr"} {
		d := path.Join(dir, sub)
		ents, err := fs.ReadDir(root, d)
		if err != nil {
			if sub == "adr" {
				continue
			}
			return nil, err
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".md") || (sub == "" && excluded[name]) {
				continue
			}
			src := path.Join(d, name)
			raw, err := fs.ReadFile(root, src)
			if err != nil {
				return nil, err
			}
			title := strings.TrimSuffix(name, ".md")
			if m := h1Re.FindSubmatch(raw); m != nil {
				title = strings.TrimSpace(string(m[1]))
			}
			out := path.Join(outPrefix, sub, strings.TrimSuffix(name, ".md")+".html")
			docs = append(docs, doc{src: src, out: out, title: title, lang: lang, body: md.Render(string(raw))})
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].out < docs[j].out })
	return docs, nil
}

var hrefRe = regexp.MustCompile(`href="([^"]+)"`)

// rewriteLinks troca href="…md[#a]" por .html e registra os que apontam para documento que não existe no site.
func rewriteLinks(d doc, known map[string]string, warn func(string)) string {
	return hrefRe.ReplaceAllStringFunc(d.body, func(m string) string {
		href := hrefRe.FindStringSubmatch(m)[1]
		if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
			return m
		}
		target, frag, _ := strings.Cut(href, "#")
		if !strings.HasSuffix(target, ".md") {
			return m
		}
		abs := path.Clean(path.Join(path.Dir(d.src), html.UnescapeString(target)))
		out, ok := known[abs]
		if !ok {
			warn(fmt.Sprintf("%s: link para %s não resolve para uma página do site", d.src, target))
			return m
		}
		rel, err := relPath(path.Dir(d.out), out)
		if err != nil {
			return m
		}
		if frag != "" {
			rel += "#" + frag
		}
		return `href="` + html.EscapeString(rel) + `"`
	})
}

func relPath(from, to string) (string, error) {
	if from == "" {
		from = "."
	}
	r, err := filepath.Rel(filepath.FromSlash(from), filepath.FromSlash(to))
	return filepath.ToSlash(r), err
}

// Build gera o site inteiro: docs/*.md e docs/adr em pt-BR, docs/en em inglês, índices e o feature-map.
func Build(root fs.FS, opt Options) (*Site, error) {
	pt, err := collect(root, "docs", PT, "")
	if err != nil {
		return nil, fmt.Errorf("docs/: %w", err)
	}
	en, err := collect(root, "docs/en", EN, "en")
	if err != nil {
		return nil, fmt.Errorf("docs/en/: %w", err)
	}
	all := append(append([]doc{}, pt...), en...)
	known := map[string]string{}
	for _, d := range all {
		known[d.src] = d.out
	}
	site := &Site{Pages: map[string][]byte{}}
	warn := func(s string) { site.Warnings = append(site.Warnings, s) }
	version := opt.Version
	if version == "" {
		version = "dev"
	}

	nav := func(lang Lang, cur string) string { return navHTML(lang, cur) }
	for _, d := range all {
		body := rewriteLinks(d, known, warn)
		site.Pages[d.out] = []byte(page(d.lang, d.title, d.out, nav(d.lang, d.out), `<article class="md">`+body+`</article>`, version))
		site.DocPages = append(site.DocPages, d.out)
	}
	site.Pages["index.html"] = []byte(page(PT, "Documentação do Hanko", "index.html", nav(PT, "index.html"), indexBody(PT, pt), version))
	site.Pages["en/index.html"] = []byte(page(EN, "Hanko documentation", "en/index.html", nav(EN, "en/index.html"), indexBody(EN, en), version))

	fmRaw, err := fs.ReadFile(root, FeatureMapPath)
	if err != nil {
		return nil, fmt.Errorf("feature-map: %w", err)
	}
	fm, err := ParseFeatureMap(fmRaw)
	if err != nil {
		return nil, err
	}
	site.Pages["feature-map.json"] = fmRaw
	site.Pages["feature-map.html"] = []byte(page(PT, "Feature-map", "feature-map.html", nav(PT, "feature-map.html"), featureMapBody(PT, fm), version))
	site.Pages["en/feature-map.html"] = []byte(page(EN, "Feature map", "en/feature-map.html", nav(EN, "en/feature-map.html"), featureMapBody(EN, fm), version))

	var urls []string
	for p := range site.Pages {
		if strings.HasSuffix(p, ".html") {
			urls = append(urls, p)
		}
	}
	sort.Strings(urls)
	site.Pages["sitemap.txt"] = []byte(strings.Join(urls, "\n") + "\n")
	sort.Strings(site.DocPages)
	sort.Strings(site.Warnings)
	return site, nil
}

// WriteTo grava o site em dir (cria os diretórios; sobrescreve arquivos).
func (s *Site) WriteTo(dir string) error {
	for p, b := range s.Pages {
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil { //nolint:gosec // site público estático
			return err
		}
	}
	return nil
}

func navHTML(lang Lang, cur string) string {
	pre := ""
	if lang == EN {
		pre = "en/"
	}
	t := func(pt, en string) string {
		if lang == EN {
			return en
		}
		return pt
	}
	link := func(target, label string) string {
		rel, _ := relPath(path.Dir(cur), target)
		a := ""
		if target == cur {
			a = ` aria-current="page"`
		}
		return `<a href="` + html.EscapeString(rel) + `"` + a + `>` + html.EscapeString(label) + `</a>`
	}
	other := "en/index.html"
	otherLabel := "English"
	if lang == EN {
		other, otherLabel = "index.html", "Português"
	}
	return `<nav>` + link(pre+"index.html", t("Documentação", "Documentation")) + link(pre+"feature-map.html", "Feature map") + link(other, otherLabel) + `</nav>`
}

func indexBody(lang Lang, docs []doc) string {
	var b strings.Builder
	if lang == EN {
		b.WriteString("<h1>Hanko documentation</h1><p>Control plane for teams of AI personas. Generated from <code>docs/en</code>; the Portuguese source is the reference.</p>")
	} else {
		b.WriteString("<h1>Documentação do Hanko</h1><p>Control plane para times de personas de IA. Gerada de <code>docs/</code>; a versão em inglês está em <a href=\"en/index.html\">English</a>.</p>")
	}
	var guides, adrs []doc
	for _, d := range docs {
		if strings.Contains(d.out, "adr/") {
			adrs = append(adrs, d)
		} else {
			guides = append(guides, d)
		}
	}
	section := func(title string, ds []doc) {
		if len(ds) == 0 {
			return
		}
		b.WriteString("<h2>" + html.EscapeString(title) + "</h2><ul>")
		for _, d := range ds {
			rel, _ := relPath(path.Dir(map[Lang]string{PT: "index.html", EN: "en/index.html"}[lang]), d.out)
			b.WriteString(`<li><a href="` + html.EscapeString(rel) + `">` + html.EscapeString(d.title) + `</a></li>`)
		}
		b.WriteString("</ul>")
	}
	if lang == EN {
		section("Guides", guides)
		section("Architecture decisions", adrs)
	} else {
		section("Guias", guides)
		section("Decisões de arquitetura", adrs)
	}
	return b.String()
}

func featureMapBody(lang Lang, fm FeatureMap) string {
	t := func(i I18n) string {
		if lang == EN {
			return i.EN
		}
		return i.PT
	}
	var b strings.Builder
	st := fm.Stats()
	if lang == EN {
		b.WriteString("<h1>Feature map</h1><p>Features by domain, with the surfaces where each one lives and the journey script that e2e tests and behavior evals follow. ")
		fmt.Fprintf(&b, "%d features: %d covered by an e2e journey, %d partial, %d gaps.</p>", st.Features, st.Covered, st.Partial, st.Gap)
		b.WriteString(`<p><a href="feature-map.json">feature-map.json</a></p>`)
	} else {
		b.WriteString("<h1>Feature-map</h1><p>Funcionalidades por domínio, com as superfícies onde cada uma vive e o roteiro que os testes e2e e os evals de comportamento seguem. ")
		fmt.Fprintf(&b, "%d funcionalidades: %d com jornada e2e, %d parciais, %d lacunas.</p>", st.Features, st.Covered, st.Partial, st.Gap)
		b.WriteString(`<p><a href="feature-map.json">feature-map.json</a></p>`)
	}
	code := func(items []string) string {
		if len(items) == 0 {
			return "—"
		}
		parts := make([]string, len(items))
		for i, x := range items {
			parts[i] = "<code>" + html.EscapeString(x) + "</code>"
		}
		return strings.Join(parts, "<br>")
	}
	hdr := []string{"Funcionalidade", "UI", "CLI", "API", "MCP", "Roteiro", "E2E", "Evals", "Status"}
	if lang == EN {
		hdr = []string{"Feature", "UI", "CLI", "API", "MCP", "Script", "E2E", "Evals", "Status"}
	}
	for _, d := range fm.Domains {
		b.WriteString(`<h2 id="` + html.EscapeString(d.ID) + `">` + html.EscapeString(t(d.Title)) + `</h2>`)
		if len(d.ADRs) > 0 {
			b.WriteString("<p>ADR: " + html.EscapeString(strings.Join(d.ADRs, ", ")) + "</p>")
		}
		b.WriteString(`<div class="scroll"><table><thead><tr>`)
		for _, h := range hdr {
			b.WriteString("<th>" + h + "</th>")
		}
		b.WriteString("</tr></thead><tbody>")
		for _, f := range d.Features {
			b.WriteString(`<tr id="` + html.EscapeString(f.ID) + `"><td><b>` + html.EscapeString(t(f.Title)) + `</b><br><small>` + html.EscapeString(f.ID) + `</small></td>`)
			for _, c := range [][]string{f.Surfaces.UI, f.Surfaces.CLI, f.Surfaces.API, f.Surfaces.MCP} {
				b.WriteString("<td>" + code(c) + "</td>")
			}
			b.WriteString("<td>" + html.EscapeString(t(f.Script)) + "</td><td>" + code(f.E2E) + "</td><td>" + code(f.Evals) + `</td><td><span class="st ` + html.EscapeString(f.Status) + `">` + html.EscapeString(f.Status) + "</span></td></tr>")
		}
		b.WriteString("</tbody></table></div>")
	}
	return b.String()
}

const css = `:root{--bg:#fff;--fg:#1c1917;--muted:#78716c;--border:#e7e5e4;--card:#fafaf9;--link:#1d4ed8;--ok:#15803d;--warn:#b45309;--bad:#b91c1c}
@media (prefers-color-scheme:dark){:root{--bg:#0c0a09;--fg:#fafaf9;--muted:#a8a29e;--border:#292524;--card:#1c1917;--link:#93c5fd;--ok:#4ade80;--warn:#fbbf24;--bad:#f87171}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 Inter,system-ui,sans-serif}
header{border-bottom:1px solid var(--border);padding:12px 16px;display:flex;gap:16px;align-items:center;flex-wrap:wrap}
header b{font-weight:600}nav{display:flex;gap:14px;flex-wrap:wrap}nav a[aria-current]{font-weight:600}
a{color:var(--link)}main{max-width:1100px;margin:0 auto;padding:24px 16px}footer{border-top:1px solid var(--border);color:var(--muted);padding:16px;font-size:14px;text-align:center}
.md pre{background:var(--card);border:1px solid var(--border);padding:12px;overflow:auto;border-radius:8px}.md code,code{background:var(--card);padding:1px 4px;border-radius:4px;font-size:.9em}
.md table,table{border-collapse:collapse;width:100%}.md th,.md td,th,td{border:1px solid var(--border);padding:6px 8px;text-align:left;vertical-align:top;font-size:14px}
.scroll{overflow-x:auto}small{color:var(--muted)}.st{font-weight:600}.st.covered{color:var(--ok)}.st.partial{color:var(--warn)}.st.gap{color:var(--bad)}
blockquote{border-left:3px solid var(--border);margin:0;padding-left:12px;color:var(--muted)}`

func page(lang Lang, title, cur, nav, body, version string) string {
	foot := "Hanko " + version
	return `<!doctype html><html lang="` + string(lang) + `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>` +
		html.EscapeString(title) + ` · Hanko</title><style>` + css + `</style></head><body><header><b>Hanko</b>` + nav + `</header><main>` + body + `</main><footer>` + html.EscapeString(foot) + `</footer></body></html>`
}
