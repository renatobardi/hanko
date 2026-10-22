package ui

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Caixa por pessoa, avisos e visões "Minhas" na UI (ADR-0042).

var ana = domain.User{ID: uitest.ID("user:ana"), Email: "ana@x.com", Name: "Ana", Role: domain.RoleMember}

// newPersonalEnv é o newEnv com uma pessoa autenticada em todas as requisições (o modo authenticated).
func newPersonalEnv(t *testing.T, f *uitest.Fake) *env {
	t.Helper()
	e := newEnv(t, f)
	inner := e.h
	e.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{User: ana, Via: auth.ViaSession})))
	})
	return e
}

const planGateTitle = "Aprovar o plano de migração do NAS 3D"

func planRef() string { return "gate:" + uitest.ID("gate:plan").String() }

func TestInboxHasNoPersonalControlsInTrustedMode(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/caixa")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, planGateTitle)
	wantNotContains(t, rec, "inbox-ctl", "/caixa/estado", "Não lidos", "data-notify", `id="toasts"`)
	// o selo e o stream não existem sem pessoa: o stream responde 204 para o navegador desistir
	wantStatus(t, e.get(t, "/c/hanko/caixa/eventos"), http.StatusNoContent)
}

func TestInboxPersonalControlsTabsAndBadge(t *testing.T) {
	f := uitest.Sample()
	e := newPersonalEnv(t, f)
	rec := e.get(t, "/c/hanko/caixa")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, planGateTitle, "inbox-ctl", `value="`+planRef()+`"`, `value="archive"`, `value="read"`, "Marcar tudo como lido",
		"Não lidos", "Arquivados", `href="/c/hanko/caixa?aba=nao-lidos"`, `href="/c/hanko/caixa?aba=arquivados"`,
		"data-notify", `data-notify-url="/c/hanko/caixa/eventos"`, `id="toasts"`, `hx-get="/c/hanko/caixa/selo"`, "unread-dot")

	// o selo é o número de não lidos
	badge := e.get(t, "/c/hanko/caixa/selo")
	wantStatus(t, badge, http.StatusOK)
	wantContains(t, badge, `class="nav-badge"`)
	if cc := badge.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("o selo não pode ser cacheado: %q", cc)
	}

	// arquivar o gate: some da Caixa padrão, aparece em Arquivados e não em Não lidos
	rec = e.post(t, "/c/hanko/caixa/estado", url.Values{"item": {planRef()}, "action": {"archive"}}, true)
	wantStatus(t, rec, http.StatusNoContent)
	if got := rec.Header().Get("HX-Redirect"); got != "/c/hanko/caixa" {
		t.Errorf("volta para a Caixa: %q", got)
	}
	if f.Caixa.LastAction != domain.InboxArchive || len(f.Caixa.LastRefs) != 1 || f.Caixa.LastRefs[0].Kind != domain.InboxGate {
		t.Fatalf("service recebeu %v %+v", f.Caixa.LastAction, f.Caixa.LastRefs)
	}
	wantNotContains(t, e.get(t, "/c/hanko/caixa"), planGateTitle)
	wantNotContains(t, e.get(t, "/c/hanko/caixa?aba=nao-lidos"), planGateTitle)
	arch := e.get(t, "/c/hanko/caixa?aba=arquivados")
	wantContains(t, arch, planGateTitle, `value="unarchive"`)
	wantNotContains(t, arch, `value="archive"`)

	// a task e o gate seguem como estavam (arquivar é só da Caixa de quem arquivou)
	for _, g := range f.Gates {
		if g.ID == uitest.ID("gate:plan") && g.Status != domain.GatePending {
			t.Fatal("arquivar não decide o gate")
		}
	}
	// desarquivar devolve; a volta respeita a aba de onde veio (HX-Current-URL)
	rec = e.do(t, req{method: "POST", path: "/c/hanko/caixa/estado", form: url.Values{"item": {planRef()}, "action": {"unarchive"}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "HX-Request": "true", "HX-Current-URL": "http://example.com/c/hanko/caixa?aba=arquivados"}})
	if got := rec.Header().Get("HX-Redirect"); got != "/c/hanko/caixa?aba=arquivados" {
		t.Errorf("volta para a aba de origem: %q", got)
	}
	wantContains(t, e.get(t, "/c/hanko/caixa"), planGateTitle)
	// um HX-Current-URL de outra página não vira redirecionamento aberto
	rec = e.do(t, req{method: "POST", path: "/c/hanko/caixa/estado", form: url.Values{"item": {planRef()}, "action": {"read"}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "HX-Request": "true", "HX-Current-URL": "https://evil.example/c/hanko/caixa?aba=x"}})
	if got := rec.Header().Get("HX-Redirect"); !strings.HasPrefix(got, "/c/hanko/caixa") {
		t.Errorf("redirecionamento fora da Caixa: %q", got)
	}
	// ler tira de "Não lidos"
	wantNotContains(t, e.get(t, "/c/hanko/caixa?aba=nao-lidos"), planGateTitle)

	// marcar tudo como lido esvazia "Não lidos"
	wantStatus(t, e.post(t, "/c/hanko/caixa/estado", url.Values{"action": {"read_all"}}, true), http.StatusNoContent)
	unread := e.get(t, "/c/hanko/caixa?aba=nao-lidos")
	wantContains(t, unread, "Você está em dia.")
	wantNotContains(t, unread, "Marcar tudo como lido")
	wantNotContains(t, e.get(t, "/c/hanko/caixa/selo"), "nav-badge")

	// item inválido e ação desconhecida: 422
	wantStatus(t, e.post(t, "/c/hanko/caixa/estado", url.Values{"item": {"lixo"}, "action": {"read"}}, true), http.StatusUnprocessableEntity)
	wantStatus(t, e.post(t, "/c/hanko/caixa/estado", url.Values{"item": {planRef()}, "action": {"dance"}}, true), http.StatusUnprocessableEntity)
	// CSRF: sem same-origin o POST é recusado
	rec = e.do(t, req{method: "POST", path: "/c/hanko/caixa/estado", form: url.Values{"action": {"read_all"}}})
	if rec.Code < 400 {
		t.Errorf("POST cross-site deveria ser recusado: %d", rec.Code)
	}
}

