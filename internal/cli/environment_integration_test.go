//go:build integration

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

func TestCLIEnvironments(t *testing.T) {
	svc, co, env := realServer(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-cli", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations.Secrets = box
	svc.Environments = execenv.Config{SSHAllowHosts: []string{".example.com"}, Sandbox: "docker", LookPath: func(s string) (string, error) { return s, nil }}
	if _, err := svc.HirePersona(context.Background(), domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	kh := filepath.Join(dir, "kh")
	if err := os.WriteFile(key, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nchave-da-cli\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kh, []byte("build.example.com ssh-ed25519 AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := mustOK(t, env, "env", "create", "Build", "--driver", "ssh", "--host", "build.example.com", "--user", "hanko", "--root", "/srv/hanko",
		"--known-hosts-file", kh, "--key-file", key, "--env", "API_TOKEN=valor-cli")
	if !strings.Contains(out, "Ambiente cadastrado: Build (ssh, build)") {
		t.Fatalf("create: %s", out)
	}
	mustOK(t, env, "env", "create", "Caixa", "--driver", "sandbox", "--image", "alpine:3")
	out = mustOK(t, env, "env", "list")
	for _, want := range []string{"local", "build", "caixa", "hanko@build.example.com:22 /srv/hanko", "Padrão da empresa: local"} {
		if !strings.Contains(out, want) {
			t.Errorf("list sem %q: %s", want, out)
		}
	}
	mustOK(t, env, "env", "default", "build")
	out = mustOK(t, env, "env", "show", "build")
	if !strings.Contains(out, "API_TOKEN") || strings.Contains(out, "valor-cli") || strings.Contains(out, "chave-da-cli") {
		t.Fatalf("show: %s", out)
	}
	out = mustOK(t, env, "--json", "env", "show", "build")
	if strings.Contains(out, "valor-cli") || strings.Contains(out, "chave-da-cli") || !strings.Contains(out, `"is_default": true`) {
		t.Fatalf("show json: %s", out)
	}
	mustOK(t, env, "persona", "update", "ana", "--environment", "caixa")
	p, err := svc.GetPersona(context.Background(), co.ID, "ana")
	if err != nil || p.EnvironmentID == nil {
		t.Fatalf("persona: %+v %v", p, err)
	}
	mustOK(t, env, "env", "rm", "caixa")
	if p, _ = svc.GetPersona(context.Background(), co.ID, "ana"); p.EnvironmentID != nil {
		t.Fatalf("persona deveria voltar ao padrão: %v", p.EnvironmentID)
	}
	mustOK(t, env, "env", "update", "build", "--description", "farm", "--drop-env", "API_TOKEN")
	out = mustOK(t, env, "env", "leases", "build")
	if !strings.Contains(out, "Nenhuma execução") {
		t.Fatalf("leases: %s", out)
	}
	if code, _, errOut := exec(t, Runtime{}, env, "", "env", "create", "X", "--driver", "local"); code == 0 || !strings.Contains(errOut, "--driver") {
		t.Fatalf("driver local: %d %s", code, errOut)
	}
	mustOK(t, env, "env", "default", "local")
}
