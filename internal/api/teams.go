package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/teams"
)

// Pacotes de time hanko.team.v1 (ADR-0044): catálogo embutido, exportar um time, prévia e instalação. Exportar e instalar são
// de admin e só de humanos (o service confere); o catálogo é de qualquer usuário autenticado.

type teamBackend interface {
	ExportTeam(ctx context.Context, companyID domain.ID, opts service.TeamExportOptions) (export.TeamPackage, error)
	InstallTeam(ctx context.Context, actor domain.Actor, companyID domain.ID, pkg export.TeamPackage, opts service.TeamInstallOptions) (service.TeamInstall, error)
	ListTeamInstalls(ctx context.Context, companyID domain.ID) ([]service.TeamInstallRecord, error)
}

func (s *server) teams() (teamBackend, error) {
	b, ok := s.svc.(teamBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "pacotes de time indisponíveis neste servidor")
	}
	return b, nil
}

// teamDoc é o pacote no OpenAPI: um objeto livre (o formato está no ADR-0044 e é validado pelo service), para não colidir
// os nomes dos tipos do arquivo com os da API.
type teamDoc export.TeamPackage

// Schema implementa huma.SchemaProvider.
func (teamDoc) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "object", Description: "Pacote hanko.team.v1 (veja docs/adr/0044-fontes-de-skills-e-pacotes-de-time.md).",
		Required: []string{"format", "package", "personas"}, AdditionalProperties: true,
		Properties: map[string]*huma.Schema{"format": {Type: "string", Enum: []any{export.TeamFormat}}, "package": {Type: "object"}, "personas": {Type: "array", Items: &huma.Schema{Type: "object"}}}}
}

// TeamSummary resume um pacote do catálogo.
type TeamSummary struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
	Author      string   `json:"author,omitempty"`
	Personas    int      `json:"personas"`
	Skills      int      `json:"skills"`
	Flows       int      `json:"flows"`
	Agents      []string `json:"agents" doc:"Harnesses exigidos."`
	Secrets     []string `json:"secrets" doc:"Segredos citados (${secret:NOME})."`
}

func teamSummary(p export.TeamPackage) TeamSummary {
	r := p.ComputeRequires()
	return TeamSummary{Slug: p.Package.Slug, Name: p.Package.Name, Description: p.Package.Description, Version: p.Package.Version,
		Author: p.Package.Author, Personas: len(p.Personas), Skills: len(p.Skills), Flows: len(p.Flows), Agents: nonNilStr(r.Agents), Secrets: nonNilStr(r.Secrets)}
}

func nonNilStr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// TeamAgentReq é um harness exigido pelo pacote e a situação dele na empresa.
type TeamAgentReq struct {
	Agent         string   `json:"agent"`
	Subscriptions []string `json:"subscriptions" doc:"Assinaturas ativas da empresa para esse harness."`
	Chosen        string   `json:"chosen,omitempty" doc:"A assinatura escolhida nesta instalação."`
}

// TeamSecretReq é um segredo citado pelo pacote.
type TeamSecretReq struct {
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
}

// TeamInstalled é a prévia (dry_run) ou o resultado da instalação.
type TeamInstalled struct {
	Package  export.TeamInfo `json:"package"`
	SHA256   string          `json:"sha256"`
	DryRun   bool            `json:"dry_run"`
	Agents   []TeamAgentReq  `json:"agents"`
	Secrets  []TeamSecretReq `json:"secrets"`
	Counts   map[string]int  `json:"counts" doc:"Quantos itens de cada tipo foram gravados (na prévia, seriam gravados)."`
	Items    []ImportItem    `json:"items"`
	Warnings []ImportWarning `json:"warnings"`
	// InstallID só existe depois de instalar.
	InstallID string `json:"install_id,omitempty" format:"uuid"`
}

