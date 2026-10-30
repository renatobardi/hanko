package api

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// externalErr, quando definido, é o erro que os métodos de agente e host devolvem (os testes de status o usam).
var externalErr error

// Os oito métodos de agente e host do fake devolvem "não encontrado": a rota chega ao handler e o erro é mapeado (404),
// sem 501 (contrato) e sem 500 (método ausente).
func (f *fakeSvc) ListExternalAgents(context.Context, domain.ID, bool, int32, int32) ([]service.ExternalAgentView, error) {
	return nil, externalFail()
}
func (f *fakeSvc) GetExternalAgent(context.Context, domain.ID, domain.ID) (service.ExternalAgentView, error) {
	return service.ExternalAgentView{}, externalFail()
}
func (f *fakeSvc) UpdateExternalAgent(context.Context, domain.Actor, domain.ID, domain.ID, service.ExternalAgentPatch) (service.ExternalAgentView, error) {
	return service.ExternalAgentView{}, externalFail()
}
func (f *fakeSvc) MergeExternalAgent(context.Context, domain.Actor, domain.ID, domain.ID, domain.ID) (service.ExternalAgentView, error) {
	return service.ExternalAgentView{}, externalFail()
}
func (f *fakeSvc) ListExternalHosts(context.Context, domain.ID, bool, int32, int32) ([]service.ExternalHostView, error) {
	return nil, externalFail()
}
func (f *fakeSvc) GetExternalHost(context.Context, domain.ID, domain.ID) (service.ExternalHostView, error) {
	return service.ExternalHostView{}, externalFail()
}
func (f *fakeSvc) UpdateExternalHost(context.Context, domain.Actor, domain.ID, domain.ID, service.ExternalHostPatch) (service.ExternalHostView, error) {
	return service.ExternalHostView{}, externalFail()
}
func (f *fakeSvc) MergeExternalHost(context.Context, domain.Actor, domain.ID, domain.ID, domain.ID) (service.ExternalHostView, error) {
	return service.ExternalHostView{}, externalFail()
}

// As oito rotas de agente e host estão ligadas: chegam ao handler (não 501) e não estouram (não 500).
func TestExternalEntitiesRoutesAreWired(t *testing.T) {
	f := &fakeSvc{}
	seedCompany(f)
	ts, _ := newTestServer(t, f, nil)
	const id = "00000000-0000-0000-0000-000000000001"
	const base = "/api/v1/companies/acme"
	for _, c := range []struct{ method, path, body string }{
		{"GET", base + "/external-agents", ""},
		{"GET", base + "/external-agents/" + id, ""},
		{"PATCH", base + "/external-agents/" + id, `{"name":"x"}`},
		{"POST", base + "/external-agents/" + id + "/merge", `{"into":"` + id + `"}`},
		{"GET", base + "/external-hosts", ""},
		{"GET", base + "/external-hosts/" + id, ""},
		{"PATCH", base + "/external-hosts/" + id, `{"always_on":true}`},
		{"POST", base + "/external-hosts/" + id + "/merge", `{"into":"` + id + `"}`},
	} {
		resp, body := do(t, c.method, ts.URL+c.path, c.body, nil)
		if resp.StatusCode == 501 || resp.StatusCode == 500 {
			t.Errorf("%s %s: %d %s; a rota devia chegar ao handler", c.method, c.path, resp.StatusCode, body)
		}
	}
}

func externalFail() error {
	if externalErr != nil {
		return externalErr
	}
	return domain.ErrNotFound
}

// Os erros do serviço viram o status certo: conflito 409, permissão 403, não encontrado 404; uuid inválido em `into` 422.
func TestExternalEntitiesErrorStatuses(t *testing.T) {
	f := &fakeSvc{}
	seedCompany(f)
	ts, _ := newTestServer(t, f, nil)
	const id = "00000000-0000-0000-0000-000000000001"
	const base = "/api/v1/companies/acme"
	defer func() { externalErr = nil }()
	for _, c := range []struct {
		err          error
		method, path string
		body         string
		want         int
	}{
		{domain.ErrConflict, "PATCH", base + "/external-agents/" + id, `{"name":"x"}`, 409},
		{domain.ErrConflict, "POST", base + "/external-agents/" + id + "/merge", `{"into":"` + id + `"}`, 409},
		{domain.ErrNotAllowed, "PATCH", base + "/external-hosts/" + id, `{"always_on":true}`, 403},
		{domain.ErrNotFound, "GET", base + "/external-hosts/" + id, "", 404},
		{nil, "POST", base + "/external-agents/" + id + "/merge", `{"into":"nao-e-uuid"}`, 422},
	} {
		externalErr = c.err
		resp, body := do(t, c.method, ts.URL+c.path, c.body, nil)
		if resp.StatusCode != c.want {
			t.Errorf("%s %s (erro %v): %d %s, quer %d", c.method, c.path, c.err, resp.StatusCode, body, c.want)
		}
	}
}
