package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

func TestVerifyActive(t *testing.T) {
	rt := RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	cid, pid, rid, tid := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	tok, err := rt.Issue(cid, pid, rid, tid)
	if err != nil {
		t.Fatal(err)
	}
	status := func(st domain.RunStatus, err error) RunStatusFunc {
		return func(_ context.Context, gc, gr domain.ID) (domain.RunStatus, error) {
			if gc != cid || gr != rid {
				t.Errorf("consulta com ids errados: %v %v", gc, gr)
			}
			return st, err
		}
	}
	dbErr := errors.New("db fora")
	tests := []struct {
		name    string
		tok     string
		f       RunStatusFunc
		wantBad bool // errors.Is(ErrBadToken)
		wantErr bool
	}{
		{"queued", tok, status(domain.RunQueued, nil), false, false},
		{"running", tok, status(domain.RunRunning, nil), false, false},
		{"done", tok, status(domain.RunDone, nil), true, true},
		{"cancelled", tok, status(domain.RunCancelled, nil), true, true},
		{"inexistente", tok, status("", domain.ErrNotFound), true, true},
		{"banco fora não é token ruim", tok, status("", dbErr), false, true},
		{"lixo", "lixo", status(domain.RunRunning, nil), true, true},
		{"sem consulta", tok, nil, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := rt.VerifyActive(context.Background(), tc.tok, tc.f)
			if (err != nil) != tc.wantErr || errors.Is(err, ErrBadToken) != tc.wantBad {
				t.Fatalf("err = %v", err)
			}
			if err == nil && c.RunID != rid.String() {
				t.Errorf("claims = %+v", c)
			}
		})
	}
}
