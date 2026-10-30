package api

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Os oito métodos de agente e host do fake devolvem "não encontrado": a rota chega ao handler e o erro é mapeado (404),
// sem 501 (contrato) e sem 500 (método ausente).
func (f *fakeSvc) ListExternalAgents(context.Context, domain.ID, bool, int32, int32) ([]service.ExternalAgentView, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeSvc) GetExternalAgent(context.Context, domain.ID, domain.ID) (service.ExternalAgentView, error) {
	return service.ExternalAgentView{}, domain.ErrNotFound
}
func (f *fakeSvc) UpdateExternalAgent(context.Context, domain.Actor, domain.ID, domain.ID, service.ExternalAgentPatch) (service.ExternalAgentView, error) {
	return service.ExternalAgentView{}, domain.ErrNotFound
}
func (f *fakeSvc) MergeExternalAgent(context.Context, domain.Actor, domain.ID, domain.ID, domain.ID) (service.ExternalAgentView, error) {
	return service.ExternalAgentView{}, domain.ErrNotFound
}
func (f *fakeSvc) ListExternalHosts(context.Context, domain.ID, bool, int32, int32) ([]service.ExternalHostView, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeSvc) GetExternalHost(context.Context, domain.ID, domain.ID) (service.ExternalHostView, error) {
	return service.ExternalHostView{}, domain.ErrNotFound
}
func (f *fakeSvc) UpdateExternalHost(context.Context, domain.Actor, domain.ID, domain.ID, service.ExternalHostPatch) (service.ExternalHostView, error) {
	return service.ExternalHostView{}, domain.ErrNotFound
}
func (f *fakeSvc) MergeExternalHost(context.Context, domain.Actor, domain.ID, domain.ID, domain.ID) (service.ExternalHostView, error) {
	return service.ExternalHostView{}, domain.ErrNotFound
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
