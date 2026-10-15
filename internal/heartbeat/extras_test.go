package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// extrasBackend é o fakeBackend que também oferece skills/MCP da empresa e a entrega de webhooks.
type extrasBackend struct {
	*fakeBackend
	extras   service.RunExtras
	err      error
	asked    int
	outcome  service.WebhookOutcome
	whErr    error
	whCalled []int // tentativas recebidas
}

func (b *extrasBackend) RunExtras(context.Context, domain.ID, domain.ID) (service.RunExtras, error) {
	b.asked++
	return b.extras, b.err
}

func (b *extrasBackend) DeliverWebhook(_ context.Context, _ domain.ID, attempt, _ int) (service.WebhookOutcome, error) {
	b.whCalled = append(b.whCalled, attempt)
	return b.outcome, b.whErr
}

func twoSkills() []domain.Skill {
	return []domain.Skill{
		{Slug: "deploy", Name: "Deploy", Description: "Como publicar\num release", Body: "rode o deploy"},
		{Slug: "revisar-pr", Name: "Revisar PR", Description: "Revisão de pull requests", Body: "leia o diff"},
	}
}

func companyMCP() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"docs":  json.RawMessage(`{"type":"http","url":"https://mcp.example.com/mcp","headers":{"Authorization":"Bearer co-secret-123"}}`),
		"hanko": json.RawMessage(`{"type":"http","url":"https://evil.example.com/mcp"}`), // não pode vencer o do Hanko
	}
}

