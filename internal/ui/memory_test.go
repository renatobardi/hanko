package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// memBE acrescenta ao fake da UI a fatia de memória (ui.MemoryBackend).
type memBE struct {
	*uitest.Fake
	status    service.MemoryStatus
	updated   []service.UpdateMemorySettingsInput
	scope     []string
	retried   []domain.ID
	cons      []service.MemoryConsolidationView
	proposals []service.MemoryProposalView
	err       error
}

func (m *memBE) GetMemoryStatus(context.Context, domain.ID) (service.MemoryStatus, error) {
	return m.status, nil
}

func (m *memBE) UpdateMemorySettings(_ context.Context, _ domain.Actor, _ domain.ID, in service.UpdateMemorySettingsInput) (service.MemoryStatus, error) {
	m.updated = append(m.updated, in)
	return m.status, m.err
}

func (m *memBE) ConsolidateMemoryNow(_ context.Context, _ domain.Actor, _ domain.ID, ws string) ([]domain.MemoryConsolidation, error) {
	m.scope = append(m.scope, ws)
	return nil, m.err
}

func (m *memBE) ListMemoryConsolidations(context.Context, domain.ID, int) ([]service.MemoryConsolidationView, error) {
	return m.cons, nil
}

func (m *memBE) ListMemoryProposals(context.Context, domain.ID, []domain.MemoryProposalStatus, int) ([]service.MemoryProposalView, error) {
	return m.proposals, nil
}

func (m *memBE) RetryMemoryProposal(_ context.Context, _ domain.Actor, _, id domain.ID) (service.MemoryProposalView, error) {
	m.retried = append(m.retried, id)
	return service.MemoryProposalView{}, m.err
}

func (m *memBE) ListWorkspaces(context.Context, domain.ID) ([]domain.Workspace, error) {
	return []domain.Workspace{{ID: domain.NewID(), Slug: "site", Name: "Site"}}, nil
}

func newMemUI(t *testing.T, be *memBE) *env {
	t.Helper()
	b := events.NewBroker()
	s := &server{be: be, broker: b, version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond}
	s.log = testLogger(t)
	mux := http.NewServeMux()
	s.routes(mux)
	return &env{f: be.Fake, broker: b, h: mux}
}

func TestMemoriaSecaoNasConfiguracoes(t *testing.T) {
	fake := uitest.Sample()
	curator := domain.NewID()
	next := uitest.Now().Add(3 * time.Hour)
	failedID := domain.NewID()
	gate := domain.NewID()
	be := &memBE{Fake: fake,
		status: service.MemoryStatus{Provider: "ai-memory", Available: true, PendingProposals: 2,
			Settings: domain.MemorySettings{Enabled: true, IntervalHours: 12, CuratorPersonaID: &curator, NextFireAt: &next}},
		cons: []service.MemoryConsolidationView{{MemoryConsolidation: domain.MemoryConsolidation{ID: domain.NewID(), WorkspaceSlug: "site",
			Origin: domain.MemoryScheduled, Status: domain.ConsolidationDone, CreatedAt: uitest.Now().Add(-time.Hour)}, TaskKey: "HNK-9", Proposals: 2}},
		proposals: []service.MemoryProposalView{{MemoryProposal: domain.MemoryProposal{ID: failedID, WorkspaceSlug: "site", Kind: domain.ProposalDelete,
			Status: domain.ProposalFailed, Targets: []string{"wiki/velha"}, LastError: "ai-memory fora do ar", GateID: &gate, Diff: "-linha\n",
			CreatedAt: uitest.Now().Add(-time.Hour)}}},
	}
	e := newMemUI(t, be)
	rec := e.get(t, "/c/hanko/configuracoes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="memoria"`, "Consolidação periódica ligada", `action="/c/hanko/configuracoes/memoria"`,
		`action="/c/hanko/configuracoes/memoria/consolidar"`, "Todos os workspaces", "HNK-9", "wiki/velha", "ai-memory fora do ar",
		"Falhou ao aplicar", "/gates/"+gate.String(), "/configuracoes/memoria/propostas/"+failedID.String()+"/repetir", "2 esperando decisão",
		"tipo=memoria")

	// provedor none: mensagem clara e sem formulário
	be.status = service.MemoryStatus{Provider: "none"}
	rec = e.get(t, "/c/hanko/configuracoes")
	wantContains(t, rec, "Memória desligada", "HANKO_MEMORY_PROVIDER=none")
	wantNotContains(t, rec, `action="/c/hanko/configuracoes/memoria"`, "Consolidar agora")

	// backend sem consolidação: a seção some
	e2 := newEnv(t, uitest.Sample())
	wantNotContains(t, e2.get(t, "/c/hanko/configuracoes"), `id="memoria"`)
}

func TestMemoriaAcoesDaSecao(t *testing.T) {
	fake := uitest.Sample()
	curator := domain.NewID()
	be := &memBE{Fake: fake, status: service.MemoryStatus{Provider: "ai-memory", Available: true,
		Settings: domain.MemorySettings{IntervalHours: 24}}}
	e := newMemUI(t, be)

	rec := e.post(t, "/c/hanko/configuracoes/memoria", url.Values{"enabled": {"1"}, "interval_hours": {"6"}, "curator": {curator.String()}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/configuracoes?mem=salvo#memoria" {
		t.Errorf("redirect = %q", loc)
	}
	if len(be.updated) != 1 || be.updated[0].Enabled == nil || !*be.updated[0].Enabled || *be.updated[0].IntervalHours != 6 ||
		be.updated[0].CuratorPersonaID == nil || *be.updated[0].CuratorPersonaID != curator || be.updated[0].ClearCurator {
		t.Fatalf("entrada do service: %+v", be.updated)
	}
	// sem curador escolhido = limpar; checkbox desmarcada = desligar
	e.post(t, "/c/hanko/configuracoes/memoria", url.Values{"interval_hours": {"6"}, "curator": {""}}, false)
	if got := be.updated[1]; got.Enabled == nil || *got.Enabled || !got.ClearCurator {
		t.Fatalf("limpar curador: %+v", got)
	}
	// entrada ruim volta com erro na seção
	rec = e.post(t, "/c/hanko/configuracoes/memoria", url.Values{"interval_hours": {"abc"}, "curator": {""}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, `id="memoria"`, "informe um número de horas")

	rec = e.post(t, "/c/hanko/configuracoes/memoria/consolidar", url.Values{"workspace": {"site"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(be.scope) != 1 || be.scope[0] != "site" {
		t.Fatalf("workspace da consolidação: %v", be.scope)
	}
	be.err = domain.Invalid("enabled", "a memória está desligada (HANKO_MEMORY_PROVIDER=none); ligue o ai-memory para consolidar")
	rec = e.post(t, "/c/hanko/configuracoes/memoria/consolidar", url.Values{}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "HANKO_MEMORY_PROVIDER=none")

	be.err = nil
	pid := domain.NewID()
	rec = e.post(t, "/c/hanko/configuracoes/memoria/propostas/"+pid.String()+"/repetir", url.Values{}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(be.retried) != 1 || be.retried[0] != pid {
		t.Fatalf("retry: %v", be.retried)
	}
	wantStatus(t, e.post(t, "/c/hanko/configuracoes/memoria/propostas/lixo/repetir", url.Values{}, false), http.StatusNotFound)
}
