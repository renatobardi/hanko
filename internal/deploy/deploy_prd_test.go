package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// preDeployBackupLine devolve o comando x "…" do deploy-prd.sh que decide se há banco no ar.
func preDeployBackupLine(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../deploy/oute-server/deploy-prd.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "x \"") && strings.Contains(l, "ps -q postgres") {
			return l
		}
	}
	t.Fatal("linha do backup pré-deploy não encontrada em deploy-prd.sh")
	return ""
}

// Erro do `docker compose ps` aborta o deploy; lista vazia é primeira instalação; com banco, faz backup.
func TestDeployPrdPreBackupDistinguishesPsFailure(t *testing.T) {
	line := preDeployBackupLine(t)
	cases := []struct {
		name, dockerBody string
		wantFail         bool
		want             string
	}{
		{"ps falha", "exit 1", true, "docker compose ps falhou"},
		{"lista vazia", "exit 0", false, "primeira instalação"},
		{"banco no ar", "echo abc123", false, "backup.sh"}, // o backup.sh real não existe no sandbox: basta ter sido chamado
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			docker := filepath.Join(dir, "docker")
			if err := os.WriteFile(docker, []byte("#!/bin/bash\n"+c.dockerBody+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			script := "x() { bash -c \"$*\"; }\n" + line + "\n"
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if c.wantFail && err == nil {
				t.Fatalf("deveria abortar: %s", out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Fatalf("saída sem %q: %s", c.want, out)
			}
			if c.name == "lista vazia" && err != nil {
				t.Fatalf("primeira instalação não pode falhar: %v %s", err, out)
			}
		})
	}
}

// O deploy usa os comandos de operação do próprio hanko (ADR-0060): doctor, update --check e as units geradas por
// `service print`, em vez de heredocs de unit escritos à mão.
func TestDeployPrdUsesOpsCommands(t *testing.T) {
	b, err := os.ReadFile("../../deploy/oute-server/deploy-prd.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"/hanko doctor", "/hanko update --check", "service print timer --name hanko-backup", "service print compose --dir /opt/app", "--part service", "--part timer"} {
		if !strings.Contains(s, want) {
			t.Errorf("deploy-prd.sh sem %q", want)
		}
	}
	if strings.Contains(s, "cat > /etc/systemd/system/hanko-backup.service") {
		t.Error("a unit do backup voltou a ser um heredoc escrito à mão")
	}
	if out, err := exec.Command("bash", "-n", "../../deploy/oute-server/deploy-prd.sh").CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v %s", err, out)
	}
}
