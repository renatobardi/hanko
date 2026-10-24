package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/extvault"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// Valores sentinela: se qualquer um aparecer num lugar proibido, o teste de não-vazamento falha.
const (
	sentinel   = "SENTINELA-valor-secreto-9f3a7c1d"
	sentinelB  = "SENTINELA-outro-valor-4b8e2d6a"
	sentinelVT = "SENTINELA-do-cofre-externo-77aa"
)

// fakeVault é um extvault.Provider em memória.
type fakeVault struct {
	mu   sync.Mutex
	data map[string]string
	err  error
	gets int
}

func (f *fakeVault) Name() string { return "fake-vault" }

func (f *fakeVault) Get(_ context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.data[ref]
	if !ok {
		return "", extvault.ErrNotFound
	}
	return v, nil
}

type secEnv struct {
	*env
	box   *secretbox.Box
	vault *fakeVault
	logs  *bytes.Buffer
}

// newSecEnv é newEnv com cofre de segredos (chave one), cofre externo falso e log capturado.
func newSecEnv(t *testing.T) *secEnv {
	t.Helper()
	e := newEnv(t)
	box, err := secretbox.New(keyOne, "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: box}
	v := &fakeVault{data: map[string]string{"apps/hanko#token": sentinelVT}}
	e.svc.Vault = v
	logs := &bytes.Buffer{}
	e.svc.Log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &secEnv{env: e, box: box, vault: v, logs: logs}
}

func (e *secEnv) create(t *testing.T, name, value string) service.SecretView {
	t.Helper()
	v, err := e.svc.CreateSecret(context.Background(), domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: name, Value: value})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return v
}

func TestSecretLifecycleAndRevealOnce(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	v := e.create(t, "openrouter-key", sentinel)
	if v.RevealedValue != "" || v.RevealedAt != nil || v.Version != 1 || v.Provider != domain.SecretProviderLocal {
		t.Fatalf("criação com valor informado não revela nada: %+v", v)
	}

	// o valor está cifrado no banco
	var enc string
	if err := e.svc.Pool.QueryRow(bg, `SELECT value_enc FROM secrets WHERE id=$1`, v.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, sentinel) || !strings.HasPrefix(enc, "v1:") {
		t.Fatalf("value_enc = %q", enc)
	}

	// a lista e o get nunca trazem o valor
	list, err := e.svc.ListSecrets(bg, e.co.ID, domain.SecretScopeCompany, true)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if b, _ := json.Marshal(list); strings.Contains(string(b), sentinel) {
		t.Fatal("a listagem vazou o valor")
	}

	// revelar: uma vez por versão
	r, err := e.svc.RevealSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "openrouter-key")
	if err != nil || r.RevealedValue != sentinel || r.RevealedAt == nil {
		t.Fatalf("reveal = %+v, %v", r, err)
	}
	_, err = e.svc.RevealSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "openrouter-key")
	wantErr(t, err, domain.ErrConflict)

	// rotacionar zera a marca e sobe a versão; a nova versão pode ser revelada de novo
	rot, err := e.svc.RotateSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "openrouter-key", service.RotateSecretInput{Value: sentinelB})
	if err != nil || rot.Version != 2 || rot.RevealedAt != nil {
		t.Fatalf("rotate = %+v, %v", rot, err)
	}
	r, err = e.svc.RevealSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "openrouter-key")
	if err != nil || r.RevealedValue != sentinelB {
		t.Fatalf("reveal v2 = %+v, %v", r, err)
	}

	if err := e.svc.DeleteSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "openrouter-key", false); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.GetSecret(bg, e.co.ID, domain.SecretScopeCompany, "openrouter-key")
	wantErr(t, err, domain.ErrNotFound)
}

func TestSecretGenerateIsRevealedOnCreate(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	v, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "gerado", Generate: true})
	if err != nil || !strings.HasPrefix(v.RevealedValue, "hk_") || len(v.RevealedValue) < 40 || v.RevealedAt == nil {
		t.Fatalf("generate = %+v, %v", v, err)
	}
	// o valor já saiu uma vez: revelar de novo é conflito
	_, err = e.svc.RevealSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "gerado")
	wantErr(t, err, domain.ErrConflict)
	got, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "x=${secret:gerado}")
	if err != nil || got != "x="+v.RevealedValue {
		t.Fatalf("resolve = %q, %v", got, err)
	}
	rot, err := e.svc.RotateSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "gerado", service.RotateSecretInput{Generate: true})
	if err != nil || rot.RevealedValue == "" || rot.RevealedValue == v.RevealedValue || rot.Version != 2 {
		t.Fatalf("rotate generate = %+v, %v", rot, err)
	}
}

