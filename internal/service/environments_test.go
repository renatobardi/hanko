package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

const envTestKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA-chave-secreta-de-teste\n-----END OPENSSH PRIVATE KEY-----"

// newEnvEnv é newEnv com cofre de segredos e o operador liberando SSH para *.example.com e o sandbox.
func newEnvEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	box, err := secretbox.New(keyOne, "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	e.svc.Environments = execenv.Config{SSHAllowHosts: []string{".example.com"}, Sandbox: "docker", RemoteURL: "https://hanko.example.com",
		LookPath: func(s string) (string, error) { return s, nil }}
	return e
}

func sshInput(name string) service.CreateEnvironmentInput {
	return service.CreateEnvironmentInput{Name: name, Driver: domain.EnvSSH, PrivateKey: envTestKey,
		SSH: &domain.SSHConfig{Host: "build.example.com", User: "hanko", Root: "/srv/hanko", KnownHosts: "build.example.com ssh-ed25519 AAAAC3Nza"},
		Env: map[string]string{"API_TOKEN": "valor-secreto-123"}}
}

func TestEnvironmentCRUDAndSecrets(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	v, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Build"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Slug != "build" || v.Driver != domain.EnvSSH || !v.KeySet || v.Config.SSH.Port != 22 || v.Unavailable != "" {
		t.Fatalf("criado: %+v", v)
	}
	if strings.Join(v.Config.EnvNames, ",") != "API_TOKEN" {
		t.Fatalf("nomes das variáveis: %v", v.Config.EnvNames)
	}
	raw, _ := json.Marshal(v)
	var dbEnc string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT secret_enc FROM environments WHERE id=$1`, v.ID).Scan(&dbEnc); err != nil {
		t.Fatal(err)
	}
	for _, blob := range []string{string(raw), dbEnc} {
		if strings.Contains(blob, "chave-secreta") || strings.Contains(blob, "valor-secreto") {
			t.Fatalf("segredo em claro: %s", blob)
		}
	}
	var act string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT string_agg(data::text, ' ') FROM activity_events WHERE subject_type='environment'`).Scan(&act); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(act, "secreta") || strings.Contains(act, "valor-secreto") {
		t.Fatalf("segredo na atividade: %s", act)
	}

	// slug repetido e reservado
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Build")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("slug repetido: %v", err)
	}
	in := sshInput("Local")
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("slug reservado: %v", err)
	}

	// edição: troca o host e uma variável; a chave continua
	name := "Build farm"
	up, err := e.svc.UpdateEnvironment(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateEnvironmentInput{Name: &name,
		SSH:    &domain.SSHConfig{Host: "farm.example.com", Port: 2222, User: "ci", Root: "/data", KnownHosts: "farm.example.com ssh-ed25519 AAAA"},
		SetEnv: map[string]string{"OUTRA": "x"}, DropEnv: []string{"API_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != name || up.Config.SSH.Host != "farm.example.com" || up.Config.SSH.Port != 2222 || !up.KeySet || strings.Join(up.Config.EnvNames, ",") != "OUTRA" {
		t.Fatalf("edição: %+v", up)
	}
	got, err := e.svc.GetEnvironment(ctx, e.co.ID, "build")
	if err != nil || got.ID != v.ID {
		t.Fatalf("get por slug: %+v %v", got, err)
	}
	list, err := e.svc.ListEnvironments(ctx, e.co.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}

	// sandbox
	sb, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, service.CreateEnvironmentInput{Name: "Caixa", Driver: domain.EnvSandbox,
		Sandbox: &domain.SandboxConfig{Image: "ghcr.io/acme/agente:1"}})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Config.Sandbox.Network != "none" || sb.Config.Sandbox.MemoryMiB != 2048 || sb.KeySet || len(sb.Warnings) == 0 {
		t.Fatalf("sandbox: %+v", sb)
	}
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, service.CreateEnvironmentInput{Name: "Ruim", Driver: domain.EnvSandbox,
		Sandbox: &domain.SandboxConfig{Image: "Imagem Inválida"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("imagem inválida: %v", err)
	}
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, service.CreateEnvironmentInput{Name: "Local de novo", Driver: domain.EnvLocal}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("local explícito: %v", err)
	}
}