func teamInstalledOut(r service.TeamInstall) TeamInstalled {
	out := TeamInstalled{Package: r.Package, SHA256: r.SHA256, DryRun: r.DryRun, Counts: r.Result.Counts, InstallID: idPtr(r.InstallID),
		Agents: make([]TeamAgentReq, len(r.Agents)), Secrets: make([]TeamSecretReq, len(r.Secrets)),
		Items: make([]ImportItem, len(r.Result.Items)), Warnings: make([]ImportWarning, len(r.Warnings))}
	for i, a := range r.Agents {
		out.Agents[i] = TeamAgentReq{Agent: a.Agent, Subscriptions: nonNilStr(a.Subscriptions), Chosen: a.Chosen}
	}
	for i, x := range r.Secrets {
		out.Secrets[i] = TeamSecretReq{Name: x.Name, Exists: x.Exists}
	}
	for i, it := range r.Result.Items {
		out.Items[i] = ImportItem{Kind: it.Kind, Key: it.Key, Name: it.Name, Action: string(it.Action), Target: it.Target}
	}
	for i, w := range r.Warnings {
		out.Warnings[i] = ImportWarning{Code: w.Code, Message: w.Message, Count: w.Count}
	}
	return out
}

// TeamInstallRecord é uma instalação já feita.
type TeamInstallRecord struct {
	ID          string         `json:"id" format:"uuid"`
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Version     string         `json:"version,omitempty"`
	Source      string         `json:"source" enum:"catalog,file,export"`
	SHA256      string         `json:"sha256"`
	Counts      map[string]int `json:"counts"`
	InstalledBy ActorRef       `json:"installed_by"`
	CreatedAt   string         `json:"created_at"`
}

type teamCatalogPath struct {
	Slug string `path:"slug" doc:"Slug do pacote no catálogo."`
}

type teamExportIn struct {
	CompanyPath
	Personas    string `query:"personas" doc:"Slugs das personas separados por vírgula; vazio = todas."`
	Name        string `query:"name" doc:"Nome do pacote."`
	Slug        string `query:"slug" doc:"Slug do pacote."`
	Description string `query:"description"`
	Version     string `query:"version"`
}

type teamInstallIn struct {
	CompanyPath
	Body struct {
		Catalog string         `json:"catalog,omitempty" doc:"Slug de um pacote do catálogo embutido. Exclusivo com package."`
		Package map[string]any `json:"package,omitempty" doc:"Pacote hanko.team.v1 (arquivo ou URL baixado pelo cliente). Exclusivo com catalog."`
		// OnConflict e Resolve seguem o import com merge (ADR-0057).
		OnConflict    string            `json:"on_conflict,omitempty" enum:"skip,rename,replace" doc:"Ação padrão para o que já existe no destino (padrão skip)."`
		Resolve       map[string]string `json:"resolve,omitempty" doc:"Exceção por item: \"tipo/chave\" → skip|rename|replace (ex.: personas/dev)."`
		Subscriptions map[string]string `json:"subscriptions,omitempty" doc:"Por harness (claude, codex...), o NOME de uma assinatura existente da empresa que as personas desse harness usam."`
	}
}

// resolvePackage devolve o pacote do corpo: do catálogo ou o enviado (lido de forma estrita).
func resolveTeamPackage(catalog string, pkg map[string]any) (export.TeamPackage, string, error) {
	switch {
	case catalog != "" && len(pkg) > 0:
		return export.TeamPackage{}, "", domain.Invalid("package", "informe catalog ou package, não os dois")
	case catalog != "":
		p, ok, err := teams.Get(catalog)
		if err != nil {
			return export.TeamPackage{}, "", fmt.Errorf("catálogo de times: %w", err)
		}
		if !ok {
			return export.TeamPackage{}, "", fmt.Errorf("pacote %q não existe no catálogo: %w", catalog, domain.ErrNotFound)
		}
		return p, service.TeamSourceCatalog, nil
	case len(pkg) > 0:
		raw, err := json.Marshal(pkg)
		if err != nil {
			return export.TeamPackage{}, "", domain.Invalid("package", "JSON inválido")
		}
		p, err := teams.Parse(raw)
		if err != nil {
			return export.TeamPackage{}, "", domain.Invalid("package", err.Error())
		}
		return p, service.TeamSourceFile, nil
	}
	return export.TeamPackage{}, "", domain.Invalid("package", "informe catalog ou package")
}

