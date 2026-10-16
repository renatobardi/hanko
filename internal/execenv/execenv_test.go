package execenv

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

const testKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----"

// fakeSSH grava um "ssh" de teste: ignora as opções até "--", descarta o host e roda o comando remoto com sh
// nesta máquina. Assim o caminho inteiro (argv, quoting, tar por stdin/stdout, cancelamento) é exercitado
// sem rede.
func fakeSSH(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sem sh")
	}
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

func sshTarget(root string) Target {
	return Target{Driver: domain.EnvSSH, Name: "build", PrivateKey: testKey,
		SSH: domain.SSHConfig{Host: "build.example.com", User: "hanko", Root: root, KnownHosts: "build.example.com ssh-ed25519 AAAAC3Nza"}}
}

func sshConfig(bin string) Config {
	return Config{SSHAllowHosts: []string{".example.com"}, SSHBin: bin, LookPath: func(s string) (string, error) { return s, nil }}
}

func TestQuote(t *testing.T) {
	for _, v := range []string{"", "a b", "it's", `$(rm -rf /)`, "`x`", `\n"q"`, "-x", "a'b'c"} {
		out, err := exec.Command("sh", "-c", "printf %s "+Quote(v)).Output()
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != v {
			t.Errorf("Quote(%q) => sh devolveu %q", v, out)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	allow := []string{"build.example.com", ".corp.local", "*.lab.io", "10.0.0.0/8", "192.168.1.5"}
	cases := map[string]bool{
		"build.example.com": true, "BUILD.example.com.": true, "x.build.example.com": false,
		"a.corp.local": true, "corp.local": false, "b.lab.io": true, "lab.io": false,
		"10.1.2.3": true, "11.0.0.1": false, "192.168.1.5": true, "192.168.1.6": false,
		"evil.com": false, "": false,
	}
	for h, want := range cases {
		if got := hostAllowed(allow, h); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", h, got, want)
		}
	}
	if !hostAllowed([]string{"*"}, "qualquer.um") {
		t.Error("* deveria liberar tudo")
	}
	c := Config{}
	if err := c.CheckSSHHost("build.example.com"); !errors.Is(err, ErrSSHOff) {
		t.Errorf("lista vazia deveria desligar o SSH: %v", err)
	}
	c.SSHAllowHosts = allow
	if err := c.CheckSSHHost("evil.com"); err == nil || !strings.Contains(err.Error(), "HANKO_ENV_SSH_ALLOW_HOSTS") {
		t.Errorf("host fora da lista: %v", err)
	}
}

func TestSSHArgsAreStrict(t *testing.T) {
	s, err := openSSH(sshConfig("ssh"), sshTarget("/srv/hanko"), RunInfo{RunID: "r1", Company: "acme", Workspace: "web"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	args := s.Args("echo oi")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-F none", "BatchMode=yes", "StrictHostKeyChecking=yes", "UserKnownHostsFile=" + s.kh,
		"IdentitiesOnly=yes", "IdentityFile=" + s.key, "ConnectTimeout=10", "ServerAliveInterval=15", "ForwardAgent=no", "-p 22", "-l hanko"} {
		if !strings.Contains(joined, want) {
			t.Errorf("faltou %q em %v", want, args)
		}
	}
	if strings.Contains(joined, "StrictHostKeyChecking=no") || strings.Contains(joined, "accept-new") {
		t.Fatal("verificação de chave do host afrouxada")
	}
	if i := slices.Index(args, "--"); i < 0 || args[i+1] != "build.example.com" || args[len(args)-1] != "echo oi" {
		t.Fatalf("host e comando remoto fora do lugar: %v", args)
	}
	if s.RemoteDir() != "/srv/hanko/acme/web/r1" || s.WorkDir() != "/srv/hanko/acme/web/r1/work" {
		t.Fatalf("layout remoto: %s %s", s.RemoteDir(), s.WorkDir())
	}
	for _, f := range []string{s.key, s.kh} {
		st, err := os.Stat(f)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, st, err)
		}
	}
	s.Close()
	if _, err := os.Stat(s.key); !os.IsNotExist(err) {
		t.Fatal("chave temporária sobreviveu ao Close")
	}
}

