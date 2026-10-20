package acp

import (
	"context"
	"strings"

	"github.com/renatobardi/hanko/internal/adapter"
)

// probeState é até onde o handshake de um probe chegou.
type probeState struct {
	initialized  bool
	sessionOK    bool
	authRequired bool
	agentName    string
	authMethods  []string
}

// Probe confere se o agente ACP da persona está pronto sem enviar prompt: inicia o processo, faz initialize e
// session/new num diretório temporário e encerra. Passos: configuração, política do operador (só `acp`), binário,
// protocolo (initialize) e autenticação (session/new; um agente que exige login falha aqui, com os métodos que ele
// anuncia). Não gasta modelo.
func (a *Adapter) Probe(ctx context.Context, spec adapter.Spec) adapter.ProbeResult {
	var out adapter.ProbeResult
	cfg, err := a.parseConfig(spec.Config)
	if err != nil {
		out.Add("configuração", false, err.Error())
		return out
	}
	out.Add("configuração", true, cfg.Command+" "+strings.Join(cfg.Args, " "))
	if a.fixed == nil {
		if err := a.currentPolicy().Check(cfg.Command); err != nil {
			out.Add("política do operador", false, err.Error())
			return out
		}
		out.Add("política do operador", true, "")
	}
	env := map[string]string{}
	for k, v := range cfg.Env {
		env[k] = v
	}
	for k, v := range spec.Env {
		env[k] = v
	}
	pspec := spec
	pspec.Env, pspec.Dir, pspec.Prompt, pspec.System, pspec.SessionRef, pspec.MCPConfig = env, "", "", "", "", nil
	secrets := adapter.SpecSecrets(pspec)
	ps := &probeState{}
	res, runErr := a.run(ctx, pspec, cfg, env, adapter.DiscardSink, secrets, ps)
	res = adapter.RedactResult(res, secrets...)

	if runErr != nil && res.ExitCode == -1 && !ps.initialized {
		out.Add("binário", false, res.Error)
		return out
	}
	out.Add("binário", true, "")
	if !ps.initialized {
		out.Add("protocolo", false, firstNonEmpty(res.Error, "o agente não respondeu ao initialize"))
		return out
	}
	out.Add("protocolo", true, strings.TrimSpace(ps.agentName+" (ACP v1)"))
	switch {
	case ps.sessionOK:
		out.Add("autenticação", true, "")
	case ps.authRequired:
		out.Add("autenticação", false, res.Error)
	default:
		out.Add("sessão", false, res.Error)
	}
	return out
}
