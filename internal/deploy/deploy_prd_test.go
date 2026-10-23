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

// O deploy usa os comandos de operação do próprio hanko (ADR-0062): doctor, update --check e as units geradas por
// `service print`, em vez de heredocs de unit escritos à mão.
func TestDeployPrdUsesOpsCommands(t *testing.T) {
	b, err := os.ReadFile("../../deploy/oute-server/deploy-prd.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"exec -T hanko hanko doctor", "exec -T hanko hanko update --check", "service print timer --name hanko-backup", "service print compose --dir /opt/app", "--part service", "--part timer"} {
		if !strings.Contains(s, want) {
			t.Errorf("deploy-prd.sh sem %q", want)
		}
	}
	// a imagem do prd (Dockerfile.agents) instala o binário em /usr/local/bin: /hanko só existe na imagem distroless
	if strings.Contains(s, "exec -T hanko /hanko") {
		t.Error("deploy-prd.sh chama /hanko, que não existe na imagem do prd")
	}
	if strings.Contains(s, "cat > /etc/systemd/system/hanko-backup.service") {
		t.Error("a unit do backup voltou a ser um heredoc escrito à mão")
	}
	if out, err := exec.Command("bash", "-n", "../../deploy/oute-server/deploy-prd.sh").CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v %s", err, out)
	}
}

// unitsBlock devolve a definição de HK e o comando x "…" (multilinha) que gera as units do systemd.
func unitsBlock(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../deploy/oute-server/deploy-prd.sh")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "HK=") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasSuffix(lines[j], "hanko-stack.service\"") {
				return strings.Join(lines[i:j+1], "\n")
			}
		}
	}
	t.Fatal("bloco das units não encontrado em deploy-prd.sh")
	return ""
}

// Uma falha ao gerar a unit mantém a unit atual (nada de arquivo vazio no lugar) e aborta o bloco; com sucesso, a unit
// nova substitui a antiga.
func TestDeployPrdUnitsAreReplacedOnlyWhenGenerated(t *testing.T) {
	block := unitsBlock(t)
	for _, c := range []struct {
		name, dockerBody string
		wantFail         bool
		want             string
	}{
		{"geração falha", "echo 'exec: hanko: not found' >&2; exit 127", true, "unit antiga"},
		{"saída vazia", "exit 0", true, "unit antiga"},
		{"geração ok", "echo '[Unit] nova'", false, "[Unit] nova"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			units := filepath.Join(dir, "units")
			for _, d := range []string{bin, units} {
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for name, body := range map[string]string{"docker": c.dockerBody, "chmod": "exit 0", "systemctl": "exit 0"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/bash\n"+body+"\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			unit := filepath.Join(units, "hanko-backup.service")
			if err := os.WriteFile(unit, []byte("unit antiga\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			script := "x() { bash -c \"$*\"; }\n" + strings.ReplaceAll(block, "/etc/systemd/system/", units+"/") + "\n"
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if c.wantFail != (err != nil) {
				t.Fatalf("falha esperada=%v, err=%v: %s", c.wantFail, err, out)
			}
			got, rerr := os.ReadFile(unit)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if !strings.Contains(string(got), c.want) {
				t.Fatalf("unit com %q, esperava %q: %s", got, c.want, out)
			}
			if _, serr := os.Stat(unit + ".tmp"); !os.IsNotExist(serr) {
				t.Fatalf("sobrou o .tmp: %v", serr)
			}
		})
	}
}

// O vhost do nginx precisa aceitar o maior envio de anexos do Hanko (10 arquivos, 128 MiB; ADR-0065), senão a borda
// devolve 413 antes de o Hanko ver o pedido.
func TestDeployPrdVhostFitsTheUploadBudget(t *testing.T) {
	b, err := os.ReadFile("../../deploy/oute-server/deploy-prd.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"client_max_body_size 130m;", "client_body_timeout 300s;", "proxy_request_buffering off;"} {
		if !strings.Contains(s, want) {
			t.Errorf("vhost sem %q", want)
		}
	}
}
