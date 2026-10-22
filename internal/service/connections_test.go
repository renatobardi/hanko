package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/oauth/oauthtest"
	"github.com/renatobardi/hanko/internal/service"
)

type connEnv struct {
	*intEnv
	fake *oauthtest.Server
}

// newConnEnv: empresa com cofre de segredos, destinos locais liberados (o servidor falso é httptest) e URL pública.
func newConnEnv(t *testing.T) *connEnv {
	t.Helper()
	e := newIntEnv(t, service.IntegrationsConfig{AllowPrivate: true})
	e.svc.PublicURL = "https://hanko.example.test"
	f := oauthtest.New()
	t.Cleanup(f.Close)
	return &connEnv{intEnv: e, fake: f}
}

func (e *connEnv) create(t *testing.T) service.ConnectionView {
	t.Helper()
	v, err := e.svc.CreateConnection(context.Background(), domain.HumanActor, e.co.ID, service.CreateConnectionInput{Name: "Docs Remoto", URL: e.fake.MCPURL()})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return v
}

// connect faz o fluxo inteiro: autorizar, "consentir" no servidor falso e voltar pelo callback.
func (e *connEnv) connect(t *testing.T, ref string) service.ConnectionView {
	t.Helper()
	ctx := context.Background()
	st, err := e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, ref)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	state, challenge, redirect, err := oauthtest.ParseAuthURL(st.URL)
	if err != nil || redirect != "https://hanko.example.test/oauth/callback" {
		t.Fatalf("redirect %q: %v", redirect, err)
	}
	v, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{State: state, Code: e.fake.IssueCode(challenge)})
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	return v
}

func (e *connEnv) tokenOf(t *testing.T, name string) string {
	t.Helper()
	v, _, err := e.svc.ResolveSecretText(context.Background(), e.co.ID, "${secret:"+name+"}")
	if err != nil {
		t.Fatalf("resolve %s: %v", name, err)
	}
	return v
}

