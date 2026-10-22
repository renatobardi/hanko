package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/store"
)

// Pacotes de time hanko.team.v1 (ADR-0044): exportar um time (personas + skills + rotinas) e instalar um pacote numa empresa
// existente. A instalação é o import com merge do ADR-0057 (prévia, conflito por item) sobre um subset do company.v1:
// personas entram pausadas e rotinas desligadas, e as assinaturas são escolhidas por quem instala.

// Origem de uma instalação, para o registro.
const (
	TeamSourceCatalog = "catalog"
	TeamSourceFile    = "file"
)

// TeamExportOptions escolhe o que sai no pacote.
type TeamExportOptions struct {
	// Personas são os slugs a exportar; vazio = todas as personas da empresa.
	Personas []string
	// Identificação do pacote; vazios usam valores derivados da empresa.
	Slug, Name, Description, Version, Author string
}

// ExportTeam monta um pacote de time a partir da empresa: as personas escolhidas, as skills que elas usam e as rotinas
// delas (desligadas). O organograma e o revisor só ficam quando o outro lado também está no pacote. Admin.
func (s *Service) ExportTeam(ctx context.Context, companyID domain.ID, opts TeamExportOptions) (export.TeamPackage, error) {
	ctx, span := lifecycleSpan(ctx, "team.export")
	defer span.End()
	doc, err := s.ExportCompany(ctx, companyID, ExportOptions{})
	if err != nil {
		return export.TeamPackage{}, err
	}
	want := map[string]bool{}
	for _, sl := range opts.Personas {
		if sl = strings.TrimSpace(sl); sl != "" {
			want[sl] = true
		}
	}
	have := map[string]bool{}
	for _, p := range doc.Personas {
		have[p.Slug] = true
	}
	for sl := range want {
		if !have[sl] {
			return export.TeamPackage{}, domain.Invalid("personas", fmt.Sprintf("persona %q não existe na empresa", sl))
		}
	}
	in := func(slug string) bool { return len(want) == 0 || want[slug] }

	pkg := export.TeamPackage{Format: export.TeamFormat, ExportedAt: s.Now().UTC()}
	usedSkills, usedProjects := map[string]bool{}, map[string]bool{}
	for _, p := range doc.Personas {
		if !in(p.Slug) {
			continue
		}
		p.Status, p.Workspace, p.Subscription = "paused", "", ""
		p.CreatedAt = p.CreatedAt.UTC().Truncate(0)
		if p.ReportsTo != "" && !in(p.ReportsTo) {
			p.ReportsTo = ""
		}
		if p.Reviewer != "" && !in(p.Reviewer) {
			p.Reviewer = ""
			if p.ReviewerAgent == "" {
				p.RequireReview = false // sem revisor no pacote, não há quem revise
			}
		}
		for _, sk := range p.Skills {
			usedSkills[sk] = true
		}
		pkg.Personas = append(pkg.Personas, p)
	}
	if len(pkg.Personas) == 0 {
		return export.TeamPackage{}, domain.Invalid("personas", "a empresa não tem personas para exportar")
	}
	for _, f := range doc.Flows {
		if !in(f.Persona) {
			continue
		}
		f.Enabled = false
		if f.Project != "" {
			usedProjects[f.Project] = true
		}
		pkg.Flows = append(pkg.Flows, f)
	}
	for _, sk := range doc.Skills {
		if usedSkills[sk.Slug] {
			pkg.Skills = append(pkg.Skills, sk)
		}
	}
	for _, pr := range doc.Projects {
		if usedProjects[pr.Slug] {
			pr.Workspace = ""
			pkg.Projects = append(pkg.Projects, pr)
		}
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "Time de " + doc.Company.Name
	}
	slug := strings.TrimSpace(opts.Slug)
	if slug == "" {
		slug = domain.Slugify(name)
	}
	version := opts.Version
	if version == "" {
		version = "1.0.0"
	}
	pkg.Package = export.TeamInfo{Slug: slug, Name: name, Description: opts.Description, Version: version, Author: opts.Author}
	pkg.Requires = pkg.ComputeRequires()
	if err := pkg.Validate(); err != nil {
		return export.TeamPackage{}, teamInvalid(err)
	}
	return pkg, nil
}

func teamInvalid(err error) error {
	var te *export.TeamError
	if errors.As(err, &te) {
		return domain.Invalid(te.Field, te.Msg)
	}
	return domain.Invalid("package", err.Error())
}

