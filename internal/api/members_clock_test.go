package api

import (
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

type clockSvc struct {
	backend
	now time.Time
}

func (c clockSvc) Clock() time.Time { return c.now }

// "expired" do convite segue o relógio do service, não o do sistema.
func TestInviteOutUsesServiceClock(t *testing.T) {
	exp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := domain.Invite{ID: domain.NewID(), Email: "a@x.com", Role: domain.MemberViewer, ExpiresAt: exp}
	before := (&server{svc: clockSvc{now: exp.Add(-time.Hour)}}).inviteOut(inv)
	after := (&server{svc: clockSvc{now: exp.Add(time.Hour)}}).inviteOut(inv)
	if before.Expired || !after.Expired {
		t.Fatalf("before=%v after=%v", before.Expired, after.Expired)
	}
}
