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
