package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type activityIn struct {
	CompanyPath
	Limit       int    `query:"limit" minimum:"1" maximum:"500" default:"50" doc:"Máximo de eventos."`
	Before      string `query:"before" doc:"Cursor: só eventos anteriores a este instante (RFC 3339). Use o occurred_at do último item para a próxima página."`
	SubjectType string `query:"subject_type" doc:"Filtra pelo tipo do sujeito (task, run, persona...)." example:"task"`
	SubjectID   string `query:"subject_id" format:"uuid" doc:"Filtra pelo id do sujeito."`
	Actor       string `query:"actor" doc:"Filtra por quem causou o fato: system, persona:<slug>, human:<e-mail> (ou persona/human sem nada, para todos do tipo)." example:"persona:ana"`
}

func (s *server) registerMisc(api huma.API) {
	huma.Register(api, op("list-activity", http.MethodGet, "/companies/{company}/activity", "activity", "Lista a atividade da empresa"),
		func(ctx context.Context, in *activityIn) (*ListOut[Activity], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			before, err := parseTime("before", in.Before)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			var sid *domain.ID
			if in.SubjectID != "" {
				id, err := domain.ParseID(in.SubjectID)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("subject_id", "não é um uuid"))
				}
				sid = &id
			}
			f := service.ActivityFilter{SubjectType: in.SubjectType, SubjectID: sid, Before: before, Limit: in.Limit}
			if in.Actor != "" {
				af, err := s.svc.ResolveActor(ctx, sc.Company.ID, in.Actor)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				f.ActorType, f.ActorID = af.Type, af.ID
			}
			as, err := s.svc.ListActivity(ctx, sc.Company.ID, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(as, Page{Limit: in.Limit}, activityOut), nil
		})

	huma.Register(api, op("get-dashboard", http.MethodGet, "/companies/{company}/dashboard", "activity", "Números do Painel"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[Dashboard], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			d, err := s.svc.Dashboard(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Dashboard]{Body: dashboardOut(d)}, nil
		})
}