func TestConnectionCreateDraftAndIntegration(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	v := e.create(t)
	if v.Status != domain.ConnDraft || v.Setup != domain.SetupAuthorize || v.Slug != "docs-remoto" || v.Identity != domain.ConnIdentityCompany {
		t.Fatalf("view: %+v", v)
	}
	// a integração MCP nasce desligada, com a referência ao segredo de token
	integ, err := e.svc.GetIntegration(ctx, e.co.ID, "docs-remoto")
	if err != nil || integ.Enabled || integ.Config.URL != e.fake.MCPURL() || len(integ.Config.HeaderNames) != 1 {
		t.Fatalf("integração: %+v %v", integ, err)
	}
	// o segredo-placeholder existe (e não é um token de verdade)
	if got := e.tokenOf(t, "conn.docs-remoto.access"); got != "pending" {
		t.Fatalf("placeholder = %q", got)
	}
	// slug repetido
	if _, err := e.svc.CreateConnection(ctx, domain.HumanActor, e.co.ID, service.CreateConnectionInput{Name: "Docs Remoto", URL: e.fake.MCPURL()}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("slug repetido: %v", err)
	}
	// URL com credenciais, esquema inválido e slug reservado
	for _, in := range []service.CreateConnectionInput{
		{Name: "A", URL: "https://u:p@example.com/mcp"},
		{Name: "B", URL: "ftp://example.com/mcp"},
		{Name: "Hanko", Slug: "hanko", URL: e.fake.MCPURL()},
		{Name: "", URL: e.fake.MCPURL()},
	} {
		if _, err := e.svc.CreateConnection(ctx, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%+v deveria ser inválido: %v", in, err)
		}
	}
	list, err := e.svc.ListConnections(ctx, e.co.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
}

func TestConnectionOAuthFlowStoresTokensAsSecrets(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	v := e.create(t)

	st, err := e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug)
	if err != nil {
		t.Fatal(err)
	}
	state, challenge, _, _ := oauthtest.ParseAuthURL(st.URL)
	if !strings.Contains(st.URL, "code_challenge_method=S256") || !strings.Contains(st.URL, "resource=") || state == "" {
		t.Fatalf("url: %s", st.URL)
	}
	// em voo: só o hash do state e o verifier cifrado ficam no banco
	var hash, verifierEnc, status string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT auth_state_hash, auth_verifier_enc, status FROM connections WHERE id = $1`, v.ID).Scan(&hash, &verifierEnc, &status); err != nil {
		t.Fatal(err)
	}
	if status != "pending_auth" || hash == state || len(hash) != 64 || !strings.HasPrefix(verifierEnc, "v1:") {
		t.Fatalf("estado em voo: %s %q %q", status, hash, verifierEnc)
	}
	got, _ := e.svc.GetConnection(ctx, e.co.ID, v.Slug)
	if got.Setup != domain.SetupFinish {
		t.Fatalf("setup = %s", got.Setup)
	}

	done, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{State: state, Code: e.fake.IssueCode(challenge)})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.ConnConnected || done.ExpiresAt == nil || done.Setup != domain.SetupNone || !done.ClientRegistered {
		t.Fatalf("conectada: %+v", done)
	}
	// o token mora em segredos; a integração foi ligada
	at, rt := e.tokenOf(t, "conn.docs-remoto.access"), e.tokenOf(t, "conn.docs-remoto.refresh")
	if at != e.fake.AccessToken() || at == "pending" || rt == "" {
		t.Fatalf("tokens: %q %q", at, rt)
	}
	integ, _ := e.svc.GetIntegration(ctx, e.co.ID, "docs-remoto")
	if !integ.Enabled {
		t.Fatal("a integração deveria estar ligada")
	}
	// nenhuma leitura devolve o token
	raw, _ := json.Marshal(done)
	list, _ := json.Marshal(mustList(t, e))
	for _, b := range [][]byte{raw, list} {
		if strings.Contains(string(b), at) || strings.Contains(string(b), rt) || strings.Contains(string(b), state) {
			t.Fatalf("a API vazou segredo: %s", b)
		}
	}
	// a atividade registra o fato sem o valor
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'connection.connected' AND data::text NOT LIKE '%'||$2||'%'`, e.co.ID, at); n != 1 {
		t.Fatalf("connection.connected = %d", n)
	}

	// o state é de uso único
	if _, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{State: state, Code: e.fake.IssueCode(challenge)}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("state reusado: %v", err)
	}
}

