package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Resiliência de run (ADR-0039): ações do operador sobre uma execução parada, steering e a fila de follow-ups.

// Followup é um item da fila unificada (chat + task): o que o operador mandou enquanto a execução trabalha.
type Followup struct {
	ID             string     `json:"id" format:"uuid"`
	Kind           string     `json:"kind" enum:"chat,task" doc:"De onde vem: uma mensagem de conversa ou uma instrução de task."`
	TaskID         string     `json:"task_id,omitempty" format:"uuid"`
	ConversationID string     `json:"conversation_id,omitempty" format:"uuid"`
	Body           string     `json:"body"`
	Author         ActorRef   `json:"author"`
	Delivery       string     `json:"delivery" enum:"queue,steer,restart" doc:"queue: espera a execução terminar; steer: injetado na execução viva; restart: a execução é interrompida e continua com ele."`
	Position       int        `json:"position" doc:"Ordem de entrega (1 = o primeiro a ser entregue)."`
	WaitReason     string     `json:"wait_reason" enum:"run_active,steer_pending,restart_pending,decision_pending,persona_unavailable,unassigned,starting" doc:"Por que ainda não foi entregue."`
	WaitDetail     string     `json:"wait_detail,omitempty" doc:"Complemento do motivo: o nome da execução, o estado da persona."`
	WaitRunID      string     `json:"wait_run_id,omitempty" format:"uuid"`
	CreatedAt      time.Time  `json:"created_at"`
	EditedAt       *time.Time `json:"edited_at,omitempty"`
}

func followupOut(f domain.Followup) Followup {
	return Followup{ID: idStr(f.ID), Kind: string(f.Kind), TaskID: idPtr(f.TaskID), ConversationID: idPtr(f.ConversationID), Body: f.Body,
		Author: actorRef(f.Author), Delivery: string(f.Delivery), Position: f.Position, WaitReason: string(f.WaitReason),
		WaitDetail: f.WaitDetail, WaitRunID: idPtr(f.WaitRunID), CreatedAt: f.CreatedAt, EditedAt: f.EditedAt}
}

// SteerResult diz como a mensagem foi tratada.
type SteerResult struct {
	Mode     string   `json:"mode" enum:"inject,restart" doc:"inject: a mensagem entra na execução viva; restart: a execução é interrompida e continua com ela."`
	Followup Followup `json:"followup"`
}

type runActionIn struct {
	RunPath
	Body struct {
		Action string `json:"action" enum:"retry,continue,close" doc:"retry = Tentar de novo (execução nova do zero); continue = Continuar (retoma a sessão com o briefing); close = Encerrar (fecha o card, sem execução nova)."`
		Note   string `json:"note,omitempty" maxLength:"2000" doc:"Nota do operador; entra no briefing da execução seguinte."`
	}
}

type steerRunIn struct {
	RunPath
	Body struct {
		Message string `json:"message" minLength:"1" maxLength:"20000" doc:"Instrução para a execução em andamento."`
	}
}

type queueFollowupIn struct {
	TaskPath
	Body struct {
		Message string `json:"message" minLength:"1" maxLength:"20000" doc:"Instrução que espera a execução da task e é entregue na próxima."`
	}
}

type FollowupPath struct {
	CompanyPath
	Followup string `path:"followup" format:"uuid" doc:"Id do follow-up."`
}

type editFollowupIn struct {
	FollowupPath
	Body struct {
		Message string `json:"message" minLength:"1" maxLength:"20000"`
	}
}

func (s *server) registerResilience(api huma.API) {
	huma.Register(api, op("run-action", http.MethodPost, "/companies/{company}/runs/{run}/actions", "runs", "Decide uma execução que parou (Tentar de novo, Continuar ou Encerrar)"),
		func(ctx context.Context, in *runActionIn) (*ItemOut[Run], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			rid, err := s.runID(in.Run)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			r, err := s.svc.RunAction(ctx, sc.Actor, sc.Company.ID, rid, service.RunActionInput{Action: domain.RunDecision(in.Body.Action), Note: in.Body.Note})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Run]{Body: runOut(r)}, nil
		})

	huma.Register(api, created(op("steer-run", http.MethodPost, "/companies/{company}/runs/{run}/steer", "runs", "Muda o rumo de uma execução ativa")),
		func(ctx context.Context, in *steerRunIn) (*ItemOut[SteerResult], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			rid, err := s.runID(in.Run)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			res, err := s.svc.SteerRun(ctx, sc.Actor, sc.Company.ID, rid, in.Body.Message)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SteerResult]{Body: SteerResult{Mode: string(res.Mode), Followup: followupOut(res.Followup)}}, nil
		})

	huma.Register(api, op("list-task-followups", http.MethodGet, "/companies/{company}/tasks/{task}/followups", "tasks", "Lista a fila de follow-ups de uma task"),
		func(ctx context.Context, in *TaskPath) (*ListOut[Followup], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			tv, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			fs, err := s.svc.ListFollowups(ctx, sc.Company.ID, service.FollowupTarget{TaskID: &tv.ID})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(fs, Page{Limit: len(fs)}, followupOut), nil
		})

	huma.Register(api, created(op("queue-task-followup", http.MethodPost, "/companies/{company}/tasks/{task}/followups", "tasks", "Enfileira uma instrução para a task")),
		func(ctx context.Context, in *queueFollowupIn) (*ItemOut[Followup], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			tv, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			f, err := s.svc.QueueTaskFollowup(ctx, sc.Actor, sc.Company.ID, tv.ID, in.Body.Message)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Followup]{Body: followupOut(f)}, nil
		})

	huma.Register(api, op("list-conversation-followups", http.MethodGet, "/companies/{company}/conversations/{conversation}/followups", "chat", "Lista a fila de mensagens que esperam a resposta da persona"),
		func(ctx context.Context, in *ConversationPath) (*ListOut[Followup], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.conversationID(in.Conversation)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			fs, err := s.svc.ListFollowups(ctx, sc.Company.ID, service.FollowupTarget{ConversationID: &id})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(fs, Page{Limit: len(fs)}, followupOut), nil
		})

	huma.Register(api, op("edit-followup", http.MethodPatch, "/companies/{company}/followups/{followup}", "runs", "Edita um follow-up que ainda não foi entregue"),
		func(ctx context.Context, in *editFollowupIn) (*ItemOut[Followup], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Followup)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("followup", "não é um uuid"))
			}
			f, err := s.svc.EditFollowup(ctx, sc.Actor, sc.Company.ID, id, in.Body.Message)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Followup]{Body: followupOut(f)}, nil
		})

	huma.Register(api, noContent(op("discard-followup", http.MethodDelete, "/companies/{company}/followups/{followup}", "runs", "Descarta um follow-up que ainda não foi entregue")),
		func(ctx context.Context, in *FollowupPath) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Followup)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("followup", "não é um uuid"))
			}
			if err := s.svc.DiscardFollowup(ctx, sc.Actor, sc.Company.ID, id); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})
}