// TeamInstallOptions controla a prévia e a instalação.
type TeamInstallOptions struct {
	// OnConflict é a ação padrão para o que já existe no destino (padrão skip); Resolve, as exceções "tipo/chave" → ação.
	OnConflict domain.ConflictAction
	Resolve    map[string]domain.ConflictAction
	// Subscriptions escolhe, por harness (claude, codex...), a assinatura EXISTENTE da empresa que as personas desse harness usam.
	Subscriptions map[string]string
	// Source diz de onde veio o pacote (catalog | file); vazio = file.
	Source string
	// DryRun só valida e devolve a prévia: nada é gravado.
	DryRun bool
}

// TeamAgentReq é a situação de um harness exigido pelo pacote.
type TeamAgentReq struct {
	Agent string `json:"agent"`
	// Subscriptions são as assinaturas ativas da empresa para esse harness.
	Subscriptions []string `json:"subscriptions"`
	// Chosen é a assinatura escolhida na instalação (vazio = nenhuma; as personas usam o login do harness).
	Chosen string `json:"chosen,omitempty"`
}

// TeamSecretReq é um segredo citado pelo pacote.
type TeamSecretReq struct {
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
}

// TeamInstall é a prévia ou o resultado de uma instalação.
type TeamInstall struct {
	Package export.TeamInfo `json:"package"`
	SHA256  string          `json:"sha256"`
	Agents  []TeamAgentReq  `json:"agents"`
	Secrets []TeamSecretReq `json:"secrets"`
	// Warnings somam os avisos do import e os do pacote (assinatura, segredo).
	Warnings []ImportWarning `json:"warnings"`
	Result   ImportResult    `json:"-"`
	DryRun   bool            `json:"dry_run"`
	// InstallID só existe depois de instalar.
	InstallID *domain.ID `json:"install_id,omitempty"`
}

// InstallTeam instala (ou, com DryRun, só mostra a prévia de) um pacote de time numa empresa existente. Admin.
func (s *Service) InstallTeam(ctx context.Context, actor domain.Actor, companyID domain.ID, pkg export.TeamPackage, opts TeamInstallOptions) (TeamInstall, error) {
	ctx, span := lifecycleSpan(ctx, "team.install")
	defer span.End()
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return TeamInstall{}, err
	}
	if actor.Type != domain.ActorHuman {
		return TeamInstall{}, fmt.Errorf("install team: só um humano instala pacote de time: %w", domain.ErrNotAllowed)
	}
	if err := pkg.Validate(); err != nil {
		return TeamInstall{}, teamInvalid(err)
	}
	pkg.Requires = pkg.ComputeRequires() // o que o arquivo diz sobre si não vale: o instalador recalcula
	raw, err := json.Marshal(pkg)
	if err != nil {
		return TeamInstall{}, fmt.Errorf("marshal team package: %w", err)
	}
	sum := sha256.Sum256(raw)
	out := TeamInstall{Package: pkg.Package, SHA256: hex.EncodeToString(sum[:]), DryRun: opts.DryRun}

	q := s.read()
	subs, err := q.ListSubscriptions(ctx, companyID)
	if err != nil {
		return out, fmt.Errorf("list subscriptions: %w", err)
	}
	doc := pkg.Document()
	resolve := map[string]domain.ConflictAction{}
	for k, v := range opts.Resolve {
		resolve[k] = v
	}
	for agent := range opts.Subscriptions {
		if !slices.Contains(pkg.Requires.Agents, agent) {
			return out, domain.Invalid("subscriptions", fmt.Sprintf("o pacote não usa o harness %q", agent))
		}
	}
	noSub, missing := 0, 0
	for _, agent := range pkg.Requires.Agents {
		req := TeamAgentReq{Agent: agent, Subscriptions: []string{}}
		for _, sb := range subs {
			if sb.Agent == agent && sb.Active && sb.EndedAt == nil {
				req.Subscriptions = append(req.Subscriptions, sb.Name)
			}
		}
		if chosen := strings.TrimSpace(opts.Subscriptions[agent]); chosen != "" {
			var row *store.Subscription
			for i := range subs {
				if subs[i].Name == chosen {
					row = &subs[i]
				}
			}
			if row == nil || row.Agent != agent {
				return out, domain.Invalid("subscriptions", fmt.Sprintf("a assinatura %q não existe na empresa para o harness %s", chosen, agent))
			}
			req.Chosen = chosen
			doc.Subscriptions = append(doc.Subscriptions, export.Subscription{Name: row.Name, Agent: row.Agent, MonthlyUSD: row.MonthlyUsd,
				ModelPrefixes: nonEmpty(row.ModelPrefixes), WeeklyLimitUSD: row.WeeklyLimitUsd, WeeklyGuardPct: int(row.WeeklyGuardPct), Active: row.Active})
			resolve[KindSubscriptions+"/"+row.Name] = domain.ConflictSkip // a assinatura é da empresa: nunca é alterada pelo pacote
			for i := range doc.Personas {
				if doc.Personas[i].Agent == agent {
					doc.Personas[i].Subscription = row.Name
				}
			}
		}
		out.Agents = append(out.Agents, req)
		if req.Chosen == "" && len(req.Subscriptions) == 0 && agent != "fake" {
			noSub += countAgent(pkg, agent)
		}
	}
	if noSub > 0 {
		out.Warnings = append(out.Warnings, ImportWarning{Code: "team_no_subscription", Count: noSub,
			Message: "%d persona(s) usam um harness sem assinatura ativa na empresa: rodam com o login do harness até você escolher uma assinatura"})
	}
	for _, name := range pkg.Requires.Secrets {
		_, serr := q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: name})
		out.Secrets = append(out.Secrets, TeamSecretReq{Name: name, Exists: serr == nil})
		if serr != nil {
			missing++
		}
	}
	if missing > 0 {
		out.Warnings = append(out.Warnings, ImportWarning{Code: "team_missing_secret", Count: missing,
			Message: "%d segredo(s) citados pelo pacote não existem na empresa: crie-os antes de liberar as personas"})
	}

	res, err := s.ImportCompany(ctx, actor, doc, ImportOptions{Into: &companyID, OnConflict: opts.OnConflict, Resolve: resolve, DryRun: opts.DryRun})
	if err != nil {
		return out, err
	}
	out.Result = res
	out.Warnings = append(out.Warnings, res.Warnings...)
	if opts.DryRun {
		return out, nil
	}
	source := opts.Source
	if source == "" {
		source = TeamSourceFile
	}
	counts, err := json.Marshal(res.Counts)
	if err != nil {
		return out, fmt.Errorf("marshal counts: %w", err)
	}
	err = s.withTx(ctx, func(t *txc) error {
		row, e := t.q.InsertTeamInstall(ctx, store.InsertTeamInstallParams{ID: domain.NewID(), CompanyID: companyID, PackageSlug: pkg.Package.Slug,
			PackageName: pkg.Package.Name, PackageVersion: pkg.Package.Version, Source: source, Sha256: out.SHA256, Counts: counts,
			InstalledByType: string(actor.Type), InstalledByID: actor.ID, Now: t.now})
		if e != nil {
			return mapErr(e, "insert team install")
		}
		out.InstallID = &row.ID
		return t.activity(actor, companyID, "team.installed", "team_install", row.ID, map[string]any{"package": pkg.Package.Slug,
			"name": pkg.Package.Name, "version": pkg.Package.Version, "source": source, "sha256": out.SHA256, "counts": res.Counts,
			"actions": tallyItems(res.Items)}, "")
	})
	return out, err
}

