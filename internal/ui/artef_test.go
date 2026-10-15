package ui

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Artefatos e metas na UI (ADR-0028).

func artifactIDOf(t *testing.T, f *uitest.Fake, name string, version int) string {
	t.Helper()
	for _, a := range f.Artifacts {
		if a.Name == name && a.Version == version {
			return a.ID.String()
		}
	}
	t.Fatalf("o Sample não tem %s v%d", name, version)
	return ""
}

// upload envia um multipart same-origin com os campos e, se filename != "", o arquivo.
func (e *env) upload(t *testing.T, path string, fields map[string]string, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if filename != "" {
		fw, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	rq := httptest.NewRequest("POST", path, &buf)
	rq.Header.Set("Content-Type", mw.FormDataContentType())
	rq.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, rq)
	return rec
}

func TestArtifactPagesRender(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	f := e.f
	slide2, slide1 := artifactIDOf(t, f, "slide-01-tese.md", 2), artifactIDOf(t, f, "slide-01-tese.md", 1)
	t.Run("lista mostra só a versão mais nova de cada artefato", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/artefatos")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Artefatos", "Enviar artefato", "slide-01-tese.md", "plano-migracao.txt", "entidades.json", "grafo.png", "2 versões",
			`href="/c/hanko/artefatos/`+slide2+`"`, `href="/c/hanko/tasks/HNK-221"`, `href="/c/hanko/personas/ada"`, `href="/c/hanko/projetos/kubo"`, "Usado de")
		wantNotContains(t, rec, `href="/c/hanko/artefatos/`+slide1+`"`)
	})
	t.Run("filtros", func(t *testing.T) {
		for path, c := range map[string]struct{ in, out []string }{
			"/c/hanko/artefatos?projeto=nas-3d":  {[]string{"plano-migracao.txt"}, []string{"entidades.json", "slide-01-tese.md"}},
			"/c/hanko/artefatos?task=HNK-221":    {[]string{"slide-01-tese.md"}, []string{"plano-migracao.txt", "grafo.png"}},
			"/c/hanko/artefatos?persona=kenji":   {[]string{"plano-migracao.txt"}, []string{"entidades.json"}},
			"/c/hanko/artefatos?tipo=image":      {[]string{"grafo.png"}, []string{"entidades.json", "slide-01-tese.md"}},
			"/c/hanko/artefatos?tipo=markdown":   {[]string{"slide-01-tese.md"}, []string{"grafo.png"}},
			"/c/hanko/artefatos?q=PLANO":         {[]string{"plano-migracao.txt"}, []string{"slide-01-tese.md"}},
			"/c/hanko/artefatos?q=nada-disso":    {[]string{"Nenhum artefato com esses filtros."}, []string{"slide-01-tese.md"}},
			"/c/hanko/artefatos?projeto=kubo&q=": {[]string{"entidades.json", "limpar filtros"}, []string{"plano-migracao.txt"}},
		} {
			rec := e.get(t, path)
			wantStatus(t, rec, http.StatusOK)
			wantContains(t, rec, c.in...)
			wantNotContains(t, rec, c.out...)
		}
		for _, bad := range []string{"/c/hanko/artefatos?tipo=zip", "/c/hanko/artefatos?projeto=nao-existe", "/c/hanko/artefatos?task=HNK-9999", "/c/hanko/artefatos?persona=zzz"} {
			if rec := e.get(t, bad); rec.Code == http.StatusOK {
				t.Errorf("%s: status 200, queria erro", bad)
			}
		}
	})
	t.Run("detalhe markdown com versões", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/artefatos/"+slide2)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "slide-01-tese.md", "v2", "<strong>melhor</strong>", `class="md"`, "Versões", "comparar com a anterior",
			`href="/c/hanko/artefatos/`+slide1+`"`, "Baixar", `/conteudo?baixar=1`, "HNK-221", "Ada", "text/markdown")
		wantNotContains(t, rec, "versão antiga")
	})
	t.Run("versão antiga avisa e aponta a nova", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/artefatos/"+slide1)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "versão antiga", "ver a mais nova", `href="/c/hanko/artefatos/`+slide2+`"`)
	})
	t.Run("diff entre versões", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/artefatos/"+slide2+"?contra="+slide1)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, `id="diff"`, "Diferenças: v1 → v2", `class="diff"`, "diff-row add", "diff-row del", "+2", "−1", "fonte: relatório de setembro")
		// contra uma versão de outro artefato: aviso, não erro de página
		other := artifactIDOf(t, f, "plano-migracao.txt", 1)
		rec = e.get(t, "/c/hanko/artefatos/"+slide2+"?contra="+other)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Não foi possível comparar estas duas versões")
		wantNotContains(t, rec, `class="diff"`)
		// mesma versão: nada a mostrar
		rec = e.get(t, "/c/hanko/artefatos/"+slide2+"?contra="+slide2)
		wantContains(t, rec, "As duas versões têm o mesmo texto.")
	})
	t.Run("texto, imagem e sem prévia", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/artefatos/"+artifactIDOf(t, f, "entidades.json", 1))
		wantContains(t, rec, `<pre class="art-pre">`, "&#34;pessoas&#34;: 41")
		id := artifactIDOf(t, f, "grafo.png", 1)
		rec = e.get(t, "/c/hanko/artefatos/"+id)
		wantContains(t, rec, `<img class="art-img" src="/c/hanko/artefatos/`+id+`/conteudo"`)
		rec = e.get(t, "/c/hanko/artefatos/"+artifactIDOf(t, f, "relatorio.pdf", 1))
		wantContains(t, rec, "Sem prévia para este tipo de arquivo.")
		wantNotContains(t, rec, "<img", "art-pre")
	})
	t.Run("em inglês", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/artefatos/"+slide2+"?contra="+slide1, "Cookie", "hanko_lang=en")
		wantContains(t, rec, "Differences: v1 → v2", "Versions", "compare with the previous one", "Preview", "Download")
	})
	t.Run("não encontrado", func(t *testing.T) {
		wantStatus(t, e.get(t, "/c/hanko/artefatos/"+uitest.ID("nada").String()), http.StatusNotFound)
		wantStatus(t, e.get(t, "/c/hanko/artefatos/not-a-uuid"), http.StatusNotFound)
		wantStatus(t, e.get(t, "/c/hanko/artefatos/not-a-uuid/conteudo"), http.StatusNotFound)
	})
}