func TestSecretValidation(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	bad := []service.CreateSecretInput{
		{Name: "1ruim", Value: "x"},
		{Name: "tem espaço", Value: "x"},
		{Name: "ok", Value: ""},
		{Name: "ok", Value: "x", Generate: true},
		{Name: "ok", Value: strings.Repeat("a", 9000)},
		{Name: "ok", Value: "a\x00b"},
		{Name: "ok", Provider: domain.SecretProviderVault, ExternalRef: "../x"},
		{Name: "ok", Provider: domain.SecretProviderVault, Value: "x", ExternalRef: "a#b"},
		{Name: "ok", Value: "x", ExternalRef: "a#b"},
		{Name: "ok", Value: "x", Description: strings.Repeat("d", 301)},
		{Name: "ok", Value: "x", Scope: "other"},
	}
	for i, in := range bad {
		_, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, in)
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("caso %d: err = %v, want invalid", i, err)
		}
	}
	e.create(t, "dup", "x")
	_, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "dup", Value: "y"})
	wantErr(t, err, domain.ErrConflict)

	// sem chave de cifragem não se guarda segredo local
	e.svc.Integrations = service.IntegrationsConfig{}
	_, err = e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "semchave", Value: "x"})
	wantErr(t, err, domain.ErrInvalid)
}

func TestSecretRoles(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "base", "valor")
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin} {
		u := mkUser(t, e.env, string(r)+"@x.com", domain.RoleMember)
		join(t, e.env, u, r)
		users[r] = u
	}
	// viewer: nada; member: lista nomes mas não escreve; admin: tudo
	if _, err := e.svc.ListSecrets(as(users[domain.MemberViewer]), e.co.ID, domain.SecretScopeCompany, false); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer listar: %v", err)
	}
	if l, err := e.svc.ListSecrets(as(users[domain.MemberMember]), e.co.ID, domain.SecretScopeCompany, true); err != nil || len(l) != 1 || len(l[0].UsedBy) != 0 {
		t.Errorf("member listar: %v %v", l, err)
	}
	mctx := as(users[domain.MemberMember])
	if _, err := e.svc.CreateSecret(mctx, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "m", Value: "x"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member criar: %v", err)
	}
	if _, err := e.svc.RevealSecret(mctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "base"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member revelar: %v", err)
	}
	if err := e.svc.DeleteSecret(mctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "base", false); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member apagar: %v", err)
	}
	if _, err := e.svc.CreateSecret(as(users[domain.MemberAdmin]), domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "adm", Value: "x"}); err != nil {
		t.Errorf("admin criar: %v", err)
	}
	// persona nunca mexe em segredo
	p := e.persona(t, "Ana", nil)
	pa := domain.PersonaActor(p.ID)
	if _, err := e.svc.CreateSecret(bg, pa, e.co.ID, service.CreateSecretInput{Name: "p", Value: "x"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("persona criar: %v", err)
	}
	if _, err := e.svc.RevealSecret(bg, pa, e.co.ID, domain.SecretScopeCompany, "base"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("persona revelar: %v", err)
	}
}

