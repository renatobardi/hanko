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

// envSample é a amostra com um ambiente SSH (padrão) e um sandbox.
func envSample() *uitest.Fake {
	f := uitest.Sample()
	now := uitest.Now()
	ok := true
	f.Env.List = []service.EnvironmentView{
		{Environment: domain.Environment{ID: uitest.ID("env:build"), Slug: "build", Name: "Servidor de build", Driver: domain.EnvSSH, Status: domain.EnvActive,
			IsDefault: true, KeySet: true, LastProbeAt: &now, LastProbeOK: &ok, CreatedAt: now, UpdatedAt: now,
			LastProbeReport: &domain.ProbeReport{OK: true, System: "Linux x86_64", Steps: []domain.ProbeStep{{Name: "connect", OK: true, Detail: "ok"}, {Name: "bin:claude", OK: false, Detail: "—"}}},
			Config: domain.EnvironmentConfig{SSH: &domain.SSHConfig{Host: "build.example.com", Port: 22, User: "hanko", Root: "/srv/hanko", KnownHosts: "build.example.com ssh-ed25519 AAAA"},
				EnvNames: []string{"API_TOKEN"}}}},
		{Environment: domain.Environment{ID: uitest.ID("env:caixa"), Slug: "caixa", Name: "Caixa de areia", Driver: domain.EnvSandbox, Status: domain.EnvActive,
			CreatedAt: now, UpdatedAt: now, Config: domain.EnvironmentConfig{Sandbox: &domain.SandboxConfig{Image: "ghcr.io/acme/agente:1", Network: "none", MemoryMiB: 2048, CPUs: 2, PIDsLimit: 512}}},
			Warnings: []string{"imagem sem digest"}},
	}
	f.Env.Leases = map[domain.ID][]service.EnvironmentLeaseView{uitest.ID("env:build"): {{
		EnvironmentLease: domain.EnvironmentLease{ID: domain.NewID(), RunID: f.Runs[0].ID, Status: domain.LeaseFailed, RemoteDir: "/srv/hanko/hanko/site/x",
			AcquiredAt: now, FailureReason: "trazer o workspace de volta: falhou"}, RunName: "corrida-azul", PersonaName: "Ada", RunStatus: domain.RunFailed}}}
	f.Env.RunEnv = map[domain.ID]service.RunEnvironmentInfo{f.Runs[0].ID: {EnvironmentID: uitest.ID("env:build"), Slug: "build", Name: "Servidor de build",
		Driver: domain.EnvSSH, Lease: domain.EnvironmentLease{Status: domain.LeaseFailed, RemoteDir: "/srv/hanko/hanko/site/x"}}}
	f.Env.Probe = domain.ProbeReport{OK: true, System: "Linux aarch64", Steps: []domain.ProbeStep{{Name: "system", OK: true, Detail: "Linux aarch64"}}}
	return f
}

func TestEnvironmentsHub(t *testing.T) {
	e := newEnv(t, envSample())
	rec := e.get(t, "/c/hanko/configuracoes/ambientes")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	wantContains(t, rec, "Ambientes de execução", "Local · este servidor", "Roda neste servidor do Hanko.", "Servidor de build", "hanko@build.example.com:22",
		"Caixa de areia", "ghcr.io/acme/agente:1", "Ambiente padrão", `value="build" selected`, "Ambiente SSH", "Sandbox", "Testar",
		`action="/c/hanko/configuracoes/ambientes/padrao"`)
	// operador desligou o SSH: sem botão de criar, com o motivo
	e.f.Env.Drivers = map[domain.EnvironmentDriver]string{domain.EnvSSH: "SSH está desligado neste servidor (HANKO_ENV_SSH_ALLOW_HOSTS vazio)"}
	rec = e.get(t, "/c/hanko/configuracoes/ambientes")
	wantContains(t, rec, "HANKO_ENV_SSH_ALLOW_HOSTS vazio")
	wantNotContains(t, rec, "/ambientes/novo?driver=ssh")

	// padrão
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/padrao", url.Values{"environment": {"local"}}, false)
	if rec.Code != http.StatusSeeOther || !e.f.Called("SetDefaultEnvironment") {
		t.Fatalf("padrão: %d", rec.Code)
	}
	for _, v := range e.f.Env.List {
		if v.IsDefault {
			t.Fatal("local deveria ser o padrão")
		}
	}
}

