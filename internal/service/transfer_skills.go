package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/store"
)

// Skills e integrações no arquivo hanko.company.v1 (ADR-0022, ADR-0029). Os campos são opcionais: um arquivo sem
// eles continua valendo, e um arquivo com eles importa em instalações que ainda não os tinham como campos ignorados.

// exportSkillsIntegrations preenche doc.Skills, doc.Integrations e personas[].skills. Integrações saem SEM segredos.
func exportSkillsIntegrations(ctx context.Context, q *store.Queries, companyID domain.ID, doc *export.Document, peSlug map[domain.ID]string) error {
	skRows, err := q.ListSkills(ctx, companyID)
	if err != nil {
		return fmt.Errorf("export skills: %w", err)
	}
	for _, k := range skRows {
		doc.Skills = append(doc.Skills, export.Skill{Slug: k.Slug, Name: k.Name, Description: red(k.Description), Body: red(k.Body), CreatedAt: k.CreatedAt})
	}
	links, err := q.ExportPersonaSkills(ctx, companyID)
	if err != nil {
		return fmt.Errorf("export persona skills: %w", err)
	}
	bySlug := map[string][]string{}
	for _, l := range links {
		bySlug[l.PersonaSlug] = append(bySlug[l.PersonaSlug], l.SkillSlug)
	}
	for i := range doc.Personas {
		doc.Personas[i].Skills = bySlug[doc.Personas[i].Slug]
	}

	inRows, err := q.ListIntegrations(ctx, companyID)
	if err != nil {
		return fmt.Errorf("export integrations: %w", err)
	}
	for _, r := range inRows {
		cfg := parseIntegrationConfig(r.Config)
		ei := export.Integration{Kind: r.Kind, Slug: r.Slug, Name: r.Name, AllPersonas: r.AllPersonas, CreatedAt: r.CreatedAt}
		switch domain.IntegrationKind(r.Kind) {
		case domain.IntegrationWebhook:
			ei.URL = originOnly(cfg.URL) // o caminho e a query de um webhook costumam levar o token
			ei.Events = slices.Clone(r.Events)
		case domain.IntegrationMCP:
			ei.Transport = cfg.Transport
			if cfg.Transport == domain.TransportStdio {
				ei.Command = cfg.Command // sem argumentos: podem levar credenciais
				ei.EnvNames = cfg.EnvNames
			} else {
				ei.URL = stripURLSecrets(cfg.URL)
				ei.HeaderNames = cfg.HeaderNames
			}
			pl, err := q.ListIntegrationPersonaLinks(ctx, store.ListIntegrationPersonaLinksParams{CompanyID: companyID, IntegrationID: r.ID})
			if err != nil {
				return fmt.Errorf("export integration personas: %w", err)
			}
			for _, p := range pl {
				if p.Enabled {
					if sl := peSlug[p.PersonaID]; sl != "" {
						ei.Personas = append(ei.Personas, sl)
					}
				}
			}
		}
		doc.Integrations = append(doc.Integrations, ei)
	}
	return nil
}

