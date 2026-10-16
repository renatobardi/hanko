package execenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

// Target é o ambiente resolvido de uma run, já com os segredos abertos. Vive só na memória do executor.
type Target struct {
	Driver     domain.EnvironmentDriver
	Name       string
	SSH        domain.SSHConfig
	PrivateKey string
	Sandbox    domain.SandboxConfig
	// Env são as variáveis do ambiente (valores em claro); entram no ambiente do harness no alvo.
	Env map[string]string
}

// RunInfo identifica a run no alvo.
type RunInfo struct {
	RunID     string
	Company   string // slug da empresa
	Workspace string // slug do workspace; vazio = run sem workspace
	// LocalDir é o diretório de trabalho local da run (o workspace ou um diretório temporário).
	LocalDir string
	// TempDir: LocalDir é um diretório temporário da run (sem workspace), descartável.
	TempDir bool
}

// Session é a execução de uma run num ambiente remoto: implementa adapter.ExecTarget e sabe sincronizar o
// trabalho de volta e limpar no fim.
type Session interface {
	adapter.ExecTarget
	// LocalWorkDir é o diretório local da run, que pode ter sido trocado pela sessão (sandbox atrás de
	// HANKO_ENV_SANDBOX_HOST_ROOT precisa de um diretório que o host do docker enxergue).
	LocalWorkDir() string
	// RemoteDir é o diretório da run no alvo (para o registro de uso).
	RemoteDir() string
	// Finish traz o trabalho de volta e limpa o alvo; ok=false mantém o diretório remoto para diagnóstico.
	Finish(ctx context.Context, ok bool) error
	// Close apaga os arquivos temporários locais (chave, known_hosts, auxiliares). Idempotente.
	Close()
}

// Open prepara a sessão de uma run no alvo. Nada é executado no alvo ainda: o primeiro contato é o Prepare
// do adapter (que sobe o workspace).
func Open(cfg Config, t Target, run RunInfo) (Session, error) {
	if !safeSeg.MatchString(run.RunID) || !safeSeg.MatchString(run.Company) || run.Workspace != "" && !safeSeg.MatchString(run.Workspace) {
		return nil, errors.New("identificadores da run inválidos para o ambiente")
	}
	switch t.Driver {
	case domain.EnvSSH:
		return openSSH(cfg, t, run)
	case domain.EnvSandbox:
		return openSandbox(cfg, t, run)
	}
	return nil, fmt.Errorf("ambiente %q: driver %q não roda remotamente", t.Name, t.Driver)
}

// safeSeg é o formato de slug e de UUID: seguro como segmento de caminho e dentro de aspas simples.
var safeSeg = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Quote devolve s entre aspas simples POSIX: nada dentro é interpretado pelo shell do alvo. Uma aspa
// simples dentro de s fecha as aspas, entra escapada e as reabre.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteArgs junta os argumentos com Quote.
func QuoteArgs(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = Quote(a)
	}
	return strings.Join(q, " ")
}

// envFile monta o arquivo de variáveis que o comando remoto lê com "." (POSIX): uma linha export por
// variável, valores entre aspas simples. Assim os valores (run token, segredos do ambiente) nunca aparecem
// na linha de comando do processo remoto.
func envFile(env map[string]string) (string, error) {
	keys := make([]string, 0, len(env))
	for k := range env {
		if !domain.EnvNameRe.MatchString(k) {
			return "", fmt.Errorf("nome de variável inválido para o ambiente: %q", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if strings.ContainsRune(env[k], 0) {
			return "", fmt.Errorf("valor inválido na variável %q", k)
		}
		b.WriteString("export " + k + "=" + Quote(env[k]) + "\n")
	}
	return b.String(), nil
}

// mergeEnv junta as variáveis do ambiente e as da run; as da run vencem (run token, OTel).
func mergeEnv(base, run map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(run))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range run {
		out[k] = v
	}
	return out
}

// localTemp cria um diretório temporário 0700 para os arquivos da sessão.
func localTemp(base, pattern string) (string, error) {
	if base != "" {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", err
		}
	}
	d, err := os.MkdirTemp(base, pattern)
	if err != nil {
		return "", err
	}
	return d, os.Chmod(d, 0o700) //nolint:gosec // diretório: 0700 é o mínimo para o dono entrar nele
}

// under informa se p está estritamente dentro de root (caminhos limpos e absolutos).
func under(root, p string) bool {
	if root == "" || p == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