func mustList(t *testing.T, e *connEnv) []service.ConnectionView {
	t.Helper()
	l, err := e.svc.ListConnections(context.Background(), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestConnectionCallbackFailures(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	v := e.create(t)

	// state desconhecido
	if _, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{State: "nao-existe", Code: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("state desconhecido: %v", err)
	}
	// o usuário negou: volta ao rascunho com o motivo, e o state é consumido
	st, _ := e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug)
	state, challenge, _, _ := oauthtest.ParseAuthURL(st.URL)
	_, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{State: state, ErrorCode: "access_denied", ErrorDesc: "nope"})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("negado: %v", err)
	}
	got, _ := e.svc.GetConnection(ctx, e.co.ID, v.Slug)
	if got.Status != domain.ConnDraft || !strings.Contains(got.LastError, "access_denied") {
		t.Fatalf("após negar: %+v", got)
	}
	if _, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{State: state, Code: e.fake.IssueCode(challenge)}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("state consumido: %v", err)
	}

	// código de outro desafio PKCE: o servidor recusa a troca
	st, _ = e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug)
	state, _, _, _ = oauthtest.ParseAuthURL(st.URL)
	if _, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{State: state, Code: e.fake.IssueCode("outro-desafio")}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("PKCE inválido: %v", err)
	}
	got, _ = e.svc.GetConnection(ctx, e.co.ID, v.Slug)
	if got.Status != domain.ConnDraft || got.LastError == "" {
		t.Fatalf("após falha: %+v", got)
	}

	// autorização vencida
	st, _ = e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug)
	state, challenge, _, _ = oauthtest.ParseAuthURL(st.URL)
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE connections SET auth_expires_at = now() - interval '1 minute' WHERE id = $1`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{State: state, Code: e.fake.IssueCode(challenge)}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("vencida: %v", err)
	}
	// uma persona nunca autoriza
	p := e.persona(t, "Ana", nil)
	if _, err := e.svc.AuthorizeConnection(ctx, domain.Actor{Type: domain.ActorPersona, ID: &p.ID}, e.co.ID, v.Slug); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("persona autorizando: %v", err)
	}
}

func TestConnectionWithoutPublicURLOrPKCE(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	v := e.create(t)
	e.svc.PublicURL = ""
	if _, err := e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem URL pública: %v", err)
	}
	e.svc.PublicURL = "https://hanko.example.test"
	e.fake.NoPKCE = true
	if _, err := e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem PKCE: %v", err)
	}
	e.fake.NoPKCE = false
	e.fake.NoDCR = true
	if _, err := e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem registro dinâmico e sem client_id: %v", err)
	}
	// com client_id informado, o fluxo segue sem registro dinâmico
	cid := "client-abc"
	if _, err := e.svc.UpdateConnection(ctx, domain.HumanActor, e.co.ID, v.Slug, service.UpdateConnectionInput{ClientID: &cid}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug); err != nil {
		t.Fatalf("com client_id manual: %v", err)
	}
}

func TestConnectionSSRFBlocksPrivateWhenNotAllowed(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{}) // AllowPrivate desligado
	e.svc.PublicURL = "https://hanko.example.test"
	f := oauthtest.New()
	defer f.Close()
	_, err := e.svc.CreateConnection(context.Background(), domain.HumanActor, e.co.ID, service.CreateConnectionInput{Name: "Local", URL: f.MCPURL()})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("destino interno deveria ser recusado: %v", err)
	}
}

func TestConnectionProbeRefreshAndRevoke(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	v := e.create(t)

	// antes de autorizar, não há o que testar
	if _, err := e.svc.ProbeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("probe de rascunho: %v", err)
	}
	e.connect(t, v.Slug)

	p, err := e.svc.ProbeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug)
	if err != nil || p.Status != domain.ConnConnected || p.LastProbeStatus != "ok" || p.LastProbeAt == nil {
		t.Fatalf("probe: %+v %v", p, err)
	}

	// o servidor invalida o access token: o probe renova com o refresh e passa
	before := e.tokenOf(t, "conn.docs-remoto.access")
	e.fake.RotateAccess()
	// após RotateAccess o servidor não aceita token nenhum até renovar; o refresh devolve um novo
	p, err = e.svc.ProbeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug)
	if err != nil || p.Status != domain.ConnConnected || e.fake.RefreshHits == 0 {
		t.Fatalf("probe com renovação: %+v %v (refresh hits %d)", p, err, e.fake.RefreshHits)
	}
	if after := e.tokenOf(t, "conn.docs-remoto.access"); after == before || after != e.fake.AccessToken() {
		t.Fatalf("o segredo deveria ter sido rotacionado: %q -> %q", before, after)
	}

	// refresh revogado: a conexão vira revoked e pede reconexão
	e.fake.Revoked = true
	e.fake.RotateAccess()
	p, err = e.svc.ProbeConnection(ctx, domain.HumanActor, e.co.ID, v.Slug)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.GetConnection(ctx, e.co.ID, v.Slug)
	if got.Status != domain.ConnRevoked || got.Setup != domain.SetupReconnect {
		t.Fatalf("revogada: %+v (probe %+v)", got, p)
	}
	// reconectar funciona de qualquer estado
	e.fake.Revoked = false
	again := e.connect(t, v.Slug)
	if again.Status != domain.ConnConnected {
		t.Fatalf("reconectar: %+v", again)
	}
}

func TestConnectionRefreshDueAndManual(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	e.fake.ExpiresIn = 300 // vence em 5 min: dentro da janela da varredura (10 min) e fora da do gateway (2 min)
	v := e.create(t)
	e.connect(t, v.Slug)
	old := e.tokenOf(t, "conn.docs-remoto.access")

	n, err := e.svc.RefreshDueConnections(ctx, time.Now().UTC(), 10)
	if err != nil || n != 1 {
		t.Fatalf("varredura: %d %v", n, err)
	}
	if e.tokenOf(t, "conn.docs-remoto.access") == old || e.fake.RefreshHits != 1 {
		t.Fatalf("token não renovado (hits %d)", e.fake.RefreshHits)
	}
	// token com validade longa não é tocado pela varredura
	e.fake.ExpiresIn = 7200
	if _, err := e.svc.RefreshConnection(ctx, domain.HumanActor, e.co.ID, v.Slug); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.RefreshDueConnections(ctx, time.Now().UTC(), 10); n != 0 {
		t.Fatalf("não havia nada a renovar, renovou %d", n)
	}
	// renovação reservada por outra execução: não duplica a chamada
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE connections SET refreshing_until = now() + interval '1 minute' WHERE id = $1`, v.ID); err != nil {
		t.Fatal(err)
	}
	hits := e.fake.RefreshHits
	if _, err := e.svc.RefreshConnection(ctx, domain.HumanActor, e.co.ID, v.Slug); err != nil || e.fake.RefreshHits != hits {
		t.Fatalf("a reserva deveria impedir a segunda renovação (%d -> %d): %v", hits, e.fake.RefreshHits, err)
	}
}

