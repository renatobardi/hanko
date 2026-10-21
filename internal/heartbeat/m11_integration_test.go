//go:build integration

package heartbeat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// fakeDockerClient é um execenv.DockerClient sem daemon: o "contêiner" é um processo sh no diretório montado em /work.
type fakeDockerClient struct {
	mu    sync.Mutex
	calls [][]string
}

func (f *fakeDockerClient) Available() error { return nil }

func (f *fakeDockerClient) Command(args []string, env []string) procrun.Invocation {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	work, i := "", 0
	for ; i < len(args); i++ {
		if args[i] == "--mount" && strings.Contains(args[i+1], "dst=/work") {
			for _, kv := range strings.Split(args[i+1], ",") {
				if v, ok := strings.CutPrefix(kv, "src="); ok {
					work = v
				}
			}
		}
		if args[i] == "-w" {
			i += 2
			break
		}
	}
	for i < len(args) && args[i] == "-e" {
		i += 2
	}
	cmd := args[i+1:]
	return procrun.Invocation{Bin: "sh", Args: append([]string{"-c", `cd "$1" && shift && exec "$@"`, "sh", work}, cmd...), Env: append([]string{"PATH=" + os.Getenv("PATH")}, env...)}
}

func (f *fakeDockerClient) Do(_ context.Context, _ time.Duration, _ io.Writer, args ...string) error {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	return nil
}

func (f *fakeDockerClient) ran(verb string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c[0] == verb {
			return true
		}
	}
	return false
}

