package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

const (
	memoryMinAge      = 30 * time.Second // sweep ignores rows the delivery job is probably still handling
	memoryMaxAttempts = 20
)

// GetMemoryItem loads one outbox row (any status) with company slug and workspace.
func (s *Service) GetMemoryItem(ctx context.Context, id domain.ID) (MemoryItem, error) {
	q := s.read()
	m, err := q.GetMemoryOutbox(ctx, id)
	if err != nil {
		return MemoryItem{}, mapErr(err, "get memory item")
	}
	item := MemoryItem{ID: m.ID, CompanyID: m.CompanyID, CompanySlug: m.CompanySlug, Kind: m.Kind,
		Payload: m.Payload, EventID: m.EventID, Attempts: int(m.Attempts)}
	if m.WorkspaceID != nil {
		ws, err := q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: m.CompanyID, ID: *m.WorkspaceID})
		if err == nil {
			w := workspaceFromRow(ws)
			item.Workspace = &w
		} else if !isNoRows(err) {
			return MemoryItem{}, fmt.Errorf("get memory workspace: %w", err)
		}
	}
	return item, nil
}

// MarkMemorySent / MarkMemoryFailed update delivery state (attempts++ on failure).
func (s *Service) MarkMemorySent(ctx context.Context, id domain.ID) error {
	if err := s.read().MarkMemorySent(ctx, store.MarkMemorySentParams{ID: id, Now: ptr(s.Now().UTC())}); err != nil {
		return fmt.Errorf("mark memory sent: %w", err)
	}
	return nil
}

func (s *Service) MarkMemoryFailed(ctx context.Context, id domain.ID, errMsg string) error {
	if err := s.read().MarkMemoryFailed(ctx, store.MarkMemoryFailedParams{ID: id, LastError: errMsg, MaxAttempts: memoryMaxAttempts}); err != nil {
		return fmt.Errorf("mark memory failed: %w", err)
	}
	return nil
}

// PendingMemory lists pending outbox ids older than memoryMinAge (re-enqueue sweep).
func (s *Service) PendingMemory(ctx context.Context, limit int) ([]domain.ID, error) {
	ids, err := s.read().ListPendingMemory(ctx, store.ListPendingMemoryParams{
		OlderThan: s.Now().UTC().Add(-memoryMinAge), Lim: int32(clamp(limit, 100, 1000))})
	if err != nil {
		return nil, fmt.Errorf("pending memory: %w", err)
	}
	return ids, nil
}