func TestConnectionDisconnectAndDelete(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	v := e.create(t)
	e.connect(t, v.Slug)

	d, err := e.svc.DisconnectConnection(ctx, domain.HumanActor, e.co.ID, v.Slug)
	if err != nil || d.Status != domain.ConnDraft || d.ExpiresAt != nil {
		t.Fatalf("desconectar: %+v %v", d, err)
	}
	integ, _ := e.svc.GetIntegration(ctx, e.co.ID, "docs-remoto")
	if integ.Enabled {
		t.Fatal("a integração deveria ter sido desligada")
	}
	if got := e.tokenOf(t, "conn.docs-remoto.access"); got == e.fake.AccessToken() {
		t.Fatal("o token antigo ainda está no segredo")
	}
	if _, _, err := e.svc.ResolveSecretText(ctx, e.co.ID, "${secret:conn.docs-remoto.refresh}"); err == nil {
		t.Fatal("o refresh deveria ter sido apagado")
	}

	if err := e.svc.DeleteConnection(ctx, domain.HumanActor, e.co.ID, v.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetConnection(ctx, e.co.ID, v.Slug); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("apagada: %v", err)
	}
	if _, err := e.svc.GetIntegration(ctx, e.co.ID, "docs-remoto"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("integração deveria ter sumido: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM secrets WHERE company_id = $1 AND name LIKE 'conn.%'`, e.co.ID); n != 0 {
		t.Fatalf("sobraram %d segredos de token", n)
	}
}

func TestConnectionGateResumesTask(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	v := e.create(t)
	p := e.persona(t, "Ana", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "Ler o wiki", PersonaID: &p.ID})
	pa := domain.Actor{Type: domain.ActorPersona, ID: &p.ID}

	// o gate genérico não abre kind=connection
	if _, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Kind: domain.GateConnection, Title: "x"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("gate connection genérico: %v", err)
	}
	r, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{Slug: v.Slug, Reason: "preciso ler a documentação", TaskID: &tv.ID})
	if err != nil || r.Connected || r.Gate == nil || r.Gate.Kind != domain.GateConnection || r.Gate.Status != domain.GatePending {
		t.Fatalf("pedido: %+v %v", r, err)
	}
	if tk := taskStatus(t, e.env, tv); tk.Status != domain.TaskBlocked {
		t.Fatalf("a task deveria ficar bloqueada: %s", tk.Status)
	}
	// idempotente: o segundo pedido devolve o mesmo gate
	r2, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{Slug: v.Slug, TaskID: &tv.ID})
	if err != nil || r2.Gate == nil || r2.Gate.ID != r.Gate.ID {
		t.Fatalf("segundo pedido: %+v %v", r2, err)
	}
	// aprovar o gate sem autorizar não vale
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, r.Gate.ID, service.DecideGateInput{Decision: domain.GateApproved}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("aprovar sem conectar: %v", err)
	}
	e.jobs.reset()
	e.connect(t, v.Slug)

	g, err := e.svc.GetGate(ctx, e.co.ID, r.Gate.ID)
	if err != nil || g.Status != domain.GateApproved {
		t.Fatalf("o gate deveria estar aprovado: %+v %v", g, err)
	}
	if tk := taskStatus(t, e.env, tv); tk.Status != domain.TaskInProgress {
		t.Fatalf("a task deveria retomar: %s", tk.Status)
	}
	if e.jobs.wakeCount() == 0 {
		t.Fatal("a persona deveria ter sido acordada")
	}
	// a persona que pediu passa a poder usar a integração
	integ, _ := e.svc.GetIntegration(ctx, e.co.ID, "docs-remoto")
	ps, err := e.svc.ListIntegrationPersonas(ctx, e.co.ID, integ.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked := false
	for _, ip := range ps {
		if ip.PersonaID == p.ID && ip.Enabled {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("persona não ligada à integração: %+v", ps)
	}
	// conectada: novo pedido não abre gate
	r3, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{Slug: v.Slug})
	if err != nil || !r3.Connected || r3.Gate != nil {
		t.Fatalf("já conectada: %+v %v", r3, err)
	}
}