func TestUserSecretsArePrivateToTheOwner(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	alice := mkUser(t, e.env, "alice@x.com", domain.RoleMember)
	bob := mkUser(t, e.env, "bob@x.com", domain.RoleMember)
	root := mkUser(t, e.env, "root@x.com", domain.RoleOwner) // dono da instalação
	for _, u := range []domain.User{alice, bob} {
		join(t, e.env, u, domain.MemberAdmin)
	}
	ac, bc, rc := as(alice), as(bob), as(root)

	// sem identidade (modo trusted) o escopo user é recusado
	_, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Scope: domain.SecretScopeUser, Name: "pessoal", Value: "x"})
	wantErr(t, err, domain.ErrInvalid)

	v, err := e.svc.CreateSecret(ac, domain.HumanActor, e.co.ID, service.CreateSecretInput{Scope: domain.SecretScopeUser, Name: "pessoal", Value: sentinel})
	if err != nil || v.Scope != domain.SecretScopeUser {
		t.Fatalf("create user secret: %+v %v", v, err)
	}
	// o mesmo nome em outro usuário não conflita
	if _, err := e.svc.CreateSecret(bc, domain.HumanActor, e.co.ID, service.CreateSecretInput{Scope: domain.SecretScopeUser, Name: "pessoal", Value: sentinelB}); err != nil {
		t.Fatalf("o nome é por usuário: %v", err)
	}

	for who, ctx := range map[string]context.Context{"bob": bc, "root": rc} {
		list, err := e.svc.ListSecrets(ctx, e.co.ID, domain.SecretScopeUser, false)
		if who == "root" {
			if err != nil || len(list) != 0 {
				t.Errorf("%s: o dono da instalação não vê segredo de usuário: %v %v", who, list, err)
			}
			continue
		}
		if err != nil || len(list) != 1 {
			t.Fatalf("%s lista só o seu: %v %v", who, list, err)
		}
		r, err := e.svc.RevealSecret(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeUser, "pessoal")
		if err != nil || r.RevealedValue != sentinelB {
			t.Fatalf("%s revela o seu, não o da alice: %+v %v", who, r, err)
		}
	}
	if _, err := e.svc.RevealSecret(rc, domain.HumanActor, e.co.ID, domain.SecretScopeUser, "pessoal"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("root revelar segredo alheio: %v", err)
	}
	// a lista da empresa nunca traz segredo de usuário
	if l, _ := e.svc.ListSecrets(bg, e.co.ID, domain.SecretScopeCompany, false); len(l) != 0 {
		t.Errorf("lista da empresa: %v", l)
	}
	// referência user/ não vale em configuração compartilhada
	_, err = e.svc.CreateIntegration(bg, domain.HumanActor, e.co.ID, service.CreateIntegrationInput{Kind: domain.IntegrationMCP, Name: "x", URL: "https://mcp.example.com",
		Headers: map[string]string{"Authorization": "Bearer ${secret:user/pessoal}"}})
	wantErr(t, err, domain.ErrInvalid)

	// o dono usa o seu pela resolução em ação própria; o outro usuário não resolve o dela
	got, _, err := e.svc.ResolveUserSecretText(ac, e.co.ID, "k=${secret:user/pessoal}")
	if err != nil || got != "k="+sentinel {
		t.Fatalf("ResolveUserSecretText = %q %v", got, err)
	}
	got, _, err = e.svc.ResolveUserSecretText(bc, e.co.ID, "k=${secret:user/pessoal}")
	if err != nil || got != "k="+sentinelB {
		t.Fatalf("bob resolve o dele: %q %v", got, err)
	}
	if _, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:user/pessoal}"); err == nil {
		t.Fatal("o despacho (sem usuário) não resolve segredo de usuário")
	}
	// apagar o seu não toca o do outro
	if err := e.svc.DeleteSecret(ac, domain.HumanActor, e.co.ID, domain.SecretScopeUser, "pessoal", false); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.svc.ListSecrets(bc, e.co.ID, domain.SecretScopeUser, false); len(l) != 1 {
		t.Fatalf("o segredo do bob some junto? %v", l)
	}
}

