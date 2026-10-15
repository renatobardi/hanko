package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

const (
	// maxImportBytes limita o arquivo de importação (o padrão do huma, 1 MiB, é pouco para um histórico com
	// logs). O corpo é decodificado em fluxo, sem cópia intermediária, então o pico de memória é o do documento.
	maxImportBytes = 64 << 20
	// importReadTimeout é o prazo para o cliente enviar o arquivo (o padrão do huma, 5 s, derruba uploads de
	// dezenas de MiB com 408).
	importReadTimeout = 5 * time.Minute
)

const importPath = Prefix + "/companies/import"

var slugParamRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type exportIn struct {
	CompanyPath
	WithRuns bool `query:"with_runs" doc:"Inclui as execuções encerradas (sem os logs)."`
	WithLogs bool `query:"with_logs" doc:"Inclui também os eventos (log) das execuções; implica with_runs. O arquivo é escrito em fluxo, run a run."`
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

// ImportedCompany é a resposta de POST /companies/import.
type ImportedCompany struct {
	Company Company        `json:"company"`
	Counts  map[string]int `json:"counts" doc:"Quantos itens de cada tipo foram criados."`
}

func (s *server) registerExport(api huma.API) {
	o := op("export-company", http.MethodGet, "/companies/{company}/export", "companies",
		"Exporta a empresa como arquivo portátil (hanko.company.v1), sem segredos (admin ou owner)")
	o.Responses = map[string]*huma.Response{"200": {Description: "Arquivo hanko.company.v1.", Content: map[string]*huma.MediaType{
		"application/json": {Schema: exportDoc{}.Schema(nil)}}}}
	huma.Register(api, o, func(ctx context.Context, in *exportIn) (*huma.StreamResponse, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		// OpenExport confere o papel e monta tudo menos os logs ANTES do primeiro byte: os erros ainda viram
		// status HTTP. Os eventos saem run a run, em páginas, durante a escrita.
		st, err := s.svc.OpenExport(ctx, sc.Company.ID, service.ExportOptions{WithRuns: in.WithRuns, WithLogs: in.WithLogs})
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer st.Close()
			hctx.SetHeader("Content-Type", "application/json")
			hctx.SetStatus(http.StatusOK)
			if err := st.WriteTo(ctx, hctx.BodyWriter()); err != nil && ctx.Err() == nil {
				// o status 200 já foi enviado: o arquivo termina truncado (JSON inválido), o que o cliente detecta
				s.log.ErrorContext(ctx, "export: escrita interrompida", "err", err, "request_id", requestIDFrom(ctx))
			}
		}}, nil
	})
}

// registerImportDoc descreve a importação no OpenAPI (a rota em si não passa pelo huma: o corpo é lido
// em fluxo e nunca é ecoado nos erros).
func (s *server) registerImportDoc(api huma.API) {
	reg := api.OpenAPI().Components.Schemas // huma.Registry
	errBody := func(d string) *huma.Response {
		return &huma.Response{Description: d, Content: map[string]*huma.MediaType{
			"application/problem+json": {Schema: reg.Schema(reflect.TypeOf(huma.ErrorModel{}), true, "")}}}
	}
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "import-company",
		Method:      http.MethodPost,
		Path:        importPath,
		Tags:        []string{"companies"},
		Summary:     "Cria uma empresa NOVA a partir de um arquivo exportado; tudo ou nada (só humanos)",
		Description: fmt.Sprintf("Por padrão a empresa entra PAUSADA (personas pausadas, fluxos desligados); `paused=false` liga. "+
			"Corpo de até %d MiB, decodificado em fluxo; um corpo inválido nunca é devolvido na resposta.", maxImportBytes>>20),
		Parameters: []*huma.Param{
			{Name: "slug", In: "query", Description: "Slug da empresa nova; vazio = o do arquivo.",
				Schema: &huma.Schema{Type: "string", Pattern: slugParamRe.String()}},
			{Name: "paused", In: "query", Description: "Importa personas pausadas e fluxos desligados (padrão). `false` as deixa como no arquivo.",
				Schema: &huma.Schema{Type: "boolean", Default: true}},
		},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: exportDoc{}.Schema(nil)}}},
		Responses: map[string]*huma.Response{
			"201": {Description: "Empresa criada.", Content: map[string]*huma.MediaType{
				"application/json": {Schema: reg.Schema(reflect.TypeOf(ImportedCompany{}), true, "")}}},
			"400": errBody("JSON inválido."),
			"401": errBody("Não autenticado."),
			"403": errBody("Sem permissão (run token, ou usuário comum da instalação)."),
			"409": errBody("Slug em uso."),
			"413": errBody("Arquivo maior que o limite."),
			"422": errBody("Arquivo inválido; nada foi gravado."),
			"500": errBody("Erro interno."),
		},
	})
}

func (s *server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := forbidPersona(ctx); err != nil {
		p, _ := toProblem(err)
		writeProblem(w, p)
		return
	}
	q := r.URL.Query()
	slug := q.Get("slug")
	if slug != "" && !slugParamRe.MatchString(slug) {
		writeProblem(w, problem(http.StatusUnprocessableEntity, "entrada inválida: slug",
			&huma.ErrorDetail{Location: "query.slug", Message: "expected string to match pattern " + slugParamRe.String()}))
		return
	}
	paused := true // seguro por padrão: nada trabalha até alguém liberar
	if v := q.Get("paused"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeProblem(w, problem(http.StatusUnprocessableEntity, "entrada inválida: paused",
				&huma.ErrorDetail{Location: "query.paused", Message: "expected boolean"}))
			return
		}
		paused = b
	}

	// Upload longo: o prazo de leitura é só do corpo, e o limite é aplicado durante a leitura (413).
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(importReadTimeout))
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBytes))
	var doc export.Document
	err := dec.Decode(&doc)
	if err == nil {
		if _, e2 := dec.Token(); !errors.Is(e2, io.EOF) { // lixo depois do documento
			err = errors.New("unexpected data after the JSON document")
			if e2 != nil {
				err = e2
			}
		}
	}
	if err != nil {
		writeProblem(w, importBodyProblem(err))
		return
	}

	res, err := s.svc.ImportCompany(ctx, principalFrom(ctx).Actor, doc, service.ImportOptions{Slug: slug, Active: !paused})
	if err != nil {
		p, known := toProblem(err)
		if !known {
			s.log.ErrorContext(ctx, "erro inesperado", "err", err, "request_id", requestIDFrom(ctx))
		}
		writeProblem(w, p)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(ImportedCompany{Company: companyOut(res.Company), Counts: res.Counts})
}

// importBodyProblem traduz a falha de leitura do corpo. A mensagem nunca inclui o conteúdo do arquivo
// (os erros do decoder descrevem a posição e o tipo, não o valor).
func importBodyProblem(err error) *huma.ErrorModel {
	var tooBig *http.MaxBytesError
	var nErr interface{ Timeout() bool }
	switch {
	case errors.As(err, &tooBig):
		return problem(http.StatusRequestEntityTooLarge, fmt.Sprintf("o arquivo excede o limite de %d MiB", maxImportBytes>>20))
	case errors.Is(err, io.EOF):
		return problem(http.StatusBadRequest, "corpo obrigatório")
	case errors.As(err, &nErr) && nErr.Timeout():
		return problem(http.StatusRequestTimeout, "tempo esgotado ao receber o arquivo")
	}
	return problem(http.StatusBadRequest, "JSON inválido: "+err.Error())
}