func TestEnvironmentValidationAndSwitches(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	bad := []func(*service.CreateEnvironmentInput){
		func(in *service.CreateEnvironmentInput) { in.SSH.KnownHosts = "" },
		func(in *service.CreateEnvironmentInput) { in.SSH.Host = "-oProxyCommand=x" },
		func(in *service.CreateEnvironmentInput) { in.SSH.Root = "relativo" },
		func(in *service.CreateEnvironmentInput) { in.SSH.Root = "/a/../b" },
		func(in *service.CreateEnvironmentInput) { in.SSH.User = "root;rm" },
		func(in *service.CreateEnvironmentInput) { in.PrivateKey = "" },
		func(in *service.CreateEnvironmentInput) { in.Env = map[string]string{"HANKO_RUN_TOKEN": "x"} },
		func(in *service.CreateEnvironmentInput) { in.SSH.Host = "fora.da.lista.com" },
	}
	for i, mod := range bad {
		in := sshInput("x")
		in.Slug = "x" + string(rune('a'+i))
		mod(&in)
		if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("caso %d aceito: %v", i, err)
		}
	}
	// interruptores desligados
	e.svc.Environments.SSHAllowHosts = nil
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Desligado")); err == nil || !strings.Contains(err.Error(), "HANKO_ENV_SSH_ALLOW_HOSTS") {
		t.Fatalf("ssh desligado: %v", err)
	}
	e.svc.Environments.Sandbox = ""
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, service.CreateEnvironmentInput{Name: "S", Driver: domain.EnvSandbox,
		Sandbox: &domain.SandboxConfig{Image: "alpine:3"}}); err == nil || !strings.Contains(err.Error(), "HANKO_ENV_SANDBOX") {
		t.Fatalf("sandbox desligado: %v", err)
	}
	// sem chave de cifragem não há como guardar a chave privada
	e.svc.Environments.SSHAllowHosts = []string{"*"}
	e.svc.Integrations.Secrets = nil
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Sem cofre")); err == nil || !strings.Contains(err.Error(), "HANKO_SECRET_KEY") {
		t.Fatalf("sem cofre: %v", err)
	}
}

