package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
	"github.com/renatobardi/hanko/internal/webhook"
)

const (
	keyOne = "0123456789abcdef0123456789abcdef-one"
	keyTwo = "fedcba9876543210fedcba9876543210-two"
)

// hookJobs é o fakeJobs que também enfileira entregas de webhook.
type hookJobs struct {
	fakeJobs
	hmu        sync.Mutex
	deliveries []domain.ID
}

func (h *hookJobs) DeliverWebhookTx(_ context.Context, _ pgx.Tx, id domain.ID) error {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	h.deliveries = append(h.deliveries, id)
	return nil
}

func (h *hookJobs) queued() []domain.ID {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	return append([]domain.ID(nil), h.deliveries...)
}

type intEnv struct {
	*env
	hooks *hookJobs
	box   *secretbox.Box
}

// newIntEnv é newEnv com cofre de segredos (chave one) e fila de webhooks.
func newIntEnv(t *testing.T, cfg service.IntegrationsConfig) *intEnv {
	t.Helper()
	pool := testdb.New(t)
	jobs := &hookJobs{}
	broker := events.NewBroker()
	svc := service.New(pool, jobs, broker)
	box, err := secretbox.New(keyOne, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Secrets == nil {
		cfg.Secrets = box
	}
	svc.Integrations = cfg
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme Corp"})
	if err != nil {
		t.Fatal(err)
	}
	return &intEnv{env: &env{svc: svc, jobs: &jobs.fakeJobs, broker: broker, co: co}, hooks: jobs, box: box}
}

func (e *intEnv) mcp(t *testing.T, in service.CreateIntegrationInput) service.IntegrationView {
	t.Helper()
	in.Kind = domain.IntegrationMCP
	v, err := e.svc.CreateIntegration(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create mcp %s: %v", in.Name, err)
	}
	return v
}

func (e *intEnv) webhook(t *testing.T, in service.CreateIntegrationInput) service.IntegrationView {
	t.Helper()
	in.Kind = domain.IntegrationWebhook
	v, err := e.svc.CreateIntegration(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create webhook %s: %v", in.Name, err)
	}
	return v
}

// rawSecretColumn lê secret_enc direto do banco.
func rawSecretColumn(t *testing.T, e *intEnv, id domain.ID) string {
	t.Helper()
	var s string
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT secret_enc FROM integrations WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMCPIntegrationSecretsAreEncryptedAndNeverReturned(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{})
	ctx := context.Background()
	const token = "Bearer tok_super_secreto_123456"

	v := e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: "https://mcp.example.com/mcp",
		Headers: map[string]string{"Authorization": token, "X-Team": "alpha-team-value"}, AllPersonas: true})
	if !v.SecretSet || v.RevealedSecret != "" || v.Config.URL != "https://mcp.example.com/mcp" ||
		len(v.Config.HeaderNames) != 2 || v.Config.HeaderNames[0] != "Authorization" || v.Config.HeaderNames[1] != "X-Team" {
		t.Fatalf("view: %+v", v)
	}

	// em repouso: cifrado, sem o valor em claro
	raw := rawSecretColumn(t, e, v.ID)
	if raw == "" || strings.Contains(raw, "tok_super") || strings.Contains(raw, "alpha-team") || !strings.HasPrefix(raw, "v1:") {
		t.Fatalf("coluna cifrada: %q", raw)
	}
	var cfg string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT config::text FROM integrations WHERE id = $1`, v.ID).Scan(&cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg, "tok_super") || strings.Contains(cfg, "alpha-team") {
		t.Fatalf("config vaza segredo: %s", cfg)
	}

	// nenhuma leitura devolve o valor
	got, err := e.svc.GetIntegration(ctx, e.co.ID, "docs")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.ListIntegrations(ctx, e.co.ID)
	for _, x := range []any{got, list} {
		b, _ := json.Marshal(x)
		if strings.Contains(string(b), "tok_super") || strings.Contains(string(b), "alpha-team") {
			t.Fatalf("leitura vaza segredo: %s", b)
		}
	}
	// o feed de atividade também não
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "integration"})
	b, _ := json.Marshal(acts)
	if len(acts) == 0 || strings.Contains(string(b), "tok_super") || strings.Contains(string(b), "mcp.example.com") {
		t.Fatalf("atividade: %s", b)
	}

	// trocar um header mantém os outros; remover tira o nome
	up, err := e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateIntegrationInput{
		SetHeaders: map[string]string{"X-Team": "beta-team-value"}, DropHeaders: []string{"Authorization"}})
	if err != nil || len(up.Config.HeaderNames) != 1 || up.Config.HeaderNames[0] != "X-Team" {
		t.Fatalf("update headers: %+v %v", up, err)
	}
	ex, err := e.svc.RunExtras(ctx, e.co.ID, e.persona(t, "Ana", nil).ID)
	if err != nil {
		t.Fatal(err)
	}
	// MCP http vai pelo gateway de tools (ADR-0036): o agente não recebe a URL nem os headers guardados
	exJSON, _ := json.Marshal(ex)
	if len(ex.ToolGateway) != 1 || ex.ToolGateway[0] != "docs" || len(ex.MCP) != 0 || strings.Contains(string(exJSON), "beta-team") {
		t.Fatalf("extras: %s", exJSON)
	}
}

func TestSecretsRequireServerKey(t *testing.T) {
	pool := testdb.New(t)
	svc := service.New(pool, &hookJobs{}, events.NewBroker())
	co, _ := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme"})
	ctx := context.Background()

	// MCP sem segredos funciona sem chave
	if _, err := svc.CreateIntegration(ctx, domain.HumanActor, co.ID, service.CreateIntegrationInput{
		Kind: domain.IntegrationMCP, Name: "Aberto", URL: "https://mcp.example.com/mcp"}); err != nil {
		t.Fatalf("sem segredos: %v", err)
	}
	// com headers (segredos) ou webhook (sempre tem segredo): recusa
	_, err := svc.CreateIntegration(ctx, domain.HumanActor, co.ID, service.CreateIntegrationInput{
		Kind: domain.IntegrationMCP, Name: "Fechado", URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer abc"}})
	wantErr(t, err, domain.ErrInvalid)
	_, err = svc.CreateIntegration(ctx, domain.HumanActor, co.ID, service.CreateIntegrationInput{
		Kind: domain.IntegrationWebhook, Name: "Hook", URL: "https://hooks.example.com/in", Events: []string{domain.EventTaskCreated}})
	wantErr(t, err, domain.ErrInvalid)
}

func TestIntegrationValidation(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{})
	ctx := context.Background()
	hook := func(url string, events ...string) service.CreateIntegrationInput {
		return service.CreateIntegrationInput{Kind: domain.IntegrationWebhook, Name: "H", URL: url, Events: events}
	}
	bad := map[string]service.CreateIntegrationInput{
		"webhook http":            hook("http://hooks.example.com/in", domain.EventTaskCreated),
		"webhook loopback":        hook("https://127.0.0.1/in", domain.EventTaskCreated),
		"webhook localhost":       hook("https://localhost/in", domain.EventTaskCreated),
		"webhook rfc1918":         hook("https://10.1.2.3/in", domain.EventTaskCreated),
		"webhook metadados":       hook("https://169.254.169.254/latest", domain.EventTaskCreated),
		"webhook v4 mapeado":      hook("https://[::ffff:127.0.0.1]/in", domain.EventTaskCreated),
		"webhook credenciais":     hook("https://u:p@hooks.example.com/in", domain.EventTaskCreated),
		"webhook sem eventos":     hook("https://hooks.example.com/in"),
		"webhook evento inválido": hook("https://hooks.example.com/in", "task.exploded"),
		"webhook ping":            hook("https://hooks.example.com/in", domain.EventPing),
		"webhook segredo curto":   {Kind: domain.IntegrationWebhook, Name: "H", URL: "https://hooks.example.com/in", Events: []string{domain.EventTaskCreated}, Secret: "curto"},
		"webhook com headers":     {Kind: domain.IntegrationWebhook, Name: "H", URL: "https://hooks.example.com/in", Events: []string{domain.EventTaskCreated}, Headers: map[string]string{"A": "b"}},
		"mcp sem url":             {Kind: domain.IntegrationMCP, Name: "M"},
		"mcp esquema ftp":         {Kind: domain.IntegrationMCP, Name: "M", URL: "ftp://x.example.com"},
		"mcp header inválido":     {Kind: domain.IntegrationMCP, Name: "M", URL: "https://m.example.com", Headers: map[string]string{"Bad Name": "x"}},
		"mcp header com quebra":   {Kind: domain.IntegrationMCP, Name: "M", URL: "https://m.example.com", Headers: map[string]string{"A": "x\r\nInjected: 1"}},
		"mcp slug reservado":      {Kind: domain.IntegrationMCP, Name: "Hanko", URL: "https://m.example.com"},
		"mcp com eventos":         {Kind: domain.IntegrationMCP, Name: "M", URL: "https://m.example.com", Events: []string{domain.EventTaskCreated}},
		"mcp stdio desligado":     {Kind: domain.IntegrationMCP, Name: "M", Transport: "stdio", Command: "npx", Args: []string{"-y", "x"}},
		"mcp transporte inválido": {Kind: domain.IntegrationMCP, Name: "M", Transport: "ws", URL: "https://m.example.com"},
		"tipo inválido":           {Kind: "ftp", Name: "M"},
		"nome vazio":              {Kind: domain.IntegrationMCP, URL: "https://m.example.com"},
	}
	for name, in := range bad {
		_, err := e.svc.CreateIntegration(ctx, domain.HumanActor, e.co.ID, in)
		if err == nil {
			t.Errorf("%s: deveria ser recusado", name)
			continue
		}
		wantErr(t, err, domain.ErrInvalid)
	}
	// persona de outra empresa
	other, _ := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	p, err := e.svc.HirePersona(ctx, domain.HumanActor, other.ID, service.HirePersonaInput{Name: "X", Agent: "fake", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateIntegration(ctx, domain.HumanActor, e.co.ID, service.CreateIntegrationInput{
		Kind: domain.IntegrationMCP, Name: "M", URL: "https://m.example.com", PersonaIDs: []domain.ID{p.ID}})
	wantErr(t, err, domain.ErrInvalid)

	// o operador libera destinos internos
	e.svc.Integrations.AllowPrivate = true
	if _, err := e.svc.CreateIntegration(ctx, domain.HumanActor, e.co.ID, hook("https://127.0.0.1:8443/in", domain.EventTaskCreated)); err != nil {
		t.Fatalf("AllowPrivate: %v", err)
	}
}

func TestStdioOnlyWhenOperatorEnables(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{AllowStdio: true})
	ctx := context.Background()
	v := e.mcp(t, service.CreateIntegrationInput{Name: "Local", Transport: "stdio", Command: "npx", Args: []string{"-y", "@acme/mcp"},
		Env: map[string]string{"ACME_TOKEN": "acme_tok_987654321"}, AllPersonas: true})
	if v.Config.Command != "npx" || len(v.Config.EnvNames) != 1 || strings.Contains(rawSecretColumn(t, e, v.ID), "acme_tok") {
		t.Fatalf("view: %+v", v)
	}
	p := e.persona(t, "Ana", nil)
	ex, err := e.svc.RunExtras(ctx, e.co.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(ex.MCP["local"], &entry); err != nil {
		t.Fatalf("stdio ausente: %v (%v)", err, ex.Warnings)
	}
	if entry.Type != "stdio" || entry.Command != "npx" || len(entry.Args) != 2 || entry.Env["ACME_TOKEN"] != "acme_tok_987654321" {
		t.Fatalf("entrada stdio: %+v", entry)
	}

	// o operador desliga depois: o servidor sai da run, com aviso
	e.svc.Integrations.AllowStdio = false
	ex, err = e.svc.RunExtras(ctx, e.co.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ex.MCP["local"]; ok || len(ex.Warnings) != 1 || !strings.Contains(ex.Warnings[0], "stdio") {
		t.Fatalf("stdio desligado: %v %v", ex.MCP, ex.Warnings)
	}
}

func TestMCPInjectionPerPersona(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{})
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	bia := e.persona(t, "Bia", nil)

	all := e.mcp(t, service.CreateIntegrationInput{Name: "Todas", URL: "https://all.example.com/mcp", AllPersonas: true})
	onlyAna := e.mcp(t, service.CreateIntegrationInput{Name: "So Ana", URL: "https://ana.example.com/mcp", PersonaIDs: []domain.ID{ana.ID}})
	off := e.mcp(t, service.CreateIntegrationInput{Name: "Desligado", URL: "https://off.example.com/mcp", AllPersonas: true, Enabled: ptr(false)})

	keys := func(p domain.Persona) []string {
		ex, err := e.svc.RunExtras(ctx, e.co.ID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		var ks []string
		for k := range ex.MCP {
			ks = append(ks, k)
		}
		return append(ks, ex.ToolGateway...)
	}
	if got := keys(ana); len(got) != 2 {
		t.Fatalf("Ana: %v", got)
	}
	if got := keys(bia); len(got) != 1 || got[0] != "todas" {
		t.Fatalf("Bia: %v", got)
	}

	// ligar/desligar pela persona
	if err := e.svc.SetPersonaIntegration(ctx, domain.HumanActor, e.co.ID, bia.ID, onlyAna.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := keys(bia); len(got) != 2 {
		t.Fatalf("Bia depois de ligar: %v", got)
	}
	if err := e.svc.SetPersonaIntegration(ctx, domain.HumanActor, e.co.ID, ana.ID, onlyAna.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := keys(ana); len(got) != 1 {
		t.Fatalf("Ana depois de desligar: %v", got)
	}
	links, err := e.svc.ListPersonaIntegrations(ctx, e.co.ID, bia.ID)
	if err != nil || len(links) != 3 {
		t.Fatalf("links: %+v %v", links, err)
	}
	pl, err := e.svc.ListIntegrationPersonas(ctx, e.co.ID, onlyAna.ID)
	if err != nil || len(pl) != 2 || pl[0].Enabled == pl[1].Enabled {
		t.Fatalf("personas do MCP: %+v %v", pl, err)
	}
	// trocar o conjunto inteiro
	if _, err := e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, onlyAna.ID, service.UpdateIntegrationInput{PersonaIDs: &[]domain.ID{ana.ID}}); err != nil {
		t.Fatal(err)
	}
	if got := keys(bia); len(got) != 1 {
		t.Fatalf("Bia depois do conjunto novo: %v", got)
	}
	// religar a desligada
	if _, err := e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, off.ID, service.UpdateIntegrationInput{Enabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if got := keys(bia); len(got) != 2 {
		t.Fatalf("Bia com a religada: %v", got)
	}
	// um webhook não é ligado a persona
	wh := e.webhook(t, service.CreateIntegrationInput{Name: "Hook", URL: "https://hooks.example.com/in", Events: []string{domain.EventTaskCreated}})
	wantErr(t, e.svc.SetPersonaIntegration(ctx, domain.HumanActor, e.co.ID, ana.ID, wh.ID, true), domain.ErrInvalid)
	_ = all
}

func TestRunExtrasSkipsUnreadableSecrets(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{})
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	e.mcp(t, service.CreateIntegrationInput{Name: "Com Segredo", URL: "https://a.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer xyz12345"}, AllPersonas: true})
	e.mcp(t, service.CreateIntegrationInput{Name: "Sem Segredo", URL: "https://b.example.com/mcp", AllPersonas: true})

	// a chave do servidor mudou sem a anterior: a run segue sem o servidor ilegível, com aviso
	nb, _ := secretbox.New(keyTwo, "")
	e.svc.Integrations.Secrets = nb
	ex, err := e.svc.RunExtras(ctx, e.co.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.ToolGateway) != 1 || ex.ToolGateway[0] != "sem-segredo" || len(ex.Warnings) != 1 || strings.Contains(ex.Warnings[0], "xyz12345") {
		t.Fatalf("extras: %v %v", ex.ToolGateway, ex.Warnings)
	}
}

func TestSecretKeyRotationAndRekey(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{})
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: "https://a.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer xyz12345"}, AllPersonas: true})

	// rotação: chave nova + anterior continua lendo
	both, _ := secretbox.New(keyTwo, keyOne)
	e.svc.Integrations.Secrets = both
	ex, err := e.svc.RunExtras(ctx, e.co.ID, p.ID)
	if err != nil || len(ex.ToolGateway) != 1 || len(ex.Warnings) != 0 {
		t.Fatalf("lendo com a anterior: %v %v %v", ex.ToolGateway, ex.Warnings, err)
	}
	n, err := e.svc.RekeyIntegrationSecrets(ctx)
	if err != nil || n != 1 {
		t.Fatalf("rekey: %d %v", n, err)
	}
	if n, _ := e.svc.RekeyIntegrationSecrets(ctx); n != 0 {
		t.Fatalf("rekey de novo deveria ser 0, foi %d", n)
	}
	// depois do rekey, só a chave nova basta
	only, _ := secretbox.New(keyTwo, "")
	e.svc.Integrations.Secrets = only
	ex, err = e.svc.RunExtras(ctx, e.co.ID, p.ID)
	if err != nil || len(ex.ToolGateway) != 1 || len(ex.Warnings) != 0 {
		t.Fatalf("só a nova: %v %v %v", ex.ToolGateway, ex.Warnings, err)
	}
	// rekey é do dono da instalação
	admin := mkUser(t, e.env, "adm@x.com", domain.RoleAdmin)
	_, err = e.svc.RekeyIntegrationSecrets(as(admin))
	wantErr(t, err, domain.ErrForbidden)
	owner := mkUser(t, e.env, "own@x.com", domain.RoleOwner)
	if _, err := e.svc.RekeyIntegrationSecrets(as(owner)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRoles(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{})
	p := e.persona(t, "Ana", nil)
	hook := e.webhook(t, service.CreateIntegrationInput{Name: "Hook", URL: "https://hooks.example.com/in?token=SEGREDO-NA-URL", Events: []string{domain.EventTaskCreated}})
	mcp := e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: "https://mcp.example.com/mcp"})
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
		u := mkUser(t, e.env, string(r)+"@x.com", domain.RoleMember)
		join(t, e.env, u, r)
		users[r] = u
	}
	outsider := mkUser(t, e.env, "fora@x.com", domain.RoleMember)

	// leitura: todos os papéis; a URL completa só para admin+
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
		ctx := as(users[r])
		list, err := e.svc.ListIntegrations(ctx, e.co.ID)
		if err != nil || len(list) != 2 {
			t.Fatalf("list como %s: %v %v", r, list, err)
		}
		g, err := e.svc.GetIntegration(ctx, e.co.ID, "hook")
		if err != nil {
			t.Fatal(err)
		}
		full := strings.Contains(g.Config.URL, "SEGREDO-NA-URL")
		if full != r.AtLeast(domain.MemberAdmin) {
			t.Errorf("%s: URL completa = %v (%q)", r, full, g.Config.URL)
		}
		if !full && g.Config.URL != "https://hooks.example.com" {
			t.Errorf("%s: origem = %q", r, g.Config.URL)
		}
		if _, err := e.svc.ListWebhookDeliveries(ctx, e.co.ID, hook.ID, 10); err != nil {
			t.Errorf("deliveries como %s: %v", r, err)
		}
	}
	_, err := e.svc.ListIntegrations(as(outsider), e.co.ID)
	wantErr(t, err, domain.ErrNotFound)

	writes := map[string]func(ctx context.Context, u domain.User) error{
		"Create": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.CreateIntegration(ctx, u.Actor(), e.co.ID, service.CreateIntegrationInput{Kind: domain.IntegrationMCP, Name: "N-" + u.Email, URL: "https://m.example.com"})
			return err
		},
		"Update": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.UpdateIntegration(ctx, u.Actor(), e.co.ID, mcp.ID, service.UpdateIntegrationInput{Name: ptr("Docs " + u.Email)})
			return err
		},
		"Test": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.TestIntegration(ctx, u.Actor(), e.co.ID, hook.ID)
			return err
		},
		"SetPersonaIntegration": func(ctx context.Context, u domain.User) error {
			return e.svc.SetPersonaIntegration(ctx, u.Actor(), e.co.ID, p.ID, mcp.ID, true)
		},
	}
	for name, run := range writes {
		for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember} {
			wantErr(t, run(as(users[r]), users[r]), domain.ErrForbidden)
		}
		for _, r := range []domain.MemberRole{domain.MemberAdmin, domain.MemberOwner} {
			if err := run(as(users[r]), users[r]); err != nil {
				t.Errorf("%s como %s: %v", name, r, err)
			}
		}
		wantErr(t, run(as(outsider), outsider), domain.ErrNotFound)
	}
	wantErr(t, e.svc.DeleteIntegration(as(users[domain.MemberMember]), users[domain.MemberMember].Actor(), e.co.ID, mcp.ID), domain.ErrForbidden)
	if err := e.svc.DeleteIntegration(as(users[domain.MemberAdmin]), users[domain.MemberAdmin].Actor(), e.co.ID, mcp.ID); err != nil {
		t.Fatal(err)
	}
}

// ---- webhooks ----

// hookServer é um receptor TLS que guarda o que recebeu e responde com o código escolhido.
type hookServer struct {
	*httptest.Server
	mu     sync.Mutex
	code   int
	got    []received
	slowBy time.Duration
}

type received struct {
	hdr  http.Header
	body []byte
}

func newHookServer(t *testing.T) *hookServer {
	t.Helper()
	h := &hookServer{code: http.StatusOK}
	h.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		h.mu.Lock()
		h.got = append(h.got, received{hdr: r.Header.Clone(), body: []byte(b.String())})
		code, slow := h.code, h.slowBy
		h.mu.Unlock()
		if slow > 0 {
			time.Sleep(slow)
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *hookServer) setCode(c int) { h.mu.Lock(); h.code = c; h.mu.Unlock() }
func (h *hookServer) hits() []received {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]received(nil), h.got...)
}

// webhookEnv: receptor TLS em loopback, liberado pelo operador, com o cliente que confia no certificado de teste.
func webhookEnv(t *testing.T) (*intEnv, *hookServer) {
	t.Helper()
	srv := newHookServer(t)
	e := newIntEnv(t, service.IntegrationsConfig{AllowPrivate: true, HTTP: srv.Client()})
	return e, srv
}

func lastDelivery(t *testing.T, e *intEnv, id domain.ID) domain.WebhookDelivery {
	t.Helper()
	ds, err := e.svc.ListWebhookDeliveries(context.Background(), e.co.ID, id, 10)
	if err != nil || len(ds) == 0 {
		t.Fatalf("entregas: %v %v", ds, err)
	}
	return ds[0]
}

func TestWebhookCreateRevealsGeneratedSecretOnce(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	v := e.webhook(t, service.CreateIntegrationInput{Name: "Slack-like", URL: srv.URL + "/in", Events: []string{domain.EventGateOpened, domain.EventTaskCreated}})
	if !strings.HasPrefix(v.RevealedSecret, "whsec_") || len(v.RevealedSecret) < 40 || v.SecretHint != v.RevealedSecret[:10] || !v.SecretSet {
		t.Fatalf("segredo gerado: %+v", v)
	}
	if strings.Contains(rawSecretColumn(t, e, v.ID), v.RevealedSecret) {
		t.Fatal("segredo em claro no banco")
	}
	g, _ := e.svc.GetIntegration(ctx, e.co.ID, v.Slug)
	b, _ := json.Marshal(g)
	if strings.Contains(string(b), v.RevealedSecret) || g.SecretHint != v.SecretHint {
		t.Fatalf("leitura: %s", b)
	}
	if got := strings.Join(v.Events, ","); got != "gate.opened,task.created" {
		t.Fatalf("eventos: %v", v.Events)
	}

	// segredo informado: nada é revelado nem há dica
	own := e.webhook(t, service.CreateIntegrationInput{Name: "Meu", URL: srv.URL + "/in", Events: []string{domain.EventRunFailed}, Secret: "meu-segredo-longo-123456"})
	if own.RevealedSecret != "" || own.SecretHint != "" || !own.SecretSet {
		t.Fatalf("segredo informado: %+v", own)
	}
	// rotação gera outro, revelado uma vez
	rot, err := e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateIntegrationInput{RotateSecret: true})
	if err != nil || rot.RevealedSecret == "" || rot.RevealedSecret == v.RevealedSecret {
		t.Fatalf("rotação: %+v %v", rot, err)
	}
}

func TestWebhookEnqueuedWithTheFact(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	taskHook := e.webhook(t, service.CreateIntegrationInput{Name: "Tasks", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated, domain.EventTaskCompleted}})
	gateHook := e.webhook(t, service.CreateIntegrationInput{Name: "Gates", URL: srv.URL + "/g", Events: []string{domain.EventGateOpened, domain.EventGateDecided}})
	offHook := e.webhook(t, service.CreateIntegrationInput{Name: "Off", URL: srv.URL + "/o", Events: []string{domain.EventTaskCreated}, Enabled: ptr(false)})

	count := func(id domain.ID) int {
		var n int
		if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_deliveries WHERE integration_id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	tk := e.task(t, service.CreateTaskInput{Title: "Publicar release", PersonaID: &p.ID})
	if count(taskHook.ID) != 1 || count(gateHook.ID) != 0 || count(offHook.ID) != 0 || len(e.hooks.queued()) != 1 {
		t.Fatalf("após task.created: tasks=%d gates=%d off=%d fila=%d", count(taskHook.ID), count(gateHook.ID), count(offHook.ID), len(e.hooks.queued()))
	}
	var payload, event string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT payload, event FROM webhook_deliveries WHERE integration_id = $1`, taskHook.ID).Scan(&payload, &event); err != nil {
		t.Fatal(err)
	}
	var env service.WebhookEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatal(err)
	}
	if event != "task.created" || env.Event != "task.created" || env.Company.Slug != e.co.Slug || env.Subject.Type != "task" ||
		env.Subject.ID != tk.ID.String() || env.OccurredAt.IsZero() || !strings.Contains(string(env.Data), "Publicar release") {
		t.Fatalf("envelope: %s", payload)
	}

	// gate aberto e decidido
	g, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "Pode publicar?", TaskID: &tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if count(gateHook.ID) != 1 {
		t.Fatalf("gate.opened: %d", count(gateHook.ID))
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "ok"}); err != nil {
		t.Fatal(err)
	}
	if count(gateHook.ID) != 2 {
		t.Fatalf("gate.decided: %d", count(gateHook.ID))
	}

	// concluir a task: task.completed; cancelar não conta
	done := domain.TaskDone
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	if count(taskHook.ID) != 2 {
		t.Fatalf("task.completed: %d", count(taskHook.ID))
	}
	tk2 := e.task(t, service.CreateTaskInput{Title: "Outra"})
	canc := domain.TaskCancelled
	before := count(taskHook.ID)
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tk2.ID, service.UpdateTaskInput{Status: &canc}); err != nil {
		t.Fatal(err)
	}
	if count(taskHook.ID) != before+0 && count(taskHook.ID) != before {
		t.Fatalf("cancelar não gera task.completed: %d -> %d", before, count(taskHook.ID))
	}

	// nenhum payload carrega o segredo do webhook
	rows, err := e.svc.Pool.Query(ctx, `SELECT payload FROM webhook_deliveries`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var pl string
		_ = rows.Scan(&pl)
		if strings.Contains(pl, "whsec_") {
			t.Fatalf("payload com segredo: %s", pl)
		}
	}
}