func countAgent(p export.TeamPackage, agent string) int {
	n := 0
	for _, x := range p.Personas {
		if x.Agent == agent {
			n++
		}
	}
	return n
}

func tallyItems(items []ImportItem) map[string]int {
	m := map[string]int{}
	for _, it := range items {
		m[string(it.Action)]++
	}
	return m
}

// TeamInstallRecord é uma instalação já feita.
type TeamInstallRecord struct {
	ID          domain.ID      `json:"id"`
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Version     string         `json:"version,omitempty"`
	Source      string         `json:"source"`
	SHA256      string         `json:"sha256"`
	Counts      map[string]int `json:"counts"`
	InstalledBy domain.Actor   `json:"installed_by"`
	CreatedAt   string         `json:"created_at"`
}

// ListTeamInstalls lista as instalações de pacotes da empresa, da mais nova para a mais antiga. Qualquer membro.
func (s *Service) ListTeamInstalls(ctx context.Context, companyID domain.ID) ([]TeamInstallRecord, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListTeamInstalls(ctx, store.ListTeamInstallsParams{CompanyID: companyID, Lim: 100})
	if err != nil {
		return nil, fmt.Errorf("list team installs: %w", err)
	}
	out := make([]TeamInstallRecord, len(rows))
	for i, r := range rows {
		c := map[string]int{}
		_ = json.Unmarshal(r.Counts, &c)
		out[i] = TeamInstallRecord{ID: r.ID, Slug: r.PackageSlug, Name: r.PackageName, Version: r.PackageVersion, Source: r.Source,
			SHA256: r.Sha256, Counts: c, InstalledBy: actorOf(r.InstalledByType, r.InstalledByID), CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}
