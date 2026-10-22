package docsite_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/renatobardi/hanko/internal/cli"
	"github.com/renatobardi/hanko/internal/docsite"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("raiz do repositório não encontrada: %v", err)
	}
	return dir
}

func set(items ...string) map[string]bool {
	m := map[string]bool{}
	for _, i := range items {
		m[i] = true
	}
	return m
}

// scan junta os trechos que casam com re (grupo 1) nos .go (sem _test) de um diretório.
func scan(t *testing.T, dir string, re *regexp.Regexp) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("sem fontes em %s: %v", dir, err)
	}
	out := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, "_templ.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
	}
	return out
}

func realRefs(t *testing.T, root string) docsite.Refs {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "api/openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	ops := map[string]bool{}
	for _, methods := range spec.Paths {
		for _, o := range methods {
			if o.OperationID != "" {
				ops[o.OperationID] = true
			}
		}
	}
	adrs := map[string]bool{}
	ents, err := os.ReadDir(filepath.Join(root, "docs/adr"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if n, _, ok := strings.Cut(e.Name(), "-"); ok {
			adrs[n] = true
		}
	}
	uiDir := filepath.Join(root, "internal/ui")
	routes := scan(t, uiDir, regexp.MustCompile(`"((?:GET|POST|PUT|DELETE|PATCH) /[^"\s]*)"`))
	// rotas escritas como "GET "+constante
	consts := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(uiDir, "*.go"))
	constRe := regexp.MustCompile(`(\w+)\s*=\s*"(/[^"\s]*)"`)
	comboRe := regexp.MustCompile(`"((?:GET|POST|PUT|DELETE|PATCH)) "\s*\+\s*(\w+)`)
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, m := range constRe.FindAllStringSubmatch(string(b), -1) {
			consts[m[1]] = m[2]
		}
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, m := range comboRe.FindAllStringSubmatch(string(b), -1) {
			if p, ok := consts[m[2]]; ok {
				routes[m[1]+" "+p] = true
			}
		}
	}
	return docsite.Refs{
		UIRoutes:   routes,
		CLIPaths:   set(cli.CommandPaths()...),
		APIOps:     ops,
		MCPTools:   scan(t, filepath.Join(root, "internal/mcp"), regexp.MustCompile(`"(hanko_[a-z_]+)"`)),
		ADRNumbers: adrs,
		FileExists: func(p string) bool { _, err := os.Stat(filepath.Join(root, p)); return err == nil },
	}
}