func TestIntegrationRefsResolveAtDispatch(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.svc.Integrations.AllowPrivate = true // o upstream de teste é um httptest em loopback
	p := e.persona(t, "Ana", nil)
	e.create(t, "docs-token", sentinel)
	up := newUpstreamMCP(t)
	up.add("ler", nil)
	lastAuth := func() string {
		up.mu.Lock()
		defer up.mu.Unlock()
		return up.auths[len(up.auths)-1]
	}

	// referência a segredo inexistente é recusada
	in := service.CreateIntegrationInput{Kind: domain.IntegrationMCP, Name: "Docs", URL: up.hs.URL, AllPersonas: true,
		Headers: map[string]string{"Authorization": "Bearer ${secret:nao-existe}"}}
	_, err := e.svc.CreateIntegration(bg, domain.HumanActor, e.co.ID, in)
	wantErr(t, err, domain.ErrInvalid)

	in.Headers = map[string]string{"Authorization": "Bearer ${secret:docs-token}"}
	view, err := e.svc.CreateIntegration(bg, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(view); strings.Contains(string(b), sentinel) {
		t.Fatal("a integração vazou o valor")
	}
	// MCP http vai pelo gateway de tools (ADR-0036): nunca entra no mcpServers da run, nem com segredo
	ex, err := e.svc.RunExtras(bg, e.co.ID, p.ID)
	if err != nil || len(ex.Warnings) != 0 || len(ex.MCP) != 0 || len(ex.ToolGateway) != 1 || ex.ToolGateway[0] != "docs" {
		t.Fatalf("RunExtras: %+v %v", ex, err)
	}
	// o gateway abre o upstream com o header resolvido no uso
	if res, err := e.svc.RefreshIntegrationTools(bg, domain.HumanActor, e.co.ID, view.ID); err != nil || !res.OK {
		t.Fatalf("refresh: %+v %v", res, err)
	}
	if got := lastAuth(); got != "Bearer "+sentinel {
		t.Fatalf("o header deveria chegar resolvido ao upstream: %q", got)
	}

	// rotacionar vale na próxima chamada, sem tocar a integração
	if _, err := e.svc.RotateSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "docs-token", service.RotateSecretInput{Value: sentinelB}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RefreshIntegrationTools(bg, domain.HumanActor, e.co.ID, view.ID); err != nil {
		t.Fatal(err)
	}
	if got := lastAuth(); got != "Bearer "+sentinelB {
		t.Fatalf("depois da rotação: %q", got)
	}

	// em uso: apagar é recusado, listando quem usa
	err = e.svc.DeleteSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "docs-token", false)
	wantErr(t, err, domain.ErrConflict)
	if err == nil || !strings.Contains(err.Error(), "integration:docs") {
		t.Fatalf("a recusa deve listar o uso: %v", err)
	}
	sv, _ := e.svc.GetSecret(bg, e.co.ID, domain.SecretScopeCompany, "docs-token")
	if len(sv.UsedBy) != 1 || sv.UsedBy[0].Kind != "integration" {
		t.Fatalf("UsedBy = %+v", sv.UsedBy)
	}
	// force apaga; sem o segredo o gateway não chama o upstream e o erro nunca traz o valor
	if err := e.svc.DeleteSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "docs-token", true); err != nil {
		t.Fatal(err)
	}
	before := up.requests()
	res, err := e.svc.RefreshIntegrationTools(bg, domain.HumanActor, e.co.ID, view.ID)
	msg := res.Error
	if err != nil {
		msg += err.Error()
	}
	if res.OK || up.requests() != before || !strings.Contains(msg, "segredo não resolvido") || strings.Contains(msg, sentinel) || strings.Contains(msg, sentinelB) {
		t.Fatalf("sem o segredo: res=%+v err=%v hits %d→%d", res, err, before, up.requests())
	}

	// MCP stdio continua indo no mcpServers, com o env resolvido
	e.svc.Integrations.AllowStdio = true
	e.create(t, "stdio-token", sentinel)
	if _, err := e.svc.CreateIntegration(bg, domain.HumanActor, e.co.ID, service.CreateIntegrationInput{Kind: domain.IntegrationMCP, Name: "Local",
		Transport: "stdio", Command: "npx", AllPersonas: true, Env: map[string]string{"TOKEN": "${secret:stdio-token}"}}); err != nil {
		t.Fatal(err)
	}
	ex, _ = e.svc.RunExtras(bg, e.co.ID, p.ID)
	if got := string(ex.MCP["local"]); !strings.Contains(got, sentinel) {
		t.Fatalf("env do stdio deveria sair resolvido: %s", got)
	}
}