func TestConnectionRequestByPersonaProposesDraftAndRejectWorks(t *testing.T) {
	e := newConnEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	pa := domain.Actor{Type: domain.ActorPersona, ID: &p.ID}

	if _, err := e.svc.RequestConnection(ctx, domain.HumanActor, e.co.ID, service.RequestConnectionInput{Slug: "x"}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("humano pedindo: %v", err)
	}
	if _, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem nada: %v", err)
	}
	if _, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{Slug: "nao-existe"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("slug inexistente: %v", err)
	}
	if _, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{URL: e.fake.MCPURL()}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("proposta sem nome: %v", err)
	}
	r, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{Name: "Wiki Interna", URL: e.fake.MCPURL(), TaskID: &tv.ID})
	if err != nil || r.Connection.Status != domain.ConnDraft || r.Connection.CreatedBy.Type != domain.ActorPersona || r.Gate == nil {
		t.Fatalf("proposta: %+v %v", r, err)
	}
	// proposta repetida para a mesma URL reaproveita a conexão
	r2, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{Name: "Outro nome", URL: e.fake.MCPURL(), TaskID: &tv.ID})
	if err != nil || r2.Connection.ID != r.Connection.ID {
		t.Fatalf("reaproveitar: %+v %v", r2, err)
	}
	// rejeitar sempre funciona e devolve a task a todo
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, r.Gate.ID, service.DecideGateInput{Decision: domain.GateRejected, Note: "não"}); err != nil {
		t.Fatal(err)
	}
	if tk := taskStatus(t, e.env, tv); tk.Status != domain.TaskTodo {
		t.Fatalf("task = %s", tk.Status)
	}
	// depois de rejeitado, um novo pedido abre outro gate
	r4, err := e.svc.RequestConnection(ctx, pa, e.co.ID, service.RequestConnectionInput{Slug: r.Connection.Slug, TaskID: &tv.ID})
	if err != nil || r4.Gate == nil || r4.Gate.ID == r.Gate.ID {
		t.Fatalf("novo gate: %+v %v", r4, err)
	}
}