func TestEnvironmentPrecedenceDefaultAndArchive(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	re, err := e.svc.RunEnvironment(ctx, e.co.ID, ana.ID)
	if err != nil || re.Environment != nil || re.Source != "local" {
		t.Fatalf("sem ambiente deveria ser local: %+v %v", re, err)
	}
	build, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Build"))
	if err != nil {
		t.Fatal(err)
	}
	in := sshInput("Outro")
	other, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetDefaultEnvironment(ctx, domain.HumanActor, e.co.ID, &build.ID); err != nil {
		t.Fatal(err)
	}
	re, err = e.svc.RunEnvironment(ctx, e.co.ID, ana.ID)
	if err != nil || re.Environment == nil || re.Environment.ID != build.ID || re.Source != "default" {
		t.Fatalf("padrão da empresa: %+v %v", re, err)
	}
	if re.Target.PrivateKey != envTestKey || re.Target.Env["API_TOKEN"] != "valor-secreto-123" || re.Target.SSH.Host != "build.example.com" {
		t.Fatalf("segredos abertos para o executor: %+v", re.Target)
	}
	p, err := e.svc.SetPersonaEnvironment(ctx, domain.HumanActor, e.co.ID, ana.ID, &other.ID)
	if err != nil || p.EnvironmentID == nil || *p.EnvironmentID != other.ID {
		t.Fatalf("ambiente da persona: %+v %v", p, err)
	}
	re, _ = e.svc.RunEnvironment(ctx, e.co.ID, ana.ID)
	if re.Environment == nil || re.Environment.ID != other.ID || re.Source != "persona" {
		t.Fatalf("persona vence o padrão: %+v", re)
	}
	// trocar o padrão: só um por empresa
	if err := e.svc.SetDefaultEnvironment(ctx, domain.HumanActor, e.co.ID, &other.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.ListEnvironments(ctx, e.co.ID)
	defaults := 0
	for _, v := range list {
		if v.IsDefault {
			defaults++
		}
		if v.ID == other.ID && (len(v.Personas) != 1 || v.Personas[0].ID != ana.ID) {
			t.Fatalf("personas do ambiente: %+v", v.Personas)
		}
	}
	if defaults != 1 {
		t.Fatalf("padrões = %d", defaults)
	}
	// arquivar leva as personas para outro ambiente e tira o padrão
	arch, err := e.svc.ArchiveEnvironment(ctx, domain.HumanActor, e.co.ID, other.ID, &build.ID)
	if err != nil || arch.Status != domain.EnvArchived || arch.IsDefault {
		t.Fatalf("arquivar: %+v %v", arch, err)
	}
	got, _ := e.svc.GetPersona(ctx, e.co.ID, ana.Slug)
	if got.EnvironmentID == nil || *got.EnvironmentID != build.ID {
		t.Fatalf("persona movida: %+v", got.EnvironmentID)
	}
	if _, err := e.svc.SetPersonaEnvironment(ctx, domain.HumanActor, e.co.ID, ana.ID, &other.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("ambiente arquivado aceito: %v", err)
	}
	if err := e.svc.SetDefaultEnvironment(ctx, domain.HumanActor, e.co.ID, &other.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("padrão arquivado aceito: %v", err)
	}
	// arquivar sem destino: a persona volta ao padrão (agora o local)
	if _, err := e.svc.ArchiveEnvironment(ctx, domain.HumanActor, e.co.ID, build.ID, nil); err != nil {
		t.Fatal(err)
	}
	re, _ = e.svc.RunEnvironment(ctx, e.co.ID, ana.ID)
	if re.Environment != nil {
		t.Fatalf("sem ambiente ativo deveria ser local: %+v", re.Environment)
	}
	// reativar
	if _, err := e.svc.UpdateEnvironment(ctx, domain.HumanActor, e.co.ID, build.ID, service.UpdateEnvironmentInput{Reactivate: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetDefaultEnvironment(ctx, domain.HumanActor, e.co.ID, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentRoles(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	v, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Build"))
	if err != nil {
		t.Fatal(err)
	}
	ana := e.persona(t, "Ana", nil)
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember} {
		u := mkUser(t, e, string(r)+"@x.com", domain.RoleMember)
		join(t, e, u, r)
		c := as(u)
		if _, err := e.svc.ListEnvironments(c, e.co.ID); err != nil {
			t.Fatalf("%s lê: %v", r, err)
		}
		if _, err := e.svc.ListEnvironmentLeases(c, e.co.ID, v.ID, 10); err != nil {
			t.Fatalf("%s lê usos: %v", r, err)
		}
		in := sshInput("Novo " + string(r))
		if _, err := e.svc.CreateEnvironment(c, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s cria: %v", r, err)
		}
		if err := e.svc.SetDefaultEnvironment(c, domain.HumanActor, e.co.ID, &v.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s padrão: %v", r, err)
		}
		if _, err := e.svc.SetPersonaEnvironment(c, domain.HumanActor, e.co.ID, ana.ID, &v.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s persona: %v", r, err)
		}
		if _, err := e.svc.ProbeEnvironment(c, domain.HumanActor, e.co.ID, v.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s testa: %v", r, err)
		}
		if _, err := e.svc.ArchiveEnvironment(c, domain.HumanActor, e.co.ID, v.ID, nil); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s arquiva: %v", r, err)
		}
	}
	stranger := mkUser(t, e, "fora@x.com", domain.RoleMember)
	if _, err := e.svc.ListEnvironments(as(stranger), e.co.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("não membro: %v", err)
	}
}

// fakeSSHBin grava um "ssh" que roda o comando remoto com sh nesta máquina (ver execenv).
func fakeSSHBin(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("sem tar")
	}
	p := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = \"--\" ]; then shift; break; fi; shift; done\nshift\nexec sh -c \"$1\"\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil { //nolint:gosec // script de teste executável
		t.Fatal(err)
	}
	return p
}

func TestEnvironmentProbeAndLeases(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	e.svc.Environments.SSHBin = fakeSSHBin(t)
	in := sshInput("Build")
	in.SSH.Root = filepath.Join(t.TempDir(), "remoto")
	v, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	pv, err := e.svc.ProbeEnvironment(ctx, domain.HumanActor, e.co.ID, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pv.LastProbeOK == nil || !*pv.LastProbeOK || pv.LastProbeReport == nil || pv.LastProbeReport.System == "" || pv.LastProbeAt == nil {
		t.Fatalf("probe: %+v", pv)
	}
	draft := sshInput("Rascunho")
	draft.SSH.Root = in.SSH.Root
	rep, err := e.svc.ProbeEnvironmentDraft(ctx, e.co.ID, draft)
	if err != nil || !rep.OK {
		t.Fatalf("rascunho: %+v %v", rep, err)
	}
	if list, _ := e.svc.ListEnvironments(ctx, e.co.ID); len(list) != 1 {
		t.Fatal("o rascunho não pode ser gravado")
	}

	// uso por uma run
	p := e.persona(t, "Ana", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	run, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AcquireEnvironmentLease(ctx, e.co.ID, v.ID, run.ID, "/srv/x"); err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.ListEnvironments(ctx, e.co.ID)
	if list[0].ActiveLeases != 1 {
		t.Fatalf("uso ativo: %+v", list[0])
	}
	ri, err := e.svc.RunEnvironmentOf(ctx, e.co.ID, run.ID)
	if err != nil || ri.Name != "Build" || ri.Lease.Status != domain.LeaseActive {
		t.Fatalf("ambiente da run: %+v %v", ri, err)
	}
	if err := e.svc.ReleaseEnvironmentLease(ctx, e.co.ID, run.ID, "sincronização falhou"); err != nil {
		t.Fatal(err)
	}
	leases, err := e.svc.ListEnvironmentLeases(ctx, e.co.ID, v.ID, 10)
	if err != nil || len(leases) != 1 || leases[0].Status != domain.LeaseFailed || leases[0].FailureReason != "sincronização falhou" || leases[0].RemoteDir != "/srv/x" {
		t.Fatalf("usos: %+v %v", leases, err)
	}
	if _, err := e.svc.RunEnvironmentOf(ctx, e.co.ID, domain.NewID()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("run local: %v", err)
	}
}

func TestEnvironmentRekey(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	v, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Build"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(keyTwo, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	n, err := e.svc.RekeyIntegrationSecrets(ctx)
	if err != nil || n != 1 {
		t.Fatalf("rekey: %d %v", n, err)
	}
	only, _ := secretbox.New(keyTwo, "")
	e.svc.Integrations.Secrets = only
	p := e.persona(t, "Bia", nil)
	if _, err := e.svc.SetPersonaEnvironment(ctx, domain.HumanActor, e.co.ID, p.ID, &v.ID); err != nil {
		t.Fatal(err)
	}
	re, err := e.svc.RunEnvironment(ctx, e.co.ID, p.ID)
	if err != nil || re.Target.PrivateKey != envTestKey {
		t.Fatalf("segredo depois da rotação: %v", err)
	}
}