func TestArtifactsEmpty(t *testing.T) {
	e := newEnv(t, uitest.Empty())
	rec := e.get(t, "/c/hanko/artefatos")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Nenhum artefato ainda.", "Enviar artefato")
	wantNotContains(t, rec, `class="toolbar art-filters"`)
}

// O conteúdo é de terceiros: nunca vira página.
func TestArtifactContentHeaders(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	put := func(name, mime, body string) string {
		av, err := f.PutArtifact(context.Background(), domain.HumanActor, f.Companies[0].ID, service.PutArtifactInput{Name: name, Mime: mime, Content: []byte(body)})
		if err != nil {
			t.Fatal(err)
		}
		return av.ID.String()
	}
	html := put("pagina.html", "text/html", `<script>alert(1)</script>`)
	svg := put("desenho.svg", "image/svg+xml", `<svg onload="alert(1)"/>`)
	md := put("nota.md", "text/markdown", "# oi")
	check := func(name, id, query, disp, ctype string) {
		t.Helper()
		rec := e.get(t, "/c/hanko/artefatos/"+id+"/conteudo"+query)
		wantStatus(t, rec, http.StatusOK)
		h := rec.Header()
		if got := h.Get("Content-Disposition"); !strings.HasPrefix(got, disp) {
			t.Errorf("%s: Content-Disposition = %q, quer %s", name, got, disp)
		}
		if got := h.Get("Content-Type"); got != ctype {
			t.Errorf("%s: Content-Type = %q, quer %q", name, got, ctype)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: sem nosniff", name)
		}
		csp := h.Get("Content-Security-Policy")
		for _, w := range []string{"default-src 'none'", "sandbox"} {
			if !strings.Contains(csp, w) {
				t.Errorf("%s: CSP %q sem %q", name, csp, w)
			}
		}
	}
	check("html", html, "", "attachment", "text/html; charset=utf-8")
	check("svg", svg, "", "attachment", "image/svg+xml; charset=utf-8")
	check("md inline", md, "", "inline", "text/markdown; charset=utf-8")
	check("md baixar", md, "?baixar=1", "attachment", "text/markdown; charset=utf-8")
	check("png", artifactIDOf(t, f, "grafo.png", 1), "", "inline", "image/png")

	// a prévia de um HTML é texto escapado, nunca marcação viva
	rec := e.get(t, "/c/hanko/artefatos/"+html)
	wantContains(t, rec, "&lt;script&gt;alert(1)&lt;/script&gt;")
	wantNotContains(t, rec, "<script>alert(1)</script>")
	rec = e.get(t, "/c/hanko/artefatos/"+svg)
	wantNotContains(t, rec, "<img", `<svg onload`)

	// markdown com HTML cru: escapado pelo renderizador
	xss := put("xss.md", "text/markdown", "<img src=x onerror=alert(1)>\n\n[x](javascript:alert(1))")
	rec = e.get(t, "/c/hanko/artefatos/"+xss)
	wantNotContains(t, rec, "<img src=x", `href="javascript:`)
}

