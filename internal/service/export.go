package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/store"
)

// ExportOptions escolhe o que mais entra no arquivo além da configuração da empresa.
type ExportOptions struct {
	WithRuns bool // execuções encerradas
	WithLogs bool // e os eventos (log) delas; implica WithRuns
}

// exportStatus: estados transitórios de persona não são configuração e voltam como idle.
func exportStatus(s string) string {
	switch domain.PersonaStatus(s) {
	case domain.PersonaRunning, domain.PersonaError, domain.PersonaAwaitingGate:
		return string(domain.PersonaIdle)
	}
	return s
}

// ExportCompany monta o arquivo portátil da empresa (ADR-0022) numa leitura consistente (uma única
// transação de snapshot). Exige admin: as instruções das personas e o histórico não são para qualquer membro.
func (s *Service) ExportCompany(ctx context.Context, companyID domain.ID, opts ExportOptions) (export.Document, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return export.Document{}, err
	}
	if opts.WithLogs {
		opts.WithRuns = true
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return export.Document{}, fmt.Errorf("begin export tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := store.New(tx)

	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return export.Document{}, mapErr(err, "get company")
	}
	doc := export.Document{Format: export.Format, ExportedAt: s.Now().UTC(), Company: export.Company{
		Name: co.Name, Slug: co.Slug, TaskPrefix: co.TaskPrefix, Timezone: co.Timezone,
		BudgetMonthlyCents: co.BudgetMonthlyCents, NextTaskNumber: co.NextTaskNumber}}

	wsRows, err := q.ListWorkspaces(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export workspaces: %w", err)
	}
	wsSlug := map[domain.ID]string{}
	for _, w := range wsRows {
		wsSlug[w.ID] = w.Slug
		doc.Workspaces = append(doc.Workspaces, export.Workspace{Slug: w.Slug, Name: w.Name, Path: w.Path, RepoURL: w.RepoUrl, CreatedAt: w.CreatedAt})
	}
	prRows, err := q.ListProjects(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export projects: %w", err)
	}
	prSlug := map[domain.ID]string{}
	for _, p := range prRows {
		prSlug[p.ID] = p.Slug
		doc.Projects = append(doc.Projects, export.Project{Slug: p.Slug, Name: p.Name, Description: p.Description,
			Workspace: refSlug(wsSlug, p.WorkspaceID), CreatedAt: p.CreatedAt})
	}

	subRows, err := q.ListSubscriptions(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export subscriptions: %w", err)
	}
	periodRows, err := q.ListSubscriptionPeriods(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export subscription periods: %w", err)
	}
	periods := map[domain.ID][]export.Period{}
	for _, p := range periodRows {
		periods[p.SubscriptionID] = append(periods[p.SubscriptionID], export.Period{StartedAt: p.StartedAt, EndedAt: p.EndedAt})
	}
	subName := map[domain.ID]string{}
	for _, r := range subRows {
		subName[r.ID] = r.Name
		doc.Subscriptions = append(doc.Subscriptions, export.Subscription{Name: r.Name, Agent: r.Agent, MonthlyUSD: r.MonthlyUsd,
			ModelPrefixes: nonEmpty(r.ModelPrefixes), WeeklyLimitUSD: r.WeeklyLimitUsd, WeeklyGuardPct: int(r.WeeklyGuardPct),
			Active: r.Active, EndedAt: r.EndedAt, Periods: periods[r.ID], CreatedAt: r.CreatedAt})
	}

	peRows, err := q.ListPersonas(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export personas: %w", err)
	}
	peSlug := map[domain.ID]string{}
	for _, p := range peRows {
		peSlug[p.ID] = p.Slug
	}
	for _, p := range peRows {
		var cfg map[string]any
		if len(p.AdapterConfig) > 0 {
			if err := json.Unmarshal(p.AdapterConfig, &cfg); err != nil {
				return doc, fmt.Errorf("export persona %s: adapter_config: %w", p.Slug, err)
			}
		}
		doc.Personas = append(doc.Personas, export.Persona{Slug: p.Slug, Name: p.Name, Title: p.Title, Instructions: p.Instructions,
			Agent: p.Agent, Model: p.Model, AdapterConfig: export.ScrubConfig(cfg), Status: exportStatus(p.Status),
			ReportsTo: refSlug(peSlug, p.ReportsTo), Workspace: refSlug(wsSlug, p.WorkspaceID),
			HeartbeatIntervalSec: int(p.HeartbeatIntervalSec), BudgetMonthlyCents: p.BudgetMonthlyCents,
			Subscription: refSlug(subName, p.SubscriptionID), RequireReview: p.RequireReview,
			Reviewer: refSlug(peSlug, p.ReviewerPersonaID), ReviewerAgent: p.ReviewerAgent, ReviewerModel: p.ReviewerModel,
			CreatedAt: p.CreatedAt})
	}

	taskRows, err := q.ExportTasks(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export tasks: %w", err)
	}
	taskNum := map[domain.ID]int64{}
	for _, t := range taskRows {
		taskNum[t.ID] = t.Number
	}
	var runKey map[domain.ID]string
	var runRows []store.Run
	if opts.WithRuns {
		if runRows, err = q.ExportRuns(ctx, companyID); err != nil {
			return doc, fmt.Errorf("export runs: %w", err)
		}
		runKey = make(map[domain.ID]string, len(runRows))
		for i, r := range runRows {
			runKey[r.ID] = fmt.Sprintf("r%d", i+1)
		}
	}
	comRows, err := q.ExportComments(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export comments: %w", err)
	}
	comments := map[domain.ID][]export.Comment{}
	for _, c := range comRows {
		comments[c.TaskID] = append(comments[c.TaskID], export.Comment{Author: exportActor(c.AuthorType, c.AuthorID, peSlug),
			Body: c.Body, Run: refSlug(runKey, c.RunID), CreatedAt: c.CreatedAt})
	}
	for _, t := range taskRows {
		et := export.Task{Number: t.Number, Title: t.Title, Description: t.Description, Status: t.Status, Priority: t.Priority,
			Persona: refSlug(peSlug, t.PersonaID), Project: refSlug(prSlug, t.ProjectID), Workspace: refSlug(wsSlug, t.WorkspaceID),
			Model: t.Model, CreatedBy: exportActor(t.CreatedByType, t.CreatedByID, peSlug), CreatedAt: t.CreatedAt,
			UpdatedAt: t.UpdatedAt, ClosedAt: t.ClosedAt, Comments: comments[t.ID]}
		if t.ParentID != nil {
			et.Parent = taskNum[*t.ParentID]
		}
		doc.Tasks = append(doc.Tasks, et)
	}

	flRows, err := q.ListFlows(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export flows: %w", err)
	}
	for _, f := range flRows {
		doc.Flows = append(doc.Flows, export.Flow{Slug: f.Slug, Name: f.Name, Description: f.Description, Cron: f.Cron,
			Timezone: f.Timezone, Enabled: f.Enabled, Persona: peSlug[f.PersonaID], Project: refSlug(prSlug, f.ProjectID),
			Priority: f.Priority, TaskTitle: f.TaskTitle, TaskDescription: f.TaskDescription, CreatedAt: f.CreatedAt})
	}

	if opts.WithRuns {
		events := map[domain.ID][]export.RunEvent{}
		if opts.WithLogs {
			evRows, err := q.ExportRunEvents(ctx, companyID)
			if err != nil {
				return doc, fmt.Errorf("export run events: %w", err)
			}
			for _, e := range evRows {
				events[e.RunID] = append(events[e.RunID], export.RunEvent{Seq: e.Seq, Kind: e.Kind, Text: e.Text,
					Payload: e.Payload, OccurredAt: e.OccurredAt})
			}
		}
		for _, r := range runRows {
			var exit *int
			if r.ExitCode != nil {
				v := int(*r.ExitCode)
				exit = &v
			}
			doc.Runs = append(doc.Runs, export.Run{Key: runKey[r.ID], Name: r.Name, Task: taskNum[r.TaskID], Persona: peSlug[r.PersonaID],
				Status: r.Status, Trigger: r.Trigger, Agent: r.Agent, Model: r.Model, ModelSource: r.ModelSource,
				Subscription: refSlug(subName, r.SubscriptionID), ExitCode: exit, Error: r.Error, Summary: r.Summary,
				CostUSD: r.CostUsd, CostSource: r.CostSource, ListCostUSD: r.ListCostUsd,
				Usage: export.Usage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheReadTokens: r.CacheReadTokens,
					CacheCreationTokens: r.CacheCreationTokens},
				CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Events: events[r.ID]})
		}
	}
	return doc, nil
}

func nonEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// refSlug resolve um id opcional no nome que o arquivo usa para referenciá-lo.
func refSlug[K comparable](m map[K]string, id *K) string {
	if id == nil {
		return ""
	}
	return m[*id]
}

// exportActor: o id de um humano pertence à instalação de origem e não viaja; persona vai por slug.
// Persona que já não existe vira system, para o arquivo continuar válido.
func exportActor(typ string, id *domain.ID, personas map[domain.ID]string) export.Actor {
	if typ == string(domain.ActorPersona) {
		if slug := refSlug(personas, id); slug != "" {
			return export.Actor{Type: typ, Persona: slug}
		}
		return export.Actor{Type: string(domain.ActorSystem)}
	}
	return export.Actor{Type: typ}
}