func TestPersonalConnectionUsesUserScopeAndIsPrivate(t *testing.T) {
	e := newConnEnv(t)
	bg := context.Background()
	ana := mkUser(t, e.env, "ana@example.com", domain.RoleMember)
	bia := mkUser(t, e.env, "bia@example.com", domain.RoleMember)
	join(t, e.env, ana, domain.MemberMember)
	join(t, e.env, bia, domain.MemberMember)
	anaCtx, biaCtx := as(ana), as(bia)
	anaActor := domain.Actor{Type: domain.ActorHuman, ID: &ana.ID}

	v, err := e.svc.CreateConnection(anaCtx, anaActor, e.co.ID, service.CreateConnectionInput{Name: "Minha Agenda", URL: e.fake.MCPURL(), Identity: domain.ConnIdentityUser})
	if err != nil || v.IntegrationID != nil {
		t.Fatalf("pessoal: %+v %v", v, err)
	}
	// membro comum não cria conexão da empresa
	if _, err := e.svc.CreateConnection(anaCtx, anaActor, e.co.ID, service.CreateConnectionInput{Name: "Da Empresa", URL: e.fake.MCPURL()}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro criando da empresa: %v", err)
	}
	// outra pessoa não enxerga nem opera
	if _, err := e.svc.GetConnection(biaCtx, e.co.ID, v.Slug); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bia vendo a de ana: %v", err)
	}
	if l, _ := e.svc.ListConnections(biaCtx, e.co.ID); len(l) != 0 {
		t.Fatalf("bia lista %d", len(l))
	}
	// ana conecta; o token vira segredo de USUÁRIO
	st, err := e.svc.AuthorizeConnection(anaCtx, anaActor, e.co.ID, v.Slug)
	if err != nil {
		t.Fatal(err)
	}
	state, challenge, _, _ := oauthtest.ParseAuthURL(st.URL)
	// quem volta do navegador tem de ser quem começou
	biaActor := domain.Actor{Type: domain.ActorHuman, ID: &bia.ID}
	if _, err := e.svc.CompleteConnectionAuth(biaCtx, biaActor, service.CompleteConnectionInput{State: state, Code: e.fake.IssueCode(challenge)}); err == nil {
		t.Fatal("bia não deveria concluir a autorização de ana")
	}
	st, _ = e.svc.AuthorizeConnection(anaCtx, anaActor, e.co.ID, v.Slug)
	state, challenge, _, _ = oauthtest.ParseAuthURL(st.URL)
	done, err := e.svc.CompleteConnectionAuth(anaCtx, anaActor, service.CompleteConnectionInput{State: state, Code: e.fake.IssueCode(challenge)})
	if err != nil || done.Status != domain.ConnConnected {
		t.Fatalf("ana conecta: %+v %v", done, err)
	}
	if n := e.count(t, `SELECT count(*) FROM secrets WHERE company_id = $1 AND scope = 'user' AND owner_user_id = $2 AND name LIKE 'conn.minha-agenda.%'`, e.co.ID, ana.ID); n != 2 {
		t.Fatalf("segredos de usuário = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM secrets WHERE company_id = $1 AND scope = 'company' AND name LIKE 'conn.minha-agenda.%'`, e.co.ID); n != 0 {
		t.Fatalf("a conexão pessoal não pode gerar segredo da empresa: %d", n)
	}
	// e a persona não consegue pedi-la
	p := e.persona(t, "Cia", nil)
	if _, err := e.svc.RequestConnection(bg, domain.Actor{Type: domain.ActorPersona, ID: &p.ID}, e.co.ID, service.RequestConnectionInput{Slug: v.Slug}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("persona pedindo conexão pessoal: %v", err)
	}
	// probe do dono funciona com o segredo de usuário
	pr, err := e.svc.ProbeConnection(anaCtx, anaActor, e.co.ID, v.Slug)
	if err != nil || pr.LastProbeStatus != "ok" {
		t.Fatalf("probe pessoal: %+v %v", pr, err)
	}
}