func TestPersonaEnvIsSealedOnHire(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	cfg := json.RawMessage(fmt.Sprintf(`{"env":{"ANTHROPIC_AUTH_TOKEN":%q,"ANTHROPIC_BASE_URL":"https://gw.example.com"}}`, sentinel))
	p := e.persona(t, "Ana", func(in *service.HirePersonaInput) { in.AdapterConfig = cfg })

	var raw string
	if err := e.svc.Pool.QueryRow(bg, `SELECT adapter_config::text FROM personas WHERE id=$1`, p.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, sentinel) || strings.Contains(raw, "gw.example.com") {
		t.Fatalf("o env em claro ficou no banco: %s", raw)
	}
	if !strings.Contains(raw, "${secret:persona.ana.ANTHROPIC_AUTH_TOKEN}") {
		t.Fatalf("adapter_config = %s", raw)
	}
	names, err := e.svc.SecretNames(bg, e.co.ID)
	if err != nil || len(names) != 2 {
		t.Fatalf("segredos criados: %v %v", names, err)
	}
	resolved, used, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, p.ID, p.AdapterConfig)
	if err != nil || !strings.Contains(string(resolved), sentinel) || len(used) != 2 {
		t.Fatalf("ResolveAdapterConfig = %s %v %v", resolved, used, err)
	}

	// sem chave de cifragem: continua em claro (retrocompatível) e a resolução passa o texto
	e.svc.Integrations = service.IntegrationsConfig{}
	p2 := e.persona(t, "Beto", func(in *service.HirePersonaInput) { in.AdapterConfig = cfg })
	if !strings.Contains(string(p2.AdapterConfig), sentinel) {
		t.Fatalf("sem chave o env fica como estava: %s", p2.AdapterConfig)
	}
	if r, u, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, p2.ID, p2.AdapterConfig); err != nil || len(u) != 0 || string(r) != string(p2.AdapterConfig) {
		t.Fatalf("legado: %s %v %v", r, u, err)
	}
}

func TestMigratePersonaEnv(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	// simula uma persona criada antes do M7: env em claro direto no banco
	legacy := e.persona(t, "Antiga", nil)
	cfg := fmt.Sprintf(`{"permission_mode":"plan","env":{"MY_TOKEN":%q,"PLAIN":"v"}}`, sentinel)
	if _, err := e.svc.Pool.Exec(bg, `UPDATE personas SET adapter_config=$2::jsonb WHERE id=$1`, legacy.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.CountPersonasWithPlainEnv(bg); err != nil || n != 1 {
		t.Fatalf("antes da migração: %d %v", n, err)
	}

	n, err := e.svc.MigratePersonaEnv(bg)
	if err != nil || n != 1 {
		t.Fatalf("migrar = %d %v", n, err)
	}
	var raw string
	_ = e.svc.Pool.QueryRow(bg, `SELECT adapter_config::text FROM personas WHERE id=$1`, legacy.ID).Scan(&raw)
	if strings.Contains(raw, sentinel) || !strings.Contains(raw, "${secret:persona.antiga.MY_TOKEN}") || !strings.Contains(raw, `"permission_mode"`) {
		t.Fatalf("adapter_config migrado = %s", raw)
	}
	got, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:persona.antiga.MY_TOKEN}")
	if err != nil || got != sentinel {
		t.Fatalf("o valor migrado deve resolver igual: %q %v", got, err)
	}
	// idempotente
	if n, err := e.svc.MigratePersonaEnv(bg); err != nil || n != 0 {
		t.Fatalf("segunda migração = %d %v", n, err)
	}
	if n, _ := e.svc.CountPersonasWithPlainEnv(bg); n != 0 {
		t.Fatalf("depois: %d", n)
	}
	// há atividade da migração, sem valor
	var data string
	if err := e.svc.Pool.QueryRow(bg, `SELECT data::text FROM activity_events WHERE verb='persona.env_migrated'`).Scan(&data); err != nil || strings.Contains(data, sentinel) {
		t.Fatalf("evento da migração: %q %v", data, err)
	}
	// sem chave não migra
	e.svc.Integrations = service.IntegrationsConfig{}
	if _, err := e.svc.MigratePersonaEnv(bg); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem chave: %v", err)
	}
}

