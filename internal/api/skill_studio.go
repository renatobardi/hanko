package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// Versões, fontes e estúdio de skills (ADR-0035). Ler é de qualquer membro; restaurar, fixar versão, mexer em
// fontes e rodar testes é de admin (o service confere). Run tokens só leem.

type skillStudioBackend interface {
	ListSkillVersions(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillVersion, error)
	GetSkillVersion(ctx context.Context, companyID, skillID domain.ID, revision int) (domain.SkillVersion, error)
	DiffSkillVersions(ctx context.Context, companyID, skillID domain.ID, from, to int) (textdiff.Result, error)
	RestoreSkillVersion(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, revision int) (domain.Skill, error)
	SetPersonaSkillPin(ctx context.Context, actor domain.Actor, companyID, personaID, skillID domain.ID, revision *int) error

	SkillSourcesEnabled() bool
	DiscoverSkillSource(ctx context.Context, companyID domain.ID, in service.SkillSourceInput) (service.SkillSourceDiscovery, error)
	CreateSkillSource(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSkillSourceInput) (service.SkillSourceView, service.SkillSyncResult, error)
	ListSkillSources(ctx context.Context, companyID domain.ID) ([]service.SkillSourceView, error)
	GetSkillSource(ctx context.Context, companyID, id domain.ID) (service.SkillSourceView, error)
	UpdateSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.UpdateSkillSourceInput) (service.SkillSourceView, error)
	DeleteSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, keepSkills bool) error
	SyncSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, force bool) (service.SkillSyncResult, error)

	ListSkillTestInputs(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillTestInput, error)
	GetSkillTestInput(ctx context.Context, companyID, skillID, id domain.ID) (domain.SkillTestInput, error)
	CreateSkillTestInput(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, name, content string) (domain.SkillTestInput, error)
	UpdateSkillTestInput(ctx context.Context, actor domain.Actor, companyID, skillID, id domain.ID, name, content *string) (domain.SkillTestInput, error)
	DeleteSkillTestInput(ctx context.Context, actor domain.Actor, companyID, skillID, id domain.ID) error
	ListSkillTestRuns(ctx context.Context, companyID domain.ID, skillID *domain.ID, limit int) ([]domain.SkillTestRun, error)
	GetSkillTestRun(ctx context.Context, companyID, id domain.ID) (domain.SkillTestRun, error)
	RunSkillTest(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in service.RunSkillTestInput) (domain.SkillTestRun, error)
	RerunSkillTest(ctx context.Context, actor domain.Actor, companyID, testID domain.ID) (domain.SkillTestRun, error)
	CancelSkillTest(ctx context.Context, actor domain.Actor, companyID, testID domain.ID) (domain.SkillTestRun, error)
}

func (s *server) studio() (skillStudioBackend, error) {
	b, ok := s.svc.(skillStudioBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "estúdio e fontes de skills indisponíveis neste servidor")
	}
	return b, nil
}

// ---- DTOs ----

// SkillVersion é uma versão imutável de uma skill.
type SkillVersion struct {
	Revision     int       `json:"revision" doc:"Número da versão (o version da skill quando foi gravada)."`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Body         string    `json:"body,omitempty" doc:"Corpo em markdown; ausente nas listagens."`
	BodyBytes    int       `json:"body_bytes"`
	SHA256       string    `json:"sha256" doc:"sha256 (hex) do corpo."`
	SourceCommit string    `json:"source_commit,omitempty" doc:"Commit da fonte de onde a versão veio, quando veio de fonte."`
	Author       ActorRef  `json:"author"`
	CreatedAt    time.Time `json:"created_at"`
	// Diff só vem com ?against=N: as linhas de N para esta versão.
	Diff *ArtifactDiff `json:"diff,omitempty"`
}

func skillVersionOut(v domain.SkillVersion) SkillVersion {
	return SkillVersion{Revision: v.Revision, Name: v.Name, Description: v.Description, Body: v.Body, BodyBytes: v.BodyBytes,
		SHA256: v.SHA256, SourceCommit: v.SourceCommit, Author: actorRef(v.Author), CreatedAt: v.CreatedAt}
}