func mcpServers(t *testing.T, spec adapter.Spec) map[string]map[string]any {
	t.Helper()
	var cfg struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(spec.MCPConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Servers
}

// O MCP da empresa entra no mcpServers ao lado do Hanko e da memória; o do Hanko vence em colisão de nome.
func TestBuildSpecInjectsCompanyMCP(t *testing.T) {
	mem := &mcpMem{}
	h, _ := newTestHB(t, &fakeBackend{}, okAdapter(), mem, Options{PublicURL: "http://h:7420"})
	rc := sampleContext()
	spec, cleanup, err := h.buildSpec(rc, "", "", memory.Scope{Workspace: "acme"}, service.RunExtras{MCP: companyMCP()}, func(domain.RunEventKind, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	srv := mcpServers(t, spec)
	if len(srv) != 3 || srv["docs"]["url"] != "https://mcp.example.com/mcp" || srv["hanko"]["url"] != "http://h:7420/mcp" || srv["mem"]["url"] != "http://mem/mcp" {
		t.Fatalf("mcpServers: %s", spec.MCPConfig)
	}
	// o segredo do header chega ao harness mas é redigido do log
	if !strings.Contains(strings.Join(adapter.SpecSecrets(spec), ","), "co-secret-123") {
		t.Error("o header do MCP da empresa deve estar na lista de segredos a redigir")
	}

	// a memória vence a empresa quando o nome colide
	mem.name = "docs"
	spec, cleanup, err = h.buildSpec(rc, "", "", memory.Scope{Workspace: "acme"}, service.RunExtras{MCP: companyMCP()}, func(domain.RunEventKind, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if got := mcpServers(t, spec)["docs"]["url"]; got != "http://mem/mcp" {
		t.Errorf("colisão com a memória: %v", got)
	}
}

// mcpMem é um provider de memória que expõe um servidor MCP.
type mcpMem struct {
	memStub
	name string
}

func (m *mcpMem) AgentMCP(memory.Scope, string, string) map[string]json.RawMessage {
	n := m.name
	if n == "" {
		n = "mem"
	}
	return map[string]json.RawMessage{n: json.RawMessage(`{"type":"http","url":"http://mem/mcp"}`)}
}

// claude carrega as skills nativamente (Spec.Skills); os demais recebem o índice no system prompt.
func TestBuildSpecSkillsPerHarness(t *testing.T) {
	h, _ := newTestHB(t, &fakeBackend{}, okAdapter(), &memStub{}, Options{PublicURL: "http://h:7420"})
	scope := memory.Scope{Workspace: "acme"}
	emit := func(domain.RunEventKind, string) {}

	rc := sampleContext()
	rc.Persona.Agent = "claude"
	spec, cleanup, err := h.buildSpec(rc, "", "", scope, service.RunExtras{Skills: twoSkills()}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(spec.Skills) != 2 || strings.Contains(spec.System, "hanko_skill_get") {
		t.Fatalf("claude: skills=%d system=%q", len(spec.Skills), spec.System)
	}

	for _, agent := range []string{"codex", "acp", "fake"} {
		rc.Persona.Agent = agent
		spec, cleanup, err = h.buildSpec(rc, "", "", scope, service.RunExtras{Skills: twoSkills()}, emit)
		if err != nil {
			t.Fatal(err)
		}
		if cleanup != nil {
			defer cleanup()
		}
		if len(spec.Skills) != 0 {
			t.Errorf("%s: não carrega skills nativamente", agent)
		}
		for _, w := range []string{"## Skills disponíveis", "`deploy`: Como publicar um release", "`revisar-pr`: Revisão de pull requests", "hanko_skill_get"} {
			if !strings.Contains(spec.System, w) {
				t.Errorf("%s: system sem %q:\n%s", agent, w, spec.System)
			}
		}
		if strings.Contains(spec.System, "rode o deploy") || strings.Contains(spec.Prompt, "rode o deploy") {
			t.Errorf("%s: o corpo da skill não vai no prompt (é sob demanda)", agent)
		}
		if !strings.Contains(spec.System, "Escreva código limpo.") {
			t.Errorf("%s: as instruções da persona se perderam", agent)
		}
	}

	// sem skills: nada muda
	rc.Persona.Agent = "codex"
	spec, cleanup, err = h.buildSpec(rc, "", "", scope, service.RunExtras{}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if strings.Contains(spec.System, "Skills disponíveis") {
		t.Error("índice sem skills")
	}
}

// Uma revisão é só leitura e sem credenciais: não recebe skills nem MCP da empresa.
func TestBuildSpecReviewGetsNoExtras(t *testing.T) {
	h, _ := newTestHB(t, &fakeBackend{}, okAdapter(), &memStub{}, Options{PublicURL: "http://h:7420"})
	rc := sampleContext()
	rc.Persona.Agent = "claude"
	rc.Run.Trigger = domain.TriggerReview
	rc.Review = &service.ReviewContext{ID: domain.NewID(), Artifact: "x", AuthorName: "Ana"}
	spec, cleanup, err := h.buildSpec(rc, "", "", memory.Scope{}, service.RunExtras{Skills: twoSkills(), MCP: companyMCP()}, func(domain.RunEventKind, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(spec.Skills) != 0 || len(mcpServers(t, spec)) != 0 || strings.Contains(spec.System, "Skills disponíveis") {
		t.Fatalf("revisão com extras: skills=%d mcp=%s", len(spec.Skills), spec.MCPConfig)
	}
}

func TestRunExtrasLookup(t *testing.T) {
	var logged []string
	emit := func(k domain.RunEventKind, s string) {
		if k != domain.EventSystem {
			t.Errorf("kind %s", k)
		}
		logged = append(logged, s)
	}
	rc := sampleContext()

	// avisos do service viram eventos de sistema
	be := &extrasBackend{fakeBackend: &fakeBackend{}, extras: service.RunExtras{Skills: twoSkills(), Warnings: []string{"integração \"x\" ignorada"}}}
	h, _ := newTestHB(t, be.fakeBackend, okAdapter(), &memStub{}, Options{})
	h.svc = be
	ex := h.runExtras(context.Background(), rc, emit)
	if be.asked != 1 || len(ex.Skills) != 2 || len(logged) != 1 || !strings.Contains(logged[0], "ignorada") {
		t.Fatalf("extras: asked=%d %+v logged=%v", be.asked, ex, logged)
	}

	// falha: a run segue sem os extras, com aviso
	logged = nil
	be.err = errors.New("banco fora")
	ex = h.runExtras(context.Background(), rc, emit)
	if len(ex.Skills) != 0 || ex.MCP != nil || len(logged) != 1 || !strings.Contains(logged[0], "banco fora") {
		t.Fatalf("falha: %+v %v", ex, logged)
	}

	// revisão: nem consulta
	be.err, be.asked = nil, 0
	rv := rc
	rv.Run.Trigger = domain.TriggerReview
	rv.Review = &service.ReviewContext{ID: domain.NewID()}
	if ex = h.runExtras(context.Background(), rv, emit); be.asked != 0 || len(ex.Skills) != 0 {
		t.Fatalf("revisão consultou os extras: asked=%d", be.asked)
	}

	// backend sem a interface (fakes antigos): vazio, sem erro
	h.svc = &fakeBackend{}
	if ex = h.runExtras(context.Background(), rc, emit); len(ex.Skills) != 0 || ex.MCP != nil {
		t.Fatalf("sem interface: %+v", ex)
	}
}

func TestSkillIndexIsCompactAndOneLinePerSkill(t *testing.T) {
	idx := SkillIndex(twoSkills())
	lines := strings.Split(idx, "\n")
	var items int
	for _, l := range lines {
		if strings.HasPrefix(l, "- `") {
			items++
		}
	}
	if items != 2 || strings.Contains(idx, "rode o deploy") || strings.HasSuffix(idx, "\n") {
		t.Fatalf("índice:\n%s", idx)
	}
}

// ---- deliver_webhook ----

func TestDeliverWebhookJobOptsAndBackoff(t *testing.T) {
	if (DeliverWebhookArgs{}).Kind() != "deliver_webhook" || (DeliverWebhookArgs{}).InsertOpts().MaxAttempts != service.WebhookMaxAttempts {
		t.Fatalf("args: %+v", (DeliverWebhookArgs{}).InsertOpts())
	}
	w := &deliverWebhookWorker{}
	for _, attempt := range []int{1, 3, 6} {
		job := &river.Job[DeliverWebhookArgs]{JobRow: &rivertype.JobRow{Attempt: attempt}}
		want := service.WebhookBackoff(attempt)
		got := time.Until(w.NextRetry(job))
		if got < want-2*time.Second || got > want+2*time.Second {
			t.Errorf("tentativa %d: próxima em %v, esperado ~%v", attempt, got, want)
		}
	}
	if w.Timeout(nil) <= 0 {
		t.Error("timeout")
	}
}

func TestDeliverWebhookOutcomes(t *testing.T) {
	be := &extrasBackend{fakeBackend: &fakeBackend{}}
	h, _ := newTestHB(t, be.fakeBackend, okAdapter(), &memStub{}, Options{})
	h.svc = be
	args := DeliverWebhookArgs{DeliveryID: domain.NewID()}

	be.outcome = service.WebhookDone
	if err := h.deliverWebhook(context.Background(), args, 1, 7); err != nil {
		t.Fatalf("entregue: %v", err)
	}
	// retry: o erro volta para a fila agendar a próxima tentativa
	be.outcome, be.whErr = service.WebhookRetry, errors.New("HTTP 500")
	if err := h.deliverWebhook(context.Background(), args, 2, 7); err == nil {
		t.Fatal("retry deveria devolver erro")
	}
	be.whErr = nil
	if err := h.deliverWebhook(context.Background(), args, 3, 7); err == nil {
		t.Fatal("retry sem erro explícito ainda deve devolver erro")
	}
	// desistência definitiva: o job termina sem erro (não gasta tentativas à toa)
	be.outcome, be.whErr = service.WebhookGiveUp, errors.New("destino não permitido")
	if err := h.deliverWebhook(context.Background(), args, 4, 7); err != nil {
		t.Fatalf("give up: %v", err)
	}
	if len(be.whCalled) != 4 || be.whCalled[3] != 4 {
		t.Fatalf("tentativas: %v", be.whCalled)
	}
	// backend sem a interface
	h.svc = &fakeBackend{}
	if err := h.deliverWebhook(context.Background(), args, 1, 7); !errors.Is(err, errNotAttached) {
		t.Fatalf("sem interface: %v", err)
	}
}