func TestVaultSecretResolvesAtDispatch(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	v, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "cofre", Provider: domain.SecretProviderVault, ExternalRef: "apps/hanko#token"})
	if err != nil || v.Provider != domain.SecretProviderVault || v.ExternalRef != "apps/hanko#token" {
		t.Fatalf("create vault: %+v %v", v, err)
	}
	var enc string
	_ = e.svc.Pool.QueryRow(bg, `SELECT value_enc FROM secrets WHERE id=$1`, v.ID).Scan(&enc)
	if enc != "" {
		t.Fatalf("segredo de cofre não guarda valor: %q", enc)
	}
	got, used, err := e.svc.ResolveSecretText(bg, e.co.ID, "t=${secret:cofre}")
	if err != nil || got != "t="+sentinelVT || len(used) != 1 {
		t.Fatalf("resolve = %q %v %v", got, used, err)
	}
	// o valor é lido do cofre a cada despacho (sem cache): mudar lá vale na hora
	e.vault.mu.Lock()
	e.vault.data["apps/hanko#token"] = "novo-no-cofre"
	e.vault.mu.Unlock()
	if got, _, _ := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:cofre}"); got != "novo-no-cofre" {
		t.Fatalf("rotação no cofre: %q", got)
	}
	// revelar não se aplica ao cofre
	if _, err := e.svc.RevealSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "cofre"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("reveal vault: %v", err)
	}
	// cofre fora do ar: erro claro, sem valor e sem cair para valor antigo
	e.vault.err = errors.New("vault: cofre inacessível")
	_, _, err = e.svc.ResolveSecretText(bg, e.co.ID, "${secret:cofre}")
	if err == nil || strings.Contains(err.Error(), sentinelVT) || !strings.Contains(err.Error(), "cofre") {
		t.Fatalf("cofre fora: %v", err)
	}
	// sem cofre configurado, provider vault é recusado
	e.svc.Vault = nil
	_, err = e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "x", Provider: domain.SecretProviderVault, ExternalRef: "a#b"})
	wantErr(t, err, domain.ErrInvalid)
}

func TestSecretRekeyAndAAD(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	a := e.create(t, "a", sentinel)
	b := e.create(t, "b", sentinelB)

	// o texto cifrado de "a" colado na linha de "b" não decifra (AAD = id do segredo)
	var encA, encB string
	_ = e.svc.Pool.QueryRow(bg, `SELECT value_enc FROM secrets WHERE id=$1`, a.ID).Scan(&encA)
	_ = e.svc.Pool.QueryRow(bg, `SELECT value_enc FROM secrets WHERE id=$1`, b.ID).Scan(&encB)
	if _, err := e.svc.Pool.Exec(bg, `UPDATE secrets SET value_enc=$2 WHERE id=$1`, b.ID, encA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:b}"); err == nil {
		t.Fatal("copiar o texto cifrado para outra linha não pode decifrar")
	}
	if _, err := e.svc.Pool.Exec(bg, `UPDATE secrets SET value_enc=$2 WHERE id=$1`, b.ID, encB); err != nil {
		t.Fatal(err)
	}

	// rotação de HANKO_SECRET_KEY: a nova decifra o antigo (com PREVIOUS) e o rekey reescreve
	boxNew, err := secretbox.New(keyTwo, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: boxNew}
	if got, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:a}"); err != nil || got != sentinel {
		t.Fatalf("com a chave anterior: %q %v", got, err)
	}
	n, err := e.svc.RekeyIntegrationSecrets(bg)
	if err != nil || n < 1 {
		t.Fatalf("rekey = %d %v", n, err)
	}
	var encNow string
	_ = e.svc.Pool.QueryRow(bg, `SELECT value_enc FROM secrets WHERE id=$1`, a.ID).Scan(&encNow)
	if boxNew.NeedsReseal(encNow) {
		t.Fatalf("depois do rekey o texto ainda usa a chave antiga: %s", encNow)
	}
	// sem a chave anterior, depois do rekey ainda decifra
	boxOnlyNew, _ := secretbox.New(keyTwo, "")
	e.svc.Integrations = service.IntegrationsConfig{Secrets: boxOnlyNew}
	if got, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:a}"); err != nil || got != sentinel {
		t.Fatalf("depois do rekey: %q %v", got, err)
	}
}

