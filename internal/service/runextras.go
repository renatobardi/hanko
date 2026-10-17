package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// RunExtras é o que skills e integrações acrescentam a uma run (ADR-0029): as skills habilitadas para a persona
// (já dentro dos tetos) e os servidores MCP da empresa, prontos para entrar no mcpServers. Warnings viram
// eventos de sistema no log da run; nunca carregam segredo.
type RunExtras struct {
	Skills   []domain.Skill
	MCP      map[string]json.RawMessage
	Warnings []string
}

// RunExtras monta as skills e os servidores MCP da empresa para uma run da persona. É chamado pelo executor
// (sem identidade de usuário): não aplica papéis. Uma integração ilegível ou desligada pelo operador (stdio)
// é pulada com aviso, e a run segue.
func (s *Service) RunExtras(ctx context.Context, companyID, personaID domain.ID) (RunExtras, error) {
	var ex RunExtras
	q := s.read()

	skills, err := s.PersonaSkills(ctx, companyID, personaID)
	if err != nil {
		return ex, err
	}
	var skipped []string
	ex.Skills, skipped = FitSkills(skills)
	if len(skipped) > 0 {
		ex.Warnings = append(ex.Warnings, fmt.Sprintf("skills omitidas por exceder os limites por execução (%d skills, %d KiB): %v",
			domain.MaxSkillsPerRun, domain.MaxSkillsBytesPerRun>>10, skipped))
	}

	rows, err := q.ListMCPIntegrationsForPersona(ctx, store.ListMCPIntegrationsForPersonaParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return ex, fmt.Errorf("list persona mcp integrations: %w", err)
	}
	for _, r := range rows {
		cfg := parseIntegrationConfig(r.Config)
		sec, err := s.open(r.ID, r.SecretEnc)
		if err != nil {
			ex.Warnings = append(ex.Warnings, fmt.Sprintf("integração %q ignorada: segredos ilegíveis (%v)", r.Slug, err))
			continue
		}
		hdrs, _, herr := s.expandMap(ctx, companyID, sec.Headers)
		env, _, eerr := s.expandMap(ctx, companyID, sec.Env)
		if herr != nil || eerr != nil {
			// o texto do erro nomeia o segredo que faltou, nunca o valor
			ex.Warnings = append(ex.Warnings, fmt.Sprintf("integração %q ignorada: %v", r.Slug, errors.Join(herr, eerr)))
			continue
		}
		sec.Headers, sec.Env = hdrs, env
		var entry map[string]any
		switch cfg.Transport {
		case domain.TransportStdio:
			if !s.Integrations.stdioAllowed() {
				ex.Warnings = append(ex.Warnings, fmt.Sprintf("integração %q ignorada: stdio está desligado neste servidor", r.Slug))
				continue
			}
			entry = map[string]any{"type": "stdio", "command": cfg.Command, "args": nonNilStrings(cfg.Args)}
			if len(sec.Env) > 0 {
				entry["env"] = sec.Env
			}
		default:
			entry = map[string]any{"type": "http", "url": cfg.URL}
			if len(sec.Headers) > 0 {
				entry["headers"] = sec.Headers
			}
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return ex, fmt.Errorf("marshal mcp entry: %w", err)
		}
		if ex.MCP == nil {
			ex.MCP = map[string]json.RawMessage{}
		}
		ex.MCP[r.Slug] = raw
	}
	return ex, nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
