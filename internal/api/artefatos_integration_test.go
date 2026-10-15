//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Artefatos e metas pela API (ADR-0028).

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func putBody(name, mime, content string, extra string) string {
	return fmt.Sprintf(`{"name":%q,"mime":%q,"content":%q%s}`, name, mime, b64(content), extra)
}

func (e *apiEnv) raw(path string, hdr map[string]string) (*http.Response, []byte) {
	e.t.Helper()
	req, err := http.NewRequest("GET", e.url+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAPIArtifactsLifecycle(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	tk := e.task(e.co, "relatório", nil, nil)

	var v1, v2 api.Artifact
	e.want(201, "POST", base+"/artifacts", putBody("plano.md", "", "# um\n", fmt.Sprintf(`,"task":%q`, tk.Key)), &v1)
	if v1.Version != 1 || v1.Mime != "text/markdown" || v1.Kind != "markdown" || v1.TaskKey != tk.Key || v1.SizeBytes != 5 || v1.Author.Type != "human" {
		t.Fatalf("v1: %+v", v1)
	}
	e.want(201, "POST", base+"/artifacts", putBody("plano.md", "", "# dois\n", fmt.Sprintf(`,"task":%q`, tk.Key)), &v2)
	if v2.Version != 2 || v2.Versions != 2 || v2.ID == v1.ID {
		t.Fatalf("v2: %+v", v2)
	}

	// detalhe com o histórico
	var det api.Artifact
	e.want(200, "GET", base+"/artifacts/"+v1.ID, "", &det)
	if det.Version != 1 || len(det.History) != 2 || det.History[0].Version != 2 {
		t.Fatalf("detalhe: %+v", det)
	}

	// lista: só a mais nova, e com all_versions todas
	var list api.ListBody[api.Artifact]
	e.want(200, "GET", base+"/artifacts?task="+tk.Key, "", &list)
	if len(list.Items) != 1 || list.Items[0].ID != v2.ID {
		t.Fatalf("lista: %+v", list.Items)
	}
	e.want(200, "GET", base+"/artifacts?task="+tk.Key+"&all_versions=true", "", &list)
	if len(list.Items) != 2 {
		t.Fatalf("todas: %+v", list.Items)
	}
	e.want(200, "GET", base+"/artifacts?kind=image", "", &list)
	if len(list.Items) != 0 {
		t.Errorf("tipo imagem: %+v", list.Items)
	}
	if st, _ := e.call("GET", base+"/artifacts?kind=nope", "", nil); st != http.StatusUnprocessableEntity {
		t.Errorf("tipo inválido: %d", st)
	}

	// conteúdo bruto de markdown: inline, com cabeçalhos de segurança
	resp, body := e.raw(base+"/artifacts/"+v1.ID+"/content", nil)
	if resp.StatusCode != 200 || string(body) != "# um\n" {
		t.Fatalf("conteúdo: %d %q", resp.StatusCode, body)
	}
	h := resp.Header
	if h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") ||
		!strings.HasPrefix(h.Get("Content-Disposition"), "inline") || !strings.HasPrefix(h.Get("Content-Type"), "text/markdown") {
		t.Errorf("cabeçalhos do markdown: %v", h)
	}

	// diff com a versão anterior
	var df api.ArtifactDiff
	e.want(200, "GET", base+"/artifacts/"+v2.ID+"/diff", "", &df)
	if df.From != 1 || df.To != 2 || df.Added != 1 || df.Removed != 1 {
		t.Fatalf("diff: %+v", df)
	}
	e.want(200, "GET", base+"/artifacts/"+v2.ID+"/diff?against="+v1.ID, "", &df)
	if st, _ := e.call("GET", base+"/artifacts/"+v1.ID+"/diff", "", nil); st != http.StatusUnprocessableEntity {
		t.Errorf("diff da primeira versão: %d", st)
	}

	var use api.ArtifactUsage
	e.want(200, "GET", base+"/artifact-usage", "", &use)
	if use.UsedBytes != 12 || use.Versions != 2 || use.MaxFileBytes != service.DefaultArtifactMaxBytes {
		t.Errorf("uso: %+v", use)
	}

	// apagar
	e.want(204, "DELETE", base+"/artifacts/"+v2.ID, "", nil)
	if st, _ := e.call("GET", base+"/artifacts/"+v1.ID, "", nil); st != http.StatusNotFound {
		t.Errorf("depois de apagar: %d", st)
	}
	if st, _ := e.call("GET", base+"/artifacts/nao-e-uuid", "", nil); st != http.StatusUnprocessableEntity && st != http.StatusNotFound {
		t.Errorf("id inválido: %d", st)
	}
}

func TestAPIArtifactServingIsSafe(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	cases := []struct {
		name, mime, content string
		inline              bool
	}{
		{"pagina.html", "text/html", "<script>alert(1)</script>", false},
		{"desenho.svg", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`, false},
		{"dados.json", "application/json", `{"a":1}`, false},
		{"nota.txt", "text/plain", "<b>oi</b>", true},
		{"relatorio.pdf", "application/pdf", "%PDF-1.4", false},
		{"binario.bin", "application/octet-stream", "\x00\x01\x02\x03", false},
	}
	for _, c := range cases {
		var a api.Artifact
		e.want(201, "POST", base+"/artifacts", putBody(c.name, c.mime, c.content, ""), &a)
		resp, body := e.raw(base+"/artifacts/"+a.ID+"/content", nil)
		if string(body) != c.content {
			t.Errorf("%s: conteúdo alterado", c.name)
		}
		cd := resp.Header.Get("Content-Disposition")
		if c.inline != strings.HasPrefix(cd, "inline") || (!c.inline && !strings.HasPrefix(cd, "attachment")) {
			t.Errorf("%s: disposição %q", c.name, cd)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s: faltam cabeçalhos de segurança: %v", c.name, resp.Header)
		}
	}
	// nome com aspas e quebra de linha não injeta cabeçalho: o nome é recusado ou o filename é escapado
	if st, _ := e.call("POST", base+"/artifacts", putBody("a\"b\r\nX-Evil: 1.txt", "", "x", ""), nil); st != http.StatusUnprocessableEntity {
		t.Errorf("nome com controle: %d", st)
	}
	var a api.Artifact
	e.want(201, "POST", base+"/artifacts", putBody(`com "aspas" e ção.txt`, "", "x", ""), &a)
	resp, _ := e.raw(base+"/artifacts/"+a.ID+"/content", nil)
	if strings.Count(resp.Header.Get("Content-Disposition"), "\n") != 0 || resp.Header.Get("X-Evil") != "" {
		t.Errorf("disposição: %q", resp.Header.Get("Content-Disposition"))
	}
}

func TestAPIArtifactLimits(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	e.svc.ArtifactMaxBytes, e.svc.ArtifactQuotaBytes = 64, 100
	if st, b := e.call("POST", base+"/artifacts", putBody("grande.txt", "", strings.Repeat("x", 65), ""), nil); st != http.StatusUnprocessableEntity || !strings.Contains(string(b), "too large") {
		t.Errorf("acima do limite: %d %s", st, b)
	}
	e.want(201, "POST", base+"/artifacts", putBody("a.txt", "", strings.Repeat("a", 60), ""), nil)
	if st, b := e.call("POST", base+"/artifacts", putBody("b.txt", "", strings.Repeat("b", 60), ""), nil); st != http.StatusUnprocessableEntity || !strings.Contains(string(b), "quota") {
		t.Errorf("acima da cota: %d %s", st, b)
	}
	for name, body := range map[string]string{
		"base64 inválido": `{"name":"a.txt","content":"!!!"}`,
		"sem conteúdo":    `{"name":"a.txt"}`,
		"conteúdo vazio":  `{"name":"a.txt","content":""}`,
		"sem nome":        fmt.Sprintf(`{"content":%q}`, b64("x")),
		"task fantasma":   putBody("a.txt", "", "x", `,"task":"HNK-999"`),
		"run inválida":    putBody("a.txt", "", "x", `,"run_id":"nao"`),
	} {
		if st, b := e.call("POST", base+"/artifacts", body, nil); st < 400 || st >= 500 {
			t.Errorf("%s: status %d: %s", name, st, b)
		}
	}
	// corpo muito acima do teto: recusado (413) sem processar
	huge := bytes.Repeat([]byte("A"), int(service.MaxArtifactMaxBytes)*2)
	st, _ := e.call("POST", base+"/artifacts", `{"name":"x","content":"`+string(huge)+`"}`, nil)
	if st != http.StatusRequestEntityTooLarge {
		t.Errorf("corpo gigante: %d", st)
	}
}

func TestAPIArtifactRunToken(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	base := e.base()
	p := e.persona(e.co, "Ana", nil)
	mine := e.task(e.co, "minha", &p, nil)
	other := e.task(e.co, "outra", nil, nil)
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, mine))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := e.tokens.Issue(e.co.ID, p.ID, run.ID, mine.ID)
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + tok}

	// sem task nem run no corpo: vale a execução do token
	var a api.Artifact
	b, st := []byte(nil), 0
	st, b = e.call("POST", base+"/artifacts", putBody("saida.md", "", "# oi", ""), hdr)
	if st != 201 {
		t.Fatalf("persona publica: %d %s", st, b)
	}
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	if a.TaskID != mine.ID.String() || a.RunID != run.ID.String() || a.PersonaID != p.ID.String() || a.Author.Type != "persona" {
		t.Fatalf("escopo do token: %+v", a)
	}
	// em outra task: proibido
	if st, _ := e.call("POST", base+"/artifacts", putBody("x.md", "", "x", fmt.Sprintf(`,"task":%q`, other.Key)), hdr); st != http.StatusForbidden {
		t.Errorf("task alheia: %d", st)
	}
	// a persona lê, mas não apaga
	if st, _ := e.call("GET", base+"/artifacts/"+a.ID, "", hdr); st != 200 {
		t.Errorf("persona lê: %d", st)
	}
	if st, _ := e.call("DELETE", base+"/artifacts/"+a.ID, "", hdr); st != http.StatusForbidden {
		t.Errorf("persona apaga: %d", st)
	}
	// o token de outra empresa não vale aqui
	co2 := e.company("Outra", "outra")
	if st, _ := e.call("GET", "/api/v1/companies/"+co2.Slug+"/artifacts", "", hdr); st != http.StatusForbidden {
		t.Errorf("outra empresa: %d", st)
	}
}

func TestAPIGoals(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	var pr api.Project
	e.want(201, "POST", base+"/projects", `{"name":"Crescimento"}`, &pr)

	var g api.Goal
	e.want(201, "POST", base+"/goals", `{"title":"Chegar a 100 usuários","metric":"usuários ativos","unit":"usuários","start_value":10,"target_value":100,"due_date":"2026-12-31","project":"crescimento"}`, &g)
	if g.Slug == "" || g.Status != "active" || g.CurrentValue != 10 || g.ProgressPercent != 0 || g.DueDate != "2026-12-31" || g.ProjectSlug != "crescimento" {
		t.Fatalf("criada: %+v", g)
	}
	for name, body := range map[string]string{
		"sem título":      `{"start_value":0,"target_value":1}`,
		"inicial = alvo":  `{"title":"x","start_value":5,"target_value":5}`,
		"data inválida":   `{"title":"x","start_value":0,"target_value":1,"due_date":"31/12/2026"}`,
		"projeto inexist": `{"title":"x","start_value":0,"target_value":1,"project":"nao-existe"}`,
	} {
		if st, b := e.call("POST", base+"/goals", body, nil); st < 400 || st >= 500 {
			t.Errorf("%s: %d %s", name, st, b)
		}
	}

	// progresso e histórico
	e.want(201, "POST", base+"/goals/"+g.Slug+"/progress", `{"value":55,"note":"metade"}`, &g)
	if g.CurrentValue != 55 || g.ProgressPercent != 50 || len(g.Updates) != 1 || g.Updates[0].Note != "metade" {
		t.Fatalf("progresso: %+v", g)
	}

	// tasks ligadas e contagem por status
	var tk api.Task
	e.want(201, "POST", base+"/tasks", fmt.Sprintf(`{"title":"campanha","goal":%q}`, g.Slug), &tk)
	if tk.GoalID != g.ID {
		t.Fatalf("task sem meta: %+v", tk)
	}
	e.want(200, "PATCH", base+"/tasks/"+tk.Key, `{"status":"in_progress"}`, &tk)
	e.want(200, "GET", base+"/goals/"+g.ID, "", &g)
	if g.TasksTotal != 1 || g.Tasks["in_progress"] != 1 {
		t.Fatalf("contagens: %+v", g.Tasks)
	}
	var tasks api.ListBody[api.Task]
	e.want(200, "GET", base+"/tasks?goal="+g.Slug, "", &tasks)
	if len(tasks.Items) != 1 {
		t.Fatalf("filtro de tasks: %+v", tasks.Items)
	}
	e.want(200, "PATCH", base+"/tasks/"+tk.Key, `{"clear_goal":true}`, &tk)
	if tk.GoalID != "" {
		t.Errorf("desligar: %+v", tk)
	}

	// listas e filtros
	var list api.ListBody[api.Goal]
	e.want(200, "GET", base+"/goals?status=active&project=crescimento", "", &list)
	if len(list.Items) != 1 {
		t.Fatalf("lista: %+v", list.Items)
	}
	e.want(200, "GET", base+"/goals?status=achieved", "", &list)
	if len(list.Items) != 0 {
		t.Errorf("atingidas: %+v", list.Items)
	}

	// atingir, depois reabrir com alvo maior
	e.want(201, "POST", base+"/goals/"+g.Slug+"/progress", `{"value":100}`, &g)
	if g.Status != "achieved" || g.ProgressPercent != 100 || g.AchievedAt == nil {
		t.Fatalf("atingida: %+v", g)
	}
	if st, _ := e.call("POST", base+"/goals/"+g.Slug+"/progress", `{"value":120}`, nil); st != http.StatusUnprocessableEntity {
		t.Errorf("progresso numa atingida: %d", st)
	}
	e.want(200, "PATCH", base+"/goals/"+g.Slug, `{"target_value":200,"status":"active","clear_due":true}`, &g)
	if g.Status != "active" || g.TargetValue != 200 || g.DueDate != "" || g.AchievedAt != nil {
		t.Fatalf("reaberta: %+v", g)
	}
	if st, _ := e.call("PATCH", base+"/goals/"+g.Slug, `{"status":"achieved"}`, nil); st >= 200 && st < 300 {
		t.Errorf("achieved à mão: %d", st)
	}
	e.want(200, "PATCH", base+"/goals/"+g.Slug, `{"status":"abandoned"}`, &g)
	if g.Status != "abandoned" {
		t.Errorf("abandonada: %+v", g)
	}
	if st, _ := e.call("GET", base+"/goals/nao-existe", "", nil); st != http.StatusNotFound {
		t.Errorf("meta inexistente: %d", st)
	}
}

func TestAPIGoalsPersonaToken(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	var g api.Goal
	e.want(201, "POST", base+"/goals", `{"title":"Reduzir tempo","start_value":200,"target_value":50}`, &g)
	p := e.persona(e.co, "Ana", nil)
	hdr := e.personaToken(e.co, p)

	if st, _ := e.call("POST", base+"/goals", `{"title":"da persona","start_value":0,"target_value":1}`, hdr); st != http.StatusForbidden {
		t.Errorf("persona cria meta: %d", st)
	}
	if st, _ := e.call("PATCH", base+"/goals/"+g.Slug, `{"title":"x"}`, hdr); st != http.StatusForbidden {
		t.Errorf("persona edita meta: %d", st)
	}
	var out api.Goal
	b, st := []byte(nil), 0
	st, b = e.call("POST", base+"/goals/"+g.Slug+"/progress", `{"value":120,"note":"medi hoje"}`, hdr)
	if st != 201 {
		t.Fatalf("persona registra progresso: %d %s", st, b)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Updates) != 1 || out.Updates[0].Author.Type != "persona" || out.ProgressPercent < 53 || out.ProgressPercent > 54 {
		t.Errorf("progresso da persona: %+v", out)
	}
	if st, _ := e.call("GET", base+"/goals", "", hdr); st != 200 {
		t.Errorf("persona lista metas: %d", st)
	}
}

var _ = domain.GoalActive