func TestEnvironmentCreateAndProbeDraft(t *testing.T) {
	e := newEnv(t, envSample())
	rec := e.get(t, "/c/hanko/configuracoes/ambientes/novo?driver=ssh")
	wantContains(t, rec, "Novo ambiente SSH", `name="host"`, `name="private_key"`, `name="known_hosts"`, "Testar conexão", "Cadastrar ambiente")
	rec = e.get(t, "/c/hanko/configuracoes/ambientes/novo?driver=sandbox")
	wantContains(t, rec, "Novo sandbox", `name="image"`, `name="network"`, `name="memory"`, `name="pids"`)

	form := url.Values{"driver": {"ssh"}, "name": {"Farm"}, "host": {"farm.example.com"}, "port": {"2222"}, "username": {"ci"}, "root": {"/data"},
		"known_hosts": {"farm.example.com ssh-ed25519 AAAA"}, "private_key": {"-----BEGIN OPENSSH PRIVATE KEY-----\nchave-unica-xyz\n-----END OPENSSH PRIVATE KEY-----"},
		"env": {"API_KEY=valor-secreto"}}
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/testar", form, true)
	wantContains(t, rec, "Conexão ok", "Linux aarch64")
	if e.f.Env.LastCreate.SSH == nil || e.f.Env.LastCreate.SSH.Port != 2222 {
		t.Fatalf("rascunho: %+v", e.f.Env.LastCreate)
	}
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/novo", form, false)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/configuracoes/ambientes/farm?ok=criado") {
		t.Fatalf("criar: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	in := e.f.Env.LastCreate
	if in.Driver != domain.EnvSSH || in.Env["API_KEY"] != "valor-secreto" || !strings.Contains(in.PrivateKey, "chave-unica-xyz") {
		t.Fatalf("entrada: %+v", in)
	}
	// erro de validação volta ao formulário sem os segredos
	e.f.SetErr("CreateEnvironment", domain.Invalid("known_hosts", "required"))
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/novo", form, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	wantContains(t, rec, "Cole as linhas de known_hosts", `value="farm.example.com"`)
	wantNotContains(t, rec, "chave-unica-xyz", "valor-secreto")
	e.f.SetErr("CreateEnvironment", nil)
	// porta não numérica
	bad := url.Values{"driver": {"ssh"}, "name": {"X"}, "port": {"abc"}}
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/novo", bad, false)
	wantContains(t, rec, "Porta inválida")
}

func TestEnvironmentPage(t *testing.T) {
	e := newEnv(t, envSample())
	rec := e.get(t, "/c/hanko/configuracoes/ambientes/build")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	wantContains(t, rec, "Servidor de build", "SSH · host remoto", "Último teste de conexão", "Linux x86_64", "corrida-azul", "trazer o workspace de volta",
		"Mover as personas para o ambiente…", `value="caixa"`, "API_TOKEN", "guardada (cifrada)", "Testar conexão", "Salvar alterações")
	wantNotContains(t, rec, "-----END")
	rec = e.get(t, "/c/hanko/configuracoes/ambientes/caixa")
	wantContains(t, rec, "imagem sem digest", `name="image"`, "ghcr.io/acme/agente:1")
	e.get(t, "/c/hanko/configuracoes/ambientes/nao-existe")

	// teste (htmx devolve o relatório; sem htmx volta para a página)
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/build/testar", nil, true)
	wantContains(t, rec, "Conexão ok", "Linux aarch64")
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/build/testar", nil, false)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=testado") {
		t.Fatalf("teste: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// edição: chave em branco não troca a chave
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/build", url.Values{"name": {"Build 2"}, "host": {"build.example.com"}, "port": {"22"},
		"username": {"hanko"}, "root": {"/srv/hanko"}, "known_hosts": {"build.example.com ssh-ed25519 AAAA"}, "drop_env": {"API_TOKEN"}}, false)
	if rec.Code != http.StatusSeeOther || e.f.Env.LastUpdate.PrivateKey != nil || len(e.f.Env.LastUpdate.DropEnv) != 1 {
		t.Fatalf("editar: %d %+v", rec.Code, e.f.Env.LastUpdate)
	}
	// arquivar movendo as personas
	ada := uitest.ID("persona:ada")
	for i := range e.f.Personas {
		if e.f.Personas[i].ID == ada {
			b := uitest.ID("env:build")
			e.f.Personas[i].EnvironmentID = &b
		}
	}
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/build/arquivar", url.Values{"move_to": {"caixa"}}, false)
	if rec.Code != http.StatusSeeOther || !e.f.Called("ArchiveEnvironment") {
		t.Fatalf("arquivar: %d", rec.Code)
	}
	p, _ := e.f.GetPersona(t.Context(), uitest.ID("company"), "ada")
	if p.EnvironmentID == nil || *p.EnvironmentID != uitest.ID("env:caixa") {
		t.Fatalf("persona movida: %+v", p.EnvironmentID)
	}
	rec = e.get(t, "/c/hanko/configuracoes/ambientes/build")
	wantContains(t, rec, "Ambiente arquivado", "Reativar")
}

