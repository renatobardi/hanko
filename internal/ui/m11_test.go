package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func TestEnvironmentFormsLocalAndCloud(t *testing.T) {
	e := newEnv(t, envSample())
	rec := e.get(t, "/c/hanko/configuracoes/ambientes")
	wantContains(t, rec, "Local explícito", "Nuvem", "/ambientes/novo?driver=local", "/ambientes/novo?driver=cloud")

	rec = e.get(t, "/c/hanko/configuracoes/ambientes/novo?driver=local")
	wantContains(t, rec, "Novo ambiente local", `name="name"`, `name="env"`)
	wantNotContains(t, rec, `name="host"`, `name="image"`, `name="api_key"`)

	rec = e.get(t, "/c/hanko/configuracoes/ambientes/novo?driver=cloud")
	wantContains(t, rec, "Novo sandbox em nuvem", `name="api_key"`, `type="password"`, `name="snapshot"`, `name="cloud_network"`, `name="cloud_cpus"`, `name="cloud_memory"`, `name="cloud_disk"`)
	wantNotContains(t, rec, `name="host"`)

	// criar o cloud: a chave de API e a configuração chegam ao service; a resposta não devolve a chave
	form := url.Values{"driver": {"cloud"}, "name": {"Nuvem"}, "api_key": {"dtn_segredo-do-formulario"}, "snapshot": {"meu-snap"},
		"cloud_cpus": {"4"}, "cloud_memory": {"8"}, "cloud_disk": {"20"}, "cloud_network": {"none"}, "cloud_root": {"/home/daytona/x"}}
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/novo", form, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("criar cloud: %d %s", rec.Code, rec.Body)
	}
	in := e.f.Env.LastCreate
	if in.Driver != domain.EnvCloud || in.APIKey != "dtn_segredo-do-formulario" || in.Cloud == nil || in.Cloud.CPUs != 4 || in.Cloud.MemoryGiB != 8 ||
		in.Cloud.DiskGiB != 20 || in.Cloud.Network != "none" || in.Cloud.Snapshot != "meu-snap" || in.Cloud.Root != "/home/daytona/x" {
		t.Fatalf("entrada do cloud: %+v", in)
	}
	// número inválido volta ao formulário com o erro, sem a chave
	form.Set("cloud_cpus", "x")
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/novo", form, false)
	wantNotContains(t, rec, "dtn_segredo-do-formulario")
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("cpus inválido deveria falhar: %d", rec.Code)
	}

	// criar o local explícito
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/novo", url.Values{"driver": {"local"}, "name": {"Servidor"}, "env": {"DA_LOCAL=sim"}}, false)
	if rec.Code != http.StatusSeeOther || e.f.Env.LastCreate.Driver != domain.EnvLocal || e.f.Env.LastCreate.Env["DA_LOCAL"] != "sim" {
		t.Fatalf("criar local: %d %+v", rec.Code, e.f.Env.LastCreate)
	}
}

func TestEnvironmentPageShowsCapabilities(t *testing.T) {
	f := envSample()
	f.Env.List[0].LastProbeReport.Capabilities = []string{"sync", "claude", "hanko-reachable"}
	f.Env.Probe.Capabilities = []string{"isolated", "network"}
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/configuracoes/ambientes/build")
	wantContains(t, rec, "Capacidades:", "hanko-reachable", "sync")
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/testar", url.Values{"driver": {"sandbox"}, "name": {"x"}, "image": {"alpine:3"}}, true)
	wantContains(t, rec, "isolated", "network")
}

