package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

// accessRevoker simula o service depois que o papel do chamador caiu: GetRun passa a devolver o erro de
// autorização. Só o GetRun falha de propósito: o re-check do tick não pode depender de a leitura de
// eventos também falhar (um tick sem eventos novos não a recusaria).
type accessRevoker struct {
	runSource
	err atomic.Value // error
}

func (a *accessRevoker) revoke(err error) { a.err.Store(&err) }
func (a *accessRevoker) failure() error {
	if p, ok := a.err.Load().(*error); ok {
		return *p
	}
	return nil
}
func (a *accessRevoker) GetRun(ctx context.Context, c, r domain.ID) (service.RunView, error) {
	if err := a.failure(); err != nil {
		return service.RunView{}, err
	}
	return a.runSource.GetRun(ctx, c, r)
}

// Quem perde o papel com o stream aberto deixa de receber: o tick re-checa o acesso e encerra.
func TestSSEEncerraQuandoOAcessoCai(t *testing.T) {
	for name, lost := range map[string]error{"papel insuficiente": domain.ErrForbidden, "sem vínculo": domain.ErrNotFound} {
		t.Run(name, func(t *testing.T) {
			f := &fakeSvc{runs: map[domain.ID]service.RunView{}, events: map[domain.ID][]domain.RunEvent{}}
			c := seedCompany(f)
			rid := domain.NewID()
			f.runs[rid] = service.RunView{Run: domain.Run{ID: rid, CompanyID: c.ID, Status: domain.RunRunning}}
			src := &accessRevoker{runSource: f}

			rec := newFlushRecorder()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				streamRun(ctx, rec, src, events.NewBroker(), discardLogger(), c.ID, rid, 0, 20*time.Millisecond, nil)
			}()
			ch := readFrames(t, rec.reader())
			if fr := next(t, ch); fr.Event != "ping" {
				t.Fatalf("esperava ping com acesso válido, veio %+v", fr)
			}
			src.revoke(lost)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("o stream continuou aberto depois que o acesso caiu")
			}
		})
	}
}

// Sessão encerrada, senha redefinida ou API key revogada com o stream aberto: o tick reverifica a credencial
// e encerra, mesmo que o papel na empresa continue valendo.
func TestSSEEncerraQuandoACredencialCai(t *testing.T) {
	f := &fakeSvc{runs: map[domain.ID]service.RunView{}, events: map[domain.ID][]domain.RunEvent{}}
	c := seedCompany(f)
	rid := domain.NewID()
	f.runs[rid] = service.RunView{Run: domain.Run{ID: rid, CompanyID: c.ID, Status: domain.RunRunning}}

	var revoked atomic.Bool
	credential := func(context.Context) error {
		if revoked.Load() {
			return domain.ErrUnauthenticated
		}
		return nil
	}
	rec := newFlushRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		streamRun(ctx, rec, f, events.NewBroker(), discardLogger(), c.ID, rid, 0, 20*time.Millisecond, credential)
	}()
	ch := readFrames(t, rec.reader())
	if fr := next(t, ch); fr.Event != "ping" {
		t.Fatalf("esperava ping com a credencial válida, veio %+v", fr)
	}
	revoked.Store(true)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("o stream continuou aberto depois que a credencial caiu")
	}
}
