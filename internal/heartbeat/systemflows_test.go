package heartbeat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func (f *fakeBackend) DueSystemFlows(context.Context, time.Time) ([]domain.SystemFlow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sysDue, nil
}

func (f *fakeBackend) FireSystemFlow(_ context.Context, key string, _ time.Time) (service.SystemFlowResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sysFired = append(f.sysFired, key)
	if err := f.sysErr[key]; err != nil {
		return service.SystemFlowResult{}, err
	}
	return f.sysRes[key], nil
}

func TestSystemFlowTickFiresDueFlows(t *testing.T) {
	at := time.Date(2026, 3, 10, 3, 0, 0, 0, time.UTC)
	be := &fakeBackend{
		sysDue: []domain.SystemFlow{{Key: "backup", NextFireAt: &at}, {Key: "sem-proxima"}},
		sysRes: map[string]service.SystemFlowResult{"backup": {Backup: &domain.InstanceBackup{Status: domain.BackupOK}}},
	}
	h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
	if err := h.systemFlowTick(context.Background(), at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(be.sysFired) != 1 || be.sysFired[0] != "backup" {
		t.Fatalf("disparados = %v", be.sysFired)
	}
}

func TestSystemFlowTickReportsFailuresAndSkips(t *testing.T) {
	at := time.Date(2026, 3, 10, 3, 0, 0, 0, time.UTC)
	be := &fakeBackend{
		sysDue: []domain.SystemFlow{{Key: "backup", NextFireAt: &at}},
		sysErr: map[string]error{"backup": errors.New("db fora")},
	}
	h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
	if err := h.systemFlowTick(context.Background(), at); err == nil || !strings.Contains(err.Error(), "system flow backup") {
		t.Fatalf("err = %v", err)
	}
	// pulado e backup que falhou não são erros do job (ficam registrados no histórico)
	be.sysErr = nil
	be.sysRes = map[string]service.SystemFlowResult{"backup": {Skipped: true, SkipReason: "running"}}
	if err := h.systemFlowTick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	be.sysRes = map[string]service.SystemFlowResult{"backup": {Backup: &domain.InstanceBackup{Status: domain.BackupFailed, Error: "pg_dump"}}}
	if err := h.systemFlowTick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
}

func TestSystemFlowTickWithoutBackendAndRegistration(t *testing.T) {
	h, _ := New(lazyPool(t), Options{})
	if err := h.systemFlowTick(context.Background(), time.Now()); !errors.Is(err, errNotAttached) {
		t.Fatalf("err = %v", err)
	}
	if k := (SystemFlowTickArgs{}).Kind(); k != "system_flow_tick" {
		t.Fatalf("kind = %q", k)
	}
	w := &systemFlowTickWorker{}
	if w.Timeout(nil) < time.Hour {
		t.Fatalf("o backup agendado precisa de teto maior que o padrão do River: %v", w.Timeout(nil))
	}
}