func TestHireFormModelPicker(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/personas/nova")
	wantContains(t, rec, `name="model_choice"`, "Padrão do harness", "Outro…", `name="model"`, `<optgroup label="Claude Code">`, `<optgroup label="Codex">`,
		"modelo-sem-preco", "sem preço", "data-unpriced", "Sem preço próprio")

	// escolha do catálogo: vale o select, não o campo livre
	form := url.Values{"name": {"Yuki"}, "agent": {"claude"}, "model_choice": {"sonnet"}, "model": {"ignorado"}, "heartbeat": {"30"}}
	rec = e.post(t, "/c/hanko/personas/nova", form, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("contratar: %d %s", rec.Code, rec.Body)
	}
	if p := e.f.Personas[len(e.f.Personas)-1]; p.Model != "sonnet" {
		t.Fatalf("modelo do select: %q", p.Model)
	}
	// "Outro…": vale o campo livre
	form = url.Values{"name": {"Zeca"}, "agent": {"claude"}, "model_choice": {"__other"}, "model": {"meu-modelo-9"}, "heartbeat": {"30"}}
	if rec = e.post(t, "/c/hanko/personas/nova", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("contratar outro: %d", rec.Code)
	}
	if p := e.f.Personas[len(e.f.Personas)-1]; p.Model != "meu-modelo-9" {
		t.Fatalf("modelo livre: %q", p.Model)
	}
	// padrão do harness: vazio
	form = url.Values{"name": {"Ivo"}, "agent": {"claude"}, "model_choice": {""}, "model": {"resto"}, "heartbeat": {"30"}}
	if rec = e.post(t, "/c/hanko/personas/nova", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("contratar padrão: %d", rec.Code)
	}
	if p := e.f.Personas[len(e.f.Personas)-1]; p.Model != "" {
		t.Fatalf("modelo padrão: %q", p.Model)
	}
	// erro de validação devolve o formulário com o valor livre preservado e o "Outro…" marcado
	form = url.Values{"name": {""}, "agent": {"claude"}, "model_choice": {"__other"}, "model": {"x-9"}}
	rec = e.post(t, "/c/hanko/personas/nova", form, false)
	wantContains(t, rec, `value="__other" selected`, `value="x-9"`)
	// sem catálogo (falha do service) o seletor continua funcionando
	e.f.SetErr("ModelCatalog", domain.ErrNotFound)
	rec = e.get(t, "/c/hanko/personas/nova")
	wantContains(t, rec, "Outro…", `name="model"`)
}

