package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/service"
)

// SearchSegment é um pedaço de texto de um resultado; match marca o que casou com a consulta. Nunca HTML.
type SearchSegment struct {
	Text  string `json:"text" doc:"Texto original (sem HTML; quem exibe deve escapar)."`
	Match bool   `json:"match,omitempty" doc:"Verdadeiro quando este pedaço casou com a consulta."`
}

// SearchHit é um resultado da busca.
type SearchHit struct {
	Type      string          `json:"type" enum:"task,comment,persona,run,flow,goal,project,gate,artifact,memory" doc:"Tipo do resultado."`
	ID        string          `json:"id,omitempty" format:"uuid" doc:"Id da task, comentário, persona, execução, fluxo, meta, projeto, gate ou artefato (ausente na memória)."`
	Ref       string          `json:"ref" example:"HNK-12" doc:"Referência para navegar: chave da task (também nos comentários), slug da persona, do fluxo, da meta ou do projeto, id da execução, do gate ou do artefato, path da página na memória."`
	Title     []SearchSegment `json:"title" doc:"Título/nome, com as palavras que casaram marcadas."`
	Snippet   []SearchSegment `json:"snippet,omitempty" doc:"Trecho que casou (ou o início do texto, sem marcas)."`
	TaskKey   string          `json:"task_key,omitempty" doc:"Task do comentário ou da execução."`
	TaskTitle string          `json:"task_title,omitempty"`
	Status    string          `json:"status,omitempty" doc:"Status da task, persona ou execução; enabled/disabled no fluxo."`
	Author    string          `json:"author,omitempty" doc:"Autor do comentário: nome da persona, human ou system."`
	Persona   string          `json:"persona,omitempty" doc:"Persona da execução."`
	At        *time.Time      `json:"at,omitempty" doc:"Última atualização (criação, para comentários e execuções)."`
	Score     float64         `json:"score" doc:"Relevância (ts_rank × recência); só serve para ordenar dentro do grupo."`
	Exact     bool            `json:"exact,omitempty" doc:"A consulta é exatamente a chave desta task."`
}

// SearchGroup agrupa os resultados de um tipo.
type SearchGroup struct {
	Type        string      `json:"type" enum:"task,comment,persona,run,flow,goal,project,gate,artifact,memory"`
	Hits        []SearchHit `json:"hits"`
	More        bool        `json:"more,omitempty" doc:"Há mais resultados além do limite."`
	Unavailable bool        `json:"unavailable,omitempty" doc:"O tipo não pôde ser consultado agora (memória fora do ar); os demais valem."`
}

// SearchResult é a resposta da busca: só os grupos que têm resultado.
type SearchResult struct {
	Query  string        `json:"query"`
	Total  int           `json:"total" doc:"Resultados nos grupos devolvidos."`
	Groups []SearchGroup `json:"groups" doc:"Grupos na ordem task, comment, persona, run, flow, goal, project, gate, artifact, memory."`
}

type searchIn struct {
	CompanyPath
	Q     string `query:"q" required:"true" minLength:"1" maxLength:"512" doc:"O que buscar. Palavras separadas por espaço (todas), \"frase exata\", -excluir, OR; a última palavra casa por prefixo. A chave exata de uma task (HNK-12) vai direto a ela." example:"deploy produção"`
	Types string `query:"types" doc:"Tipos separados por vírgula: task,comment,persona,run,flow,goal,project,gate,artifact,memory. Vazio = todos." example:"task,comment"`
	Limit int    `query:"limit" minimum:"1" maximum:"50" default:"10" doc:"Máximo de resultados por tipo."`
}

func searchResultOut(r service.SearchResult) SearchResult {
	out := SearchResult{Query: r.Query, Total: r.Total, Groups: make([]SearchGroup, len(r.Groups))}
	for i, g := range r.Groups {
		og := SearchGroup{Type: string(g.Type), More: g.More, Unavailable: g.Unavailable, Hits: make([]SearchHit, len(g.Hits))}
		for j, h := range g.Hits {
			oh := SearchHit{Type: string(h.Type), Ref: h.Ref, Title: segs(h.Title), Snippet: segs(h.Snippet), TaskKey: h.TaskKey,
				TaskTitle: h.TaskTitle, Status: h.Status, Author: h.Author, Persona: h.Persona, At: h.At, Score: h.Score, Exact: h.Exact}
			if h.ID != (domain.ID{}) {
				oh.ID = h.ID.String()
			}
			og.Hits[j] = oh
		}
		out.Groups[i] = og
	}
	return out
}

func segs(in []search.Segment) []SearchSegment {
	out := make([]SearchSegment, len(in))
	for i, s := range in {
		out[i] = SearchSegment{Text: s.Text, Match: s.Match}
	}
	return out
}

func (s *server) registerSearch(api huma.API) {
	huma.Register(api, op("search", http.MethodGet, "/companies/{company}/search", "search", "Busca na empresa"),
		func(ctx context.Context, in *searchIn) (*ItemOut[SearchResult], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			types, err := service.ParseSearchTypes(in.Types)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			r, err := s.svc.Search(ctx, sc.Company.ID, in.Q, service.SearchFilter{Types: types, Limit: in.Limit})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SearchResult]{Body: searchResultOut(r)}, nil
		})
}
