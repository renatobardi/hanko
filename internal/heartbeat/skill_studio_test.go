package heartbeat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// studioBackend é o fakeBackend com o estúdio e as fontes de skills (ADR-0035).
type studioBackend struct {
	*extrasBackend
	spec      service.SkillTestSpec
	specErr   error
	specAsked int
	due       []service.SkillSourceRef
	synced    []domain.ID
	syncErr   error
	stale     int
}

func (b *studioBackend) SkillTestRunSpec(context.Context, domain.ID, domain.ID) (service.SkillTestSpec, error) {
	b.specAsked++
	return b.spec, b.specErr
}

func (b *studioBackend) DueSkillSources(context.Context, time.Time, int) ([]service.SkillSourceRef, error) {
	return b.due, nil
}

func (b *studioBackend) SyncDueSkillSource(_ context.Context, ref service.SkillSourceRef) (service.SkillSyncResult, error) {
	b.synced = append(b.synced, ref.ID)
	return service.SkillSyncResult{}, b.syncErr
}

func (b *studioBackend) ResolveStaleSkillTests(context.Context) (int, error) {
	b.stale++
	return 0, nil
}

func skillTestContext() service.RunContext {
	rc := sampleContext()
	rc.Run.Trigger = domain.TriggerSkillTest
	rc.Workspace = &domain.Workspace{Slug: "acme", Path: "/nao/deve/ser/usado"}
	return rc
}

func testSpec() service.SkillTestSpec {
	return service.SkillTestSpec{TestID: domain.NewID(), Input: "Faça o deploy do serviço X",
		Skill: domain.Skill{Slug: "deploy", Name: "Deploy", Description: "Como publicar", Body: "corpo v1", Version: 1, Pinned: true}}
}

// A run de teste recebe só a skill testada (e não as habilitadas da persona), a entrada como prompt, nenhum MCP
// da empresa nem memória, e roda num diretório temporário.
func TestSkillTestRunGetsOnlyTheSkill(t *testing.T) {
	be := &studioBackend{extrasBackend: &extrasBackend{fakeBackend: &fakeBackend{rc: skillTestContext()},
		extras: service.RunExtras{Skills: twoSkills(), MCP: companyMCP()}}, spec: testSpec()}
	ad := okAdapter()
	mem := &memStub{handoff: "H", briefing: "B"}
	h, _ := newTestHB(t, be.fakeBackend, ad, mem, Options{PublicURL: "http://h:7420"})
	h.svc = be
	if err := h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID}); err != nil {
		t.Fatal(err)
	}
	if be.asked != 0 || be.specAsked != 1 {
		t.Fatalf("extras da persona consultados=%d spec=%d", be.asked, be.specAsked)
	}
	s := ad.spec
	if s.Prompt != "Faça o deploy do serviço X" || !strings.Contains(s.System, "Teste de skill") || !strings.Contains(s.System, "Escreva código limpo.") {
		t.Fatalf("prompt=%q system=%q", s.Prompt, s.System)
	}
	if !strings.Contains(s.System, "`deploy`") || strings.Contains(s.System, "revisar-pr") {
		t.Fatalf("índice de skills: %s", s.System)
	}
	srv := mcpServers(t, s)
	if len(srv) != 1 || srv["hanko"] == nil {
		t.Fatalf("mcpServers numa run de teste: %s", s.MCPConfig)
	}
	if s.Dir == "/nao/deve/ser/usado" || s.Dir == "" {
		t.Fatalf("dir = %q", s.Dir)
	}
	if be.handoff != "" || len(mem.finalized) != 0 || strings.Contains(s.Prompt, "B") && strings.Contains(s.Prompt, "Briefing") {
		t.Fatalf("memória numa run de teste: handoff=%q finalized=%v", be.handoff, mem.finalized)
	}
	if len(be.finished) != 1 || be.finished[0].Status != domain.RunDone {
		t.Fatalf("finished=%+v", be.finished)
	}

	// claude carrega a skill nativamente: só ela
	rc := skillTestContext()
	rc.Persona.Agent = "claude"
	spec, cleanup, err := h.buildSpec(context.Background(), rc, "", "", memory.Scope{}, service.RunExtras{Skills: []domain.Skill{testSpec().Skill}, SkillTest: ptrSpec(testSpec())},
		func(domain.RunEventKind, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(spec.Skills) != 1 || spec.Skills[0].Body != "corpo v1" {
		t.Fatalf("claude: %+v", spec.Skills)
	}
}

func ptrSpec(s service.SkillTestSpec) *service.SkillTestSpec { return &s }

// Sem a especificação do teste a run falha com o motivo, em vez de rodar com as skills da persona.
func TestSkillTestRunWithoutSpecFails(t *testing.T) {
	be := &studioBackend{extrasBackend: &extrasBackend{fakeBackend: &fakeBackend{rc: skillTestContext()}}, specErr: domain.ErrNotFound}
	ad := okAdapter()
	h, _ := newTestHB(t, be.fakeBackend, ad, &memStub{}, Options{})
	h.svc = be
	if err := h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID}); err != nil {
		t.Fatal(err)
	}
	if len(be.finished) != 1 || be.finished[0].Status != domain.RunFailed || !strings.Contains(be.finished[0].Result.Error, "teste de skill") {
		t.Fatalf("finished=%+v", be.finished)
	}
}

func TestSkillTick(t *testing.T) {
	a, b := domain.NewID(), domain.NewID()
	be := &studioBackend{extrasBackend: &extrasBackend{fakeBackend: &fakeBackend{}},
		due: []service.SkillSourceRef{{ID: a}, {ID: b}}}
	h, _ := newTestHB(t, be.fakeBackend, okAdapter(), &memStub{}, Options{})
	h.svc = be
	if err := h.skillTick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if be.stale != 1 || len(be.synced) != 2 {
		t.Fatalf("stale=%d synced=%v", be.stale, be.synced)
	}
	// uma fonte ocupada (outra sincronização em andamento) não é erro do tick
	be.syncErr = domain.ErrConflict
	if err := h.skillTick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	be.syncErr = errors.New("banco fora")
	if err := h.skillTick(context.Background(), time.Now()); err == nil {
		t.Fatal("erro do service deveria subir")
	}
	// backend sem a interface: nada
	h.svc = &fakeBackend{}
	if err := h.skillTick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSkillTestPrompt(t *testing.T) {
	rc := skillTestContext()
	rc.Persona.Instructions = ""
	sys, prompt := BuildSkillTestPrompt(rc, testSpec())
	if !strings.HasPrefix(sys, "## Teste de skill") || !strings.Contains(sys, "versão 1") || prompt != testSpec().Input {
		t.Fatalf("%q %q", sys, prompt)
	}
}