func TestArtifactPreviewLimit(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	big := strings.Repeat("linha de texto\n", artifactPreviewMax/10)
	av, err := f.PutArtifact(context.Background(), domain.HumanActor, f.Companies[0].ID, service.PutArtifactInput{Name: "grande.txt", Content: []byte(big)})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.get(t, "/c/hanko/artefatos/"+av.ID.String())
	wantContains(t, rec, "Prévia limitada ao começo do arquivo.")
	if n := rec.Body.Len(); n > 2*artifactPreviewMax {
		t.Errorf("a página tem %d bytes; a prévia deveria ser cortada em %d", n, artifactPreviewMax)
	}
	// binário rotulado como texto: sem prévia, nunca lixo na tela
	bin, err := f.PutArtifact(context.Background(), domain.HumanActor, f.Companies[0].ID, service.PutArtifactInput{Name: "falso.txt", Mime: "text/plain", Content: []byte{0xff, 0xfe, 0x00, 0x01}})
	if err != nil {
		t.Fatal(err)
	}
	rec = e.get(t, "/c/hanko/artefatos/"+bin.ID.String())
	wantContains(t, rec, "Sem prévia para este tipo de arquivo.")
}

func TestArtifactUpload(t *testing.T) {
	t.Run("formulário", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.get(t, "/c/hanko/artefatos/novo?task=HNK-221")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, `enctype="multipart/form-data"`, `type="file"`, `name="task"`, `value="HNK-221"`, `name="project"`, "Até 5 MiB por arquivo")
	})
	t.Run("ok cria a versão seguinte na task", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.upload(t, "/c/hanko/artefatos/novo", map[string]string{"task": "HNK-221"}, "slide-01-tese.md", []byte("# v3\n"))
		wantStatus(t, rec, http.StatusSeeOther)
		loc := rec.Header().Get("Location")
		if !strings.HasPrefix(loc, "/c/hanko/artefatos/") || !strings.HasSuffix(loc, "?ok=enviado") {
			t.Fatalf("Location = %q", loc)
		}
		if !e.f.Called("PutArtifact") {
			t.Error("PutArtifact não foi chamado")
		}
		page := e.get(t, loc)
		wantContains(t, page, "Artefato enviado.", "v3", "slide-01-tese.md")
	})
	t.Run("nome do campo vence e caminho do navegador é descartado", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.upload(t, "/c/hanko/artefatos/novo", nil, `C:\fakepath\..\relatorio.txt`, []byte("x"))
		wantStatus(t, rec, http.StatusSeeOther)
		last := e.f.Artifacts[len(e.f.Artifacts)-1]
		if last.Name != "relatorio.txt" {
			t.Errorf("nome = %q", last.Name)
		}
		rec = e.upload(t, "/c/hanko/artefatos/novo", map[string]string{"name": "meu-nome.txt"}, "outro.txt", []byte("x"))
		wantStatus(t, rec, http.StatusSeeOther)
		if last := e.f.Artifacts[len(e.f.Artifacts)-1]; last.Name != "meu-nome.txt" {
			t.Errorf("nome = %q", last.Name)
		}
	})
	t.Run("sem arquivo", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.upload(t, "/c/hanko/artefatos/novo", map[string]string{"task": "HNK-221"}, "", nil)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "escolha um arquivo")
	})
	t.Run("task desconhecida", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.upload(t, "/c/hanko/artefatos/novo", map[string]string{"task": "HNK-9999"}, "a.txt", []byte("x"))
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "Task não encontrada nesta empresa.")
	})
	t.Run("acima do limite por arquivo", func(t *testing.T) {
		f := uitest.Sample()
		f.ArtifactMaxBytes = 1024
		e := newEnv(t, f)
		rec := e.upload(t, "/c/hanko/artefatos/novo", nil, "grande.bin", bytes.Repeat([]byte("a"), 2048))
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "O arquivo passa do limite de 1 KiB por arquivo.")
		// um corpo muito maior que o limite é cortado antes de ler tudo
		rec = e.upload(t, "/c/hanko/artefatos/novo", nil, "enorme.bin", bytes.Repeat([]byte("a"), 3<<20))
		wantStatus(t, rec, http.StatusRequestEntityTooLarge)
		wantContains(t, rec, "O arquivo passa do limite de 1 KiB por arquivo.")
		if n := len(e.f.Artifacts); n != 6 {
			t.Errorf("artefatos = %d; nada deveria ter sido gravado", n)
		}
	})
	t.Run("cota da empresa", func(t *testing.T) {
		f := uitest.Sample()
		f.ArtifactQuotaBytes = 100
		e := newEnv(t, f)
		rec := e.upload(t, "/c/hanko/artefatos/novo", nil, "a.txt", bytes.Repeat([]byte("a"), 90))
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "A cota de artefatos da empresa foi atingida")
	})
	t.Run("proibido para quem o service recusa", func(t *testing.T) {
		f := uitest.Sample()
		f.MyRoleIs = domain.MemberViewer
		f.SetErr("PutArtifact", domain.ErrForbidden)
		e := newEnv(t, f)
		rec := e.upload(t, "/c/hanko/artefatos/novo", nil, "a.txt", []byte("x"))
		wantStatus(t, rec, http.StatusForbidden)
		wantContains(t, rec, "Seu papel nesta empresa não permite esta ação.")
	})
	t.Run("CSRF", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", "a.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		rq := httptest.NewRequest("POST", "/c/hanko/artefatos/novo", &buf)
		rq.Header.Set("Content-Type", mw.FormDataContentType())
		rq.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, rq)
		wantStatus(t, rec, http.StatusForbidden)
		if e.f.Called("PutArtifact") {
			t.Error("upload cross-site não pode chegar ao service")
		}
	})
}

