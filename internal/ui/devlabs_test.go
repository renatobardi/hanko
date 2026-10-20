package ui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// /design-guide e /dev/ux (ADR-0058 §E): catálogo do DS e cenários sintéticos, só admin/dev.

func TestDesignGuideBothThemes(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/design-guide")
	wantStatus(t, rec, http.StatusOK)
	// lado a lado: painel claro e painel escuro (.dark), com o html fixado em claro
	wantContains(t, rec, `data-pane="light"`, `data-pane="dark"`, `class="dg-pane dark"`, `data-theme="light"`,
		`data-section="cores"`, `data-section="botoes"`, `data-section="formularios"`, `data-section="thread"`,
		"sw-primary", "btn primary", "badge warn", `class="log"`, "k-tool_use")
	if n := strings.Count(rec.Body.String(), `data-section="cores"`); n != 2 {
		t.Errorf("cada tema renderiza o catálogo inteiro: %d seções de cores, quer 2", n)
	}

	dark := e.get(t, "/design-guide?tema=escuro")
	wantStatus(t, dark, http.StatusOK)
	wantContains(t, dark, `data-theme="dark"`, `data-pane="dark"`)
	wantNotContains(t, dark, `data-pane="light"`)

	light := e.get(t, "/design-guide?tema=claro")
	wantContains(t, light, `data-theme="light"`, `data-pane="light"`)
	wantNotContains(t, light, `data-pane="dark"`)

	// o tema forçado vence o cookie do usuário
	forced := e.get(t, "/design-guide?tema=claro", "Cookie", "hanko_theme=dark")
	wantContains(t, forced, `data-theme="light"`)
	wantNotContains(t, forced, `data-theme="dark"`)
}

func TestUXLabsThread(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	idx := e.get(t, "/dev/ux")
	wantStatus(t, idx, http.StatusOK)
	wantContains(t, idx, "/dev/ux/thread-longo", "/dev/ux/run-10k")

	rec := e.get(t, "/dev/ux/thread-longo")
	wantStatus(t, rec, http.StatusOK)
	if n := strings.Count(rec.Body.String(), `class="msg`); n != uxThreadCount {
		t.Errorf("mensagens = %d, quer %d", n, uxThreadCount)
	}
	wantContains(t, rec, `data-testid="ux-thread-count"`, "Atualização #1 da persona", "Comentário humano #3")
	small := e.get(t, "/dev/ux/thread-longo?n=7")
	if n := strings.Count(small.Body.String(), `class="msg`); n != 7 {
		t.Errorf("?n=7 deu %d mensagens", n)
	}
}

func TestUXLabsRunEventsAPI(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	page := e.get(t, "/dev/ux/run-10k")
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, `data-island="run-log"`, `data-api="/dev/ux/api"`, `data-run="run-10k"`, "10000")

	type resp struct {
		Items []struct {
			Seq  int64  `json:"seq"`
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"items"`
	}
	get := func(q string) resp {
		t.Helper()
		rec := e.get(t, "/dev/ux/api/runs/run-10k/events?"+q)
		wantStatus(t, rec, http.StatusOK)
		var out resp
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	tail := get("tail=true&limit=500")
	if len(tail.Items) != 500 || tail.Items[0].Seq != 9501 || tail.Items[499].Seq != 10000 {
		t.Fatalf("tail: %d itens, %d..%d", len(tail.Items), tail.Items[0].Seq, tail.Items[len(tail.Items)-1].Seq)
	}
	before := get("before=9501&limit=500")
	if len(before.Items) != 500 || before.Items[0].Seq != 9001 || before.Items[499].Seq != 9500 {
		t.Fatalf("before: %d itens, %d..%d", len(before.Items), before.Items[0].Seq, before.Items[len(before.Items)-1].Seq)
	}
	first := get("before=11&limit=500") // chega ao começo: só 10 itens
	if len(first.Items) != 10 || first.Items[0].Seq != 1 {
		t.Fatalf("início: %d itens", len(first.Items))
	}
	if after := get("after=9990&limit=500"); len(after.Items) != 10 {
		t.Fatalf("after: %d itens", len(after.Items))
	}
	// determinístico: o mesmo seq dá sempre o mesmo texto
	a, b := get("after=4999&limit=1"), get("after=4999&limit=1")
	if a.Items[0].Text != b.Items[0].Text || a.Items[0].Seq != 5000 {
		t.Fatalf("não determinístico: %+v %+v", a, b)
	}
}

func TestDevRoutesAdminOnlyWhenAuthenticated(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	owner := e.fa.addUser("dona@x.com", "Dona", "senha-bem-longa-123")
	member := e.fa.addUser("membro@x.com", "Membro", "senha-bem-longa-123")
	for i := range e.fa.users {
		if e.fa.users[i].ID == member.ID {
			e.fa.users[i].Role = domain.RoleMember
		}
	}
	_ = owner

	// anônimo cai no login (o Guard), como qualquer tela
	anon := e.get(t, "/design-guide")
	if anon.Code != http.StatusSeeOther {
		t.Fatalf("anônimo = %d", anon.Code)
	}
	cm := cookieOf(e.login(t, "membro@x.com", "senha-bem-longa-123", ""))
	co := cookieOf(e.login(t, "dona@x.com", "senha-bem-longa-123", ""))
	for _, p := range []string{"/design-guide", "/dev/ux", "/dev/ux/thread-longo", "/dev/ux/run-10k", "/dev/ux/api/runs/run-10k/events?tail=true"} {
		if rec := e.withCookie(t, cm, req{method: "GET", path: p}); rec.Code != http.StatusNotFound {
			t.Errorf("membro em %s = %d, quer 404", p, rec.Code)
		}
		if rec := e.withCookie(t, co, req{method: "GET", path: p}); rec.Code != http.StatusOK {
			t.Errorf("dona em %s = %d, quer 200", p, rec.Code)
		}
	}
}