func TestInboxShowsMentions(t *testing.T) {
	f := uitest.Sample()
	m := service.InboxMention{ID: uitest.ID("mention:1"), TaskKey: "HNK-217", TaskTitle: "Templates de e-mail do digest", Author: "Kenji",
		Excerpt: "@ana pode revisar o tom?", At: uitest.Now().Add(-5 * time.Minute)}
	f.Caixa.Mentions = []service.InboxMention{m}
	e := newPersonalEnv(t, f)
	rec := e.get(t, "/c/hanko/caixa")
	wantContains(t, rec, "Menções", "@ana pode revisar o tom?", "Kenji", `href="/c/hanko/tasks/HNK-217"`, `value="mention:`+m.ID.String()+`"`, "Responder")
	// arquivar a menção
	wantStatus(t, e.post(t, "/c/hanko/caixa/estado", url.Values{"item": {"mention:" + m.ID.String()}, "action": {"archive"}}, true), http.StatusNoContent)
	wantNotContains(t, e.get(t, "/c/hanko/caixa"), "@ana pode revisar o tom?")
	wantContains(t, e.get(t, "/c/hanko/caixa?aba=arquivados"), "@ana pode revisar o tom?")
}

// O stream de avisos entrega ao navegador só o que foi publicado no tópico da pessoa.
func TestInboxEventsStreamDeliversToastsToTheUser(t *testing.T) {
	f := uitest.Sample()
	e := newPersonalEnv(t, f)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rq, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/c/hanko/caixa/eventos", nil)
	rq.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(rq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	co := f.Companies[0].ID
	// outro usuário e outra empresa não chegam a esta pessoa
	e.broker.Publish(events.Message{Topic: service.UserTopic(co, uitest.ID("user:bruno")), Type: "notify", Data: map[string]string{"title": "do Bruno"}})
	e.broker.Publish(events.Message{Topic: service.UserTopic(co, ana.ID), Type: "notify", Data: map[string]string{"title": "Você foi mencionado em HNK-1", "url": "/c/hanko/tasks/HNK-1"}})

	sc := bufio.NewScanner(resp.Body)
	var event, data string
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			data = v
			break
		}
	}
	if event != "notify" || !strings.Contains(data, "Você foi mencionado em HNK-1") || strings.Contains(data, "do Bruno") {
		t.Fatalf("evento: %q %q", event, data)
	}
}

