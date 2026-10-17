//go:build integration

package heartbeat

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// writeFakeSSH grava um "ssh" que roda o comando remoto com sh nesta máquina. failOn, quando não vazio, faz o
// "ssh" falhar nos comandos que contêm o texto (para simular falha de sincronização).
func writeFakeSSH(t *testing.T, failOn string) string {
	t.Helper()
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("sem tar")
	}
	p := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = \"--\" ]; then shift; break; fi; shift; done\nshift\n"
	if failOn != "" {
		script += "case \"$1\" in *'" + failOn + "'*) echo 'falha simulada' >&2; exit 7;; esac\n"
	}
	script += "exec sh -c \"$1\"\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil { //nolint:gosec // script de teste executável
		t.Fatal(err)
	}
	return p
}

type envStack struct {
	svc  *service.Service
	spy  *spyAdapter
	co   domain.Company
	root string // raiz "remota"
	env  service.EnvironmentView
}

func newEnvStack(t *testing.T) *envStack {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	hb, err := New(pool, Options{Workers: 2, PeriodicInterval: time.Hour, RunTimeout: time.Minute, PublicURL: "http://127.0.0.1:7420"})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-env", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations.Secrets = box
	root := filepath.Join(t.TempDir(), "remoto")
	svc.Environments = execenv.Config{SSHAllowHosts: []string{"build.example.com"}, RemoteURL: "https://hanko.example.com",
		SSHBin: writeFakeSSH(t, ""), LookPath: func(s string) (string, error) { return s, nil }}
	spy := &spyAdapter{Adapter: fake.New(fake.Options{Delay: -1})}
	hb.Attach(svc, adapter.Registry{"fake": spy}, memory.Noop{}, broker, authForTest())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := svc.CreateEnvironment(ctx, domain.HumanActor, co.ID, service.CreateEnvironmentInput{Name: "Build", Driver: domain.EnvSSH,
		PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----",
		SSH:        &domain.SSHConfig{Host: "build.example.com", User: "hanko", Root: root, KnownHosts: "build.example.com ssh-ed25519 AAAA"},
		Env:        map[string]string{"SEGREDO_DO_AMBIENTE": "valor-que-nao-pode-vazar"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDefaultEnvironment(ctx, domain.HumanActor, co.ID, &ev.ID); err != nil {
		t.Fatal(err)
	}
	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})
	return &envStack{svc: svc, spy: spy, co: co, root: root, env: ev}
}

// persona contrata uma persona fake com o workspace dado e a configuração do adapter.
func (s *envStack) persona(t *testing.T, name, wsDir string, cfg map[string]any) domain.Persona {
	t.Helper()
	ctx := context.Background()
	raw, _ := json.Marshal(cfg)
	in := service.HirePersonaInput{Name: name, Agent: "fake", AdapterConfig: raw}
	if wsDir != "" {
		ws, err := s.svc.CreateWorkspace(ctx, domain.HumanActor, s.co.ID, service.CreateWorkspaceInput{Name: "WS " + name, Path: wsDir})
		if err != nil {
			t.Fatal(err)
		}
		in.WorkspaceID = &ws.ID
	}
	p, err := s.svc.HirePersona(ctx, domain.HumanActor, s.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (s *envStack) runTask(t *testing.T, p domain.Persona) service.TaskView {
	t.Helper()
	tv, err := s.svc.CreateTask(context.Background(), domain.HumanActor, s.co.ID, service.CreateTaskInput{Title: "Tarefa de " + p.Name, PersonaID: &p.ID, Status: domain.TaskTodo})
	if err != nil {
		t.Fatal(err)
	}
	return tv
}

func (s *envStack) lastRun(t *testing.T, tv service.TaskView, terminal bool) service.RunView {
	t.Helper()
	var out service.RunView
	waitFor(t, 30*time.Second, "run da task", func() bool {
		runs, err := s.svc.ListRuns(context.Background(), s.co.ID, service.RunFilter{TaskID: &tv.ID})
		if err != nil || len(runs) == 0 {
			return false
		}
		out = runs[0]
		return !terminal || runs[0].Status.Terminal()
	})
	return out
}

func (s *envStack) logText(t *testing.T, runID domain.ID) string {
	t.Helper()
	evs, err := s.svc.ListRunEvents(context.Background(), s.co.ID, runID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range evs {
		b.WriteString(e.Text + "\n")
	}
	return b.String()
}

// Uma run de persona fake num ambiente SSH (com um "ssh" que roda local): o workspace sobe, o fake grava um
// arquivo no alvo, o arquivo volta, o diretório remoto é limpo e o uso fica registrado.
func TestRemoteSSHRunEndToEnd(t *testing.T) {
	s := newEnvStack(t)
	ctx := context.Background()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "apagar.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := s.persona(t, "Ana", ws, map[string]any{"touch": "feito.txt"})
	tv := s.runTask(t, p)
	run := s.lastRun(t, tv, true)
	if run.Status != domain.RunDone {
		t.Fatalf("status=%s erro=%q log=%s", run.Status, run.Error, s.logText(t, run.ID))
	}
	if b, err := os.ReadFile(filepath.Join(ws, "feito.txt")); err != nil || string(b) != "fake\n" {
		t.Fatalf("arquivo gravado no alvo não voltou: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(ws, "apagar.txt")); err != nil {
		t.Fatal("arquivo do workspace sumiu sem ter sido apagado no alvo")
	}
	log := s.logText(t, run.ID)
	if !strings.Contains(log, "executando no ambiente Build (ssh hanko@build.example.com:22)") || !strings.Contains(log, "/acme/ws-ana/"+run.ID.String()+"/work") {
		t.Fatalf("log não mostra o ambiente: %s", log)
	}
	if strings.Contains(log, "valor-que-nao-pode-vazar") {
		t.Fatal("segredo do ambiente no log")
	}
	specs := s.spy.all()
	sp := specs[len(specs)-1]
	if sp.Exec == nil || sp.Env["HANKO_URL"] != "https://hanko.example.com" || !strings.Contains(string(sp.MCPConfig), "https://hanko.example.com/mcp") {
		t.Fatalf("spec remoto: exec=%v env=%v mcp=%s", sp.Exec, sp.Env["HANKO_URL"], sp.MCPConfig)
	}
	if _, err := os.Stat(filepath.Join(s.root, "acme", "ws-ana", run.ID.String())); !os.IsNotExist(err) {
		t.Fatal("diretório remoto deveria ser apagado depois de uma run ok")
	}
	ri, err := s.svc.RunEnvironmentOf(ctx, s.co.ID, run.ID)
	if err != nil || ri.Lease.Status != domain.LeaseReleased || ri.Name != "Build" {
		t.Fatalf("uso: %+v %v", ri, err)
	}

	// falha de sincronização na volta: a run falha com o motivo e o diretório remoto fica
	s.svc.Environments.SSHBin = writeFakeSSH(t, "-cf - .")
	tv2 := s.runTask(t, p)
	run2 := s.lastRun(t, tv2, true)
	if run2.Status != domain.RunFailed || !strings.Contains(run2.Error, "sincronização com o ambiente falhou") {
		t.Fatalf("falha de sync: status=%s erro=%q", run2.Status, run2.Error)
	}
	if _, err := os.Stat(filepath.Join(s.root, "acme", "ws-ana", run2.ID.String(), "work")); err != nil {
		t.Fatal("diretório remoto deveria ficar depois de uma falha")
	}
	ri2, _ := s.svc.RunEnvironmentOf(ctx, s.co.ID, run2.ID)
	if ri2.Lease.Status != domain.LeaseFailed || !strings.Contains(ri2.Lease.FailureReason, "trazer o workspace de volta") {
		t.Fatalf("uso com falha: %+v", ri2.Lease)
	}

	// falha no envio: a run falha antes do harness
	s.svc.Environments.SSHBin = writeFakeSSH(t, "-xpf -")
	run3 := s.lastRun(t, s.runTask(t, p), true)
	if run3.Status != domain.RunFailed || !strings.Contains(run3.Error, "enviar o workspace") {
		t.Fatalf("falha de envio: status=%s erro=%q", run3.Status, run3.Error)
	}
}

func TestRemoteSSHRunCancelAndSwitchOff(t *testing.T) {
	s := newEnvStack(t)
	ctx := context.Background()
	p := s.persona(t, "Bia", "", map[string]any{"remote_sleep_sec": 30})
	tv := s.runTask(t, p)
	run := s.lastRun(t, tv, false)
	waitFor(t, 20*time.Second, "run rodando", func() bool {
		r, err := s.svc.GetRun(ctx, s.co.ID, run.ID)
		return err == nil && r.Status == domain.RunRunning && strings.Contains(s.logText(t, run.ID), "executando no ambiente")
	})
	time.Sleep(300 * time.Millisecond)
	if _, err := s.svc.CancelRun(ctx, domain.HumanActor, s.co.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "run cancelada", func() bool {
		r, err := s.svc.GetRun(ctx, s.co.ID, run.ID)
		return err == nil && r.Status.Terminal()
	})
	r, _ := s.svc.GetRun(ctx, s.co.ID, run.ID)
	if r.Status != domain.RunCancelled {
		t.Fatalf("status=%s erro=%q", r.Status, r.Error)
	}
	waitFor(t, 20*time.Second, "uso fechado", func() bool {
		ri, err := s.svc.RunEnvironmentOf(ctx, s.co.ID, run.ID)
		return err == nil && ri.Lease.Status == domain.LeaseFailed
	})

	// o operador desliga o SSH: a run falha com o motivo, sem rodar o harness
	s.svc.Environments.SSHAllowHosts = nil
	run2 := s.lastRun(t, s.runTask(t, p), true)
	if run2.Status != domain.RunFailed || !strings.Contains(run2.Error, "HANKO_ENV_SSH_ALLOW_HOSTS") {
		t.Fatalf("ssh desligado: status=%s erro=%q", run2.Status, run2.Error)
	}

	// sem URL pública o ambiente remoto recusa
	s.svc.Environments.SSHAllowHosts = []string{"*"}
	s.svc.Environments.RemoteURL = "http://127.0.0.1:7420"
	run3 := s.lastRun(t, s.runTask(t, p), true)
	if run3.Status != domain.RunFailed || !strings.Contains(run3.Error, "HANKO_PUBLIC_URL") {
		t.Fatalf("sem URL pública: status=%s erro=%q", run3.Status, run3.Error)
	}

	// persona no local explícito (padrão local): roda como sempre
	if err := s.svc.SetDefaultEnvironment(ctx, domain.HumanActor, s.co.ID, nil); err != nil {
		t.Fatal(err)
	}
	local := s.persona(t, "Caio", "", nil)
	run4 := s.lastRun(t, s.runTask(t, local), true)
	if run4.Status != domain.RunDone {
		t.Fatalf("local: status=%s erro=%q", run4.Status, run4.Error)
	}
	specs := s.spy.all()
	if specs[len(specs)-1].Exec != nil {
		t.Fatal("run local recebeu alvo remoto")
	}
}