// Sem a fila de webhooks (Jobs que não a implementa) o fato é gravado normalmente e nada é entregue.
func TestNoWebhookJobsIsNoOp(t *testing.T) {
	e := newEnv(t) // fakeJobs não implementa WebhookJobs
	e.svc.Integrations = service.IntegrationsConfig{}
	if _, err := e.svc.CreateTask(context.Background(), domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeliverWebhookSuccessAndSignature(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	h := e.webhook(t, service.CreateIntegrationInput{Name: "Tasks", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated}})
	e.task(t, service.CreateTaskInput{Title: "Entrega"})
	ids := e.hooks.queued()
	if len(ids) != 1 {
		t.Fatalf("fila: %v", ids)
	}
	out, err := e.svc.DeliverWebhook(ctx, ids[0], 1, service.WebhookMaxAttempts)
	if err != nil || out != service.WebhookDone {
		t.Fatalf("entrega: %v %v", out, err)
	}
	hits := srv.hits()
	if len(hits) != 1 {
		t.Fatalf("hits: %d", len(hits))
	}
	r := hits[0]
	if r.hdr.Get(webhook.HeaderEvent) != "task.created" || r.hdr.Get(webhook.HeaderDelivery) != ids[0].String() ||
		r.hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("cabeçalhos: %v", r.hdr)
	}
	if err := webhook.Verify(h.RevealedSecret, r.hdr.Get(webhook.HeaderSignature), r.body, time.Now(), 0); err != nil {
		t.Fatalf("assinatura: %v", err)
	}
	d := lastDelivery(t, e, h.ID)
	if d.Status != domain.DeliverySuccess || d.Attempts != 1 || d.HTTPStatus == nil || *d.HTTPStatus != 200 || d.LatencyMs == nil || d.DeliveredAt == nil || d.Error != "" {
		t.Fatalf("registro: %+v", d)
	}
	// repetir o job depois do sucesso não reenvia
	if out, _ := e.svc.DeliverWebhook(ctx, ids[0], 2, service.WebhookMaxAttempts); out != service.WebhookDone || len(srv.hits()) != 1 {
		t.Fatalf("reenvio: %v hits=%d", out, len(srv.hits()))
	}
}