func TestPersonaReadinessCard(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	// página da persona carrega o cartão por htmx; ?testar=1 pede o teste automático
	rec := e.get(t, "/c/hanko/personas/ada")
	wantContains(t, rec, `hx-get="/c/hanko/personas/ada/prontidao"`)
	rec = e.get(t, "/c/hanko/personas/ada?testar=1")
	wantContains(t, rec, `/c/hanko/personas/ada/prontidao?auto=1`)

	rec = e.get(t, "/c/hanko/personas/ada/prontidao", "HX-Request", "true")
	wantContains(t, rec, "Prontidão do harness", "Nunca testada", "Esta persona ainda não foi testada.", "Testar agora")
	rec = e.get(t, "/c/hanko/personas/ada/prontidao?auto=1", "HX-Request", "true")
	wantContains(t, rec, `hx-trigger="load"`)

	// testar agora: ok
	rec = e.post(t, "/c/hanko/personas/ada/prontidao", url.Values{}, true)
	wantContains(t, rec, "Pronta", "Binário do harness", "Login ou chave de API", "Teste concluído")
	wantNotContains(t, rec, "Despacho bloqueado")
	if !e.f.Called("TestPersonaReadiness") {
		t.Fatal("o teste não foi pedido")
	}

	// auth quebrada: a tela mostra o motivo e que o despacho está bloqueado
	e.f.Ready.Next = &domain.Readiness{Steps: []domain.ReadinessStep{
		{Name: domain.StepEnvironment, Status: domain.StepOK, Detail: "local"},
		{Name: domain.StepBinary, Status: domain.StepOK, Detail: "/usr/bin/claude"},
		{Name: domain.StepAuth, Status: domain.StepFail, Detail: "sem login do claude nem chave de API"},
		{Name: domain.StepModel, Status: domain.StepWarn, Detail: "sem preço próprio"}}}
	rec = e.post(t, "/c/hanko/personas/ada/prontidao", url.Values{}, true)
	wantContains(t, rec, "Não pronta", "Despacho bloqueado: auth: sem login do claude nem chave de API", "aviso: sem preço próprio")
	rec = e.get(t, "/c/hanko/personas/ada/prontidao", "HX-Request", "true")
	wantContains(t, rec, "Não pronta", "Despacho bloqueado")

	// sem HX (formulário sem JS): volta à página da persona
	rec = e.post(t, "/c/hanko/personas/ada/prontidao", url.Values{}, false)
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "/personas/ada#prontidao") {
		t.Fatalf("redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// erro do service no htmx volta o cartão com a mensagem
	e.f.SetErr("TestPersonaReadiness", domain.ErrConflict)
	rec = e.post(t, "/c/hanko/personas/ada/prontidao", url.Values{}, true)
	wantContains(t, rec, "Prontidão do harness", `class="err"`)
}

func TestReadinessHiddenForMembers(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberMember
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/personas/ada/prontidao", "HX-Request", "true")
	wantContains(t, rec, "Prontidão do harness")
	wantNotContains(t, rec, "Testar agora")
}

func TestProjectEnvironmentCard(t *testing.T) {
	e := newEnv(t, envSample())
	proj := e.f.Projects[0]
	rec := e.get(t, "/c/hanko/projetos/"+proj.Slug+"?secao=configuracao")
	wantContains(t, rec, `hx-get="/c/hanko/projetos/`+proj.Slug+`/ambiente"`)
	rec = e.get(t, "/c/hanko/projetos/"+proj.Slug+"/ambiente", "HX-Request", "true")
	wantContains(t, rec, "Ambiente de execução", "Padrão da empresa (Servidor de build)", "Salvar ambiente", "vence o projeto")
	rec = e.post(t, "/c/hanko/projetos/"+proj.Slug+"/ambiente", url.Values{"environment": {"caixa"}}, true)
	wantContains(t, rec, "Ambiente salvo", "Caixa de areia")
	if e.f.Ready.LastProjectEnv == nil || *e.f.Ready.LastProjectEnv != uitest.ID("env:caixa") {
		t.Fatalf("ambiente do projeto: %v", e.f.Ready.LastProjectEnv)
	}
	// voltar ao padrão
	if rec = e.post(t, "/c/hanko/projetos/"+proj.Slug+"/ambiente", url.Values{"environment": {"default"}}, true); rec.Code != http.StatusOK {
		t.Fatalf("padrão: %d", rec.Code)
	}
	if e.f.Ready.LastProjectEnv != nil {
		t.Fatalf("default deveria limpar: %v", e.f.Ready.LastProjectEnv)
	}
	// sem HX redireciona para a aba
	rec = e.post(t, "/c/hanko/projetos/"+proj.Slug+"/ambiente", url.Values{"environment": {"caixa"}}, false)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "secao=configuracao") {
		t.Fatalf("redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// ambiente inexistente
	if rec = e.post(t, "/c/hanko/projetos/"+proj.Slug+"/ambiente", url.Values{"environment": {"nao-existe"}}, true); rec.Code != http.StatusNotFound {
		t.Fatalf("inexistente: %d", rec.Code)
	}
	// membro não vê o seletor
	e.f.MyRoleIs = domain.MemberMember
	rec = e.get(t, "/c/hanko/projetos/"+proj.Slug+"/ambiente", "HX-Request", "true")
	wantNotContains(t, rec, "Salvar ambiente")
}

func TestRunEnvironmentShowsOrigin(t *testing.T) {
	f := envSample()
	run := f.Runs[0]
	f.Env.RunEnv[run.ID] = service.RunEnvironmentInfo{EnvironmentID: uitest.ID("env:build"), Slug: "build", Name: "Servidor de build",
		Driver: domain.EnvSSH, Source: domain.EnvFromProject, Lease: domain.EnvironmentLease{Status: domain.LeaseReleased}}
	f.Runs[1].EnvironmentSource = domain.EnvFromDefault
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/execucoes/"+run.ID.String()+"/ambiente", "HX-Request", "true")
	wantContains(t, rec, "Servidor de build (ssh)", "origem:", "do projeto")
	rec = e.get(t, "/c/hanko/execucoes/"+f.Runs[1].ID.String()+"/ambiente", "HX-Request", "true")
	wantContains(t, rec, "Local · este servidor", "origem:", "padrão da empresa")
}

func TestInboxLabelForNotReadyAlert(t *testing.T) {
	if alertKindText[domain.AlertPersonaNotReady] == "" {
		t.Fatal("o alerta persona_not_ready precisa de um rótulo na Caixa de entrada")
	}
}
