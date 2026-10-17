package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
)

// Rascunhos no servidor (ADR-0052): o texto começado de um comentário de task ou de uma mensagem de conversa, por
// usuário. Persona (run token) não tem rascunho.

type draftBackend interface {
	GetDraft(ctx context.Context, companyID domain.ID, target domain.DraftTarget, targetID domain.ID) (domain.Draft, error)
	SaveDraft(ctx context.Context, companyID domain.ID, target domain.DraftTarget, targetID domain.ID, body string) (domain.Draft, error)
	DeleteDraft(ctx context.Context, companyID domain.ID, target domain.DraftTarget, targetID domain.ID) error
}

// Draft é o rascunho do usuário para um alvo.
type Draft struct {
	TargetType string     `json:"target_type" enum:"task,conversation"`
	TargetID   string     `json:"target_id" format:"uuid"`
	Body       string     `json:"body" doc:"Vazio quando não há rascunho."`
	Exists     bool       `json:"exists"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type DraftPath struct {
	CompanyPath
	TargetType string `path:"target_type" enum:"task,conversation" doc:"task (comentário) ou conversation (mensagem de chat ou sala)."`
	TargetID   string `path:"target_id" format:"uuid" doc:"Id da task ou da conversa."`
}

type putDraftIn struct {
	DraftPath
	Body struct {
		Body string `json:"body" maxLength:"100000" doc:"O texto. Em branco apaga o rascunho."`
	}
}

func draftOut(d domain.Draft) Draft {
	o := Draft{TargetType: string(d.TargetType), TargetID: idStr(d.TargetID), Body: d.Body, Exists: d.Exists()}
	if d.Exists() {
		t := d.UpdatedAt
		o.UpdatedAt = &t
	}
	return o
}

func (s *server) registerDrafts(api huma.API) {
	with := func(ctx context.Context, p DraftPath) (draftBackend, scope, domain.DraftTarget, domain.ID, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, scope{}, "", domain.ID{}, err
		}
		db, ok := s.svc.(draftBackend)
		if !ok {
			return nil, scope{}, "", domain.ID{}, problem(http.StatusNotImplemented, "rascunhos indisponíveis neste servidor")
		}
		sc, err := s.scope(ctx, p.Company)
		if err != nil {
			return nil, scope{}, "", domain.ID{}, err
		}
		id, err := domain.ParseID(p.TargetID)
		if err != nil {
			return nil, scope{}, "", domain.ID{}, s.fail(ctx, domain.Invalid("target_id", "não é um uuid"))
		}
		return db, sc, domain.DraftTarget(p.TargetType), id, nil
	}

	huma.Register(api, op("get-draft", http.MethodGet, "/companies/{company}/drafts/{target_type}/{target_id}", "drafts", "Lê o seu rascunho para uma task ou conversa"),
		func(ctx context.Context, in *DraftPath) (*ItemOut[Draft], error) {
			db, sc, tt, id, err := with(ctx, *in)
			if err != nil {
				return nil, err
			}
			d, err := db.GetDraft(ctx, sc.Company.ID, tt, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Draft]{Body: draftOut(d)}, nil
		})

	huma.Register(api, op("put-draft", http.MethodPut, "/companies/{company}/drafts/{target_type}/{target_id}", "drafts", "Grava o seu rascunho (texto em branco apaga)"),
		func(ctx context.Context, in *putDraftIn) (*ItemOut[Draft], error) {
			db, sc, tt, id, err := with(ctx, in.DraftPath)
			if err != nil {
				return nil, err
			}
			d, err := db.SaveDraft(ctx, sc.Company.ID, tt, id, in.Body.Body)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Draft]{Body: draftOut(d)}, nil
		})

	del := op("delete-draft", http.MethodDelete, "/companies/{company}/drafts/{target_type}/{target_id}", "drafts", "Apaga o seu rascunho")
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *DraftPath) (*struct{}, error) {
		db, sc, tt, id, err := with(ctx, *in)
		if err != nil {
			return nil, err
		}
		if err := db.DeleteDraft(ctx, sc.Company.ID, tt, id); err != nil {
			return nil, s.fail(ctx, err)
		}
		return &struct{}{}, nil
	})
}