func TestDeliverWebhookRetryThenGiveUp(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	h := e.webhook(t, service.CreateIntegrationInput{Name: "Tasks", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated}})
	e.task(t, service.CreateTaskInput{Title: "Entrega"})
	id := e.hooks.queued()[0]
	srv.setCode(http.StatusInternalServerError)

	for attempt := 1; attempt < 3; attempt++ {
		out, err := e.svc.DeliverWebhook(ctx, id, attempt, 3)
		if err != nil || out != service.WebhookRetry {
			t.Fatalf("tentativa %d: %v %v", attempt, out, err)
		}
		d := lastDelivery(t, e, h.ID)
		if d.Status != domain.DeliveryRetrying || d.Attempts != attempt || d.HTTPStatus == nil || *d.HTTPStatus != 500 ||
			d.Error != "HTTP 500" || d.NextAttemptAt == nil {
			t.Fatalf("tentativa %d registrada: %+v", attempt, d)
		}
		want := service.WebhookBackoff(attempt)
		if got := time.Until(*d.NextAttemptAt); got <= 0 || got > want+time.Second {
			t.Fatalf("próxima tentativa em %v, esperado até %v", got, want)
		}
	}
	// a última tentativa falha de vez
	out, err := e.svc.DeliverWebhook(ctx, id, 3, 3)
	if err != nil || out != service.WebhookGiveUp {
		t.Fatalf("última: %v %v", out, err)
	}
	d := lastDelivery(t, e, h.ID)
	if d.Status != domain.DeliveryFailed || d.Attempts != 3 || d.NextAttemptAt != nil {
		t.Fatalf("falha final: %+v", d)
	}
	if len(srv.hits()) != 3 {
		t.Fatalf("hits: %d", len(srv.hits()))
	}
	// depois de falhar de vez, outro job não reenvia
	if out, _ := e.svc.DeliverWebhook(ctx, id, 4, 3); out != service.WebhookDone || len(srv.hits()) != 3 {
		t.Fatalf("pós-falha: %v hits=%d", out, len(srv.hits()))
	}

	// recuperação: um receptor que volta no meio das tentativas
	e.task(t, service.CreateTaskInput{Title: "Outra"})
	id2 := e.hooks.queued()[1]
	if out, _ := e.svc.DeliverWebhook(ctx, id2, 1, 3); out != service.WebhookRetry {
		t.Fatalf("1ª: %v", out)
	}
	srv.setCode(http.StatusAccepted)
	if out, _ := e.svc.DeliverWebhook(ctx, id2, 2, 3); out != service.WebhookDone {
		t.Fatalf("2ª: %v", out)
	}
	d = lastDelivery(t, e, h.ID)
	if d.Status != domain.DeliverySuccess || d.Attempts != 2 || d.Error != "" {
		t.Fatalf("recuperada: %+v", d)
	}
}