func (s *server) registerTeams(api huma.API) {
	huma.Register(api, op("list-team-catalog", http.MethodGet, "/team-catalog", "teams", "Lista os pacotes de time do catálogo embutido"),
		func(ctx context.Context, in *struct{ Page }) (*ListOut[TeamSummary], error) {
			l, err := teams.List()
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(l, in.Page, teamSummary), nil
		})

	huma.Register(api, op("get-team-catalog", http.MethodGet, "/team-catalog/{slug}", "teams", "Obtém um pacote do catálogo inteiro (hanko.team.v1)"),
		func(ctx context.Context, in *teamCatalogPath) (*ItemOut[teamDoc], error) {
			p, ok, err := teams.Get(in.Slug)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if !ok {
				return nil, problem(http.StatusNotFound, "pacote não encontrado")
			}
			return &ItemOut[teamDoc]{Body: teamDoc(p)}, nil
		})

	prelude := func(ctx context.Context, company string) (teamBackend, scope, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, scope{}, err
		}
		b, err := s.teams()
		if err != nil {
			return nil, scope{}, err
		}
		sc, err := s.scope(ctx, company)
		return b, sc, err
	}

	huma.Register(api, op("export-team", http.MethodGet, "/companies/{company}/team-export", "teams",
		"Exporta personas, skills e rotinas escolhidas como pacote hanko.team.v1 (admin)"),
		func(ctx context.Context, in *teamExportIn) (*ItemOut[teamDoc], error) {
			b, sc, err := prelude(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			var personas []string
			for _, p := range strings.Split(in.Personas, ",") {
				if p = strings.TrimSpace(p); p != "" {
					personas = append(personas, p)
				}
			}
			p, err := b.ExportTeam(ctx, sc.Company.ID, service.TeamExportOptions{Personas: personas, Name: in.Name, Slug: in.Slug,
				Description: in.Description, Version: in.Version})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[teamDoc]{Body: teamDoc(p)}, nil
		})

	install := func(dry bool) func(ctx context.Context, in *teamInstallIn) (*ItemOut[TeamInstalled], error) {
		return func(ctx context.Context, in *teamInstallIn) (*ItemOut[TeamInstalled], error) {
			b, sc, err := prelude(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pkg, source, err := resolveTeamPackage(in.Body.Catalog, in.Body.Package)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			resolve := map[string]domain.ConflictAction{}
			for k, v := range in.Body.Resolve {
				resolve[k] = domain.ConflictAction(v)
			}
			r, err := b.InstallTeam(ctx, sc.Actor, sc.Company.ID, pkg, service.TeamInstallOptions{OnConflict: domain.ConflictAction(in.Body.OnConflict),
				Resolve: resolve, Subscriptions: in.Body.Subscriptions, Source: source, DryRun: dry})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TeamInstalled]{Body: teamInstalledOut(r)}, nil
		}
	}
	huma.Register(api, op("preview-team", http.MethodPost, "/companies/{company}/teams/preview", "teams",
		"Prévia da instalação de um pacote de time: itens e ação de cada um, requisitos e avisos; nada é gravado (admin)"), install(true))
	huma.Register(api, created(op("install-team", http.MethodPost, "/companies/{company}/teams/install", "teams",
		"Instala um pacote de time (personas pausadas, rotinas desligadas); tudo ou nada (admin, só humanos)")), install(false))

	huma.Register(api, op("list-team-installs", http.MethodGet, "/companies/{company}/team-installs", "teams", "Lista as instalações de pacotes de time da empresa"),
		func(ctx context.Context, in *struct {
			CompanyPath
			Page
		}) (*ListOut[TeamInstallRecord], error) {
			b, err := s.teams()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			l, err := b.ListTeamInstalls(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			sort.SliceStable(l, func(i, j int) bool { return l[i].CreatedAt > l[j].CreatedAt })
			return listOf(l, in.Page, func(r service.TeamInstallRecord) TeamInstallRecord {
				return TeamInstallRecord{ID: idStr(r.ID), Slug: r.Slug, Name: r.Name, Version: r.Version, Source: r.Source, SHA256: r.SHA256,
					Counts: r.Counts, InstalledBy: actorRef(r.InstalledBy), CreatedAt: r.CreatedAt}
			}), nil
		})

}