// Nenhum caminho de saída do Hanko pode carregar o valor de um segredo: atividade, export, log do servidor,
// listagens, mensagens de erro. (Run events, briefing e resumo da run são cobertos no teste do heartbeat.)
func TestSecretValueNeverLeaks(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	hr := e.persona(t, "Ana", func(in *service.HirePersonaInput) {
		in.AdapterConfig = json.RawMessage(fmt.Sprintf(`{"env":{"ANTHROPIC_AUTH_TOKEN":%q}}`, sentinel))
	})
	e.create(t, "docs-token", sentinelB)
	_, err := e.svc.CreateIntegration(bg, domain.HumanActor, e.co.ID, service.CreateIntegrationInput{Kind: domain.IntegrationMCP, Name: "Docs",
		URL: "https://mcp.example.com/mcp", AllPersonas: true, Headers: map[string]string{"Authorization": "Bearer ${secret:docs-token}"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.RevealSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "docs-token")
	_, _ = e.svc.RotateSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "docs-token", service.RotateSecretInput{Value: sentinelB})
	_, _ = e.svc.RunExtras(bg, e.co.ID, hr.ID)
	_, _, _ = e.svc.ResolveAdapterConfig(bg, e.co.ID, hr.ID, hr.AdapterConfig)
	_, _ = e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "dup", Value: sentinel})
	_, errDup := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "dup", Value: sentinel})
	if errDup != nil && strings.Contains(errDup.Error(), sentinel) {
		t.Fatalf("a mensagem de erro vazou o valor: %v", errDup)
	}

	scan := func(where, text string) {
		t.Helper()
		for _, s := range []string{sentinel, sentinelB, sentinelVT} {
			if strings.Contains(text, s) {
				t.Errorf("%s contém o valor de um segredo (%s)", where, s)
			}
		}
	}

	// 1. atividade (o feed e o gatilho dos webhooks de saída)
	rows, err := e.svc.Pool.Query(bg, `SELECT data::text FROM activity_events`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		scan("activity_events", d)
	}
	rows.Close()
	// 2. o corpo enfileirado dos webhooks e os eventos de memória
	for _, q := range []string{`SELECT payload::text FROM memory_outbox`, `SELECT coalesce(string_agg(payload::text, ''), '') FROM webhook_deliveries`} {
		var d string
		if err := e.svc.Pool.QueryRow(bg, q).Scan(&d); err == nil {
			scan(q, d)
		}
	}
	// 3. export da empresa inteira (inclui personas com adapter_config)
	doc, err := e.svc.ExportCompany(bg, e.co.ID, service.ExportOptions{WithLogs: true, WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(doc)
	scan("export", string(b))
	if !strings.Contains(string(b), "${secret:persona.ana.ANTHROPIC_AUTH_TOKEN}") {
		t.Errorf("o export deve levar a referência (nome), não o valor")
	}
	// 4. listagens e views
	l, _ := e.svc.ListSecrets(bg, e.co.ID, domain.SecretScopeCompany, true)
	b, _ = json.Marshal(l)
	scan("ListSecrets", string(b))
	il, _ := e.svc.ListIntegrations(bg, e.co.ID)
	b, _ = json.Marshal(il)
	scan("ListIntegrations", string(b))
	pl, _ := e.svc.ListPersonas(bg, e.co.ID)
	b, _ = json.Marshal(pl)
	scan("ListPersonas", string(b))
	// 5. log do servidor (debug ligado)
	scan("log do servidor", e.logs.String())
}

// O arquivo de export leva só a referência; quem importa descobre quantos segredos precisa recriar.
func TestImportReportsSecretRefsToCreate(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.persona(t, "Ana", func(in *service.HirePersonaInput) {
		in.AdapterConfig = json.RawMessage(fmt.Sprintf(`{"env":{"ANTHROPIC_AUTH_TOKEN":%q,"OTHER":"x"}}`, sentinel))
	})
	doc, err := e.svc.ExportCompany(bg, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.ImportCompany(bg, domain.HumanActor, doc, service.ImportOptions{Slug: "copia"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts["secret_refs_to_create"] != 2 {
		t.Fatalf("counts = %v", res.Counts)
	}
	// a empresa importada não tem os segredos (e a persona dela falharia cedo, com o nome, ao rodar)
	names, err := e.svc.SecretNames(bg, res.Company.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("segredos na empresa importada: %v %v", names, err)
	}
	if _, _, err := e.svc.ResolveSecretText(bg, res.Company.ID, "${secret:persona.ana.OTHER}"); err == nil {
		t.Fatal("o segredo não existe na empresa nova")
	}
}