func TestWebhookBackoffIsExponentialAndCapped(t *testing.T) {
	want := []time.Duration{30 * time.Second, 2 * time.Minute, 8 * time.Minute, 32 * time.Minute, 128 * time.Minute, 6 * time.Hour, 6 * time.Hour}
	for i, w := range want {
		if got := service.WebhookBackoff(i + 1); got != w {
			t.Errorf("tentativa %d: %v, esperado %v", i+1, got, w)
		}
	}
	if got := service.WebhookBackoff(1000); got != 6*time.Hour {
		t.Errorf("teto: %v", got)
	}
}

// A política pode endurecer depois de salvar: a entrega confere de novo e não conecta.
func TestDeliverWebhookReChecksDestination(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	h := e.webhook(t, service.CreateIntegrationInput{Name: "Tasks", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated}})
	e.task(t, service.CreateTaskInput{Title: "Entrega"})
	id := e.hooks.queued()[0]

	e.svc.Integrations.AllowPrivate = false
	e.svc.Integrations.HTTP = nil // o cliente padrão, com a proteção SSRF
	out, err := e.svc.DeliverWebhook(ctx, id, 1, 7)
	if err != nil || out != service.WebhookGiveUp {
		t.Fatalf("destino interno: %v %v", out, err)
	}
	if len(srv.hits()) != 0 {
		t.Fatal("o receptor interno foi alcançado")
	}
	d := lastDelivery(t, e, h.ID)
	if d.Status != domain.DeliveryFailed || !strings.Contains(d.Error, "destino não permitido") {
		t.Fatalf("registro: %+v", d)
	}
}

