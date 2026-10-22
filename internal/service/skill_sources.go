package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/skillgit"
	"github.com/renatobardi/hanko/internal/store"
)

// Fontes de skills (ADR-0035): um repositório git público de onde a empresa importa skills e as mantém em dia.
// Clonar sai do servidor, então é interruptor do operador (HANKO_SKILL_SOURCES, padrão desligado). Só admin mexe
// em fontes; qualquer membro as lê.

// SkillSourcesConfig é a política do operador para as fontes.
type SkillSourcesConfig struct {
	// Enabled liga as fontes (HANKO_SKILL_SOURCES). Desligado, toda operação que clona responde 422.
	Enabled bool
	// AllowPrivate libera hosts internos (HANKO_INTEGRATIONS_ALLOW_PRIVATE).
	AllowPrivate bool
	// GitBin, Timeout e MaxBytes ajustam o clone; zero usa os padrões (git do PATH, 60 s, 50 MiB).
	GitBin   string
	Timeout  time.Duration
	MaxBytes int64

	// allowFile aceita file:// (só testes: export_test.go). Em produção a fonte é sempre https.
	allowFile bool
}

const (
	errSkillSourcesOff = "as fontes de skills estão desligadas neste servidor: o operador liga com HANKO_SKILL_SOURCES=true"
	// skillSyncStaleAfter é quando uma sincronização "syncing" é considerada abandonada (o processo caiu no meio).
	skillSyncStaleAfter = 10 * time.Minute
	maxSourceError      = 1000
)

// SkillSourcesEnabled diz se o operador ligou as fontes.
func (s *Service) SkillSourcesEnabled() bool { return s.SkillSources.Enabled }

// isPluginSource diz se a URL é de uma fonte de skills de plugin (plugin://, ADR-0045): não clona nada, então não
// depende de HANKO_SKILL_SOURCES.
func isPluginSource(rawURL string) bool {
	_, _, ok := domain.ParsePluginSkillURL(strings.TrimSpace(rawURL))
	return ok
}

func (s *Service) requireSourcesOn() error {
	if !s.SkillSources.Enabled {
		return domain.Invalid("skill_sources", errSkillSourcesOff)
	}
	return nil
}

func (s *Service) gitOptions() skillgit.Options {
	c := s.SkillSources
	return skillgit.Options{GitBin: c.GitBin, Timeout: c.Timeout, MaxBytes: c.MaxBytes, MaxSkills: domain.MaxSkillsPerSource,
		AllowFile: c.allowFile, AllowPrivate: c.AllowPrivate}
}

// SkillSourceInput é o endereço de uma fonte e onde procurar nela.
type SkillSourceInput struct {
	URL        string
	Ref        string // branch ou HEAD (padrão)
	PathPrefix string
	Excluded   []string
}

// CreateSkillSourceInput cria uma fonte. Select escolhe os caminhos a importar (os demais ficam excluídos);
// nil importa todos os encontrados.
type CreateSkillSourceInput struct {
	SkillSourceInput
	AutoUpdate *bool // padrão true
	ImportNew  bool
	Select     []string
}

// UpdateSkillSourceInput muda as configurações e a seleção; campos nil ficam como estão.
type UpdateSkillSourceInput struct {
	PathPrefix *string
	Excluded   *[]string
	AutoUpdate *bool
	ImportNew  *bool
	Enabled    *bool
	// Selection muda a escolha por caminho (selected ou excluded). Excluir uma entrada importada solta a skill da
	// fonte (a cópia fica, como skill local).
	Selection map[string]domain.EntrySelection
}

// DiscoveredSkill é uma skill encontrada na descoberta (nada é gravado).
type DiscoveredSkill struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	HasScripts  bool   `json:"has_scripts"`
	Error       string `json:"error,omitempty"`
	// Conflict: já existe na empresa uma skill com este slug; ela não será importada.
	Conflict bool `json:"conflict,omitempty"`
}

// SkillSourceDiscovery é o resultado da descoberta.
type SkillSourceDiscovery struct {
	URL       string            `json:"url"`
	Ref       string            `json:"ref"`
	Commit    string            `json:"commit"`
	Skills    []DiscoveredSkill `json:"skills"`
	Truncated bool              `json:"truncated"`
}

// SkillSourceView é uma fonte com o que ela contém.
type SkillSourceView struct {
	domain.SkillSource
	Label    string                    `json:"label"`
	Entries  []domain.SkillSourceEntry `json:"entries,omitempty"`
	Found    int                       `json:"found"`
	Imported int                       `json:"imported"`
}

