package service

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

func actorOf(typ string, id *uuid.UUID) domain.Actor {
	return domain.Actor{Type: domain.ActorType(typ), ID: id}
}

func companyFromRow(r store.Company) domain.Company {
	return domain.Company{ID: r.ID, Slug: r.Slug, Name: r.Name, TaskPrefix: r.TaskPrefix,
		Timezone: r.Timezone, BudgetMonthlyCents: r.BudgetMonthlyCents, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func workspaceFromRow(r store.Workspace) domain.Workspace {
	return domain.Workspace{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, Path: r.Path,
		RepoURL: r.RepoUrl, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func projectFromRow(r store.Project) domain.Project {
	return domain.Project{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name,
		Description: r.Description, WorkspaceID: r.WorkspaceID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func personaFromRow(r store.Persona) domain.Persona {
	reason := ""
	if r.PauseReason != nil {
		reason = *r.PauseReason
	}
	return domain.Persona{PauseReason: reason, ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, Title: r.Title,
		Instructions: r.Instructions, Agent: r.Agent, Model: r.Model, AdapterConfig: json.RawMessage(r.AdapterConfig),
		Status: domain.PersonaStatus(r.Status), ReportsTo: r.ReportsTo, WorkspaceID: r.WorkspaceID,
		HeartbeatIntervalSec: int(r.HeartbeatIntervalSec), BudgetMonthlyCents: r.BudgetMonthlyCents,
		RequireReview: r.RequireReview, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func taskFromRow(r store.Task, prefix string) domain.Task {
	return domain.Task{ID: r.ID, CompanyID: r.CompanyID, Number: r.Number, Key: domain.TaskKey(prefix, r.Number),
		Title: r.Title, Description: r.Description, Status: domain.TaskStatus(r.Status),
		Priority: domain.Priority(r.Priority), PersonaID: r.PersonaID, ProjectID: r.ProjectID,
		WorkspaceID: r.WorkspaceID, ParentID: r.ParentID, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ClosedAt: r.ClosedAt}
}

func taskView(r store.Task, prefix, personaName, projectName string, active *uuid.UUID) TaskView {
	return TaskView{Task: taskFromRow(r, prefix), PersonaName: personaName, ProjectName: projectName, ActiveRunID: active}
}

func commentFromRow(r store.TaskComment) domain.Comment {
	c := domain.Comment{ID: r.ID, CompanyID: r.CompanyID, TaskID: r.TaskID, Author: actorOf(r.AuthorType, r.AuthorID),
		RunID: r.RunID, Body: r.Body, CreatedAt: r.CreatedAt}
	if r.EventID != nil {
		c.EventID = *r.EventID
	}
	return c
}

func runFromRow(r store.Run) domain.Run {
	run := domain.Run{ID: r.ID, CompanyID: r.CompanyID, TaskID: r.TaskID, PersonaID: r.PersonaID,
		Status: domain.RunStatus(r.Status), Trigger: domain.RunTrigger(r.Trigger), Agent: r.Agent, Model: r.Model,
		SessionRef: r.SessionRef, TraceID: r.TraceID, Error: r.Error, Summary: r.Summary, CostUSD: r.CostUsd,
		CostSource: domain.CostSource(r.CostSource),
		Usage: domain.Usage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			CacheReadTokens: r.CacheReadTokens, CacheCreationTokens: r.CacheCreationTokens},
		HandoffIn: r.HandoffIn, HandoffOut: r.HandoffOut, CreatedAt: r.CreatedAt,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
	if r.ExitCode != nil {
		c := int(*r.ExitCode)
		run.ExitCode = &c
	}
	return run
}

func runEventFromRow(r store.RunEvent) domain.RunEvent {
	return domain.RunEvent{ID: r.ID, CompanyID: r.CompanyID, RunID: r.RunID, Seq: r.Seq,
		Kind: domain.RunEventKind(r.Kind), Text: r.Text, Payload: json.RawMessage(r.Payload),
		OccurredAt: r.OccurredAt, ReceivedAt: r.ReceivedAt}
}

func activityFromRow(r store.ActivityEvent) domain.ActivityEvent {
	e := domain.ActivityEvent{ID: r.ID, CompanyID: r.CompanyID, Actor: actorOf(r.ActorType, r.ActorID),
		Verb: r.Verb, SubjectType: r.SubjectType, SubjectID: r.SubjectID, Data: json.RawMessage(r.Data),
		OccurredAt: r.OccurredAt, ReceivedAt: r.ReceivedAt}
	if r.EventID != nil {
		e.EventID = *r.EventID
	}
	return e
}

func gateFromRow(r store.Gate) domain.Gate {
	g := domain.Gate{ID: r.ID, CompanyID: r.CompanyID, Kind: domain.GateKind(r.Kind), Status: domain.GateStatus(r.Status),
		Title: r.Title, Body: r.Body, TaskID: r.TaskID, RunID: r.RunID, PersonaID: r.PersonaID,
		RequestedBy: actorOf(r.RequestedByType, r.RequestedByID), DecisionNote: r.DecisionNote,
		CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt}
	if r.DecidedByType != nil {
		a := actorOf(*r.DecidedByType, r.DecidedByID)
		g.DecidedBy = &a
	}
	if r.EventID != nil {
		g.EventID = *r.EventID
	}
	return g
}