func TestArtifactFriendlyLimitErrors(t *testing.T) {
	ctx := context.Background()
	st, title, msg := friendly(ctx, service.NewArtifactLimitError(false, 5<<20, 0, 6<<20))
	if st != http.StatusUnprocessableEntity || title != "Arquivo recusado" || !strings.Contains(msg, "5 MiB") {
		t.Errorf("tamanho: %d %q %q", st, title, msg)
	}
	_, _, msg = friendly(ctx, service.NewArtifactLimitError(true, 512<<20, 500<<20, 20<<20))
	if !strings.Contains(msg, "500 MiB de 512 MiB") {
		t.Errorf("cota: %q", msg)
	}
	// embrulhado por outro erro, ainda é reconhecido
	wrapped := errors.Join(errors.New("put"), service.NewArtifactLimitError(false, 1024, 0, 2048))
	if _, title, _ := friendly(ctx, wrapped); title != "Arquivo recusado" {
		t.Errorf("embrulhado: %q", title)
	}
}

func TestArtifactDelete(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	id := artifactIDOf(t, f, "slide-01-tese.md", 2)
	rec := e.post(t, "/c/hanko/artefatos/"+id+"/excluir", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/artefatos?ok=excluido" {
		t.Errorf("Location = %q", loc)
	}
	wantContains(t, e.get(t, "/c/hanko/artefatos?ok=excluido"), "Artefato excluído.")
	wantStatus(t, e.get(t, "/c/hanko/artefatos/"+id), http.StatusNotFound)
	for _, a := range f.Artifacts {
		if a.Name == "slide-01-tese.md" {
			t.Errorf("a cadeia inteira deveria ter sido apagada: %s v%d", a.Name, a.Version)
		}
	}
	// service recusa
	f2 := uitest.Sample()
	f2.MyRoleIs = domain.MemberMember
	f2.SetErr("DeleteArtifact", domain.ErrForbidden)
	e2 := newEnv(t, f2)
	rec = e2.post(t, "/c/hanko/artefatos/"+artifactIDOf(t, f2, "grafo.png", 1)+"/excluir", nil, true)
	wantStatus(t, rec, http.StatusForbidden)
}

func TestTaskRunProjectShowArtifacts(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	slide2 := artifactIDOf(t, f, "slide-01-tese.md", 2)
	t.Run("task", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/tasks/HNK-221")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Artefatos", `href="/c/hanko/artefatos/`+slide2+`"`, "slide-01-tese.md", `href="/c/hanko/artefatos?task=HNK-221"`)
		wantContains(t, e.get(t, "/c/hanko/tasks/HNK-211"), "Nenhum artefato ainda.")
	})
	t.Run("execução lista todas as versões que ela publicou", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/execucoes/"+f.Runs[0].ID.String())
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Artefatos", `href="/c/hanko/artefatos/`+slide2+`"`, `href="/c/hanko/artefatos/`+artifactIDOf(t, f, "slide-01-tese.md", 1)+`"`)
		// filtro por execução na lista
		rec = e.get(t, "/c/hanko/artefatos?execucao="+f.Runs[0].ID.String())
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "slide-01-tese.md")
		wantNotContains(t, rec, "plano-migracao.txt")
		wantStatus(t, e.get(t, "/c/hanko/artefatos?execucao=xx"), http.StatusNotFound)
	})
	t.Run("projeto", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/projetos/kubo")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "entidades.json", `href="/c/hanko/artefatos?projeto=kubo"`, `href="/c/hanko/metas?aba=todas&amp;projeto=kubo"`)
	})
}

// ---------- metas ----------