// SkillSyncResult resume uma sincronização. Error vem preenchido quando o clone ou a leitura falharam (a fonte
// fica em erro, com nova tentativa agendada).
type SkillSyncResult struct {
	Commit    string   `json:"commit,omitempty"`
	Imported  int      `json:"imported"`
	Updated   int      `json:"updated"`
	Unchanged int      `json:"unchanged"`
	Skipped   int      `json:"skipped"`
	Notes     []string `json:"notes,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// SkillSourceLabel é o nome curto da fonte para a UI: host e caminho, sem .git.
func SkillSourceLabel(raw string) string {
	if id, key, ok := domain.ParsePluginSkillURL(raw); ok {
		return "plugin " + id + " / " + key
	}
	if _, path, ok := parseWorkspaceSkillURL(raw); ok {
		if path == "" {
			return "repositório do workspace"
		}
		return "repositório do workspace/" + path
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimPrefix(raw, "file://")
	}
	return u.Host + strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
}

func sourceFromRow(r store.SkillSource) domain.SkillSource {
	src := domain.SkillSource{ID: r.ID, CompanyID: r.CompanyID, URL: r.Url, TrackingRef: r.TrackingRef, PathPrefix: r.PathPrefix,
		ExcludedPaths: r.ExcludedPaths, AutoUpdate: r.AutoUpdate, ImportNew: r.ImportNew, Enabled: r.Enabled,
		Status: domain.SkillSourceStatus(r.Status), LastError: r.LastError, FailureCount: int(r.FailureCount), NextSyncAt: r.NextSyncAt,
		LastAttemptAt: r.LastAttemptAt, LastSuccessAt: r.LastSuccessAt, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if src.ExcludedPaths == nil {
		src.ExcludedPaths = []string{}
	}
	if r.LastCommit != nil {
		src.LastCommit = *r.LastCommit
	}
	if r.LastTreeHash != nil {
		src.LastTreeHash = *r.LastTreeHash
	}
	return src
}

// sourceInput valida a entrada de uma fonte: clone (exige HANKO_SKILL_SOURCES) ou plugin://.
func (s *Service) sourceInput(ctx context.Context, companyID domain.ID, in SkillSourceInput) (SkillSourceInput, error) {
	if isPluginSource(in.URL) {
		return s.pluginSourceInput(ctx, companyID, in)
	}
	if err := s.requireSourcesOn(); err != nil {
		return in, err
	}
	return s.normalizeSourceInput(in)
}

// normalizeSourceInput valida a URL, a ref e os caminhos.
func (s *Service) normalizeSourceInput(in SkillSourceInput) (SkillSourceInput, error) {
	u, err := skillgit.ValidateURL(in.URL, s.gitOptions())
	if err != nil {
		return in, domain.Invalid("url", err.Error())
	}
	ref := strings.TrimSpace(in.Ref)
	if ref == "" {
		ref = "HEAD"
	}
	if err := skillgit.ValidateRef(ref); err != nil {
		return in, domain.Invalid("ref", err.Error())
	}
	prefix, err := skillgit.CleanPath(in.PathPrefix)
	if err != nil {
		return in, domain.Invalid("path_prefix", err.Error())
	}
	excl, err := cleanExcluded(in.Excluded)
	if err != nil {
		return in, err
	}
	return SkillSourceInput{URL: u, Ref: ref, PathPrefix: prefix, Excluded: excl}, nil
}

func cleanExcluded(in []string) ([]string, error) {
	out := []string{}
	for _, e := range in {
		c, err := skillgit.CleanPath(e)
		if err != nil {
			return nil, domain.Invalid("excluded_paths", fmt.Sprintf("%q: %v", e, err))
		}
		if c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	if len(out) > domain.MaxSkillSourceExcluded {
		return nil, domain.Invalid("excluded_paths", fmt.Sprintf("no máximo %d caminhos", domain.MaxSkillSourceExcluded))
	}
	return out, nil
}

// skillNameFor é o nome da skill de uma entrada: o do frontmatter, senão o diretório.
func skillNameFor(f skillgit.Found) string {
	if n := strings.TrimSpace(f.Name); n != "" {
		return n
	}
	if f.Path == "." {
		return "skill"
	}
	return path.Base(f.Path)
}

// DiscoverSkillSource clona e lista as skills encontradas, sem gravar nada (admin).
func (s *Service) DiscoverSkillSource(ctx context.Context, companyID domain.ID, in SkillSourceInput) (SkillSourceDiscovery, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return SkillSourceDiscovery{}, err
	}
	n, err := s.sourceInput(ctx, companyID, in)
	if err != nil {
		return SkillSourceDiscovery{}, err
	}
	var snap skillgit.Snapshot
	if isPluginSource(n.URL) {
		snap, err = s.pluginSkillSnapshot(ctx, store.SkillSource{CompanyID: companyID, Url: n.URL})
	} else {
		snap, err = skillgit.Fetch(ctx, n.URL, n.Ref, skillgit.Scan{Prefix: n.PathPrefix, Excluded: n.Excluded}, s.gitOptions())
	}
	if err != nil {
		return SkillSourceDiscovery{}, domain.Invalid("url", "não foi possível ler o repositório: "+err.Error())
	}
	out := SkillSourceDiscovery{URL: n.URL, Ref: n.Ref, Commit: snap.Commit, Truncated: snap.Truncated, Skills: []DiscoveredSkill{}}
	q := s.read()
	for _, f := range snap.Skills {
		d := DiscoveredSkill{Path: f.Path, Name: skillNameFor(f), Description: f.Description, HasScripts: f.HasScripts, Error: f.Err}
		d.Slug = domain.Slugify(d.Name)
		if d.Error == "" {
			if _, _, _, err := normalizeSkill(d.Name, f.Description, f.Raw); err != nil {
				d.Error = err.Error()
			}
		}
		if _, err := q.GetSkillBySlug(ctx, store.GetSkillBySlugParams{CompanyID: companyID, Slug: d.Slug}); err == nil {
			d.Conflict = true
		} else if !isNoRows(err) {
			return SkillSourceDiscovery{}, fmt.Errorf("get skill by slug: %w", err)
		}
		out.Skills = append(out.Skills, d)
	}
	return out, nil
}

// CreateSkillSource grava a fonte e faz a primeira sincronização com a seleção do admin. A fonte fica criada mesmo
// quando a sincronização falha (o resultado traz o erro).
func (s *Service) CreateSkillSource(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateSkillSourceInput) (SkillSourceView, SkillSyncResult, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return SkillSourceView{}, SkillSyncResult{}, err
	}
	n, err := s.sourceInput(ctx, companyID, in.SkillSourceInput)
	if err != nil {
		return SkillSourceView{}, SkillSyncResult{}, err
	}
	auto := in.AutoUpdate == nil || *in.AutoUpdate
	status := domain.SourceIdle
	if !auto {
		status = domain.SourcePaused
	}
	var id domain.ID
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateSkillSource(t.ctx, store.CreateSkillSourceParams{ID: domain.NewID(), CompanyID: companyID, Url: n.URL,
			TrackingRef: n.Ref, PathPrefix: n.PathPrefix, ExcludedPaths: n.Excluded, AutoUpdate: auto, ImportNew: in.ImportNew,
			Status: string(status), ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			if isUnique(err) {
				return fmt.Errorf("esta fonte (URL e ref) já está conectada: %w", domain.ErrConflict)
			}
			return mapErr(err, "create skill source")
		}
		id = row.ID
		return t.activity(actor, companyID, "skill_source.created", "skill_source", row.ID,
			map[string]any{"url": row.Url, "ref": row.TrackingRef, "path_prefix": row.PathPrefix, "auto_update": auto, "import_new": in.ImportNew}, "")
	})
	if err != nil {
		return SkillSourceView{}, SkillSyncResult{}, err
	}
	initial := map[string]bool{selectAll: true} // sem Select: importa tudo o que for encontrado
	if in.Select != nil {
		initial = map[string]bool{}
		for _, p := range in.Select {
			c, err := skillgit.CleanPath(p)
			if err != nil {
				continue
			}
			if c == "" {
				c = "."
			}
			initial[c] = true
		}
	}
	res, err := s.syncSource(ctx, actor, companyID, id, initial, false)
	if err != nil {
		return SkillSourceView{}, SkillSyncResult{}, err
	}
	v, err := s.getSkillSource(ctx, companyID, id)
	return v, res, err
}

// ListSkillSources devolve as fontes da empresa.
func (s *Service) ListSkillSources(ctx context.Context, companyID domain.ID) ([]SkillSourceView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListSkillSources(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list skill sources: %w", err)
	}
	out := make([]SkillSourceView, len(rows))
	for i, r := range rows {
		src := sourceFromRow(store.SkillSource{ID: r.ID, CompanyID: r.CompanyID, Url: r.Url, TrackingRef: r.TrackingRef, PathPrefix: r.PathPrefix,
			ExcludedPaths: r.ExcludedPaths, AutoUpdate: r.AutoUpdate, ImportNew: r.ImportNew, Enabled: r.Enabled, LastCommit: r.LastCommit,
			LastTreeHash: r.LastTreeHash, Status: r.Status, LastError: r.LastError, FailureCount: r.FailureCount, NextSyncAt: r.NextSyncAt,
			LastAttemptAt: r.LastAttemptAt, LastSuccessAt: r.LastSuccessAt, CreatedByType: r.CreatedByType, CreatedByID: r.CreatedByID,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
		out[i] = SkillSourceView{SkillSource: src, Label: SkillSourceLabel(src.URL), Found: int(r.Entries), Imported: int(r.Imported)}
	}
	return out, nil
}

// GetSkillSource devolve a fonte com as entradas.
func (s *Service) GetSkillSource(ctx context.Context, companyID, id domain.ID) (SkillSourceView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return SkillSourceView{}, err
	}
	return s.getSkillSource(ctx, companyID, id)
}

func (s *Service) getSkillSource(ctx context.Context, companyID, id domain.ID) (SkillSourceView, error) {
	q := s.read()
	r, err := q.GetSkillSource(ctx, store.GetSkillSourceParams{CompanyID: companyID, ID: id})
	if err != nil {
		return SkillSourceView{}, mapErr(err, "get skill source")
	}
	rows, err := q.ListSkillSourceEntries(ctx, store.ListSkillSourceEntriesParams{CompanyID: companyID, SourceID: id})
	if err != nil {
		return SkillSourceView{}, fmt.Errorf("list skill source entries: %w", err)
	}
	v := SkillSourceView{SkillSource: sourceFromRow(r), Label: SkillSourceLabel(r.Url), Entries: make([]domain.SkillSourceEntry, 0, len(rows))}
	for _, e := range rows {
		v.Entries = append(v.Entries, domain.SkillSourceEntry{ID: e.ID, Path: e.Path, Name: e.Name, Description: e.Description,
			SkillID: e.SkillID, SkillSlug: e.SkillSlug, Modified: e.SkillModified, Selection: domain.EntrySelection(e.Selection),
			Present: e.Present, HasScripts: e.HasScripts, Error: e.Error})
		if e.Present {
			v.Found++
		}
		if e.SkillID != nil {
			v.Imported++
		}
	}
	return v, nil
}

// UpdateSkillSource muda configurações e seleção (admin). Uma entrada que passa a selected é importada na próxima
// sincronização; excluir uma entrada importada solta a skill da fonte, que fica como skill local.
func (s *Service) UpdateSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in UpdateSkillSourceInput) (SkillSourceView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return SkillSourceView{}, err
	}
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetSkillSourceForUpdate(t.ctx, store.GetSkillSourceForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get skill source")
		}
		p := store.UpdateSkillSourceSettingsParams{CompanyID: companyID, ID: id, PathPrefix: cur.PathPrefix, ExcludedPaths: cur.ExcludedPaths,
			AutoUpdate: cur.AutoUpdate, ImportNew: cur.ImportNew, Enabled: cur.Enabled, Status: cur.Status, NextSyncAt: cur.NextSyncAt, Now: t.now}
		changed := map[string]any{}
		if in.PathPrefix != nil {
			c, err := skillgit.CleanPath(*in.PathPrefix)
			if err != nil {
				return domain.Invalid("path_prefix", err.Error())
			}
			p.PathPrefix, changed["path_prefix"] = c, c
		}
		if in.Excluded != nil {
			ex, err := cleanExcluded(*in.Excluded)
			if err != nil {
				return err
			}
			p.ExcludedPaths, changed["excluded_paths"] = ex, ex
		}
		if in.ImportNew != nil {
			p.ImportNew, changed["import_new"] = *in.ImportNew, *in.ImportNew
		}
		if in.Enabled != nil {
			p.Enabled, changed["enabled"] = *in.Enabled, *in.Enabled
		}
		if in.AutoUpdate != nil {
			p.AutoUpdate, changed["auto_update"] = *in.AutoUpdate, *in.AutoUpdate
		}
		syncing := domain.SkillSourceStatus(cur.Status) == domain.SourceSyncing
		switch {
		case !p.AutoUpdate && !syncing && cur.Status != string(domain.SourceError):
			p.Status = string(domain.SourcePaused)
		case p.AutoUpdate && cur.Status == string(domain.SourcePaused):
			p.Status = string(domain.SourceOK)
			if cur.LastSuccessAt == nil {
				p.Status = string(domain.SourceIdle)
			}
		}
		if p.AutoUpdate && p.Enabled {
			if p.NextSyncAt == nil || !cur.AutoUpdate || !cur.Enabled {
				p.NextSyncAt = &t.now
			}
		} else {
			p.NextSyncAt = nil
		}
		var selected, excluded []string
		for raw, sel := range in.Selection {
			if sel != domain.EntrySelected && sel != domain.EntryExcluded {
				return domain.Invalid("selection", fmt.Sprintf("%q: use selected ou excluded", sel))
			}
			pth, err := skillgit.CleanPath(raw)
			if err != nil {
				return domain.Invalid("selection", fmt.Sprintf("%q: %v", raw, err))
			}
			if pth == "" {
				pth = "."
			}
			e, err := t.q.SetSkillSourceEntrySelection(t.ctx, store.SetSkillSourceEntrySelectionParams{CompanyID: companyID, SourceID: id,
				Path: pth, Selection: string(sel), Now: t.now})
			if isNoRows(err) {
				return domain.Invalid("selection", fmt.Sprintf("a fonte não tem o caminho %q", pth))
			}
			if err != nil {
				return fmt.Errorf("set entry selection: %w", err)
			}
			if sel == domain.EntryExcluded {
				excluded = append(excluded, pth)
				if e.SkillID != nil {
					if err := t.q.DetachSkillFromSource(t.ctx, store.DetachSkillFromSourceParams{CompanyID: companyID, ID: *e.SkillID}); err != nil {
						return fmt.Errorf("detach skill: %w", err)
					}
					if err := t.q.SetSkillSourceEntryResult(t.ctx, store.SetSkillSourceEntryResultParams{CompanyID: companyID, ID: e.ID, Now: t.now}); err != nil {
						return fmt.Errorf("entry result: %w", err)
					}
				}
			} else {
				selected = append(selected, pth)
				if e.SkillID == nil && p.AutoUpdate && p.Enabled {
					p.NextSyncAt = &t.now // a importação sai no próximo tick (ou em "Sincronizar agora")
				}
			}
		}
		if len(selected) > 0 {
			slices.Sort(selected)
			changed["selected"] = selected
		}
		if len(excluded) > 0 {
			slices.Sort(excluded)
			changed["excluded"] = excluded
		}
		if _, err := t.q.UpdateSkillSourceSettings(t.ctx, p); err != nil {
			return mapErr(err, "update skill source")
		}
		if len(changed) == 0 {
			return nil
		}
		return t.activity(actor, companyID, "skill_source.updated", "skill_source", id, changed, "")
	})
	if err != nil {
		return SkillSourceView{}, err
	}
	return s.getSkillSource(ctx, companyID, id)
}

// DeleteSkillSource desconecta a fonte (admin). keepSkills mantém as skills importadas como skills locais; senão
// elas são apagadas junto.
func (s *Service) DeleteSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, keepSkills bool) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetSkillSourceForUpdate(t.ctx, store.GetSkillSourceForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get skill source")
		}
		var n int64
		var removed []string
		if keepSkills {
			if n, err = t.q.DetachSourceSkills(t.ctx, store.DetachSourceSkillsParams{CompanyID: companyID, SourceID: &id}); err != nil {
				return fmt.Errorf("detach source skills: %w", err)
			}
		} else {
			rows, err := t.q.DeleteSourceSkills(t.ctx, store.DeleteSourceSkillsParams{CompanyID: companyID, SourceID: &id})
			if err != nil {
				return fmt.Errorf("delete source skills: %w", err)
			}
			for _, r := range rows {
				removed = append(removed, r.Slug)
				if err := t.activity(actor, companyID, "skill.deleted", "skill", r.ID, map[string]any{"slug": r.Slug, "source_id": id}, ""); err != nil {
					return err
				}
			}
			n = int64(len(rows))
		}
		if _, err := t.q.DeleteSkillSource(t.ctx, store.DeleteSkillSourceParams{CompanyID: companyID, ID: id}); err != nil {
			return fmt.Errorf("delete skill source: %w", err)
		}
		return t.activity(actor, companyID, "skill_source.deleted", "skill_source", id,
			map[string]any{"url": cur.Url, "ref": cur.TrackingRef, "keep_skills": keepSkills, "skills": n, "removed": removed}, "")
	})
}

// SyncSkillSource sincroniza agora (admin). force reimporta mesmo sem mudança na fonte e sobrescreve skills
// modificadas localmente. Uma falha de clone não é erro da chamada: vem em SkillSyncResult.Error.
func (s *Service) SyncSkillSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, force bool) (SkillSyncResult, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return SkillSyncResult{}, err
	}
	src, err := s.read().GetSkillSource(ctx, store.GetSkillSourceParams{CompanyID: companyID, ID: id})
	if err != nil {
		return SkillSyncResult{}, mapErr(err, "get skill source")
	}
	if !isPluginSource(src.Url) {
		if err := s.requireSourcesOn(); err != nil {
			return SkillSyncResult{}, err
		}
	}
	return s.syncSource(ctx, actor, companyID, id, nil, force)
}

// SkillSourceRef identifica uma fonte devida.
type SkillSourceRef struct {
	CompanyID, ID domain.ID
	// URL distingue a fonte de plugin (sem clone) da fonte de repositório.
	URL string
}

// DueSkillSources lista as fontes com atualização automática vencida (tick). Com as fontes desligadas, só as de
// plugin (que não clonam nada) seguem; com os plugins desligados, só as de repositório.
func (s *Service) DueSkillSources(ctx context.Context, now time.Time, limit int) ([]SkillSourceRef, error) {
	if !s.SkillSources.Enabled && !s.Plugins.Enabled() {
		return nil, nil
	}
	rows, err := s.read().DueSkillSources(ctx, store.DueSkillSourcesParams{Now: &now, StaleBefore: ptr(now.Add(-skillSyncStaleAfter)), Lim: i32(clamp(limit, 20, 200))})
	if err != nil {
		return nil, fmt.Errorf("due skill sources: %w", err)
	}
	out := make([]SkillSourceRef, 0, len(rows))
	for _, r := range rows {
		if plug := isPluginSource(r.Url); (plug && !s.Plugins.Enabled()) || (!plug && !s.SkillSources.Enabled) {
			continue
		}
		out = append(out, SkillSourceRef{CompanyID: r.CompanyID, ID: r.ID, URL: r.Url})
	}
	return out, nil
}

// SyncDueSkillSource sincroniza uma fonte pelo tick (sem identidade: ator sistema).
func (s *Service) SyncDueSkillSource(ctx context.Context, ref SkillSourceRef) (SkillSyncResult, error) {
	if !isPluginSource(ref.URL) {
		if err := s.requireSourcesOn(); err != nil {
			return SkillSyncResult{}, err
		}
	}
	return s.syncSource(ctx, domain.SystemActor, ref.CompanyID, ref.ID, nil, false)
}

// selectAll, na seleção inicial, escolhe todas as entradas encontradas (não é um caminho válido).
const selectAll = "\x00*"

// syncSource é a sincronização: reivindica a fonte, clona fora de transação e aplica o retrato numa transação só.
func (s *Service) syncSource(ctx context.Context, actor domain.Actor, companyID, id domain.ID, initial map[string]bool, force bool) (SkillSyncResult, error) {
	now := s.Now().UTC()
	q := s.read()
	src, err := q.ClaimSkillSourceSync(ctx, store.ClaimSkillSourceSyncParams{CompanyID: companyID, ID: id, Now: &now, StaleBefore: ptr(now.Add(-skillSyncStaleAfter))})
	if isNoRows(err) {
		if _, gerr := q.GetSkillSource(ctx, store.GetSkillSourceParams{CompanyID: companyID, ID: id}); gerr != nil {
			return SkillSyncResult{}, mapErr(gerr, "get skill source")
		}
		return SkillSyncResult{}, fmt.Errorf("a fonte já está sincronizando: %w", domain.ErrConflict)
	}
	if err != nil {
		return SkillSyncResult{}, mapErr(err, "claim skill source")
	}
	snap, ferr := s.fetchSkillSnapshot(ctx, src)
	if ferr != nil {
		return s.failSync(ctx, actor, src, ferr)
	}
	var res SkillSyncResult
	err = s.withTx(ctx, func(t *txc) error {
		res = SkillSyncResult{Commit: snap.Commit}
		return t.applySnapshot(actor, src, snap, initial, force, &res)
	})
	if err != nil {
		return s.failSync(ctx, actor, src, err)
	}
	return res, nil
}

// failSync registra a falha com backoff e devolve o resultado com o erro.
func (s *Service) failSync(ctx context.Context, actor domain.Actor, src store.SkillSource, cause error) (SkillSyncResult, error) {
	msg := clipText(cause.Error(), maxSourceError)
	fctx := context.WithoutCancel(ctx)
	err := s.withTx(fctx, func(t *txc) error {
		failures := src.FailureCount + 1
		var next *time.Time
		if src.AutoUpdate && src.Enabled {
			n := t.now.Add(domain.SkillSourceBackoff(int(failures)))
			next = &n
		}
		if _, err := t.q.FinishSkillSourceSync(t.ctx, store.FinishSkillSourceSyncParams{CompanyID: src.CompanyID, ID: src.ID,
			Status: string(domain.SourceError), LastError: msg, FailureCount: failures, NextSyncAt: next, Now: t.now}); err != nil {
			return mapErr(err, "finish skill source sync")
		}
		return t.activity(actor, src.CompanyID, "skill_source.sync_failed", "skill_source", src.ID,
			map[string]any{"error": msg, "failures": failures, "next_sync_at": next}, "")
	})
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return SkillSyncResult{}, err
	}
	return SkillSyncResult{Error: msg}, nil
}

// applySnapshot grava as entradas encontradas e importa/atualiza as selecionadas.
func (t *txc) applySnapshot(actor domain.Actor, src store.SkillSource, snap skillgit.Snapshot, initial map[string]bool, force bool, res *SkillSyncResult) error {
	unchanged := src.LastCommit != nil && *src.LastCommit == snap.Commit && src.LastTreeHash != nil && *src.LastTreeHash == snap.Tree
	paths := make([]string, 0, len(snap.Skills))
	for _, f := range snap.Skills {
		sel := domain.EntryNew
		switch {
		case initial != nil && (initial[selectAll] || initial[f.Path]):
			sel = domain.EntrySelected
		case initial != nil:
			sel = domain.EntryExcluded
		case src.ImportNew:
			sel = domain.EntrySelected
		}
		e, err := t.q.UpsertSkillSourceEntry(t.ctx, store.UpsertSkillSourceEntryParams{ID: domain.NewID(), CompanyID: src.CompanyID, SourceID: src.ID,
			Path: f.Path, Name: skillNameFor(f), Description: f.Description, Selection: string(sel), HasScripts: f.HasScripts, Error: f.Err, Now: t.now})
		if err != nil {
			return fmt.Errorf("upsert source entry: %w", err)
		}
		paths = append(paths, f.Path)
		if f.Err != "" {
			res.Skipped++
			continue
		}
		if domain.EntrySelection(e.Selection) != domain.EntrySelected {
			continue
		}
		if err := t.applyEntry(actor, src, e, f, snap.Commit, unchanged, force, res); err != nil {
			return err
		}
	}
	if err := t.q.MarkSkillSourceEntriesAbsent(t.ctx, store.MarkSkillSourceEntriesAbsentParams{CompanyID: src.CompanyID, SourceID: src.ID,
		Paths: paths, Now: t.now}); err != nil {
		return fmt.Errorf("mark absent entries: %w", err)
	}
	status := domain.SourceOK
	if !src.AutoUpdate {
		status = domain.SourcePaused
	}
	var next *time.Time
	if src.AutoUpdate && src.Enabled {
		n := t.now.Add(domain.SourceSyncInterval)
		next = &n
	}
	note := ""
	if snap.Truncated {
		note = fmt.Sprintf("a fonte tem mais de %d skills: só as %d primeiras (por caminho) foram lidas", domain.MaxSkillsPerSource, domain.MaxSkillsPerSource)
		res.Notes = append(res.Notes, note)
	}
	if _, err := t.q.FinishSkillSourceSync(t.ctx, store.FinishSkillSourceSyncParams{CompanyID: src.CompanyID, ID: src.ID, Status: string(status),
		LastError: note, FailureCount: 0, NextSyncAt: next, LastCommit: &snap.Commit, LastTreeHash: &snap.Tree, LastSuccessAt: &t.now, Now: t.now}); err != nil {
		return mapErr(err, "finish skill source sync")
	}
	return t.activity(actor, src.CompanyID, "skill_source.synced", "skill_source", src.ID, map[string]any{"commit": snap.Commit,
		"imported": res.Imported, "updated": res.Updated, "unchanged": res.Unchanged, "skipped": res.Skipped, "force": force}, "")
}

// applyEntry importa (primeira vez) ou atualiza a skill de uma entrada selecionada.
func (t *txc) applyEntry(actor domain.Actor, src store.SkillSource, e store.SkillSourceEntry, f skillgit.Found, commit string, unchanged, force bool, res *SkillSyncResult) error {
	setResult := func(skillID *domain.ID, msg string) error {
		if err := t.q.SetSkillSourceEntryResult(t.ctx, store.SetSkillSourceEntryResultParams{CompanyID: src.CompanyID, ID: e.ID,
			SkillID: skillID, Error: msg, Now: t.now}); err != nil {
			return fmt.Errorf("entry result: %w", err)
		}
		return nil
	}
	n, d, b, err := normalizeSkill(skillNameFor(f), f.Description, f.Raw)
	if err != nil {
		res.Skipped++
		return setResult(e.SkillID, err.Error())
	}
	if e.SkillID == nil {
		slug, err := resolveSlug("", n)
		if err != nil || slices.Contains(reservedUISlugs, slug) {
			res.Skipped++
			return setResult(nil, "o nome da skill não gera um identificador válido")
		}
		if ex, err := t.q.GetSkillBySlug(t.ctx, store.GetSkillBySlugParams{CompanyID: src.CompanyID, Slug: slug}); err == nil {
			res.Skipped++
			res.Notes = append(res.Notes, fmt.Sprintf("%s: já existe a skill %q na empresa", f.Path, ex.Slug))
			return setResult(nil, fmt.Sprintf("já existe uma skill %q na empresa: renomeie uma delas", ex.Slug))
		} else if !isNoRows(err) {
			return fmt.Errorf("get skill by slug: %w", err)
		}
		row, err := t.q.CreateSourceSkill(t.ctx, store.CreateSourceSkillParams{ID: domain.NewID(), CompanyID: src.CompanyID, Slug: slug,
			Name: n, Description: d, Body: b, SourceID: &src.ID, SourcePath: f.Path, SourceCommit: &commit, Now: t.now})
		if err != nil {
			return mapErr(err, "create source skill")
		}
		if err := t.recordSkillVersion(row); err != nil {
			return err
		}
		res.Imported++
		if err := setResult(&row.ID, ""); err != nil {
			return err
		}
		return t.activity(actor, src.CompanyID, "skill.created", "skill", row.ID,
			map[string]any{"slug": row.Slug, "name": row.Name, "version": row.Version, "source_id": src.ID, "source_path": f.Path, "commit": commit}, "")
	}
	cur, err := t.q.GetSkillByIDForUpdate(t.ctx, store.GetSkillByIDForUpdateParams{CompanyID: src.CompanyID, ID: *e.SkillID})
	if isNoRows(err) {
		return setResult(nil, "")
	}
	if err != nil {
		return mapErr(err, "get skill")
	}
	if cur.SourceID == nil || *cur.SourceID != src.ID {
		return setResult(nil, "")
	}
	if unchanged && !force {
		res.Unchanged++
		return nil
	}
	if cur.SourceModified && !force {
		res.Skipped++
		return setResult(&cur.ID, "modificada localmente: a sincronização não a sobrescreve (use forçar para sobrescrever)")
	}
	if n == cur.Name && d == cur.Description && b == cur.Body {
		res.Unchanged++
		if err := t.q.TouchSourceSkillCommit(t.ctx, store.TouchSourceSkillCommitParams{CompanyID: src.CompanyID, ID: cur.ID, SourceCommit: &commit}); err != nil {
			return fmt.Errorf("touch source commit: %w", err)
		}
		if cur.SourceModified {
			if err := t.q.SetSkillSourceModified(t.ctx, store.SetSkillSourceModifiedParams{CompanyID: src.CompanyID, ID: cur.ID, Modified: false}); err != nil {
				return fmt.Errorf("mark skill modified: %w", err)
			}
		}
		return setResult(&cur.ID, "")
	}
	row, err := t.q.UpdateSourceSkill(t.ctx, store.UpdateSourceSkillParams{CompanyID: src.CompanyID, ID: cur.ID, Name: n, Description: d, Body: b,
		Version: cur.Version + 1, SourceCommit: &commit, Now: t.now})
	if err != nil {
		return mapErr(err, "update source skill")
	}
	if err := t.recordSkillVersion(row); err != nil {
		return err
	}
	res.Updated++
	if err := setResult(&row.ID, ""); err != nil {
		return err
	}
	return t.activity(actor, src.CompanyID, "skill.updated", "skill", row.ID,
		map[string]any{"slug": row.Slug, "version": row.Version, "source_id": src.ID, "commit": commit, "force": force}, "")
}
