package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// sendFake responde só as operações públicas do envio: um id conhecido abre, os outros não existem.
type sendFake struct {
	*fakeSvc
}

const sendFakeID = "AAAAAAAAAAAAAAAAAAAAAA"

func (f *sendFake) CreateSend(context.Context, domain.Actor, domain.ID, service.CreateSendInput) (service.SendView, error) {
	return service.SendView{}, nil
}

func (f *sendFake) PublicSendInfo(_ context.Context, id string) (service.PublicSendView, error) {
	if id != sendFakeID {
		return service.PublicSendView{}, domain.ErrNotFound
	}
	return service.PublicSendView{Kind: domain.SendText, Openable: true}, nil
}

func (f *sendFake) OpenPublicSend(_ context.Context, id, _ string) (service.OpenedSendView, error) {
	if id != sendFakeID {
		return service.OpenedSendView{}, service.ErrSendGone
	}
	return service.OpenedSendView{Kind: domain.SendText, Ciphertext: "c2lmcmFkbw"}, nil
}

// A página pública do envio fala sem credencial (quem abre não tem conta), com resposta sem cache; criar envio
// continua exigindo login quando a API exige.
func TestEnvioPublicoNaoPedeCredencialESemCache(t *testing.T) {
	ts, s := newTestServer(t, &fakeSvc{}, func(d *Deps) { d.Authenticated = true })
	s.svc = &sendFake{fakeSvc: &fakeSvc{}}
	json := map[string]string{"content-type": "application/json"}

	resp, _ := do(t, "GET", ts.URL+"/api/v1/s/"+sendFakeID, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET do envio sem credencial = %d, quer 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, quer no-store", cc)
	}
	resp, body := do(t, "POST", ts.URL+"/api/v1/s/"+sendFakeID+"/open", `{}`, json)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("abrir sem credencial = %d %s, quer 200", resp.StatusCode, body)
	}
	resp, _ = do(t, "POST", ts.URL+"/api/v1/s/BBBBBBBBBBBBBBBBBBBBBB/open", `{}`, json)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("envio inexistente = %d, quer 404", resp.StatusCode)
	}
	resp, _ = do(t, "POST", ts.URL+"/api/v1/companies/acme/sends",
		`{"public_id":"`+sendFakeID+`","kind":"text","size_bytes":0,"ciphertext":"c2lmcmFkbw","expires_in_seconds":3600}`, json)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("criar envio sem credencial = %d, quer 401", resp.StatusCode)
	}
}
