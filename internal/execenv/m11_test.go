package execenv

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// ---- docker: cliente fake (sem daemon) ----

// fakeDocker é um DockerClient que não precisa de daemon: guarda as operações curtas e faz Command rodar o
// "contêiner" como um processo sh local no diretório montado em /work.
type fakeDocker struct {
	mu    sync.Mutex
	calls [][]string
	envs  [][]string
	stdin []string
	fail  map[string]error
}

func (f *fakeDocker) Available() error { return nil }

func (f *fakeDocker) record(args []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
}

func (f *fakeDocker) Command(args []string, env []string) procrun.Invocation {
	f.record(args)
	f.mu.Lock()
	f.envs = append(f.envs, env)
	f.mu.Unlock()
	// localiza o diretório do host montado em /work e o comando depois da imagem
	work := ""
	i := 0
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
	// args[i] é a imagem; o resto, o comando
	cmd := args[i+1:]
	return procrun.Invocation{Bin: "sh", Args: append([]string{"-c", `cd "$1" && shift && exec "$@"`, "sh", work}, cmd...), Env: append([]string{"PATH=" + os.Getenv("PATH")}, env...)}
}

func (f *fakeDocker) Do(_ context.Context, _ time.Duration, out io.Writer, args ...string) error {
	f.record(args)
	if err := f.fail[args[0]]; err != nil {
		return err
	}
	if args[0] == "version" && out != nil {
		_, _ = io.WriteString(out, "27.1.0\n")
	}
	return nil
}

func (f *fakeDocker) DoStdin(_ context.Context, _ time.Duration, stdin io.Reader, out io.Writer, args ...string) error {
	f.record(args)
	b, _ := io.ReadAll(stdin)
	f.mu.Lock()
	f.stdin = append(f.stdin, string(b))
	f.mu.Unlock()
	if err := f.fail[args[0]]; err != nil {
		return err
	}
	return nil
}

func (f *fakeDocker) first(verb string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c[0] == verb {
			return c
		}
	}
	return nil
}

func dockerCfg(fd *fakeDocker) Config {
	return Config{Sandbox: "docker", Docker: fd}
}