// stripURLSecrets tira usuário, senha, query e fragmento de uma URL: é onde costumam ir tokens.
func stripURLSecrets(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// skillsIntegrationsPlan são os valores já validados de skills e integrações de um arquivo.
type skillsIntegrationsPlan struct {
	skills       []planSkill
	integrations []planIntegration
}

type planSkill struct{ name, desc, body string }

type planIntegration struct {
	cfg []byte
	url string
}

// planSkillsIntegrations valida skills e integrações do arquivo sem tocar no banco.
func (s *Service) planSkillsIntegrations(doc export.Document, personas map[string]int) (skillsIntegrationsPlan, error) {
	var p skillsIntegrationsPlan
	skIdx, err := uniqueIdx("skills", doc.Skills, func(k export.Skill) string { return k.Slug }, "slug")
	if err != nil {
		return p, err
	}
	for i, k := range doc.Skills {
		if !domain.ValidSlug(k.Slug) || slices.Contains(reservedUISlugs, k.Slug) {
			return p, domain.Invalid(iv("skills", i, "slug"), "invalid or reserved")
		}
		n, d, b, err := normalizeSkill(k.Name, k.Description, k.Body)
		if err != nil {
			return p, domain.Invalid(iv("skills", i, "body"), err.Error())
		}
		p.skills = append(p.skills, planSkill{n, d, b})
	}
	for i, x := range doc.Personas {
		seen := map[string]bool{}
		for _, sl := range x.Skills {
			if _, ok := skIdx[sl]; !ok {
				return p, domain.Invalid(iv("personas", i, "skills"), fmt.Sprintf("unknown skill %q", sl))
			}
			if seen[sl] {
				return p, domain.Invalid(iv("personas", i, "skills"), fmt.Sprintf("duplicate %q", sl))
			}
			seen[sl] = true
		}
	}
	if _, err := uniqueIdx("integrations", doc.Integrations, func(x export.Integration) string { return x.Slug }, "slug"); err != nil {
		return p, err
	}
	for i, x := range doc.Integrations {
		if !domain.ValidSlug(x.Slug) || checkIntegrationSlug(domain.IntegrationKind(x.Kind), x.Slug) != nil {
			return p, domain.Invalid(iv("integrations", i, "slug"), "invalid or reserved")
		}
		if _, err := validateName(x.Name); err != nil {
			return p, domain.Invalid(iv("integrations", i, "name"), err.Error())
		}
		var pi planIntegration
		cfg := domain.IntegrationConfig{}
		switch domain.IntegrationKind(x.Kind) {
		case domain.IntegrationWebhook:
			u, err := s.webhookURL(x.URL)
			if err != nil {
				return p, domain.Invalid(iv("integrations", i, "url"), err.Error())
			}
			if _, err := validateEvents(x.Events); err != nil {
				return p, domain.Invalid(iv("integrations", i, "events"), err.Error())
			}
			cfg.URL, pi.url = u, u
		case domain.IntegrationMCP:
			cfg.Transport = x.Transport
			if cfg.Transport == "" {
				cfg.Transport = domain.TransportHTTP
			}
			switch cfg.Transport {
			case domain.TransportStdio:
				if err := s.checkStdioAllowed(); err != nil {
					return p, domain.Invalid(iv("integrations", i, "transport"), invalidReason(err))
				}
				cmd, err := validateStdio(x.Command, nil)
				if err != nil {
					return p, domain.Invalid(iv("integrations", i, "command"), err.Error())
				}
				cfg.Command = cmd
			case domain.TransportHTTP:
				u, err := mcpURL(x.URL)
				if err != nil {
					return p, domain.Invalid(iv("integrations", i, "url"), err.Error())
				}
				cfg.URL = u
			default:
				return p, domain.Invalid(iv("integrations", i, "transport"), fmt.Sprintf("unknown transport %q", x.Transport))
			}
			for _, sl := range x.Personas {
				if _, ok := personas[sl]; !ok {
					return p, domain.Invalid(iv("integrations", i, "personas"), fmt.Sprintf("unknown persona %q", sl))
				}
			}
		default:
			return p, domain.Invalid(iv("integrations", i, "kind"), fmt.Sprintf("unknown kind %q", x.Kind))
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return p, fmt.Errorf("marshal integration config: %w", err)
		}
		pi.cfg = raw
		p.integrations = append(p.integrations, pi)
	}
	return p, nil
}

// importSkillsIntegrations grava skills, ligações e integrações. As integrações entram DESLIGADAS e sem segredos.
func (t *txc) importSkillsIntegrations(cid domain.ID, doc export.Document, p skillsIntegrationsPlan, peID map[string]domain.ID) error {
	ctx := t.ctx
	skID := map[string]domain.ID{}
	for i, k := range doc.Skills {
		id := domain.NewID()
		row, err := t.q.CreateSkill(ctx, store.CreateSkillParams{ID: id, CompanyID: cid, Slug: k.Slug, Name: p.skills[i].name,
			Description: p.skills[i].desc, Body: p.skills[i].body, ActorType: string(domain.ActorSystem), Now: t.now})
		if err != nil {
			return mapErr(err, "import skill "+k.Slug)
		}
		if err := t.recordSkillVersion(row); err != nil { // a primeira versão (ADR-0035)
			return err
		}
		skID[k.Slug] = id
	}
	for _, x := range doc.Personas {
		for _, sl := range x.Skills {
			if err := t.q.SetPersonaSkill(ctx, store.SetPersonaSkillParams{CompanyID: cid, PersonaID: peID[x.Slug], SkillID: skID[sl], Enabled: true, Now: t.now}); err != nil {
				return mapErr(err, "import persona skill")
			}
		}
	}
	for i, x := range doc.Integrations {
		id := domain.NewID()
		events := []string{}
		if x.Kind == string(domain.IntegrationWebhook) {
			events, _ = validateEvents(x.Events)
		}
		if _, err := t.q.CreateIntegration(ctx, store.CreateIntegrationParams{ID: id, CompanyID: cid, Kind: x.Kind, Slug: x.Slug,
			Name: strings.TrimSpace(x.Name), Enabled: false, AllPersonas: x.AllPersonas && x.Kind == string(domain.IntegrationMCP),
			Config: p.integrations[i].cfg, Events: events, ActorType: string(domain.ActorSystem), Now: t.now}); err != nil {
			return mapErr(err, "import integration "+x.Slug)
		}
		if x.Kind != string(domain.IntegrationMCP) {
			continue
		}
		for _, sl := range x.Personas {
			if err := t.q.SetIntegrationPersona(ctx, store.SetIntegrationPersonaParams{CompanyID: cid, IntegrationID: id, PersonaID: peID[sl]}); err != nil {
				return mapErr(err, "import integration persona")
			}
		}
	}
	return nil
}