// SkillSourceEntry é um diretório com SKILL.md encontrado na fonte.
type SkillSourceEntry struct {
	Path        string `json:"path" doc:"Diretório da skill no repositório (\".\" = a raiz)."`
	Name        string `json:"name"`
	Description string `json:"description"`
	Selection   string `json:"selection" enum:"selected,excluded,new" doc:"selected: importada e mantida em dia; excluded: ignorada; new: apareceu na fonte e espera revisão."`
	Present     bool   `json:"present" doc:"Falso quando o diretório saiu da fonte."`
	HasScripts  bool   `json:"has_scripts" doc:"O diretório tem scripts ou executáveis; só o SKILL.md é importado."`
	SkillSlug   string `json:"skill_slug,omitempty" doc:"Skill importada desta entrada."`
	Modified    bool   `json:"modified,omitempty" doc:"A skill importada foi editada aqui (a sincronização não a sobrescreve sem force)."`
	Error       string `json:"error,omitempty" doc:"Por que a entrada não foi importada ou atualizada."`
}

// SkillSource é um repositório git público de onde a empresa importa skills.
type SkillSource struct {
	ID            string             `json:"id" format:"uuid"`
	Label         string             `json:"label" doc:"Host e caminho, para exibir."`
	URL           string             `json:"url"`
	Ref           string             `json:"ref" doc:"Branch acompanhado (ou HEAD)."`
	PathPrefix    string             `json:"path_prefix,omitempty"`
	ExcludedPaths []string           `json:"excluded_paths"`
	AutoUpdate    bool               `json:"auto_update"`
	ImportNew     bool               `json:"import_new" doc:"Skills novas na fonte entram sozinhas (senão ficam como new para revisão)."`
	Enabled       bool               `json:"enabled"`
	Status        string             `json:"status" enum:"idle,syncing,ok,error,paused"`
	LastCommit    string             `json:"last_commit,omitempty"`
	LastError     string             `json:"last_error,omitempty"`
	FailureCount  int                `json:"failure_count"`
	NextSyncAt    *time.Time         `json:"next_sync_at,omitempty"`
	LastSuccessAt *time.Time         `json:"last_success_at,omitempty"`
	Found         int                `json:"found" doc:"Diretórios com SKILL.md presentes na fonte."`
	Imported      int                `json:"imported" doc:"Skills importadas desta fonte."`
	Entries       []SkillSourceEntry `json:"entries,omitempty" doc:"Só no detalhe."`
	CreatedBy     ActorRef           `json:"created_by"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	// Sync vem na criação: o resultado da primeira sincronização.
	Sync *service.SkillSyncResult `json:"sync,omitempty"`
}

func skillSourceOut(v service.SkillSourceView) SkillSource {
	o := SkillSource{ID: idStr(v.ID), Label: v.Label, URL: v.URL, Ref: v.TrackingRef, PathPrefix: v.PathPrefix, ExcludedPaths: v.ExcludedPaths,
		AutoUpdate: v.AutoUpdate, ImportNew: v.ImportNew, Enabled: v.Enabled, Status: string(v.Status), LastCommit: v.LastCommit,
		LastError: v.LastError, FailureCount: v.FailureCount, NextSyncAt: v.NextSyncAt, LastSuccessAt: v.LastSuccessAt, Found: v.Found,
		Imported: v.Imported, CreatedBy: actorRef(v.CreatedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	for _, e := range v.Entries {
		o.Entries = append(o.Entries, SkillSourceEntry{Path: e.Path, Name: e.Name, Description: e.Description, Selection: string(e.Selection),
			Present: e.Present, HasScripts: e.HasScripts, SkillSlug: e.SkillSlug, Modified: e.Modified, Error: e.Error})
	}
	return o
}

// SkillTestInput é uma entrada salva do estúdio.
type SkillTestInput struct {
	ID        string    `json:"id" format:"uuid"`
	Name      string    `json:"name"`
	Content   string    `json:"content" doc:"Markdown, até 32 KiB."`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func testInputOut(i domain.SkillTestInput) SkillTestInput {
	return SkillTestInput{ID: idStr(i.ID), Name: i.Name, Content: i.Content, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt}
}

// SkillTestRun é uma execução de teste: a skill numa versão fixa, uma entrada e a persona que a roda.
type SkillTestRun struct {
	ID          string     `json:"id" format:"uuid"`
	Skill       string     `json:"skill" doc:"Slug da skill."`
	Revision    int        `json:"revision" doc:"Versão testada (reexecutar usa a mesma)."`
	InputID     string     `json:"input_id,omitempty" format:"uuid"`
	Input       string     `json:"input" doc:"Cópia da entrada usada."`
	Persona     string     `json:"persona" doc:"Slug da persona."`
	PersonaName string     `json:"persona_name"`
	TaskID      string     `json:"task_id,omitempty" format:"uuid" doc:"Task de sistema do teste."`
	RunID       string     `json:"run_id,omitempty" format:"uuid"`
	RunName     string     `json:"run_name,omitempty"`
	CostUSD     *float64   `json:"cost_usd,omitempty"`
	Status      string     `json:"status" enum:"queued,running,done,failed,cancelled"`
	Output      string     `json:"output" doc:"A resposta final da persona (markdown)."`
	Error       string     `json:"error,omitempty"`
	CreatedBy   ActorRef   `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

func testRunOut(r domain.SkillTestRun) SkillTestRun {
	return SkillTestRun{ID: idStr(r.ID), Skill: r.SkillSlug, Revision: r.SkillRevision, InputID: idPtr(r.InputID), Input: r.Input,
		Persona: r.PersonaSlug, PersonaName: r.PersonaName, TaskID: idPtr(r.TaskID), RunID: idPtr(r.RunID), RunName: r.RunName,
		CostUSD: r.CostUSD, Status: string(r.Status), Output: r.Output, Error: r.Error, CreatedBy: actorRef(r.CreatedBy),
		CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt}
}

func diffOut(from, to int, r textdiff.Result) *ArtifactDiff {
	out := &ArtifactDiff{From: from, To: to, Added: r.Added, Removed: r.Removed, Coarse: r.Coarse, Lines: make([]ArtifactDiffLine, 0, len(r.Lines))}
	for _, l := range r.Lines {
		op := "equal"
		switch l.Op {
		case textdiff.Insert:
			op = "insert"
		case textdiff.Delete:
			op = "delete"
		}
		out.Lines = append(out.Lines, ArtifactDiffLine{Op: op, Text: l.Text, OldNo: l.OldNo, NewNo: l.NewNo})
	}
	return out
}

// ---- inputs ----

type SkillVersionPath struct {
	SkillPath
	Revision int `path:"revision" minimum:"1" doc:"Número da versão."`
	Against  int `query:"against" minimum:"0" doc:"Inclui o diff desta versão para a pedida."`
}

type restoreSkillIn struct {
	SkillPath
	Body struct {
		Revision int `json:"revision" minimum:"1" doc:"Versão cujo conteúdo volta; vira uma versão nova."`
	}
}

type SkillSourcePath struct {
	CompanyPath
	Source string `path:"source" format:"uuid" doc:"Id da fonte."`
}

type sourceAddr struct {
	URL        string   `json:"url" maxLength:"2000" doc:"https de host público, sem usuário e senha." example:"https://example.com/org/skills.git"`
	Ref        string   `json:"ref,omitempty" maxLength:"200" doc:"Branch acompanhado; padrão HEAD."`
	PathPrefix string   `json:"path_prefix,omitempty" maxLength:"500" doc:"Só diretórios dentro deste caminho."`
	Excluded   []string `json:"excluded_paths,omitempty" maxItems:"200" doc:"Caminhos ignorados (e tudo abaixo deles)."`
}

func (a sourceAddr) input() service.SkillSourceInput {
	return service.SkillSourceInput{URL: a.URL, Ref: a.Ref, PathPrefix: a.PathPrefix, Excluded: a.Excluded}
}

type discoverSourceIn struct {
	CompanyPath
	Body sourceAddr
}

type createSourceIn struct {
	CompanyPath
	Body struct {
		URL        string   `json:"url" maxLength:"2000" doc:"https de host público, sem usuário e senha." example:"https://example.com/org/skills.git"`
		Ref        string   `json:"ref,omitempty" maxLength:"200" doc:"Branch acompanhado; padrão HEAD."`
		PathPrefix string   `json:"path_prefix,omitempty" maxLength:"500" doc:"Só diretórios dentro deste caminho."`
		Excluded   []string `json:"excluded_paths,omitempty" maxItems:"200" doc:"Caminhos ignorados (e tudo abaixo deles)."`
		AutoUpdate *bool    `json:"auto_update,omitempty" doc:"Sincroniza sozinha a cada hora; padrão true."`
		ImportNew  bool     `json:"import_new,omitempty" doc:"Skills novas na fonte entram sozinhas."`
		Select     []string `json:"select,omitempty" maxItems:"200" doc:"Caminhos a importar (os outros ficam excluídos). Ausente = todos."`
	}
}

type updateSourceIn struct {
	SkillSourcePath
	Body struct {
		PathPrefix *string           `json:"path_prefix,omitempty" maxLength:"500"`
		Excluded   *[]string         `json:"excluded_paths,omitempty" maxItems:"200"`
		AutoUpdate *bool             `json:"auto_update,omitempty"`
		ImportNew  *bool             `json:"import_new,omitempty"`
		Enabled    *bool             `json:"enabled,omitempty"`
		Selection  map[string]string `json:"selection,omitempty" doc:"Caminho → selected ou excluded. Excluir uma entrada importada solta a skill (a cópia fica)."`
	}
}

type deleteSourceIn struct {
	SkillSourcePath
	KeepSkills bool `query:"keep_skills" doc:"Mantém as skills importadas como skills locais; sem ele elas são apagadas."`
}

type syncSourceIn struct {
	SkillSourcePath
	Body *struct {
		Force bool `json:"force,omitempty" doc:"Reimporta mesmo sem mudança e sobrescreve skills modificadas localmente."`
	}
}

type SkillTestInputPath struct {
	SkillPath
	Input string `path:"input" format:"uuid"`
}

type createTestInputIn struct {
	SkillPath
	Body struct {
		Name    string `json:"name" minLength:"1" maxLength:"120"`
		Content string `json:"content" doc:"Markdown, até 32 KiB."`
	}
}

type updateTestInputIn struct {
	SkillTestInputPath
	Body struct {
		Name    *string `json:"name,omitempty" maxLength:"120"`
		Content *string `json:"content,omitempty"`
	}
}

type listTestRunsIn struct {
	SkillPath
	Limit int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type createTestRunIn struct {
	SkillPath
	Body struct {
		Persona  string `json:"persona" doc:"Id ou slug da persona que roda o teste."`
		Input    string `json:"input,omitempty" doc:"Texto livre (markdown); ou use input_id."`
		InputID  string `json:"input_id,omitempty" format:"uuid" doc:"Entrada salva."`
		Revision *int   `json:"revision,omitempty" minimum:"1" doc:"Versão da skill; padrão a atual."`
	}
}

type SkillTestRunPath struct {
	CompanyPath
	Test string `path:"test" format:"uuid" doc:"Id da execução de teste."`
}

// maxStudioRequestBytes é o teto do corpo das rotas que levam uma entrada de teste (32 KiB que podem crescer até
// 6x ao escapar em JSON).
const maxStudioRequestBytes = 256 << 10

func capStudio(o huma.Operation) huma.Operation { o.MaxBodyBytes = maxStudioRequestBytes; return o }

func (s *server) registerSkillStudio(api huma.API) {
	// resolve := skill do caminho com o backend do estúdio
	type ctxSkill struct {
		sb skillStudioBackend
		sc scope
		k  domain.Skill
	}
	withSkill := func(ctx context.Context, p SkillPath, write bool) (ctxSkill, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return ctxSkill{}, err
			}
		}
		st, err := s.studio()
		if err != nil {
			return ctxSkill{}, err
		}
		sk, err := s.skills()
		if err != nil {
			return ctxSkill{}, err
		}
		sc, err := s.scope(ctx, p.Company)
		if err != nil {
			return ctxSkill{}, err
		}
		k, err := sk.GetSkill(ctx, sc.Company.ID, p.Skill)
		if err != nil {
			return ctxSkill{}, s.fail(ctx, err)
		}
		return ctxSkill{sb: st, sc: sc, k: k}, nil
	}
	withCompany := func(ctx context.Context, company string, write bool) (skillStudioBackend, scope, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return nil, scope{}, err
			}
		}
		st, err := s.studio()
		if err != nil {
			return nil, scope{}, err
		}
		sc, err := s.scope(ctx, company)
		return st, sc, err
	}

	// ---- versões ----
	huma.Register(api, op("list-skill-versions", http.MethodGet, "/companies/{company}/skills/{skill}/versions", "skills", "Lista as versões de uma skill (mais nova primeiro)"),
		func(ctx context.Context, in *SkillPath) (*ListOut[SkillVersion], error) {
			c, err := withSkill(ctx, *in, false)
			if err != nil {
				return nil, err
			}
			vs, err := c.sb.ListSkillVersions(ctx, c.sc.Company.ID, c.k.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(vs, Page{Limit: len(vs)}, skillVersionOut), nil
		})

	huma.Register(api, op("get-skill-version", http.MethodGet, "/companies/{company}/skills/{skill}/versions/{revision}", "skills", "Obtém uma versão de uma skill, com o corpo (e o diff com ?against)"),
		func(ctx context.Context, in *SkillVersionPath) (*ItemOut[SkillVersion], error) {
			c, err := withSkill(ctx, in.SkillPath, false)
			if err != nil {
				return nil, err
			}
			v, err := c.sb.GetSkillVersion(ctx, c.sc.Company.ID, c.k.ID, in.Revision)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := skillVersionOut(v)
			if in.Against > 0 {
				d, err := c.sb.DiffSkillVersions(ctx, c.sc.Company.ID, c.k.ID, in.Against, in.Revision)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				out.Diff = diffOut(in.Against, in.Revision, d)
			}
			return &ItemOut[SkillVersion]{Body: out}, nil
		})

	huma.Register(api, op("restore-skill-version", http.MethodPost, "/companies/{company}/skills/{skill}/restore", "skills", "Restaura o conteúdo de uma versão (cria uma versão nova)"),
		func(ctx context.Context, in *restoreSkillIn) (*ItemOut[Skill], error) {
			c, err := withSkill(ctx, in.SkillPath, true)
			if err != nil {
				return nil, err
			}
			k, err := c.sb.RestoreSkillVersion(ctx, c.sc.Actor, c.sc.Company.ID, c.k.ID, in.Body.Revision)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Skill]{Body: skillOut(k, true)}, nil
		})

	// ---- fontes ----
	huma.Register(api, op("list-skill-sources", http.MethodGet, "/companies/{company}/skill-sources", "skills", "Lista as fontes git de skills"),
		func(ctx context.Context, in *CompanyPath) (*ListOut[SkillSource], error) {
			st, sc, err := withCompany(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			list, err := st.ListSkillSources(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, skillSourceOut), nil
		})

	huma.Register(api, op("discover-skill-source", http.MethodPost, "/companies/{company}/skill-sources/discover", "skills", "Clona e lista as skills de um repositório, sem gravar (admin)"),
		func(ctx context.Context, in *discoverSourceIn) (*ItemOut[service.SkillSourceDiscovery], error) {
			st, sc, err := withCompany(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			d, err := st.DiscoverSkillSource(ctx, sc.Company.ID, in.Body.input())
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[service.SkillSourceDiscovery]{Body: d}, nil
		})

	huma.Register(api, created(op("create-skill-source", http.MethodPost, "/companies/{company}/skill-sources", "skills", "Conecta uma fonte e importa as skills escolhidas (admin)")),
		func(ctx context.Context, in *createSourceIn) (*ItemOut[SkillSource], error) {
			st, sc, err := withCompany(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			b := in.Body
			addr := sourceAddr{URL: b.URL, Ref: b.Ref, PathPrefix: b.PathPrefix, Excluded: b.Excluded}
			v, res, err := st.CreateSkillSource(ctx, sc.Actor, sc.Company.ID, service.CreateSkillSourceInput{SkillSourceInput: addr.input(),
				AutoUpdate: b.AutoUpdate, ImportNew: b.ImportNew, Select: b.Select})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := skillSourceOut(v)
			out.Sync = &res
			return &ItemOut[SkillSource]{Body: out}, nil
		})

	huma.Register(api, op("get-skill-source", http.MethodGet, "/companies/{company}/skill-sources/{source}", "skills", "Obtém uma fonte com as entradas encontradas"),
		func(ctx context.Context, in *SkillSourcePath) (*ItemOut[SkillSource], error) {
			st, sc, err := withCompany(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Source)
			if err != nil {
				return nil, s.fail(ctx, domain.ErrNotFound)
			}
			v, err := st.GetSkillSource(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillSource]{Body: skillSourceOut(v)}, nil
		})

	huma.Register(api, op("update-skill-source", http.MethodPatch, "/companies/{company}/skill-sources/{source}", "skills", "Muda as configurações e a seleção de uma fonte (admin)"),
		func(ctx context.Context, in *updateSourceIn) (*ItemOut[SkillSource], error) {
			st, sc, err := withCompany(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Source)
			if err != nil {
				return nil, s.fail(ctx, domain.ErrNotFound)
			}
			b := in.Body
			up := service.UpdateSkillSourceInput{PathPrefix: b.PathPrefix, Excluded: b.Excluded, AutoUpdate: b.AutoUpdate, ImportNew: b.ImportNew, Enabled: b.Enabled}
			if len(b.Selection) > 0 {
				up.Selection = make(map[string]domain.EntrySelection, len(b.Selection))
				for p, sel := range b.Selection {
					up.Selection[p] = domain.EntrySelection(sel)
				}
			}
			v, err := st.UpdateSkillSource(ctx, sc.Actor, sc.Company.ID, id, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillSource]{Body: skillSourceOut(v)}, nil
		})

	huma.Register(api, noContent(op("delete-skill-source", http.MethodDelete, "/companies/{company}/skill-sources/{source}", "skills", "Desconecta uma fonte, mantendo ou apagando as skills importadas (admin)")),
		func(ctx context.Context, in *deleteSourceIn) (*struct{}, error) {
			st, sc, err := withCompany(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Source)
			if err != nil {
				return nil, s.fail(ctx, domain.ErrNotFound)
			}
			if err := st.DeleteSkillSource(ctx, sc.Actor, sc.Company.ID, id, in.KeepSkills); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("sync-skill-source", http.MethodPost, "/companies/{company}/skill-sources/{source}/sync", "skills", "Sincroniza agora (admin); uma falha de clone vem em error"),
		func(ctx context.Context, in *syncSourceIn) (*ItemOut[service.SkillSyncResult], error) {
			st, sc, err := withCompany(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Source)
			if err != nil {
				return nil, s.fail(ctx, domain.ErrNotFound)
			}
			force := in.Body != nil && in.Body.Force
			res, err := st.SyncSkillSource(ctx, sc.Actor, sc.Company.ID, id, force)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[service.SkillSyncResult]{Body: res}, nil
		})

	// ---- estúdio: entradas ----
	huma.Register(api, op("list-skill-test-inputs", http.MethodGet, "/companies/{company}/skills/{skill}/test-inputs", "skills", "Lista as entradas salvas do estúdio"),
		func(ctx context.Context, in *SkillPath) (*ListOut[SkillTestInput], error) {
			c, err := withSkill(ctx, *in, false)
			if err != nil {
				return nil, err
			}
			list, err := c.sb.ListSkillTestInputs(ctx, c.sc.Company.ID, c.k.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, testInputOut), nil
		})

	huma.Register(api, capStudio(created(op("create-skill-test-input", http.MethodPost, "/companies/{company}/skills/{skill}/test-inputs", "skills", "Salva uma entrada do estúdio (admin)"))),
		func(ctx context.Context, in *createTestInputIn) (*ItemOut[SkillTestInput], error) {
			c, err := withSkill(ctx, in.SkillPath, true)
			if err != nil {
				return nil, err
			}
			i, err := c.sb.CreateSkillTestInput(ctx, c.sc.Actor, c.sc.Company.ID, c.k.ID, in.Body.Name, in.Body.Content)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillTestInput]{Body: testInputOut(i)}, nil
		})

	huma.Register(api, op("get-skill-test-input", http.MethodGet, "/companies/{company}/skills/{skill}/test-inputs/{input}", "skills", "Obtém uma entrada salva"),
		func(ctx context.Context, in *SkillTestInputPath) (*ItemOut[SkillTestInput], error) {
			c, err := withSkill(ctx, in.SkillPath, false)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Input)
			if err != nil {
				return nil, s.fail(ctx, domain.ErrNotFound)
			}
			i, err := c.sb.GetSkillTestInput(ctx, c.sc.Company.ID, c.k.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillTestInput]{Body: testInputOut(i)}, nil
		})

	huma.Register(api, capStudio(op("update-skill-test-input", http.MethodPatch, "/companies/{company}/skills/{skill}/test-inputs/{input}", "skills", "Altera uma entrada salva (admin)")),
		func(ctx context.Context, in *updateTestInputIn) (*ItemOut[SkillTestInput], error) {
			c, err := withSkill(ctx, in.SkillPath, true)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Input)
			if err != nil {
				return nil, s.fail(ctx, domain.ErrNotFound)
			}
			i, err := c.sb.UpdateSkillTestInput(ctx, c.sc.Actor, c.sc.Company.ID, c.k.ID, id, in.Body.Name, in.Body.Content)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillTestInput]{Body: testInputOut(i)}, nil
		})

	huma.Register(api, noContent(op("delete-skill-test-input", http.MethodDelete, "/companies/{company}/skills/{skill}/test-inputs/{input}", "skills", "Apaga uma entrada salva (admin)")),
		func(ctx context.Context, in *SkillTestInputPath) (*struct{}, error) {
			c, err := withSkill(ctx, in.SkillPath, true)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Input)
			if err != nil {
				return nil, s.fail(ctx, domain.ErrNotFound)
			}
			if err := c.sb.DeleteSkillTestInput(ctx, c.sc.Actor, c.sc.Company.ID, c.k.ID, id); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	// ---- estúdio: execuções ----
	huma.Register(api, op("list-skill-test-runs", http.MethodGet, "/companies/{company}/skills/{skill}/test-runs", "skills", "Lista as execuções de teste da skill (mais recentes primeiro)"),
		func(ctx context.Context, in *listTestRunsIn) (*ListOut[SkillTestRun], error) {
			c, err := withSkill(ctx, in.SkillPath, false)
			if err != nil {
				return nil, err
			}
			list, err := c.sb.ListSkillTestRuns(ctx, c.sc.Company.ID, &c.k.ID, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: in.Limit}, testRunOut), nil
		})

	huma.Register(api, capStudio(created(op("create-skill-test-run", http.MethodPost, "/companies/{company}/skills/{skill}/test-runs", "skills", "Roda um teste da skill com uma persona (admin; gasta orçamento)"))),
		func(ctx context.Context, in *createTestRunIn) (*ItemOut[SkillTestRun], error) {
			c, err := withSkill(ctx, in.SkillPath, true)
			if err != nil {
				return nil, err
			}
			b := in.Body
			p, err := s.svc.GetPersona(ctx, c.sc.Company.ID, b.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			req := service.RunSkillTestInput{PersonaID: p.ID, Input: b.Input, Revision: b.Revision}
			if b.InputID != "" {
				id, err := domain.ParseID(b.InputID)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("input_id", "id inválido"))
				}
				req.InputID = &id
			}
			r, err := c.sb.RunSkillTest(ctx, c.sc.Actor, c.sc.Company.ID, c.k.ID, req)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillTestRun]{Body: testRunOut(r)}, nil
		})

	testOp := func(ctx context.Context, in *SkillTestRunPath, write bool) (skillStudioBackend, scope, domain.ID, error) {
		st, sc, err := withCompany(ctx, in.Company, write)
		if err != nil {
			return nil, scope{}, domain.ID{}, err
		}
		id, err := domain.ParseID(in.Test)
		if err != nil {
			return nil, scope{}, domain.ID{}, s.fail(ctx, domain.ErrNotFound)
		}
		return st, sc, id, nil
	}
	huma.Register(api, op("get-skill-test-run", http.MethodGet, "/companies/{company}/skill-test-runs/{test}", "skills", "Obtém uma execução de teste (estado ao vivo)"),
		func(ctx context.Context, in *SkillTestRunPath) (*ItemOut[SkillTestRun], error) {
			st, sc, id, err := testOp(ctx, in, false)
			if err != nil {
				return nil, err
			}
			r, err := st.GetSkillTestRun(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillTestRun]{Body: testRunOut(r)}, nil
		})

	huma.Register(api, op("cancel-skill-test-run", http.MethodPost, "/companies/{company}/skill-test-runs/{test}/cancel", "skills", "Cancela uma execução de teste (admin)"),
		func(ctx context.Context, in *SkillTestRunPath) (*ItemOut[SkillTestRun], error) {
			st, sc, id, err := testOp(ctx, in, true)
			if err != nil {
				return nil, err
			}
			r, err := st.CancelSkillTest(ctx, sc.Actor, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillTestRun]{Body: testRunOut(r)}, nil
		})

	huma.Register(api, created(op("rerun-skill-test-run", http.MethodPost, "/companies/{company}/skill-test-runs/{test}/rerun", "skills", "Repete o teste com a mesma versão, entrada e persona (admin)")),
		func(ctx context.Context, in *SkillTestRunPath) (*ItemOut[SkillTestRun], error) {
			st, sc, id, err := testOp(ctx, in, true)
			if err != nil {
				return nil, err
			}
			r, err := st.RerunSkillTest(ctx, sc.Actor, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SkillTestRun]{Body: testRunOut(r)}, nil
		})
}