func TestGoalPagesRender(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	t.Run("lista", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/metas")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Metas", "Nova meta", "Chegar a 100 seguidores", "Reduzir a latência p95", "Metas ativas", "Atrasadas", "Atingidas",
			`role="progressbar"`, `aria-valuenow="56"`, "56%", "65 de 100 seg.", "520 de 300 ms", "atrasada", `href="/c/hanko/metas/cem-seguidores"`,
			"Metas mensuráveis ligadas a projetos.", "0 de 2 concluídas")
		wantNotContains(t, rec, "Digest semanal no ar") // atingida: outra aba
	})
	t.Run("abas e filtro por projeto", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/metas?aba=atingidas")
		wantContains(t, rec, "Digest semanal no ar", "Atingida")
		wantNotContains(t, rec, "Chegar a 100 seguidores")
		rec = e.get(t, "/c/hanko/metas?aba=todas&projeto=linkedin")
		wantContains(t, rec, "Chegar a 100 seguidores", "limpar filtro")
		wantNotContains(t, rec, "Reduzir a latência p95")
		wantContains(t, e.get(t, "/c/hanko/metas?aba=abandonadas"), "Nenhuma meta aqui.")
		wantStatus(t, e.get(t, "/c/hanko/metas?projeto=nao-existe"), http.StatusNotFound)
	})
	t.Run("detalhe com gráfico SVG no servidor", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/metas/cem-seguidores")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Chegar a 100 seguidores", "56%", `<svg class="goal-chart"`, "<polyline", `class="gc-dot"`, "alvo 100 seg.", "início 20 seg.",
			"Histórico de progresso", "Tasks ligadas", "HNK-221", "HNK-222", "Registrar progresso", `name="value"`, "Ligar task", "Salvar alterações",
			`href="/c/hanko/projetos/linkedin"`, `href="/c/hanko/personas/ada"`, `href="/c/hanko/tasks?aba=todas&amp;meta=cem-seguidores"`, "<strong>conteúdo semanal</strong>")
	})
	t.Run("meta atingida não recebe progresso", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/metas/digest-no-ar")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Reabrir meta", "Atingida")
		wantNotContains(t, rec, `name="value"`)
	})
	t.Run("sem histórico", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/metas/digest-no-ar")
		wantContains(t, rec, "Ainda não há registros de progresso.")
		wantNotContains(t, rec, "<svg class=\"goal-chart\"")
	})
	t.Run("formulário", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/metas/nova")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, `name="title"`, `name="start"`, `name="target"`, `name="current"`, `name="due"`, `type="date"`, `name="project"`, `name="owner"`)
	})
	t.Run("em inglês", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/metas/cem-seguidores", "Cookie", "hanko_lang=en")
		wantContains(t, rec, "Progress history", "Linked tasks", "Record progress", "Link task", "target 100 seg.", "Initial")
	})
	t.Run("não encontrada", func(t *testing.T) {
		wantStatus(t, e.get(t, "/c/hanko/metas/nao-existe"), http.StatusNotFound)
	})
}

func TestGoalsEmpty(t *testing.T) {
	e := newEnv(t, uitest.Empty())
	rec := e.get(t, "/c/hanko/metas")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Você ainda não tem metas.", "Criar meta")
}

func TestCreateGoal(t *testing.T) {
	linkedin := uitest.ID("project:linkedin").String()
	t.Run("ok", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/metas/nova", url.Values{"title": {"Dobrar a receita"}, "metric": {"receita"}, "unit": {"US$"}, "start": {"1.000,5"}, "target": {"2000"},
			"current": {"1500"}, "due": {"2027-01-31"}, "project": {linkedin}, "description": {"**dobrar**"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/metas/dobrar-a-receita?ok=criada" {
			t.Fatalf("Location = %q", loc)
		}
		g := f.Goals[len(f.Goals)-1]
		if g.StartValue != 1000.5 || g.TargetValue != 2000 || g.CurrentValue != 1500 || g.DueDate == nil || g.DueDate.Format("2006-01-02") != "2027-01-31" || g.ProjectID == nil {
			t.Errorf("meta criada = %+v", g)
		}
		wantContains(t, e.get(t, "/c/hanko/metas/dobrar-a-receita?ok=criada"), "Meta criada.", "Dobrar a receita", "31 jan 2027")
	})
	t.Run("erros por campo", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		for name, c := range map[string]struct {
			form url.Values
			want string
		}{
			"sem título":       {url.Values{"title": {""}, "start": {"0"}, "target": {"10"}}, "Dê um título à meta"},
			"alvo = início":    {url.Values{"title": {"x"}, "start": {"5"}, "target": {"5"}}, "alvo numérico diferente"},
			"alvo texto":       {url.Values{"title": {"x"}, "start": {"0"}, "target": {"muito"}}, "alvo numérico diferente"},
			"início NaN":       {url.Values{"title": {"x"}, "start": {"NaN"}, "target": {"10"}}, "Informe um número, como 0 ou 25,5."},
			"atual já no alvo": {url.Values{"title": {"x"}, "start": {"0"}, "target": {"10"}, "current": {"10"}}, "ainda não alcance o alvo"},
			"data ruim":        {url.Values{"title": {"x"}, "start": {"0"}, "target": {"10"}, "due": {"31/12/2026"}}, "data válida"},
		} {
			rec := e.post(t, "/c/hanko/metas/nova", c.form, false)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s: status %d\n%.300s", name, rec.Code, rec.Body.String())
				continue
			}
			wantContains(t, rec, c.want)
		}
	})
	t.Run("service recusa", func(t *testing.T) {
		f := uitest.Sample()
		f.MyRoleIs = domain.MemberMember
		f.SetErr("CreateGoal", domain.ErrForbidden)
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/metas/nova", url.Values{"title": {"x"}, "start": {"0"}, "target": {"10"}}, false)
		wantStatus(t, rec, http.StatusForbidden)
		wantContains(t, rec, "Seu papel nesta empresa não permite esta ação.")
	})
}