// O feature-map só diz o que existe: cada rota, comando, operação, tool, ADR e arquivo e2e citado é conferido contra o código.
func TestFeatureMapMatchesTheCode(t *testing.T) {
	root := repoRoot(t)
	fm, err := docsite.LoadFeatureMap(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := fm.Validate(realRefs(t, root)); err != nil {
		t.Fatal(err)
	}
	st := fm.Stats()
	if st.Features < 20 || st.Covered == 0 {
		t.Errorf("o feature-map deveria cobrir o produto: %+v", st)
	}
}

func TestFeatureMapValidateCatchesProblems(t *testing.T) {
	refs := docsite.Refs{UIRoutes: set("GET /a"), CLIPaths: set("x"), APIOps: set("op"), MCPTools: set("hanko_t"), ADRNumbers: set("0001"),
		FileExists: func(p string) bool { return p == "e2e/ok.ts" }}
	good := `{"version":1,"domains":[{"id":"d","title":{"pt":"D","en":"D"},"adrs":["0001"],"features":[{"id":"d.f","title":{"pt":"F","en":"F"},
	 "surfaces":{"ui":["GET /a"],"cli":["x"],"api":["op"],"mcp":["hanko_t"]},"script":{"pt":"p","en":"e"},"e2e":["e2e/ok.ts"],"status":"covered"}]}]}`
	fm, err := docsite.ParseFeatureMap([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if err := fm.Validate(refs); err != nil {
		t.Fatalf("válido: %v", err)
	}
	mut := map[string]string{
		"rota":        strings.Replace(good, `"GET /a"`, `"GET /b"`, 1),
		"comando":     strings.Replace(good, `"x"`, `"y"`, 1),
		"operação":    strings.Replace(good, `"op"`, `"nope"`, 1),
		"tool":        strings.Replace(good, `"hanko_t"`, `"hanko_z"`, 1),
		"adr":         strings.Replace(good, `"0001"`, `"9999"`, 1),
		"e2e":         strings.Replace(good, `"e2e/ok.ts"`, `"e2e/missing.ts"`, 1),
		"status":      strings.Replace(good, `"covered"`, `"done"`, 1),
		"gap com e2e": strings.Replace(good, `"covered"`, `"gap"`, 1),
		"id fora":     strings.Replace(good, `"d.f"`, `"x.f"`, 1),
		"sem en":      strings.Replace(good, `"title":{"pt":"F","en":"F"}`, `"title":{"pt":"F","en":""}`, 1),
		"sem roteiro": strings.Replace(good, `"script":{"pt":"p","en":"e"}`, `"script":{"pt":"p","en":""}`, 1),
	}
	for name, doc := range mut {
		fm, err := docsite.ParseFeatureMap([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := fm.Validate(refs); err == nil {
			t.Errorf("%s: a mutação deveria ser recusada", name)
		}
	}
	// campo desconhecido e versão errada
	if _, err := docsite.ParseFeatureMap([]byte(`{"version":1,"domains":[],"extra":1}`)); err == nil {
		t.Error("campo desconhecido deveria ser recusado")
	}
	if _, err := docsite.ParseFeatureMap([]byte(`{"version":2,"domains":[]}`)); err == nil {
		t.Error("versão 2 deveria ser recusada")
	}
}

// O site do repositório real: gera sem erro, tem as duas línguas, o feature-map e nenhum link quebrado entre documentos.
func TestBuildRealSite(t *testing.T) {
	root := repoRoot(t)
	site, err := docsite.Build(os.DirFS(root), docsite.Options{Version: "teste"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"index.html", "en/index.html", "feature-map.html", "en/feature-map.html", "feature-map.json", "sitemap.txt",
		"RUNBOOK.html", "GLOSSARIO.html", "en/RUNBOOK.html", "en/GLOSSARY.html", "adr/0061-trilha-onda3-onboarding-contextos-preferencias-docs.html"} {
		if len(site.Pages[p]) == 0 {
			t.Errorf("página ausente: %s", p)
		}
	}
	for _, p := range []string{"SESSION.html", "PROGRESSO.html", "PLANO-DECISOES.html", "VALIDACAO-MANHA.html"} {
		if _, ok := site.Pages[p]; ok {
			t.Errorf("arquivo de trabalho não deve ser publicado: %s", p)
		}
	}
	if len(site.Warnings) > 0 {
		t.Errorf("links entre documentos que não resolvem:\n%s", strings.Join(site.Warnings, "\n"))
	}
	fm := string(site.Pages["feature-map.html"])
	en := string(site.Pages["en/feature-map.html"])
	if !strings.Contains(fm, "Primeiros passos") || !strings.Contains(en, "First steps") || !strings.Contains(en, "onboarding.wizard") {
		t.Error("o feature-map deve sair nos dois idiomas com os ids das funcionalidades")
	}
	// páginas autossuficientes: nada de recurso externo (sem rede, sem rastreador)
	for p, b := range site.Pages {
		if !strings.HasSuffix(p, ".html") {
			continue
		}
		s := string(b)
		if strings.Contains(s, "<script") || regexp.MustCompile(`(?i)(src|href)="https?://[^"]*\.(js|css|woff2?)"`).MatchString(s) {
			t.Errorf("%s carrega recurso externo ou script", p)
		}
		if !strings.Contains(s, "<title>") || !strings.Contains(s, `lang="`) {
			t.Errorf("%s sem title/lang", p)
		}
	}
	// determinismo
	again, err := docsite.Build(os.DirFS(root), docsite.Options{Version: "teste"})
	if err != nil {
		t.Fatal(err)
	}
	for p, b := range site.Pages {
		if string(again.Pages[p]) != string(b) {
			t.Fatalf("saída não determinística em %s", p)
		}
	}
}

func TestBuildRewritesLinksAndWritesToDisk(t *testing.T) {
	fm := `{"version":1,"domains":[]}`
	root := fstest.MapFS{
		"docs/A.md":             {Data: []byte("# Guia A\n\nVeja [B](B.md#x), [ADR](adr/0001-x.md), [fora](https://ex.com/p.md), [sumiu](Z.md) e [ancora](#a).\n")},
		"docs/B.md":             {Data: []byte("# Guia B\n\n<script>alert(1)</script>\n")},
		"docs/SESSION.md":       {Data: []byte("# interno\n")},
		"docs/adr/0001-x.md":    {Data: []byte("# ADR-0001\n\n[volta](../A.md)\n")},
		"docs/en/A.md":          {Data: []byte("# Guide A\n\n[B](B.md)\n")},
		"docs/en/B.md":          {Data: []byte("# Guide B\n")},
		"docs/feature-map.json": {Data: []byte(fm)},
	}
	site, err := docsite.Build(root, docsite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a := string(site.Pages["A.html"])
	for _, want := range []string{`href="B.html#x"`, `href="adr/0001-x.html"`, `href="https://ex.com/p.md"`, `href="Z.md"`, `href="#a"`, "<title>Guia A · Hanko</title>", "Hanko dev"} {
		if !strings.Contains(a, want) {
			t.Errorf("A.html sem %q", want)
		}
	}
	if !strings.Contains(string(site.Pages["adr/0001-x.html"]), `href="../A.html"`) {
		t.Error("link relativo do ADR para o guia")
	}
	if !strings.Contains(string(site.Pages["en/A.html"]), `href="B.html"`) {
		t.Error("link no site em inglês")
	}
	if strings.Contains(string(site.Pages["B.html"]), "<script>") {
		t.Error("HTML cru no markdown nunca vira marcação")
	}
	if len(site.Warnings) != 1 || !strings.Contains(site.Warnings[0], "Z.md") {
		t.Errorf("avisos: %v", site.Warnings)
	}
	if _, ok := site.Pages["SESSION.html"]; ok {
		t.Error("SESSION.md não é publicado")
	}
	dir := t.TempDir()
	if err := site.WriteTo(dir); err != nil {
		t.Fatal(err)
	}
	for p := range site.Pages {
		if _, err := fs.Stat(os.DirFS(dir), p); err != nil {
			t.Errorf("não gravou %s: %v", p, err)
		}
	}
	// pasta de documentação ausente é erro claro
	if _, err := docsite.Build(fstest.MapFS{}, docsite.Options{}); err == nil {
		t.Error("sem docs/ deveria falhar")
	}
}
