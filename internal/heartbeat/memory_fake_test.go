package heartbeat

import (
	"context"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Stubs da consolidação de memória no fakeBackend: os testes de unidade do heartbeat não a exercitam
// (ela é coberta pelos testes de integração e de service).

func (f *fakeBackend) DueMemorySettings(context.Context, time.Time) ([]domain.MemorySettings, error) {
	return nil, nil
}

func (f *fakeBackend) FireMemoryConsolidation(context.Context, domain.ID, time.Time) ([]domain.MemoryConsolidation, error) {
	return nil, nil
}

func (f *fakeBackend) ResolveStaleConsolidations(context.Context) (int, error) { return 0, nil }

func (f *fakeBackend) BeginMemoryProposalApply(context.Context, domain.ID, domain.ID) (service.MemoryApplyItem, error) {
	return service.MemoryApplyItem{}, domain.ErrNotFound
}

func (f *fakeBackend) MarkMemoryProposalApplied(context.Context, domain.ID, domain.ID) error {
	return nil
}

func (f *fakeBackend) MarkMemoryProposalFailed(context.Context, domain.ID, domain.ID, string) (time.Duration, error) {
	return time.Minute, nil
}

func (f *fakeBackend) DueMemoryProposals(context.Context, int) ([]service.MemoryProposalRef, error) {
	return nil, nil
}