func TestPersonaAndRunEnvironment(t *testing.T) {
	e := newEnv(t, envSample())
	rec := e.get(t, "/c/hanko/personas/ada")
	wantContains(t, rec, `hx-get="/c/hanko/personas/ada/ambiente"`)
	rec = e.get(t, "/c/hanko/personas/ada/ambiente", "HX-Request", "true")
	wantContains(t, rec, "Ambiente de execução", "Servidor de build", "(padrão da empresa)", "Padrão da empresa (Servidor de build)", "Salvar ambiente")
	rec = e.post(t, "/c/hanko/personas/ada/ambiente", url.Values{"environment": {"caixa"}}, true)
	wantContains(t, rec, "Ambiente salvo", "Caixa de areia")
	if !e.f.Called("SetPersonaEnvironment") {
		t.Fatal("persona não salva")
	}

	run := e.f.Runs[0]
	rec = e.get(t, "/c/hanko/execucoes/"+run.ID.String())
	wantContains(t, rec, `hx-get="`+run.ID.String()+`/ambiente"`)
	rec = e.get(t, "/c/hanko/execucoes/"+run.ID.String()+"/ambiente", "HX-Request", "true")
	wantContains(t, rec, "Servidor de build (ssh)", "/c/hanko/configuracoes/ambientes/build", "Falhou", "/srv/hanko/hanko/site/x")
	rec = e.get(t, "/c/hanko/execucoes/"+e.f.Runs[1].ID.String()+"/ambiente", "HX-Request", "true")
	wantContains(t, rec, "Local · este servidor")
}

func TestEnvironmentsHideControlsByRole(t *testing.T) {
	f := envSample()
	f.MyRoleIs = domain.MemberMember
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/configuracoes/ambientes")
	wantNotContains(t, rec, "/ambientes/novo?driver=", `action="/c/hanko/configuracoes/ambientes/padrao"`, ">Testar<")
	rec = e.get(t, "/c/hanko/configuracoes/ambientes/build")
	wantNotContains(t, rec, "Salvar alterações", "Mover as personas para o ambiente…", `name="private_key"`)
	rec = e.get(t, "/c/hanko/personas/ada/ambiente", "HX-Request", "true")
	wantNotContains(t, rec, "Salvar ambiente")
	// o service recusa de qualquer modo; a tela mostra o erro
	f.SetErr("SetDefaultEnvironment", domain.ErrForbidden)
	rec = e.post(t, "/c/hanko/configuracoes/ambientes/padrao", url.Values{"environment": {"local"}}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
}