func TestInboxEventsStreamEndsWhenAccessIsLost(t *testing.T) {
	f := uitest.Sample()
	e := newPersonalEnv(t, f)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rq, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/c/hanko/caixa/eventos", nil)
	resp, err := http.DefaultClient.Do(rq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	f.SetErr("NotifyPrefs", domain.ErrForbidden) // o papel caiu: a reverificação periódica encerra o stream
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("o stream deveria acabar quando o acesso cai")
	}
}

func TestNotificationSettingsPage(t *testing.T) {
	f := uitest.Sample()
	f.Caixa.PushOn = true
	e := newPersonalEnv(t, f)
	rec := e.get(t, "/c/hanko/configuracoes/notificacoes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Notificações", "Menções", "Gates para decidir", "Execuções que falharam", "Alertas operacionais",
		`name="toast_mention"`, `name="push_gate"`, "data-push-enable", `data-vapid="BFAKEVAPIDPUBLICKEY"`,
		`data-url="/c/hanko/configuracoes/notificacoes/aparelhos"`, "Enviar aviso de teste", `href="/c/hanko/configuracoes/notificacoes"`)
	// os padrões: menção com toast e push; falha de execução só com toast
	wantContains(t, rec, `name="push_mention" checked`)
	wantNotContains(t, rec, `name="push_run_failed" checked`)

	// salvar: só o que veio marcado fica ligado
	rec = e.post(t, "/c/hanko/configuracoes/notificacoes", url.Values{"toast_mention": {"on"}, "push_run_failed": {"on"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if p := f.Caixa.Prefs[domain.NotifyMention]; !p.Toast || p.Push {
		t.Errorf("mention: %+v", p)
	}
	if p := f.Caixa.Prefs[domain.NotifyRunFailed]; p.Toast || !p.Push {
		t.Errorf("run_failed: %+v", p)
	}
	if p := f.Caixa.Prefs[domain.NotifyGate]; p.Toast || p.Push {
		t.Errorf("gate desligado: %+v", p)
	}
	wantContains(t, e.get(t, "/c/hanko/configuracoes/notificacoes?ok=salvo"), "Preferências salvas.")

	// teste
	wantStatus(t, e.post(t, "/c/hanko/configuracoes/notificacoes/teste", url.Values{}, false), http.StatusSeeOther)
	if f.Caixa.TestSent != 1 {
		t.Errorf("aviso de teste: %d", f.Caixa.TestSent)
	}

	// o navegador manda a assinatura em JSON; o service valida o endpoint
	body := `{"endpoint":"https://push.example/abc","keys":{"p256dh":"BP","auth":"au"}}`
	rq := httptest.NewRequest(http.MethodPost, "/c/hanko/configuracoes/notificacoes/aparelhos", strings.NewReader(body))
	rq.Header.Set("Sec-Fetch-Site", "same-origin")
	rq.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, rq)
	wantStatus(t, rr, http.StatusNoContent)
	if len(f.Caixa.Added) != 1 || f.Caixa.Added[0].Endpoint != "https://push.example/abc" || f.Caixa.Added[0].Auth != "au" {
		t.Fatalf("assinatura recebida: %+v", f.Caixa.Added)
	}
	bad := httptest.NewRequest(http.MethodPost, "/c/hanko/configuracoes/notificacoes/aparelhos", strings.NewReader(`{"endpoint":"http://x/y","keys":{}}`))
	bad.Header.Set("Sec-Fetch-Site", "same-origin")
	rr = httptest.NewRecorder()
	e.h.ServeHTTP(rr, bad)
	wantStatus(t, rr, http.StatusUnprocessableEntity)
	junk := httptest.NewRequest(http.MethodPost, "/c/hanko/configuracoes/notificacoes/aparelhos", strings.NewReader(`{`))
	junk.Header.Set("Sec-Fetch-Site", "same-origin")
	rr = httptest.NewRecorder()
	e.h.ServeHTTP(rr, junk)
	wantStatus(t, rr, http.StatusUnprocessableEntity)

	// lista e remove o aparelho
	rec = e.get(t, "/c/hanko/configuracoes/notificacoes")
	wantContains(t, rec, "Aparelhos com notificação", "push.example", "/aparelhos/remover")
	id := f.Caixa.Devices[0].ID.String()
	wantStatus(t, e.post(t, "/c/hanko/configuracoes/notificacoes/aparelhos/remover", url.Values{"id": {id}}, false), http.StatusSeeOther)
	wantNotContains(t, e.get(t, "/c/hanko/configuracoes/notificacoes"), "Aparelhos com notificação")
	wantStatus(t, e.post(t, "/c/hanko/configuracoes/notificacoes/aparelhos/remover", url.Values{"id": {id}}, false), http.StatusNotFound)
}

func TestNotificationSettingsWithoutPushKeyOrPerson(t *testing.T) {
	f := uitest.Sample() // sem PushOn
	e := newPersonalEnv(t, f)
	rec := e.get(t, "/c/hanko/configuracoes/notificacoes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "HANKO_SECRET_KEY", `name="toast_mention"`)
	wantNotContains(t, rec, "data-push-enable")
	// modo trusted: sem preferências, só o aviso para entrar com a conta
	trusted := newEnv(t, uitest.Sample())
	rec = trusted.get(t, "/c/hanko/configuracoes/notificacoes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Os avisos são de cada pessoa")
	wantNotContains(t, rec, `name="toast_mention"`)
}

func TestMineViewsOnTasksAndGates(t *testing.T) {
	f := uitest.Sample()
	f.Caixa.MineTasks = map[string][]string{"HNK-217": {"created", "commented"}, "HNK-218": {"gate"}}
	f.Caixa.MineGates = map[domain.ID]bool{uitest.ID("gate:plan"): true}
	e := newPersonalEnv(t, f)

	rec := e.get(t, "/c/hanko/tasks?aba=minhas")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "HNK-217", "HNK-218", "criei, comentei", "audiência de gate")
	wantNotContains(t, rec, "HNK-221")
	// em modo trusted a aba continua valendo "as que eu criei"
	trusted := newEnv(t, uitest.Sample())
	wantStatus(t, trusted.get(t, "/c/hanko/tasks?aba=minhas"), http.StatusOK)

	rec = e.get(t, "/c/hanko/gates?minhas=1")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, planGateTitle, "Minha audiência", `aria-pressed`)
	wantNotContains(t, rec, "Revisar HNK-220", "Contratar Yuki")
	wantContains(t, rec, `href="/c/hanko/gates?status=pendentes&amp;minhas=1"`)
	all := e.get(t, "/c/hanko/gates")
	wantContains(t, all, planGateTitle, "Revisar HNK-220", "Minha audiência")
	wantNotContains(t, newEnv(t, uitest.Sample()).get(t, "/c/hanko/gates"), "Minha audiência")
}

func TestActivityFilterByActor(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	all := e.get(t, "/c/hanko/atividade")
	wantStatus(t, all, http.StatusOK)
	wantContains(t, all, `name="ator"`, `value="persona:kenji"`, `value="system"`, "Quem")

	rec := e.get(t, "/c/hanko/atividade?ator=persona:kenji")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "limpar filtro", `value="persona:kenji" selected`)
	if strings.Count(rec.Body.String(), `class="ev"`) >= strings.Count(all.Body.String(), `class="ev"`) {
		t.Error("o filtro deveria reduzir a lista")
	}
	rec = e.get(t, "/c/hanko/atividade?ator=system")
	if n := strings.Count(rec.Body.String(), `class="ev"`); n != 1 {
		t.Errorf("o sistema tem 1 evento no Sample, vieram %d", n)
	}
	// ator desconhecido: erro de validação
	wantStatus(t, e.get(t, "/c/hanko/atividade?ator=persona:fantasma"), http.StatusUnprocessableEntity)
}

func TestSearchPageListsNewTypes(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/buscar")
	wantContains(t, rec, "Metas", "Projetos", "Gates", "Artefatos", `tipo=goal`, `tipo=project`, `tipo=gate`, `tipo=artifact`)
}
