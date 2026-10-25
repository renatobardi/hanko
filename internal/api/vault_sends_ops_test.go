package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Operações de gestão do envio (fatia 4, #59): listar, ver, subir o conteúdo e apagar. O fake só conhece um envio de
// arquivo (sendOpsID); os demais não existem. Ele guarda o que recebeu, para o teste conferir.
type sendOpsFake struct {
	*fakeSvc
	created  service.CreateSendInput
	uploaded []byte
	deleted  bool
}

const sendOpsID = "00000000-0000-0000-0000-0000000000a1"

// sendOpsView é um envio de arquivo de 10 bytes em claro, do humano.
func sendOpsView() service.SendView {
	id, _ := domain.ParseID(sendOpsID)
	return service.SendView{SecretSend: domain.SecretSend{ID: id, PublicID: sendFakeID, Kind: domain.SendFile, SizeBytes: 10,
		ExpiresAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), CreatedBy: domain.HumanActor}}
}

// A página pública não é tema destes testes: o fake responde não encontrado, para que o backend exista (sendBackend).
func (f *sendOpsFake) PublicSendInfo(context.Context, string) (service.PublicSendView, error) {
	return service.PublicSendView{}, domain.ErrNotFound
}

func (f *sendOpsFake) OpenPublicSend(context.Context, string, string) (service.OpenedSendView, error) {
	return service.OpenedSendView{}, domain.ErrNotFound
}

func (f *sendOpsFake) CreateSend(_ context.Context, _ domain.Actor, _ domain.ID, in service.CreateSendInput) (service.SendView, error) {
	f.created = in
	return sendOpsView(), nil
}

func (f *sendOpsFake) ListSends(context.Context, domain.Actor, domain.ID, int) ([]service.SendView, error) {
	return []service.SendView{sendOpsView()}, nil
}

func (f *sendOpsFake) GetSend(_ context.Context, _ domain.Actor, _, id domain.ID) (service.SendView, error) {
	if id.String() != sendOpsID {
		return service.SendView{}, domain.ErrNotFound
	}
	return sendOpsView(), nil
}

func (f *sendOpsFake) DeleteSend(_ context.Context, _ domain.Actor, _, id domain.ID) error {
	if id.String() != sendOpsID {
		return domain.ErrNotFound
	}
	f.deleted = true
	return nil
}

func (f *sendOpsFake) UploadSendContent(_ context.Context, _ domain.Actor, _, id domain.ID, sealed []byte) (service.SendView, error) {
	if id.String() != sendOpsID {
		return service.SendView{}, domain.ErrNotFound
	}
	if len(sealed) != 10+28 { // o arquivo declarado mais a cifra; o fake recusa o resto como o service
		return service.SendView{}, domain.Invalid("content", "tamanho do conteúdo cifrado diferente do declarado")
	}
	f.uploaded = sealed
	return sendOpsView(), nil
}

// As operações de gestão ligam o service: listar, ver, subir o conteúdo e apagar respondem o que o service devolve, com
// o status certo para cada resultado (201 ao criar, 204 ao apagar, 404 e 422 para erro).
func TestEnvioGestaoLigaOService(t *testing.T) {
	ts, s := newTestServer(t, &fakeSvc{}, nil)
	f := &sendOpsFake{fakeSvc: &fakeSvc{companies: []domain.Company{{Slug: "acme"}}}}
	s.svc = f
	base := ts.URL + "/api/v1/companies/acme/sends"
	json := map[string]string{"content-type": "application/json"}
	octet := map[string]string{"content-type": "application/octet-stream"}

	resp, body := do(t, "POST", base, `{"public_id":"`+sendFakeID+`","kind":"file","size_bytes":10,"expires_in_seconds":3600}`, json)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar envio de arquivo = %d %s, quer 201", resp.StatusCode, body)
	}
	if f.created.Kind != domain.SendFile || f.created.SizeBytes != 10 || f.created.Ciphertext != "" {
		t.Fatalf("criação recebeu %+v; quer arquivo de 10 bytes sem texto cifrado", f.created)
	}

	resp, body = do(t, "GET", base, "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, sendFakeID) || !strings.Contains(body, `"items"`) {
		t.Fatalf("listar = %d %s, quer 200 com o envio na lista", resp.StatusCode, body)
	}

	resp, body = do(t, "GET", base+"/"+sendOpsID, "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"size_bytes":10`) {
		t.Fatalf("ver = %d %s, quer 200 com os metadados", resp.StatusCode, body)
	}
	resp, _ = do(t, "GET", base+"/00000000-0000-0000-0000-0000000000b2", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ver envio inexistente = %d, quer 404", resp.StatusCode)
	}

	resp, body = do(t, "PUT", base+"/"+sendOpsID+"/content", strings.Repeat("x", 38), octet)
	if resp.StatusCode != http.StatusOK || len(f.uploaded) != 38 {
		t.Fatalf("subir conteúdo = %d %s (guardado %d bytes); quer 200 e 38 bytes", resp.StatusCode, body, len(f.uploaded))
	}
	resp, _ = do(t, "PUT", base+"/"+sendOpsID+"/content", strings.Repeat("x", 10), octet)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("subir conteúdo de tamanho errado = %d, quer 422", resp.StatusCode)
	}
	resp, _ = do(t, "PUT", base+"/00000000-0000-0000-0000-0000000000b2/content", strings.Repeat("x", 38), octet)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("subir conteúdo de envio inexistente = %d, quer 404", resp.StatusCode)
	}

	resp, _ = do(t, "DELETE", base+"/"+sendOpsID, "", nil)
	if resp.StatusCode != http.StatusNoContent || !f.deleted {
		t.Fatalf("apagar = %d (apagado %v); quer 204 e o service chamado", resp.StatusCode, f.deleted)
	}
	resp, _ = do(t, "DELETE", base+"/00000000-0000-0000-0000-0000000000b2", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("apagar envio inexistente = %d, quer 404", resp.StatusCode)
	}
}

// Sem o backend de envio, as operações respondem 501 com o motivo (o servidor de teste não tem o service de envio).
func TestEnvioGestaoSemBackendRespode501(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	resp, body := do(t, "GET", ts.URL+"/api/v1/companies/acme/sends", "", nil)
	if resp.StatusCode != http.StatusNotImplemented || !strings.Contains(body, "envios indisponíveis") {
		t.Fatalf("listar sem backend = %d %s, quer 501 com o motivo", resp.StatusCode, body)
	}
}
