package auth

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// The token of a chat run carries the conversation and no task, and stays valid only while the run is active.
func TestIssueForConversation(t *testing.T) {
	rt := RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	cid, pid, rid, cvid := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	tok, err := rt.IssueForConversation(cid, pid, rid, cvid)
	if err != nil {
		t.Fatal(err)
	}
	c, err := rt.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.ConversationID != cvid.String() || c.TaskID != "" || c.RunID != rid.String() || c.CompanyID != cid.String() || c.PersonaID != pid.String() {
		t.Fatalf("claims = %+v", c)
	}
	if a, err := c.Actor(); err != nil || a.Type != domain.ActorPersona || a.ID == nil || *a.ID != pid {
		t.Fatalf("actor = %+v %v", a, err)
	}

	status := func(st domain.RunStatus) RunStatusFunc {
		return func(context.Context, domain.ID, domain.ID) (domain.RunStatus, error) { return st, nil }
	}
	if _, err := rt.VerifyActive(context.Background(), tok, status(domain.RunRunning)); err != nil {
		t.Fatalf("running run: %v", err)
	}
	if _, err := rt.VerifyActive(context.Background(), tok, status(domain.RunDone)); err == nil {
		t.Fatal("a finished chat run must not keep a valid token")
	}

	// an ordinary run token has no conversation
	plain, err := rt.Issue(cid, pid, rid, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if c, err := rt.Verify(plain); err != nil || c.ConversationID != "" || c.TaskID == "" {
		t.Fatalf("task token = %+v %v", c, err)
	}
}
