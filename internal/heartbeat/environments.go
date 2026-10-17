package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/service"
)

// Ambientes de execução remotos (ADR-0037). O executor resolve o ambiente da persona antes de chamar o
// adapter; no local nada muda. Num ambiente remoto a sessão (execenv) sobe o workspace, o adapter roda o
// harness por ela e, no fim, o trabalho volta e o uso é registrado.

// envFinishTimeout limita a volta do workspace e a limpeza do alvo depois da run.
const envFinishTimeout = 15 * time.Minute

// environmentBackend é o que o service oferece para ambientes; opcional para que os fakes de teste que não
// os usam continuem valendo (sem ele, toda run é local).
type environmentBackend interface {
	RunEnvironment(ctx context.Context, companyID, personaID domain.ID) (service.RunEnvironment, error)
	AcquireEnvironmentLease(ctx context.Context, companyID, envID, runID domain.ID, remoteDir string) error
	ReleaseEnvironmentLease(ctx context.Context, companyID, runID domain.ID, failure string) error
}

// runEnv é a execução de uma run num ambiente remoto.
type runEnv struct {
	eb      environmentBackend
	sess    execenv.Session
	a       ExecuteRunArgs
	name    string
	secrets []string
	log     *slog.Logger
}

// openEnvironment resolve o ambiente da run e, quando ele é remoto, abre a sessão e ajusta o spec: o
// diretório, o alvo e a URL pela qual o harness alcança o MCP. Devolve nil para o local.
func (h *Heartbeat) openEnvironment(ctx context.Context, a ExecuteRunArgs, rc service.RunContext, spec *adapter.Spec, tempDir bool,
	emit func(domain.RunEventKind, string), log *slog.Logger,
) (*runEnv, error) {
	eb, ok := h.svc.(environmentBackend)
	if !ok {
		return nil, nil
	}
	re, err := eb.RunEnvironment(ctx, rc.Company.ID, rc.Persona.ID)
	if err != nil {
		return nil, fmt.Errorf("ambiente de execução: %w", err)
	}
	for _, w := range re.Warnings {
		emit(domain.EventSystem, w)
	}
	if re.Environment == nil {
		return nil, nil
	}
	env := re.Environment
	d := env.Driver
	if err := re.Config.DriverStatus(d); err != nil {
		return nil, fmt.Errorf("o ambiente %q não pode rodar neste servidor: %w", env.Name, err)
	}
	needURL := d == domain.EnvSSH || d == domain.EnvSandbox && re.Target.Sandbox.Network != domain.SandboxNetworkNone
	if needURL {
		if err := re.Config.RemoteURLStatus(); err != nil {
			return nil, fmt.Errorf("o ambiente %q não pode rodar: %w", env.Name, err)
		}
	} else {
		emit(domain.EventSystem, "sandbox sem rede: o harness não alcança o MCP do Hanko nem a API do modelo")
	}
	if d == domain.EnvSandbox && !re.Target.Sandbox.Pinned() {
		emit(domain.EventSystem, "aviso: imagem do sandbox sem digest (@sha256:…); o conteúdo pode mudar entre execuções")
	}
	mcp, dropped, err := rebaseMCP(spec.MCPConfig, strings.TrimRight(h.opts.PublicURL, "/"), strings.TrimRight(re.Config.RemoteURL, "/"))
	if err != nil {
		return nil, err
	}
	spec.MCPConfig = mcp
	for _, n := range dropped {
		emit(domain.EventSystem, fmt.Sprintf("servidor MCP stdio %q não vai para o ambiente remoto (o comando é desta máquina)", n))
	}
	if _, ok := spec.Env["HANKO_URL"]; ok && needURL {
		spec.Env["HANKO_URL"] = strings.TrimRight(re.Config.RemoteURL, "/")
	}
	ws := ""
	if rc.Workspace != nil && !tempDir {
		ws = rc.Workspace.Slug
	}
	sess, err := execenv.Open(re.Config, re.Target, execenv.RunInfo{RunID: rc.Run.ID.String(), Company: rc.Company.Slug, Workspace: ws,
		LocalDir: spec.Dir, TempDir: tempDir})
	if err != nil {
		return nil, fmt.Errorf("o ambiente %q não pode rodar: %w", env.Name, err)
	}
	spec.Dir = sess.LocalWorkDir()
	spec.Exec = sess
	if err := eb.AcquireEnvironmentLease(ctx, rc.Company.ID, env.ID, rc.Run.ID, sess.RemoteDir()); err != nil {
		sess.Close()
		return nil, fmt.Errorf("registrar o uso do ambiente %q: %w", env.Name, err)
	}
	emit(domain.EventSystem, "executando no ambiente "+sess.Describe())
	return &runEnv{eb: eb, sess: sess, a: a, name: env.Name, secrets: re.EnvironmentSecretValues(), log: log}, nil
}

// finish traz o trabalho de volta, limpa o alvo e fecha o uso. Devolve o erro de sincronização (a run falha
// com ele quando não havia falha antes). runFailure é o motivo de a run não ter dado certo ("" = ok).
func (e *runEnv) finish(ctx context.Context, runFailure string) error {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), envFinishTimeout)
	defer cancel()
	syncErr := e.sess.Finish(fctx, runFailure == "")
	e.sess.Close()
	reason := runFailure
	if syncErr != nil {
		syncErr = errors.New(adapter.Redact(syncErr.Error(), e.secrets...))
		reason = syncErr.Error()
	}
	if err := e.eb.ReleaseEnvironmentLease(fctx, e.a.CompanyID, e.a.RunID, reason); err != nil {
		e.log.Warn("execute_run: liberar o ambiente", "environment", e.name, "err", err)
	}
	return syncErr
}

// rebaseMCP aponta para a URL pública os servidores MCP que usam a URL dos agentes (o host remoto não alcança
// o loopback deste servidor) e tira os servidores stdio (o comando é desta máquina e os segredos dele não
// devem sair dela). Devolve os nomes retirados.
func rebaseMCP(raw json.RawMessage, from, to string) (json.RawMessage, []string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return raw, nil, nil
	}
	var cfg struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, nil, fmt.Errorf("mcp config: %w", err)
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]map[string]any{}
	}
	var dropped []string
	for name, srv := range cfg.Servers {
		if _, stdio := srv["command"]; stdio {
			delete(cfg.Servers, name)
			dropped = append(dropped, name)
			continue
		}
		if u, ok := srv["url"].(string); ok && from != "" && to != "" && (u == from || strings.HasPrefix(u, from+"/")) {
			srv["url"] = to + strings.TrimPrefix(u, from)
		}
	}
	out, err := json.Marshal(map[string]any{"mcpServers": cfg.Servers})
	if err != nil {
		return nil, nil, fmt.Errorf("mcp config: %w", err)
	}
	return out, dropped, nil
}