// Integração desligada com entrega pendente: falha sem enviar.
func TestDeliverWebhookDisabledIntegration(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	h := e.webhook(t, service.CreateIntegrationInput{Name: "Tasks", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated}})
	e.task(t, service.CreateTaskInput{Title: "Entrega"})
	id := e.hooks.queued()[0]
	if _, err := e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, h.ID, service.UpdateIntegrationInput{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if out, _ := e.svc.DeliverWebhook(ctx, id, 1, 7); out != service.WebhookGiveUp || len(srv.hits()) != 0 {
		t.Fatalf("desligada: %v hits=%d", out, len(srv.hits()))
	}
	// apagar a integração apaga as entregas; o job que sobrou encerra com erro (para o worker registrar), não com sucesso
	if err := e.svc.DeleteIntegration(ctx, domain.HumanActor, e.co.ID, h.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := e.svc.DeliverWebhook(ctx, id, 2, 7); err == nil || out != service.WebhookGiveUp {
		t.Fatalf("apagada: %v %v", out, err)
	}
}

func TestTestIntegrationWebhook(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	h := e.webhook(t, service.CreateIntegrationInput{Name: "Tasks", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated}})

	res, err := e.svc.TestIntegration(ctx, domain.HumanActor, e.co.ID, h.ID)
	if err != nil || !res.OK || res.Status != 200 || res.Delivery == nil || !res.Delivery.Test || res.Delivery.Event != "ping" {
		t.Fatalf("teste ok: %+v %v", res, err)
	}
	hit := srv.hits()[0]
	if hit.hdr.Get(webhook.HeaderEvent) != "ping" {
		t.Fatalf("evento: %v", hit.hdr)
	}
	if err := webhook.Verify(h.RevealedSecret, hit.hdr.Get(webhook.HeaderSignature), hit.body, time.Now(), 0); err != nil {
		t.Fatalf("assinatura do ping: %v", err)
	}
	if len(e.hooks.queued()) != 0 {
		t.Fatal("o teste não passa pela fila")
	}

	srv.setCode(http.StatusBadGateway)
	res, err = e.svc.TestIntegration(ctx, domain.HumanActor, e.co.ID, h.ID)
	if err != nil || res.OK || res.Status != 502 || res.Error != "HTTP 502" || res.Delivery.Status != domain.DeliveryFailed || res.Delivery.NextAttemptAt != nil {
		t.Fatalf("teste com falha: %+v %v", res, err)
	}
	ds, _ := e.svc.ListWebhookDeliveries(ctx, e.co.ID, h.ID, 10)
	if len(ds) != 2 {
		t.Fatalf("histórico: %d", len(ds))
	}
}