func TestSandboxWithFakeDockerClient(t *testing.T) {
	fd := &fakeDocker{}
	local := localWorkspace(t)
	tg := Target{Driver: domain.EnvSandbox, Name: "sb", Sandbox: domain.SandboxConfig{Image: "ghcr.io/acme/agente@sha256:" + strings.Repeat("a", 64), Network: "bridge"},
		Env: map[string]string{"API_KEY": "segredo"}}
	sess, err := Open(dockerCfg(fd), tg, RunInfo{RunID: "0190run", Company: "acme", LocalDir: local})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	var out []string
	pr, err := procrun.Run(context.Background(), procrun.Options{Bin: "sh", Args: []string{"-c", `printf '%s|%s' "$API_KEY" "$HANKO_RUN_TOKEN" > feito.txt && pwd`},
		Target: sess, TargetEnv: map[string]string{"HANKO_RUN_TOKEN": "tok"}, OnStdout: func(l []byte) { out = append(out, string(l)) }})
	if err != nil || pr.ExitCode != 0 {
		t.Fatalf("run: %+v %v", pr, err)
	}
	// o "contêiner" rodou no workspace montado e viu as variáveis (que vão pelo ambiente do processo, não pelo argv)
	if b, err := os.ReadFile(filepath.Join(local, "feito.txt")); err != nil || string(b) != "segredo|tok" {
		t.Fatalf("saída do contêiner: %q %v", b, err)
	}
	run := fd.first("run")
	j := strings.Join(run, " ")
	for _, want := range []string{"--cap-drop ALL", "--security-opt no-new-privileges", "--read-only", "--network bridge", "--user ", "--pids-limit 512", "--memory 2048m"} {
		if !strings.Contains(j, want) {
			t.Errorf("faltou %q no argv do contêiner: %s", want, j)
		}
	}
	if strings.Contains(j, "segredo") || strings.Contains(j, "tok") {
		t.Fatalf("valor de variável no argv: %s", j)
	}
	if err := sess.Finish(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if rm := fd.first("rm"); !slices.Equal(rm, []string{"rm", "-f", "hanko-run-0190run"}) {
		t.Fatalf("fim da run deve remover o contêiner: %v", rm)
	}
	sess.Abort(context.Background())
	if k := fd.first("kill"); !slices.Equal(k, []string{"kill", "hanko-run-0190run"}) {
		t.Fatalf("abort deve matar o contêiner: %v", k)
	}
}

func TestSandboxProbeAndScriptWithFakeDocker(t *testing.T) {
	fd := &fakeDocker{}
	tg := Target{Driver: domain.EnvSandbox, Name: "sb", Sandbox: domain.SandboxConfig{Image: "alpine:3"}}
	rep := Probe(context.Background(), dockerCfg(fd), tg)
	// o fake não imprime nada do script: o passo da imagem passa, mas o sistema não vem — o relatório não é ok
	if rep.OK {
		t.Fatalf("sem saída do script o teste não pode passar: %+v", rep)
	}
	if v := fd.first("version"); v == nil {
		t.Fatal("o teste deve perguntar a versão do docker")
	}
	if !slices.Contains(rep.Capabilities, "isolated") {
		t.Fatalf("capacidades: %v", rep.Capabilities)
	}
	// o script do readiness vai por stdin, com o valor da variável só ali
	if _, err := RunScript(context.Background(), dockerCfg(fd), tg, "echo oi", map[string]string{"TOKEN": "valor-secreto"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range fd.calls {
		if strings.Contains(strings.Join(c, " "), "valor-secreto") {
			t.Fatalf("valor de variável no argv: %v", c)
		}
	}
	if len(fd.stdin) != 1 || !strings.Contains(fd.stdin[0], "export TOKEN='valor-secreto'") || !strings.Contains(fd.stdin[0], "echo oi") {
		t.Fatalf("stdin do script: %q", fd.stdin)
	}
	cfg := dockerCfg(fd)
	cfg.Sandbox = ""
	if _, err := RunScript(context.Background(), cfg, tg, "true", nil); err == nil {
		t.Fatal("sandbox desligado deve falhar")
	}
}

// ---- local explícito ----

func TestRunScriptLocal(t *testing.T) {
	out, err := RunScript(context.Background(), Config{}, Target{Driver: domain.EnvLocal}, `echo "v=$MINHA"`, map[string]string{"MINHA": "x y"})
	if err != nil || strings.TrimSpace(string(out)) != "v=x y" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := RunScript(context.Background(), Config{}, Target{Driver: domain.EnvLocal}, "exit 3", nil); err == nil {
		t.Fatal("exit != 0 deve ser erro")
	}
	rep := Probe(context.Background(), Config{}, Target{Driver: domain.EnvLocal, Name: "local"})
	if !rep.OK || rep.System == "" || !slices.Contains(rep.Capabilities, "persistent") {
		t.Fatalf("probe local: %+v", rep)
	}
}

func TestRunScriptSSH(t *testing.T) {
	bin := fakeSSH(t)
	out, err := RunScript(context.Background(), sshConfig(bin), sshTarget(t.TempDir()), `echo "t=$TOKEN"`, map[string]string{"TOKEN": "abc"})
	if err != nil || strings.TrimSpace(string(out)) != "t=abc" {
		t.Fatalf("%q %v", out, err)
	}
}

// ---- cloud (Daytona) contra httptest ----

type fakeDaytona struct {
	mu        sync.Mutex
	reqs      []string
	bodies    []map[string]any
	auth      []string
	polls     int
	readyAt   int
	state     string
	deleted   []string
	sshStatus int
}

func (f *fakeDaytona) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.RequestURI())
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer chave-daytona" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"invalid key chave-daytona"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/sandbox":
			_, _ = io.WriteString(w, `{"items":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/sandbox":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			f.bodies = append(f.bodies, b)
			st := "started"
			if f.readyAt > 0 {
				st = "starting"
			}
			if f.state != "" {
				st = f.state
			}
			_, _ = io.WriteString(w, `{"id":"sb-123","state":"`+st+`"}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/sandbox/sb-123"):
			f.polls++
			st := "starting"
			if f.polls >= f.readyAt {
				st = "started"
			}
			_, _ = io.WriteString(w, `{"id":"sb-123","state":"`+st+`"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/ssh-access"):
			if f.sshStatus != 0 {
				w.WriteHeader(f.sshStatus)
				_, _ = io.WriteString(w, `{"message":["sem permissão"]}`)
				return
			}
			_, _ = io.WriteString(w, `{"token":"tok_ABC123","expiresAt":"2030-01-01T00:00:00Z"}`)
		case r.Method == http.MethodDelete:
			f.deleted = append(f.deleted, r.URL.Path)
			if strings.Contains(r.URL.Path, "gone") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newDaytonaCfg(t *testing.T, f *fakeDaytona, sshBin string) Config {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	d, err := NewDaytona(DaytonaOptions{APIURL: srv.URL + "/api", SSHHost: "gw.example.com", KnownHosts: "gw.example.com ssh-ed25519 AAAAC3Nza",
		PollEvery: 5 * time.Millisecond, ReadyWait: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return Config{SSHBin: sshBin, LookPath: func(s string) (string, error) { return s, nil }, Cloud: d}
}

func cloudTarget() Target {
	return Target{Driver: domain.EnvCloud, Name: "nuvem", APIKey: "chave-daytona", Cloud: domain.CloudConfig{Provider: "daytona", Network: "full"},
		Env: map[string]string{"AMBIENTE": "da-nuvem"}}
}

func TestCloudSessionRunsSyncsAndDeletes(t *testing.T) {
	f := &fakeDaytona{readyAt: 2}
	cfg := newDaytonaCfg(t, f, fakeSSH(t))
	tg := cloudTarget()
	tg.Cloud.Root = filepath.Join(t.TempDir(), "remote") // o ssh fake roda aqui mesmo
	local := localWorkspace(t)
	sess, err := Open(cfg, tg, RunInfo{RunID: "0190cloud", Company: "acme", Workspace: "web", LocalDir: local})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if !strings.HasPrefix(sess.RemoteDir(), "daytona:(não criado):") {
		t.Fatalf("antes do primeiro Prepare não há sandbox: %s", sess.RemoteDir())
	}
	pr, err := procrun.Run(context.Background(), procrun.Options{Bin: "sh", Args: []string{"-c", `printf '%s' "$AMBIENTE" > sub.txt && rm old.txt`}, Target: sess})
	if err != nil || pr.ExitCode != 0 {
		t.Fatalf("run: %+v %v", pr, err)
	}
	if !strings.Contains(sess.RemoteDir(), "daytona:sb-123:") {
		t.Fatalf("o registro de uso deve nomear o sandbox: %s", sess.RemoteDir())
	}
	if err := sess.Finish(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(local, "sub.txt")); err != nil || string(b) != "da-nuvem" {
		t.Fatalf("o trabalho deve voltar: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(local, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("a remoção feita no sandbox deve voltar")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.deleted) != 1 || f.deleted[0] != "/api/sandbox/sb-123" {
		t.Fatalf("run ok apaga o sandbox: %v", f.deleted)
	}
	b := f.bodies[0]
	if b["cpu"] != float64(2) || b["memory"] != float64(4) || b["disk"] != float64(10) || b["networkBlockAll"] != false || b["public"] != false {
		t.Fatalf("pedido de criação: %v", b)
	}
	if b["autoStopInterval"] != float64(15) || b["autoDeleteInterval"] != float64(60) {
		t.Fatalf("rede de segurança contra vazamento ausente: %v", b)
	}
	for _, a := range f.auth {
		if a != "Bearer chave-daytona" {
			t.Fatalf("autorização: %q", a)
		}
	}
	if f.polls < 1 {
		t.Fatal("deveria esperar o sandbox ficar pronto")
	}
}

func TestCloudFailedRunKeepsSandbox(t *testing.T) {
	f := &fakeDaytona{}
	cfg := newDaytonaCfg(t, f, fakeSSH(t))
	tg := cloudTarget()
	tg.Cloud.Root = filepath.Join(t.TempDir(), "remote")
	sess, err := Open(cfg, tg, RunInfo{RunID: "0190fail", Company: "acme", LocalDir: localWorkspace(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := procrun.Run(context.Background(), procrun.Options{Bin: "true", Target: sess}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Finish(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.deleted) != 0 {
		t.Fatalf("run com falha mantém o sandbox para diagnóstico: %v", f.deleted)
	}
}

func TestCloudProvisionFailures(t *testing.T) {
	// estado de erro na criação: o sandbox é apagado e a run não começa
	f := &fakeDaytona{state: "error"}
	cfg := newDaytonaCfg(t, f, fakeSSH(t))
	tg := cloudTarget()
	tg.Cloud.Root = filepath.Join(t.TempDir(), "r")
	sess, err := Open(cfg, tg, RunInfo{RunID: "0190err", Company: "acme", LocalDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	_, err = procrun.Run(context.Background(), procrun.Options{Bin: "true", Target: sess})
	if err == nil || !strings.Contains(err.Error(), "não ficou pronto") {
		t.Fatalf("criação com erro: %v", err)
	}
	f.mu.Lock()
	if len(f.deleted) != 1 {
		t.Fatalf("sandbox que falhou deve ser apagado: %v", f.deleted)
	}
	f.mu.Unlock()
	if err := sess.Finish(context.Background(), false); err != nil {
		t.Fatalf("finish sem sandbox: %v", err)
	}

	// SSH negado: o erro não vaza a chave de API
	f2 := &fakeDaytona{sshStatus: 403}
	cfg2 := newDaytonaCfg(t, f2, fakeSSH(t))
	sess2, err := Open(cfg2, tg, RunInfo{RunID: "0190ssh", Company: "acme", LocalDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer sess2.Close()
	_, err = procrun.Run(context.Background(), procrun.Options{Bin: "true", Target: sess2})
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "chave-daytona") {
		t.Fatalf("ssh negado: %v", err)
	}
}

func TestCloudOffAndMisconfigured(t *testing.T) {
	if _, err := Open(Config{}, cloudTarget(), RunInfo{RunID: "r", Company: "c"}); err != ErrCloudOff {
		t.Fatalf("cloud desligado: %v", err)
	}
	f := &fakeDaytona{}
	cfg := newDaytonaCfg(t, f, "ssh")
	tg := cloudTarget()
	tg.APIKey = ""
	if _, err := Open(cfg, tg, RunInfo{RunID: "r", Company: "c"}); err == nil || !strings.Contains(err.Error(), "chave de API") {
		t.Fatalf("sem chave: %v", err)
	}
	tg = cloudTarget()
	tg.Cloud.Provider = "e2b"
	if _, err := Open(cfg, tg, RunInfo{RunID: "r", Company: "c"}); err == nil {
		t.Fatal("provider desconhecido deve falhar")
	}
	if _, err := NewDaytona(DaytonaOptions{}); err == nil || !strings.Contains(err.Error(), "KNOWN_HOSTS") {
		t.Fatalf("known_hosts do gateway é obrigatório: %v", err)
	}
	if _, err := NewDaytona(DaytonaOptions{APIURL: "ftp://x", KnownHosts: "a b c"}); err == nil {
		t.Fatal("URL de API inválida")
	}
}

func TestCloudProbeAndPing(t *testing.T) {
	f := &fakeDaytona{}
	cfg := newDaytonaCfg(t, f, fakeSSH(t))
	tg := cloudTarget()
	tg.Cloud.Root = filepath.Join(t.TempDir(), "remote")
	rep := Probe(context.Background(), cfg, tg)
	if !rep.OK {
		t.Fatalf("probe cloud: %+v", rep)
	}
	names := map[string]bool{}
	for _, s := range rep.Steps {
		names[s.Name] = s.OK
	}
	for _, n := range []string{"api", "create", "connect", "system", "bin:tar", "cleanup"} {
		if !names[n] {
			t.Errorf("passo %s ausente ou falhou: %+v", n, rep.Steps)
		}
	}
	if !slices.Contains(rep.Capabilities, "isolated") || !slices.Contains(rep.Capabilities, "sync") {
		t.Fatalf("capacidades: %v", rep.Capabilities)
	}
	f.mu.Lock()
	if len(f.deleted) != 1 {
		t.Fatalf("o probe apaga o sandbox que criou: %v", f.deleted)
	}
	f.mu.Unlock()

	bad := tg
	bad.APIKey = "errada"
	rep = Probe(context.Background(), cfg, bad)
	if rep.OK || len(rep.Steps) != 1 || rep.Steps[0].Name != "api" || !strings.Contains(rep.Steps[0].Detail, "recusada") {
		t.Fatalf("chave errada deve falhar já no ping: %+v", rep)
	}
	if strings.Contains(rep.Steps[0].Detail, "errada") {
		t.Fatal("chave no relatório")
	}
}

func TestRunScriptCloud(t *testing.T) {
	f := &fakeDaytona{}
	cfg := newDaytonaCfg(t, f, fakeSSH(t))
	tg := cloudTarget()
	tg.Cloud.Root = filepath.Join(t.TempDir(), "remote")
	out, err := RunScript(context.Background(), cfg, tg, `echo "a=$AMBIENTE"`, nil)
	if err != nil || strings.TrimSpace(string(out)) != "a=da-nuvem" {
		t.Fatalf("%q %v", out, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.deleted) != 1 {
		t.Fatalf("o sandbox do script deve ser apagado: %v", f.deleted)
	}
}

func TestDaytonaDeleteIgnoresMissing(t *testing.T) {
	f := &fakeDaytona{}
	cfg := newDaytonaCfg(t, f, "ssh")
	p := cfg.Cloud.New("chave-daytona")
	if err := p.Delete(context.Background(), "gone-1"); err != nil {
		t.Fatalf("apagar um sandbox que não existe mais não é erro: %v", err)
	}
	if err := cfg.Cloud.New("outra").Delete(context.Background(), "sb-1"); err == nil || strings.Contains(err.Error(), "outra") {
		t.Fatalf("erro de autorização: %v", err)
	}
}

func TestCloudConfigNormalize(t *testing.T) {
	var c domain.CloudConfig
	if err := c.Normalize(); err != nil || c.Provider != "daytona" || c.CPUs != 2 || c.Root != domain.DefaultCloudRoot || c.Network != "full" {
		t.Fatalf("padrões: %+v %v", c, err)
	}
	for _, bad := range []domain.CloudConfig{
		{Provider: "outro"}, {Network: "x"}, {CPUs: 99}, {MemoryGiB: 999}, {DiskGiB: 9999}, {Root: "relativo"}, {Root: "/"}, {Root: "/a/../b"}, {Snapshot: "-x y"},
	} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("aceitou %+v", bad)
		}
	}
}