func TestGoalProgressAndAchieve(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	rec := e.post(t, "/c/hanko/metas/cem-seguidores/progresso", url.Values{"value": {"80,5"}, "note": {"semana boa"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/metas/cem-seguidores?ok=progresso" {
		t.Fatalf("Location = %q", loc)
	}
	page := e.get(t, "/c/hanko/metas/cem-seguidores?ok=progresso")
	wantContains(t, page, "Progresso registrado.", "semana boa", "80,5 seg.", "76%")

	// cruzar o alvo: atingida, aviso próprio e item informativo na Caixa de entrada
	rec = e.post(t, "/c/hanko/metas/cem-seguidores/progresso", url.Values{"value": {"100"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/metas/cem-seguidores?ok=atingida" {
		t.Fatalf("Location = %q", loc)
	}
	wantContains(t, e.get(t, "/c/hanko/metas/cem-seguidores?ok=atingida"), "Meta atingida. Parabéns!", "Reabrir meta")
	inbox := e.get(t, "/c/hanko/caixa")
	wantContains(t, inbox, "Metas atingidas", "Chegar a 100 seguidores", "A meta chegou ao alvo: 100 de 100 seg.", `href="/c/hanko/metas/cem-seguidores"`)

	// valor inválido
	rec = e.post(t, "/c/hanko/metas/latencia-p95/progresso", url.Values{"value": {"abc"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	rec = e.post(t, "/c/hanko/metas/latencia-p95/progresso", url.Values{"value": {"Inf"}}, true)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	// meta que não recebe progresso: o service responde conflito
	rec = e.post(t, "/c/hanko/metas/digest-no-ar/progresso", url.Values{"value": {"1"}}, false)
	wantStatus(t, rec, http.StatusConflict)
	wantStatus(t, e.post(t, "/c/hanko/metas/nao-existe/progresso", url.Values{"value": {"1"}}, false), http.StatusNotFound)
}

func TestInboxGoalsAreInformative(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	// a meta "digest-no-ar" foi atingida ontem: aparece, mas não conta como pendência
	rec := e.get(t, "/c/hanko/caixa")
	wantContains(t, rec, "Metas atingidas", "Digest semanal no ar")
	rec = e.get(t, "/c/hanko/caixa?aba=gates")
	wantNotContains(t, rec, "Metas atingidas")
	e2 := newEnv(t, func() *uitest.Fake { f := uitest.Empty(); return f }())
	wantNotContains(t, e2.get(t, "/c/hanko/caixa"), "Metas atingidas")
}

func TestGoalEditStatusAndLink(t *testing.T) {
	t.Run("editar", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/metas/cem-seguidores", url.Values{"title": {"Chegar a 200 seguidores"}, "metric": {"seguidores"}, "unit": {"seg."}, "start": {"20"}, "target": {"200"},
			"due": {""}, "project": {""}, "owner": {""}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		g := f.Goals[0]
		if g.Title != "Chegar a 200 seguidores" || g.TargetValue != 200 || g.DueDate != nil || g.ProjectID != nil || g.OwnerPersonaID != nil {
			t.Errorf("meta = %+v", g)
		}
		wantContains(t, e.get(t, "/c/hanko/metas/cem-seguidores?ok=salva"), "Meta atualizada.")
		rec = e.post(t, "/c/hanko/metas/cem-seguidores", url.Values{"title": {"x"}, "start": {"5"}, "target": {"5"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "alvo numérico diferente")
	})
	t.Run("abandonar e reabrir", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/metas/latencia-p95/status", url.Values{"to": {"abandoned"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		wantContains(t, e.get(t, "/c/hanko/metas/latencia-p95?ok=abandonada"), "Meta abandonada", "Reabrir meta")
		wantContains(t, e.get(t, "/c/hanko/metas?aba=abandonadas"), "Reduzir a latência p95")
		rec = e.post(t, "/c/hanko/metas/latencia-p95/status", url.Values{"to": {"active"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		wantContains(t, e.get(t, "/c/hanko/metas/latencia-p95?ok=reaberta"), "Meta reaberta.", "Abandonar meta")
		// "atingida" não se escolhe
		rec = e.post(t, "/c/hanko/metas/latencia-p95/status", url.Values{"to": {"achieved"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
	})
	t.Run("ligar task", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/metas/cem-seguidores/tasks", url.Values{"task": {"HNK-217"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		wantContains(t, e.get(t, "/c/hanko/metas/cem-seguidores?ok=task-ligada"), "Task ligada à meta.", "HNK-217")
		wantStatus(t, e.post(t, "/c/hanko/metas/cem-seguidores/tasks", url.Values{"task": {"HNK-9999"}}, false), http.StatusNotFound)
	})
}

func TestTaskGoalSelect(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	cem, lat := uitest.ID("goal:cem").String(), uitest.ID("goal:latencia").String()
	rec := e.get(t, "/c/hanko/tasks/HNK-221")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `name="goal"`, `<option value="`+cem+`" selected>Chegar a 100 seguidores</option>`, "Reduzir a latência p95")
	wantNotContains(t, rec, "Digest semanal no ar") // atingida e não é a da task

	// trocar a meta pela propriedade da task (htmx)
	rec = e.post(t, "/c/hanko/tasks/HNK-221", url.Values{"goal": {lat}}, true)
	wantStatus(t, rec, http.StatusOK)
	if f.Tasks[0].GoalID == nil || f.Tasks[0].GoalID.String() != lat {
		t.Errorf("GoalID = %v", f.Tasks[0].GoalID)
	}
	rec = e.post(t, "/c/hanko/tasks/HNK-221", url.Values{"goal": {""}}, true)
	wantStatus(t, rec, http.StatusOK)
	if f.Tasks[0].GoalID != nil {
		t.Errorf("meta deveria ter sido desligada, GoalID = %v", f.Tasks[0].GoalID)
	}
	// id inválido
	rec = e.post(t, "/c/hanko/tasks/HNK-221", url.Values{"goal": {"xx"}}, true)
	wantStatus(t, rec, http.StatusUnprocessableEntity)

	// nova task já ligada à meta
	rec = e.get(t, "/c/hanko/tasks/nova?meta=latencia-p95")
	wantContains(t, rec, `<option value="`+lat+`" selected>`)
	rec = e.post(t, "/c/hanko/tasks/nova", url.Values{"title": {"Medir a p95"}, "goal": {lat}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if last := f.Tasks[len(f.Tasks)-1]; last.GoalID == nil || last.GoalID.String() != lat {
		t.Errorf("task criada sem a meta: %+v", last.GoalID)
	}
	// filtro da lista de tasks por meta
	rec = e.get(t, "/c/hanko/tasks?aba=todas&meta=cem-seguidores")
	wantContains(t, rec, "limpar filtro", `href="/c/hanko/metas/cem-seguidores"`, `name="meta"`)
	wantNotContains(t, rec, "HNK-217")
	wantStatus(t, e.get(t, "/c/hanko/tasks?meta=nao-existe"), http.StatusNotFound)
}

func TestProjectPageGoals(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/projetos/linkedin")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Metas", "Chegar a 100 seguidores", `role="progressbar"`, "56%")
	rec = e.get(t, "/c/hanko/projetos/nas-3d")
	wantContains(t, rec, "Nenhuma meta neste projeto ainda.")
}

// ---------- papéis ----------

func TestArtefMetasControlsByRole(t *testing.T) {
	f0 := uitest.Sample()
	slide := artifactIDOf(t, f0, "slide-01-tese.md", 2)
	M, A := domain.MemberMember, domain.MemberAdmin
	controls := []control{
		{"/c/hanko/artefatos", `href="/c/hanko/artefatos/novo"`, M},
		{"/c/hanko/artefatos/novo", `enctype="multipart/form-data"`, M},
		{"/c/hanko/artefatos/" + slide, `/excluir"`, A},
		{"/c/hanko/artefatos/" + slide, `/conteudo?baixar=1`, domain.MemberViewer},
		{"/c/hanko/metas", `href="/c/hanko/metas/nova"`, A},
		{"/c/hanko/metas/nova", `action="/c/hanko/metas/nova"`, A},
		{"/c/hanko/metas/cem-seguidores", `/progresso"`, M},
		{"/c/hanko/metas/cem-seguidores", `/metas/cem-seguidores/tasks"`, M},
		{"/c/hanko/metas/cem-seguidores", `/status"`, A},
		{"/c/hanko/metas/cem-seguidores", `action="/c/hanko/metas/cem-seguidores"`, A},
	}
	for _, role := range allRoles {
		t.Run(string(role), func(t *testing.T) {
			f := uitest.Sample()
			f.MyRoleIs = role
			e := newEnv(t, f)
			for _, c := range controls {
				rec := e.get(t, c.page)
				wantStatus(t, rec, http.StatusOK)
				if got, want := strings.Contains(rec.Body.String(), c.mark), role.AtLeast(c.min); got != want {
					t.Errorf("%s: %q presente=%v, quer %v (papel %s)", c.page, c.mark, got, want, role)
				}
			}
		})
	}
	t.Run("leitor lê tudo", func(t *testing.T) {
		f := uitest.Sample()
		f.MyRoleIs = domain.MemberViewer
		e := newEnv(t, f)
		wantContains(t, e.get(t, "/c/hanko/artefatos"), "slide-01-tese.md", "somente leitura")
		wantContains(t, e.get(t, "/c/hanko/metas/cem-seguidores"), "Chegar a 100 seguidores", "<svg")
	})
}

func TestForgedGoalAndArtifactPostsStillForbidden(t *testing.T) {
	const msg = "Seu papel nesta empresa não permite esta ação."
	f0 := uitest.Sample()
	slide := artifactIDOf(t, f0, "slide-01-tese.md", 2)
	cases := []struct {
		name, path string
		form       url.Values
	}{
		{"CreateGoal", "/c/hanko/metas/nova", url.Values{"title": {"x"}, "start": {"0"}, "target": {"1"}}},
		{"UpdateGoal", "/c/hanko/metas/cem-seguidores", url.Values{"title": {"x"}, "start": {"0"}, "target": {"1"}}},
		{"UpdateGoal", "/c/hanko/metas/cem-seguidores/status", url.Values{"to": {"abandoned"}}},
		{"RecordGoalProgress", "/c/hanko/metas/cem-seguidores/progresso", url.Values{"value": {"70"}}},
		{"UpdateTask", "/c/hanko/metas/cem-seguidores/tasks", url.Values{"task": {"HNK-217"}}},
		{"DeleteArtifact", "/c/hanko/artefatos/" + slide + "/excluir", nil},
	}
	for _, c := range cases {
		for _, htmx := range []bool{false, true} {
			f := uitest.Sample()
			f.MyRoleIs = domain.MemberViewer
			f.SetErr(c.name, domain.ErrForbidden)
			e := newEnv(t, f)
			rec := e.post(t, c.path, c.form, htmx)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s (htmx=%v): status %d, quer 403\n%.300s", c.name, c.path, htmx, rec.Code, rec.Body.String())
				continue
			}
			wantContains(t, rec, msg)
		}
	}
}

// ---------- funções puras ----------

func TestParseAndFormatNumbers(t *testing.T) {
	for in, want := range map[string]float64{"25": 25, "25,5": 25.5, "25.5": 25.5, "1.234,5": 1234.5, "1,234.5": 1234.5, " -3 ": -3, "1 000": 1000, "0": 0} {
		if got, ok := parseNum(in); !ok || got != want {
			t.Errorf("parseNum(%q) = %v, %v; quer %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "NaN", "Inf", "-Inf", "1e999", "1,2,x"} {
		if v, ok := parseNum(bad); ok {
			t.Errorf("parseNum(%q) = %v; deveria recusar", bad, v)
		}
	}
	ctx := context.Background()
	for v, want := range map[float64]string{25: "25", 25.5: "25,5", 0.125: "0,12", -0.001: "0", 1000.456: "1000,46"} {
		if got := fmtNum(ctx, v); got != want {
			t.Errorf("fmtNum(%v) = %q; quer %q", v, got, want)
		}
	}
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1 KiB", 1536: "1,5 KiB", 5 << 20: "5 MiB", 3 << 30: "3 GiB"} {
		if got := fmtBytes(ctx, n); got != want {
			t.Errorf("fmtBytes(%d) = %q; quer %q", n, got, want)
		}
	}
}

func TestUploadName(t *testing.T) {
	for _, c := range []struct{ field, file, want string }{
		{"", "relatorio.md", "relatorio.md"},
		{"", `C:\Users\x\relatorio.md`, "relatorio.md"},
		{"", "/etc/passwd", "passwd"},
		{"", "../../x.txt", "x.txt"},
		{"  meu.txt ", "outro.md", "meu.txt"},
		{"", "", ""},
		{"", "/", ""},
	} {
		if got := uploadName(c.field, c.file); got != c.want {
			t.Errorf("uploadName(%q, %q) = %q; quer %q", c.field, c.file, got, c.want)
		}
	}
}
