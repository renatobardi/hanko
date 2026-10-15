package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// maxImportBytes limita o arquivo de importação (o padrão do huma, 1 MiB, é pouco para um histórico com logs).
const maxImportBytes = 256 << 20

type exportIn struct {
	CompanyPath
	WithRuns bool `query:"with_runs" doc:"Inclui as execuções encerradas (sem os logs)."`
	WithLogs bool `query:"with_logs" doc:"Inclui também os eventos (log) das execuções; implica with_runs."`
}

// exportDoc é o arquivo portátil no OpenAPI: um objeto livre (o formato é documentado no ADR-0022 e
// validado pelo service), o que evita colidir os nomes dos tipos do arquivo com os da API.
type exportDoc export.Document

// Schema implementa huma.SchemaProvider.
func (exportDoc) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "object", Description: "Arquivo hanko.company.v1 (veja docs/adr/0022-export-import.md).",
		Required: []string{"format", "company"}, AdditionalProperties: true,
		Properties: map[string]*huma.Schema{"format": {Type: "string", Enum: []any{export.Format}}, "company": {Type: "object"}}}
}

type exportOut struct{ Body exportDoc }

type importIn struct {
	Slug   string `query:"slug" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Slug da empresa nova; vazio = o do arquivo."`
	Paused bool   `query:"paused" doc:"Importa personas pausadas e fluxos desligados."`
	Body   exportDoc
}

// ImportedCompany é a resposta de POST /companies/import.
type ImportedCompany struct {
	Company Company        `json:"company"`
	Counts  map[string]int `json:"counts" doc:"Quantos itens de cada tipo foram criados."`
}

func (s *server) registerExport(api huma.API) {
	huma.Register(api, op("export-company", http.MethodGet, "/companies/{company}/export", "companies",
		"Exporta a empresa como arquivo portátil (hanko.company.v1), sem segredos (admin ou owner)"),
		func(ctx context.Context, in *exportIn) (*exportOut, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			doc, err := s.svc.ExportCompany(ctx, sc.Company.ID, service.ExportOptions{WithRuns: in.WithRuns, WithLogs: in.WithLogs})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &exportOut{Body: exportDoc(doc)}, nil
		})

	imp := created(op("import-company", http.MethodPost, "/companies/import", "companies",
		"Cria uma empresa NOVA a partir de um arquivo exportado; tudo ou nada (só humanos)"))
	imp.MaxBodyBytes = maxImportBytes
	huma.Register(api, imp, func(ctx context.Context, in *importIn) (*ItemOut[ImportedCompany], error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		res, err := s.svc.ImportCompany(ctx, principalFrom(ctx).Actor, export.Document(in.Body), service.ImportOptions{Slug: in.Slug, Paused: in.Paused})
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		return &ItemOut[ImportedCompany]{Body: ImportedCompany{Company: companyOut(res.Company), Counts: res.Counts}}, nil
	})
}