// daytonaStub é a API do provider em nuvem para o teste: cria, emite acesso SSH e apaga.
func daytonaStub(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var reqs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqs = append(reqs, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/sandbox":
			_, _ = io.WriteString(w, `{"id":"sb-demo","state":"started"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/ssh-access"):
			_, _ = io.WriteString(w, `{"token":"tok_demo"}`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

// Demo do M11: a MESMA persona roda no local explícito, num contêiner (cliente docker fake), via SSH (ssh fake) e num
// sandbox em nuvem (API fake), e cada run guarda onde rodou e por quê.
func TestSamePersonaRunsEverywhere(t *testing.T) {
	ctx := context.Background()
	s := newEnvStack(t) // SSH "Build" é o padrão da empresa
	fd := &fakeDockerClient{}
	srv, cloudReqs := daytonaStub(t)
	d, err := execenv.NewDaytona(execenv.DaytonaOptions{APIURL: srv.URL + "/api", SSHHost: "gw.example.com",
		KnownHosts: "gw.example.com ssh-ed25519 AAAA", PollEvery: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	s.svc.Environments.Docker, s.svc.Environments.Sandbox, s.svc.Environments.Cloud = fd, "docker", d

	ws := t.TempDir()
	p := s.persona(t, "Ana", ws, map[string]any{"touch": "feito.txt"})
	mk := func(in service.CreateEnvironmentInput) service.EnvironmentView {
		v, err := s.svc.CreateEnvironment(ctx, domain.HumanActor, s.co.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	local := mk(service.CreateEnvironmentInput{Name: "Servidor", Driver: domain.EnvLocal, Env: map[string]string{"DA_LOCAL": "sim"}})
	box := mk(service.CreateEnvironmentInput{Name: "Caixa", Driver: domain.EnvSandbox, Sandbox: &domain.SandboxConfig{Image: "ghcr.io/acme/agente@sha256:" + strings.Repeat("b", 64), Network: "bridge"}})
	cloud := mk(service.CreateEnvironmentInput{Name: "Nuvem", Driver: domain.EnvCloud, APIKey: "chave-demo",
		Cloud: &domain.CloudConfig{Root: filepath.Join(t.TempDir(), "nuvem")}})

	cases := []struct {
		name     string
		env      *service.EnvironmentView // nil = o padrão da empresa (SSH)
		wantLog  string
		wantSrc  domain.EnvSource
		afterRun func()
	}{
		{"local explícito", &local, "executando no ambiente Servidor (local, origem: persona)", domain.EnvFromPersona, nil},
		{"contêiner", &box, "executando no ambiente Caixa (sandbox ghcr.io/acme/agente", domain.EnvFromPersona, func() {
			if !fd.ran("run") {
				t.Error("o cliente docker deveria ter recebido o run")
			}
			if !fd.ran("rm") {
				t.Error("o contêiner deveria ser removido no fim")
			}
		}},
		{"ssh (padrão da empresa)", nil, "executando no ambiente Build (ssh hanko@build.example.com:22)", domain.EnvFromDefault, nil},
		{"nuvem", &cloud, "executando no ambiente Nuvem (sandbox daytona", domain.EnvFromPersona, func() {
			if got := strings.Join(*cloudReqs, ","); !strings.Contains(got, "POST /api/sandbox") || !strings.Contains(got, "ssh-access") || !strings.Contains(got, "DELETE /api/sandbox/sb-demo") {
				t.Errorf("chamadas ao provider: %s", got)
			}
		}},
	}
	for _, c := range cases {
		var id *domain.ID
		if c.env != nil {
			id = &c.env.ID
		}
		if _, err := s.svc.SetPersonaEnvironment(ctx, domain.HumanActor, s.co.ID, p.ID, id); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(filepath.Join(ws, "feito.txt"))
		tv := s.runTask(t, p)
		run := s.lastRun(t, tv, true)
		log := s.logText(t, run.ID)
		if run.Status != domain.RunDone {
			t.Fatalf("%s: status=%s erro=%q log=%s", c.name, run.Status, run.Error, log)
		}
		if !strings.Contains(log, c.wantLog) {
			t.Fatalf("%s: o log deveria dizer onde rodou (%q):\n%s", c.name, c.wantLog, log)
		}
		if b, err := os.ReadFile(filepath.Join(ws, "feito.txt")); err != nil || string(b) != "fake\n" {
			t.Fatalf("%s: o arquivo gravado no alvo não voltou: %q %v", c.name, b, err)
		}
		full, err := s.svc.GetRun(ctx, s.co.ID, run.ID)
		if err != nil || full.EnvironmentSource != c.wantSrc {
			t.Fatalf("%s: origem gravada na run: %+v %v", c.name, full.EnvironmentSource, err)
		}
		ri, err := s.svc.RunEnvironmentOf(ctx, s.co.ID, run.ID)
		if err != nil || ri.Lease.Status != domain.LeaseReleased {
			t.Fatalf("%s: uso: %+v %v", c.name, ri, err)
		}
		if c.afterRun != nil {
			c.afterRun()
		}
	}
	// a variável do ambiente local chegou ao harness local
	var saw bool
	for _, sp := range s.spy.all() {
		if sp.Env["DA_LOCAL"] == "sim" && sp.Exec == nil {
			saw = true
		}
	}
	if !saw {
		t.Fatal("as variáveis do ambiente local deveriam chegar ao harness")
	}
}

// Auth quebrada aparece ANTES do run: a persona não está pronta, nenhuma run abre, o alerta existe; consertado o login,
// o teste passa e a task é executada.
func TestBrokenAuthShowsBeforeRun(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	hb, err := New(pool, Options{Workers: 2, PeriodicInterval: time.Hour, WakeSnooze: 200 * time.Millisecond, RunTimeout: time.Minute, PublicURL: "http://127.0.0.1:7420"})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	authOK := false
	var mu sync.Mutex
	svc.ReadinessRunner = func(context.Context, execenv.Target, string, map[string]string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if authOK {
			return []byte("bin=/usr/bin/claude\nversion=2.0\nauth=login\n"), nil
		}
		return []byte("bin=/usr/bin/claude\nversion=2.0\nauth=none\n"), nil
	}
	spy := &spyAdapter{Adapter: fake.New(fake.Options{Delay: -1})}
	hb.Attach(svc, adapter.Registry{"claude": spy}, memory.Noop{}, broker, authForTest())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
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
	raw, _ := json.Marshal(map[string]any{})
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "claude", AdapterConfig: raw})
	if err != nil {
		t.Fatal(err)
	}
	rd, err := svc.TestPersonaReadiness(ctx, domain.HumanActor, co.ID, p.ID)
	if err != nil || rd.Status != domain.ReadinessNotReady || !strings.HasPrefix(rd.Reason, "auth:") {
		t.Fatalf("o teste deve apontar a auth quebrada: %+v %v", rd, err)
	}
	tv, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "Tarefa", PersonaID: &p.ID, Status: domain.TaskTodo})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // dá tempo ao wake de tentar e ser recusado
	runs, err := svc.ListRuns(ctx, co.ID, service.RunFilter{TaskID: &tv.ID})
	if err != nil || len(runs) != 0 {
		t.Fatalf("persona não pronta não pode abrir run: %+v %v", runs, err)
	}
	if len(spy.all()) != 0 {
		t.Fatal("o harness não deveria ter rodado")
	}
	if _, err := svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	al, err := svc.ListOpenAlerts(ctx, co.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range al {
		if a.Kind == domain.AlertPersonaNotReady && a.SubjectID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("alerta persona_not_ready ausente: %+v", al)
	}

	// conserta o login, testa de novo e acorda a task: agora roda
	mu.Lock()
	authOK = true
	mu.Unlock()
	if rd, err = svc.TestPersonaReadiness(ctx, domain.HumanActor, co.ID, p.ID); err != nil || rd.Status != domain.ReadinessReady {
		t.Fatalf("depois de consertar: %+v %v", rd, err)
	}
	if err := svc.WakeTask(ctx, domain.HumanActor, co.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "run depois de consertar a auth", func() bool {
		runs, err := svc.ListRuns(ctx, co.ID, service.RunFilter{TaskID: &tv.ID})
		return err == nil && len(runs) > 0 && runs[0].Status == domain.RunDone
	})
}