func TestRemoteCommandQuotesEverything(t *testing.T) {
	s, err := openSSH(sshConfig("ssh"), sshTarget("/srv/h"), RunInfo{RunID: "r1", Company: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cmd := s.RemoteCommand("claude", []string{"-p", "it's $(bad)", "--append-system-prompt", "a\nb"})
	for _, want := range []string{"cd '/srv/h/acme/_sem-workspace/r1/work'", "exec 'claude' '-p' 'it'\\''s $(bad)'", ". '/srv/h/acme/_sem-workspace/r1/.hanko/env.sh'", "rm -f"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("faltou %q em %s", want, cmd)
		}
	}
	ef, err := envFile(map[string]string{"TOKEN": "x'; rm -rf /; '", "A": "1"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", ef+`printf %s "$TOKEN"`).Output()
	if err != nil || string(out) != "x'; rm -rf /; '" {
		t.Fatalf("envFile: %q %v", out, err)
	}
	if _, err := envFile(map[string]string{"BAD-NAME": "x"}); err == nil {
		t.Fatal("nome de variável inválido aceito")
	}
}

// localWorkspace monta um workspace com arquivo, subpasta, link e um arquivo que o "agente" vai apagar.
func localWorkspace(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	must(t, os.WriteFile(filepath.Join(d, "README.md"), []byte("oi\n"), 0o644)) //nolint:gosec // arquivo de teste
	must(t, os.MkdirAll(filepath.Join(d, ".git", "refs"), 0o755))
	must(t, os.WriteFile(filepath.Join(d, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)) //nolint:gosec // arquivo de teste
	must(t, os.WriteFile(filepath.Join(d, "old.txt"), []byte("apagar"), 0o644))                      //nolint:gosec // arquivo de teste
	must(t, os.Symlink("README.md", filepath.Join(d, "LEIA")))
	return d
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSSHSessionSyncsWorkspaceBothWays(t *testing.T) {
	bin := fakeSSH(t)
	root := filepath.Join(t.TempDir(), "remote")
	local := localWorkspace(t)
	tg := sshTarget(root)
	tg.Env = map[string]string{"AMBIENTE": "do-ambiente", "SOBRESCRITA": "ambiente"}
	sess, err := Open(sshConfig(bin), tg, RunInfo{RunID: "0190aaaa-bbbb", Company: "acme", Workspace: "web", LocalDir: local})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	auxL, auxR := sess.AuxDir()
	must(t, os.WriteFile(filepath.Join(auxL, "mcp.json"), []byte("{\"mcpServers\":{}}\n"), 0o600))

	var out []string
	script := `test -f .git/HEAD && test -L LEIA && cat "$1" && rm old.txt && mkdir -p sub && printf '%s|%s|%s' "$AMBIENTE" "$SOBRESCRITA" "$HANKO_RUN_TOKEN" > sub/env.txt && pwd`
	pr, err := procrun.Run(context.Background(), procrun.Options{Bin: "sh", Args: []string{"-c", script, "sh", auxR + "/mcp.json"},
		Target: sess, TargetEnv: map[string]string{"HANKO_RUN_TOKEN": "tok", "SOBRESCRITA": "run"},
		OnStdout: func(l []byte) { out = append(out, string(l)) }, OnStderr: func(l []byte) { t.Log("stderr:", string(l)) }})
	if err != nil || pr.ExitCode != 0 {
		t.Fatalf("run: %+v %v out=%v", pr, err, out)
	}
	if len(out) != 2 || out[0] != `{"mcpServers":{}}` || out[1] != sess.WorkDir() {
		t.Fatalf("saída: %v", out)
	}
	if _, err := os.Stat(filepath.Join(auxL, "env.sh")); !os.IsNotExist(err) {
		t.Fatal("env.sh local sobreviveu ao envio")
	}
	if _, err := os.Stat(filepath.Join(sess.RemoteDir(), ".hanko", "env.sh")); !os.IsNotExist(err) {
		t.Fatal("env.sh remoto não foi apagado depois de lido")
	}
	if err := sess.Finish(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(local, "sub", "env.txt"))
	if err != nil || string(b) != "do-ambiente|run|tok" {
		t.Fatalf("arquivo novo não voltou: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(local, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("arquivo apagado no alvo continuou local")
	}
	if l, err := os.Readlink(filepath.Join(local, "LEIA")); err != nil || l != "README.md" {
		t.Fatalf("link: %q %v", l, err)
	}
	if _, err := os.Stat(sess.RemoteDir()); !os.IsNotExist(err) {
		t.Fatal("diretório remoto não foi limpo depois de uma run ok")
	}
}

func TestSSHSyncFailureKeepsRemote(t *testing.T) {
	bin := fakeSSH(t)
	root := filepath.Join(t.TempDir(), "remote")
	sess, err := Open(sshConfig(bin), sshTarget(root), RunInfo{RunID: "r2", Company: "acme", LocalDir: localWorkspace(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// o "agente" some com o diretório de trabalho: a volta falha
	pr, err := procrun.Run(context.Background(), procrun.Options{Bin: "sh", Args: []string{"-c", "cd .. && rm -rf work"}, Target: sess})
	if err != nil || pr.ExitCode != 0 {
		t.Fatalf("%+v %v", pr, err)
	}
	err = sess.Finish(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "trazer o workspace de volta") {
		t.Fatalf("falha de sincronização deveria aparecer: %v", err)
	}
	if _, err := os.Stat(sess.RemoteDir()); err != nil {
		t.Fatal("diretório remoto deveria ficar para diagnóstico")
	}
	if _, err := os.Stat(filepath.Join(sess.RemoteDir(), ".hanko")); !os.IsNotExist(err) {
		t.Fatal("auxiliares (token) deveriam sair mesmo na falha")
	}
}

func TestSSHUploadFailure(t *testing.T) {
	bin := fakeSSH(t)
	f := filepath.Join(t.TempDir(), "arquivo")
	must(t, os.WriteFile(f, nil, 0o600))
	// a raiz remota é um arquivo: mkdir -p falha e a run não começa
	sess, err := Open(sshConfig(bin), sshTarget(f), RunInfo{RunID: "r3", Company: "acme", LocalDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	_, err = procrun.Run(context.Background(), procrun.Options{Bin: "true", Target: sess})
	if err == nil || !strings.Contains(err.Error(), "enviar o workspace") {
		t.Fatalf("falha de envio: %v", err)
	}
	if err := sess.Finish(context.Background(), false); err != nil {
		t.Fatalf("finish sem envio: %v", err)
	}
}

func TestSSHCancelKillsRemote(t *testing.T) {
	bin := fakeSSH(t)
	sess, err := Open(sshConfig(bin), sshTarget(filepath.Join(t.TempDir(), "r")), RunInfo{RunID: "r4", Company: "acme", LocalDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	pr, err := procrun.Run(ctx, procrun.Options{Bin: "sleep", Args: []string{"30"}, Target: sess, Grace: 200 * time.Millisecond})
	if err != nil || !pr.Cancelled {
		t.Fatalf("cancelamento: %+v %v", pr, err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("cancelamento demorou demais")
	}
}

func TestSwitchesOff(t *testing.T) {
	if _, err := Open(Config{}, sshTarget("/srv"), RunInfo{RunID: "r", Company: "c"}); !errors.Is(err, ErrSSHOff) {
		t.Fatalf("ssh desligado: %v", err)
	}
	cfg := sshConfig("ssh")
	cfg.SSHAllowHosts = []string{"outro.host"}
	if _, err := Open(cfg, sshTarget("/srv"), RunInfo{RunID: "r", Company: "c"}); err == nil || !strings.Contains(err.Error(), "não está na lista") {
		t.Fatalf("host fora da lista: %v", err)
	}
	sb := Target{Driver: domain.EnvSandbox, Name: "sb", Sandbox: domain.SandboxConfig{Image: "alpine:3"}}
	if _, err := Open(Config{}, sb, RunInfo{RunID: "r", Company: "c"}); !errors.Is(err, ErrSandboxOff) {
		t.Fatalf("sandbox desligado: %v", err)
	}
	if err := (Config{Sandbox: "docker", LookPath: func(string) (string, error) { return "", errors.New("x") }}).SandboxStatus(); err == nil {
		t.Fatal("sem binário docker deveria ficar indisponível")
	}
	if err := (Config{RemoteURL: "http://127.0.0.1:7420"}).RemoteURLStatus(); err == nil {
		t.Fatal("loopback não serve para ambiente remoto")
	}
	if err := (Config{RemoteURL: "https://hanko.example.com"}).RemoteURLStatus(); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxArgs(t *testing.T) {
	cfg := Config{Sandbox: "docker", LookPath: func(s string) (string, error) { return s, nil }}
	tg := Target{Driver: domain.EnvSandbox, Name: "sb", Sandbox: domain.SandboxConfig{Image: "ghcr.io/acme/agente:1"}, Env: map[string]string{"API_KEY": "segredo"}}
	local := t.TempDir()
	sess, err := Open(cfg, tg, RunInfo{RunID: "r5", Company: "acme", LocalDir: local})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	inv, err := sess.Prepare(context.Background(), "claude", []string{"-p"}, map[string]string{"HANKO_RUN_TOKEN": "tok", "PATH": "/x"})
	if err != nil {
		t.Fatal(err)
	}
	j := strings.Join(inv.Args, " ")
	for _, want := range []string{"run --rm -i", "--name hanko-run-r5", "--network none", "--memory 2048m", "--cpus 2", "--pids-limit 512",
		"--cap-drop ALL", "--security-opt no-new-privileges", "--read-only", "--tmpfs /tmp", "type=bind,src=" + local + ",dst=/work",
		",dst=/hanko,readonly", "-w /work", "-e API_KEY", "-e HANKO_RUN_TOKEN", "ghcr.io/acme/agente:1 claude -p"} {
		if !strings.Contains(j, want) {
			t.Errorf("faltou %q em %s", want, j)
		}
	}
	if strings.Contains(j, "segredo") || strings.Contains(j, "tok") || strings.Contains(j, "-e PATH") {
		t.Fatalf("valor de variável no argv: %s", j)
	}
	if !slices.Contains(inv.Env, "API_KEY=segredo") || !slices.Contains(inv.Env, "HANKO_RUN_TOKEN=tok") {
		t.Fatalf("valores deveriam ir no ambiente do docker: %v", inv.Env)
	}
	if sess.WorkDir() != "/work" {
		t.Fatal(sess.WorkDir())
	}
}

func TestSandboxHostRoot(t *testing.T) {
	ws := t.TempDir()
	cfg := Config{Sandbox: "docker", WorkspacesRoot: ws, SandboxHostRoot: "/host/ws", LookPath: func(s string) (string, error) { return s, nil }}
	tg := Target{Driver: domain.EnvSandbox, Name: "sb", Sandbox: domain.SandboxConfig{Image: "alpine:3"}}
	if _, err := Open(cfg, tg, RunInfo{RunID: "r6", Company: "acme", LocalDir: t.TempDir()}); err == nil {
		t.Fatal("workspace fora da raiz não deveria montar")
	}
	sess, err := Open(cfg, tg, RunInfo{RunID: "r6", Company: "acme", LocalDir: "/tmp/x", TempDir: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if !under(ws, sess.LocalWorkDir()) {
		t.Fatalf("diretório temporário fora da raiz: %s", sess.LocalWorkDir())
	}
	inv, err := sess.Prepare(context.Background(), "sh", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(inv.Args, " "), "src=/host/ws/.hanko-sandbox/") {
		t.Fatalf("caminho do host não traduzido: %v", inv.Args)
	}
}

func TestExtractRejectsEscapes(t *testing.T) {
	mk := func(hs ...*tar.Header) *bytes.Buffer {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for _, h := range hs {
			must(t, tw.WriteHeader(h))
			if h.Size > 0 {
				_, _ = tw.Write(bytes.Repeat([]byte("x"), int(h.Size)))
			}
		}
		must(t, tw.Close())
		return &b
	}
	dir := t.TempDir()
	if err := extractTar(mk(&tar.Header{Name: "../fora", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}), dir, nil); err == nil {
		t.Fatal(".. aceito")
	}
	if err := extractTar(mk(&tar.Header{Name: "/etc/passwd", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}), dir, nil); err == nil {
		t.Fatal("caminho absoluto aceito")
	}
	outside := t.TempDir()
	err := extractTar(mk(&tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: outside},
		&tar.Header{Name: "l/pwn", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}), dir, nil)
	if err == nil {
		t.Fatal("escrita através de link para fora aceita")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwn")); !os.IsNotExist(err) {
		t.Fatal("arquivo escapou do diretório")
	}
}

func TestProbeSSH(t *testing.T) {
	bin := fakeSSH(t)
	root := filepath.Join(t.TempDir(), "remote")
	cfg := sshConfig(bin)
	rep := Probe(context.Background(), cfg, sshTarget(root))
	if !rep.OK || rep.System == "" {
		t.Fatalf("probe: %+v", rep)
	}
	names := map[string]bool{}
	for _, s := range rep.Steps {
		names[s.Name] = true
	}
	for _, n := range []string{"connect", "system", "workspace_root", "bin:tar", "bin:git", "bin:claude", "bin:codex"} {
		if !names[n] {
			t.Errorf("passo %s ausente: %+v", n, rep.Steps)
		}
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		t.Fatal("probe deveria criar a raiz remota")
	}
	// sem URL pública: aviso
	if len(rep.Warnings) == 0 {
		t.Fatal("faltou o aviso de URL pública")
	}
	f := filepath.Join(t.TempDir(), "f")
	must(t, os.WriteFile(f, nil, 0o600))
	if rep := Probe(context.Background(), cfg, sshTarget(f)); rep.OK {
		t.Fatalf("raiz não gravável deveria falhar: %+v", rep)
	}
	cfg.SSHBin = filepath.Join(t.TempDir(), "nao-existe")
	if rep := Probe(context.Background(), cfg, sshTarget(root)); rep.OK || rep.Steps[0].Name != "connect" {
		t.Fatalf("conexão falha: %+v", rep)
	}
}
