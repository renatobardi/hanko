//go:build integration

package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

type apiFakeCloud struct{}

func (apiFakeCloud) Name() string                     { return "daytona" }
func (apiFakeCloud) KnownHosts() string               { return "gw.example.com ssh-ed25519 AAAA" }
func (apiFakeCloud) New(string) execenv.CloudProvider { return nil }

func TestAPIEnvironmentsLocalCloudProject(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	e.svc.Environments = execenv.Config{RemoteURL: "https://hanko.example.com", Cloud: apiFakeCloud{}, LookPath: func(s string) (string, error) { return s, nil }}
	base := e.base() + "/environments"
	ana := e.persona(e.co, "Ana", nil)
	e.want(201, "POST", e.base()+"/projects", `{"name":"Site"}`, nil)

	// local explícito
	var loc api.Environment
	raw := e.want(201, "POST", base, `{"name":"Servidor","driver":"local","env":{"DA_LOCAL":"sim"}}`, &loc)
	if loc.Driver != "local" || !loc.Available || len(loc.EnvNames) != 1 || strings.Contains(string(raw), "sim\"") {
		t.Fatalf("local: %s", raw)
	}
	e.want(422, "POST", base, `{"name":"Mistura","driver":"local","private_key":"x"}`, nil)

	// cloud: a chave de API só entra
	var cl api.Environment
	raw = e.want(201, "POST", base, `{"name":"Nuvem","driver":"cloud","api_key":"dtn_chave-da-api","cloud":{"network":"none","cpus":4}}`, &cl)
	if cl.Driver != "cloud" || !cl.KeySet || cl.Cloud == nil || cl.Cloud.Provider != "daytona" || cl.Cloud.CPUs != 4 || cl.Cloud.Network != "none" || cl.Cloud.MemoryGiB != 4 || len(cl.Warnings) == 0 {
		t.Fatalf("cloud: %s", raw)
	}
	if strings.Contains(string(raw), "chave-da-api") || strings.Contains(string(raw), "api_key") {
		t.Fatalf("chave de API na resposta: %s", raw)
	}
	e.want(422, "POST", base, `{"name":"Sem chave","driver":"cloud","cloud":{}}`, nil)
	e.want(422, "POST", base, `{"name":"Provider","driver":"cloud","api_key":"k","cloud":{"provider":"outro"}}`, nil)
	e.want(200, "PATCH", base+"/nuvem", `{"api_key":"dtn_outra","cloud":{"network":"full","cpus":2}}`, &cl)
	if cl.Cloud.Network != "full" {
		t.Fatalf("patch cloud: %+v", cl.Cloud)
	}
	// o operador desligou o cloud
	e.svc.Environments.Cloud = nil
	e.want(200, "GET", base+"/nuvem", "", &cl)
	if cl.Available || !strings.Contains(cl.Unavailable, "HANKO_ENV_CLOUD") {
		t.Fatalf("indisponível: %+v", cl)
	}
	e.svc.Environments.Cloud = apiFakeCloud{}

	// ambiente do projeto e precedência na run
	var pr api.Project
	e.want(200, "PUT", e.base()+"/projects/site/environment", `{"environment":"servidor"}`, &pr)
	if pr.EnvironmentID != loc.ID {
		t.Fatalf("projeto: %+v", pr)
	}
	e.want(200, "GET", base+"/servidor", "", &loc)
	if len(loc.Projects) != 1 || loc.Projects[0].Slug != "site" {
		t.Fatalf("projetos do ambiente: %+v", loc.Projects)
	}
	e.want(404, "PUT", e.base()+"/projects/site/environment", `{"environment":"nao-existe"}`, nil)
	e.want(404, "PUT", e.base()+"/projects/nao-existe/environment", `{"environment":"servidor"}`, nil)
	tok := e.personaToken(e.co, ana)
	if status, _ := e.call("PUT", e.base()+"/projects/site/environment", `{"environment":"servidor"}`, tok); status != 403 {
		t.Fatalf("run token mudou o ambiente do projeto: %d", status)
	}
	site, err := e.svc.GetProject(t.Context(), e.co.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	tv := e.task(e.co, "t", &ana, func(in *service.CreateTaskInput) { in.ProjectID = &site.ID })
	run, err := e.svc.CheckoutRun(t.Context(), service.Wake{CompanyID: e.co.ID, PersonaID: ana.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	var out api.Run
	e.want(200, "GET", e.base()+"/runs/"+run.ID.String(), "", &out)
	if out.EnvironmentSource != "project" || out.EnvironmentID != loc.ID {
		t.Fatalf("origem na run: %+v", out)
	}
	// arquivar o ambiente leva o projeto para o padrão
	e.want(200, "DELETE", base+"/servidor?move_personas_to=local", "", nil)
	e.want(200, "GET", e.base()+"/projects/site", "", &pr)
	if pr.EnvironmentID != "" {
		t.Fatalf("projeto deveria voltar ao padrão: %+v", pr.EnvironmentID)
	}
}

func TestAPIReadinessAndModels(t *testing.T) {
	e := newAPIEnv(t)
	e.svc.ReadinessRunner = func(_ context.Context, _ execenv.Target, _ string, _ map[string]string) ([]byte, error) {
		return []byte("bin=/usr/bin/claude\nversion=2\nauth=none\n"), nil
	}
	p := e.persona(e.co, "Ana", func(in *service.HirePersonaInput) { in.Agent = "claude"; in.Model = "modelo-novo-7" })
	path := e.base() + "/personas/ana/readiness"
	tok := e.personaToken(e.co, p) // antes de a persona ficar não pronta: emitir o token abre uma run

	var r api.PersonaReadiness
	e.want(200, "GET", path, "", &r)
	if r.Status != "untested" || r.Blocking || len(r.Steps) != 0 {
		t.Fatalf("nunca testada: %+v", r)
	}
	e.want(200, "POST", path, "", &r)
	if r.Status != "not_ready" || !r.Blocking || !strings.HasPrefix(r.Reason, "auth:") || r.Agent != "claude" || len(r.Steps) != 4 || r.Steps[2].Name != "auth" || r.Steps[2].Status != "fail" {
		t.Fatalf("auth quebrada: %+v", r)
	}
	if r.Steps[3].Name != "model" || r.Steps[3].Status != "warn" {
		t.Fatalf("modelo sem preço deveria ser aviso: %+v", r.Steps[3])
	}
	var again api.PersonaReadiness
	e.want(200, "GET", path, "", &again)
	if again.Status != "not_ready" || again.CheckedAt == nil {
		t.Fatalf("persistiu: %+v", again)
	}
	var all api.ListBody[api.PersonaReadiness]
	e.want(200, "GET", e.base()+"/readiness", "", &all)
	if len(all.Items) != 1 || all.Items[0].PersonaID != p.ID.String() {
		t.Fatalf("quadro: %+v", all.Items)
	}
	e.want(404, "GET", e.base()+"/personas/nao-existe/readiness", "", nil)
	// run token não testa
	if status, _ := e.call("POST", path, "", tok); status != 403 {
		t.Fatalf("run token testou: %d", status)
	}
	// o alerta e o bloqueio aparecem na API
	tv := e.task(e.co, "t", &p, nil)
	if status, b := e.call("POST", e.base()+"/tasks/"+tv.Key+"/wake", "", nil); status >= 500 {
		t.Fatalf("wake: %d %s", status, b)
	}

	var models api.ListBody[api.CatalogModel]
	e.want(200, "GET", e.base()+"/models?agent=claude", "", &models)
	by := map[string]api.CatalogModel{}
	for _, m := range models.Items {
		by[m.Model] = m
	}
	if m := by["sonnet"]; !m.Priced || m.PriceModel != "claude-sonnet" {
		t.Fatalf("sonnet: %+v", m)
	}
	e.want(200, "GET", e.base()+"/models", "", &models)
}