func TestTestIntegrationMCPHTTP(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, 2048)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		if gotAuth != "Bearer ok-token-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	// destino em loopback: bloqueado por padrão
	e := newIntEnv(t, service.IntegrationsConfig{})
	ctx := context.Background()
	m := e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: srv.URL + "/mcp", Headers: map[string]string{"Authorization": "Bearer ok-token-123"}, AllPersonas: true})
	res, err := e.svc.TestIntegration(ctx, domain.HumanActor, e.co.ID, m.ID)
	if err != nil || res.OK || !strings.Contains(res.Error, "destino não permitido") || gotBody != "" {
		t.Fatalf("loopback sem liberar: %+v %v body=%q", res, err, gotBody)
	}

	e.svc.Integrations.AllowPrivate = true
	res, err = e.svc.TestIntegration(ctx, domain.HumanActor, e.co.ID, m.ID)
	if err != nil || !res.OK || res.Status != 200 || gotAuth != "Bearer ok-token-123" || !strings.Contains(gotBody, `"method":"initialize"`) {
		t.Fatalf("liberado: %+v %v auth=%q body=%q", res, err, gotAuth, gotBody)
	}
	// header errado: 401, e o erro não repete o valor
	if _, err := e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, m.ID, service.UpdateIntegrationInput{SetHeaders: map[string]string{"Authorization": "Bearer errado-999"}}); err != nil {
		t.Fatal(err)
	}
	res, _ = e.svc.TestIntegration(ctx, domain.HumanActor, e.co.ID, m.ID)
	if res.OK || res.Status != 401 || strings.Contains(res.Error, "errado") {
		t.Fatalf("401: %+v", res)
	}
}

