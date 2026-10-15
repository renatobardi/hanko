package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type RunPath struct {
	CompanyPath
	Run string `path:"run" format:"uuid" doc:"Id da execução."`
}

type listRunsIn struct {
	CompanyPath
	Page
	Task    string `query:"task" doc:"Id ou chave da task."`
	Persona string `query:"persona" doc:"Id ou slug da persona."`
	Status  string `query:"status" doc:"Status separados por vírgula: queued,running,done,interrupted,failed,cancelled,expired."`
	Name    string `query:"name" doc:"Nome amigável da execução (ex.: Veloz_Raposa); sem diferenciar caixa nem acento."`
}

type listRunEventsIn struct {
	RunPath
	After int64 `query:"after" minimum:"0" default:"0" doc:"Devolve eventos com seq maior que este valor."`
	Limit int   `query:"limit" minimum:"1" maximum:"1000" default:"200" doc:"Máximo de eventos."`
}

// runListCap é o teto de execuções que o service devolve por consulta.
const runListCap = 500

func (s *server) runID(raw string) (domain.ID, error) {
	id, err := domain.ParseID(raw)
	if err != nil {
		return id, domain.Invalid("run", "não é um uuid")
	}
	return id, nil
}

func (s *server) registerRuns(api huma.API) {
	huma.Register(api, op("list-runs", http.MethodGet, "/companies/{company}/runs", "runs", "Lista execuções"),
		func(ctx context.Context, in *listRunsIn) (*ListOut[Run], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			statuses, err := parseRunStatuses(in.Status)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			task, err := s.taskRef(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			persona, err := s.personaRef(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			// O service só pagina por limit; offset é aplicado aqui.
			rs, err := s.svc.ListRuns(ctx, sc.Company.ID, service.RunFilter{
				TaskID: task, PersonaID: persona, Statuses: statuses, Name: in.Name, Limit: min(in.Limit+in.Offset, runListCap)})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(rs, in.Page, runOut), nil
		})

	huma.Register(api, op("get-run", http.MethodGet, "/companies/{company}/runs/{run}", "runs", "Obtém uma execução"),
		func(ctx context.Context, in *RunPath) (*ItemOut[Run], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			rid, err := s.runID(in.Run)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			r, err := s.svc.GetRun(ctx, sc.Company.ID, rid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Run]{Body: runOut(r)}, nil
		})

	huma.Register(api, op("list-run-events", http.MethodGet, "/companies/{company}/runs/{run}/events", "runs", "Lista o log de uma execução"),
		func(ctx context.Context, in *listRunEventsIn) (*ListOut[RunEvent], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			rid, err := s.runID(in.Run)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			es, err := s.svc.ListRunEvents(ctx, sc.Company.ID, rid, in.After, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(es, Page{Limit: in.Limit}, runEventOut), nil
		})

	huma.Register(api, op("cancel-run", http.MethodPost, "/companies/{company}/runs/{run}/cancel", "runs", "Cancela uma execução"),
		func(ctx context.Context, in *RunPath) (*ItemOut[Run], error) {
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
			r, err := s.svc.CancelRun(ctx, sc.Actor, sc.Company.ID, rid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Run]{Body: runOut(r)}, nil
		})
}