func TestWebhookEnableNeedsSecretAndUpdateRules(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	h := e.webhook(t, service.CreateIntegrationInput{Name: "Hook", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated}})

	// eventos e URL mudam; tipos misturados não
	up, err := e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, h.ID, service.UpdateIntegrationInput{
		Events: &[]string{domain.EventAlertOpened, domain.EventRunFailed}, URL: ptr(srv.URL + "/novo")})
	if err != nil || strings.Join(up.Events, ",") != "alert.opened,run.failed" || !strings.HasSuffix(up.Config.URL, "/novo") {
		t.Fatalf("update: %+v %v", up, err)
	}
	for name, in := range map[string]service.UpdateIntegrationInput{
		"headers em webhook": {SetHeaders: map[string]string{"A": "b"}},
		"all_personas":       {AllPersonas: ptr(true)},
		"evento inválido":    {Events: &[]string{"x.y"}},
		"url http":           {URL: ptr("http://hooks.example.com/x")},
		"segredo e rotação":  {Secret: ptr("outro-segredo-123456"), RotateSecret: true},
	} {
		_, err := e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, h.ID, in)
		if err == nil {
			t.Errorf("%s: deveria falhar", name)
			continue
		}
		wantErr(t, err, domain.ErrInvalid)
	}
	// um MCP não aceita eventos
	m := e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: "https://m.example.com/mcp"})
	_, err = e.svc.UpdateIntegration(ctx, domain.HumanActor, e.co.ID, m.ID, service.UpdateIntegrationInput{Events: &[]string{domain.EventTaskCreated}})
	wantErr(t, err, domain.ErrInvalid)
}
