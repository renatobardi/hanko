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
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

const (
	// maxImportBytes limita o arquivo de importação (o padrão do huma, 1 MiB, é pouco para um histórico com
	// logs). O json.Decoder acumula o VALOR JSON inteiro em memória antes de decodificá-lo (não há decodificação
	// incremental de um objeto), então o pico por importação é da ordem do arquivo mais o documento decodificado;
	// por isso o teto é baixo (32 MiB) e há no máximo defaultMaxTransfers importações ao mesmo tempo. Um
	// Content-Length acima do limite é recusado (413) sem ler o corpo.
	maxImportBytes = 32 << 20

	// Prazos padrão de export/import (TransferLimits). Importação: o prazo de leitura é renovado por inatividade
	// (o padrão do huma, 5 s, devolvia 408 em upload grande) com teto absoluto. Exportação: teto absoluto de
	// OpenExport+WriteTo e prazo de escrita renovado a cada flush; um leitor parado libera a conexão do banco.
	defaultImportIdle      = time.Minute
	defaultImportMax       = 15 * time.Minute
	importRenewEvery       = 16 << 10 // bytes lidos entre duas renovações do prazo
	defaultExportTimeout   = 15 * time.Minute
	defaultExportWriteIdle = time.Minute
	defaultMaxTransfers    = 2
	busyRetryAfter         = "30"
)

// TransferLimits são os tetos de tempo e de concorrência de export/import. Zero usa o padrão.
type TransferLimits struct {
	ExportTimeout   time.Duration // teto absoluto de OpenExport+WriteTo (padrão 15 min)
	ExportWriteIdle time.Duration // prazo de escrita da resposta, renovado a cada flush (padrão 60 s)
	ImportIdle      time.Duration // prazo de leitura do corpo por inatividade, renovado a cada 16 KiB (padrão 60 s)
	ImportMax       time.Duration // teto absoluto do upload (padrão 15 min)
	MaxExports      int           // exportações simultâneas por servidor (padrão 2); acima, 503 + Retry-After
	MaxImports      int           // importações simultâneas por servidor (padrão 2)
}

func (l TransferLimits) withDefaults() TransferLimits {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&l.ExportTimeout, defaultExportTimeout)
	def(&l.ExportWriteIdle, defaultExportWriteIdle)
	def(&l.ImportIdle, defaultImportIdle)
	def(&l.ImportMax, defaultImportMax)
	if l.MaxExports <= 0 {
		l.MaxExports = defaultMaxTransfers
	}
	if l.MaxImports <= 0 {
		l.MaxImports = defaultMaxTransfers
	}
	return l
}

const importPath = Prefix + "/companies/import"

func boolPtr(b bool) *bool { return &b }

var slugParamRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type exportIn struct {
	CompanyPath
	WithRuns      bool `query:"with_runs" doc:"Inclui as execuções encerradas (sem os logs)."`
	WithLogs      bool `query:"with_logs" doc:"Inclui também os eventos (log) das execuções; implica with_runs. O arquivo é escrito em fluxo, run a run."`
	WithArtifacts bool `query:"with_artifacts" doc:"Inclui todas as versões dos artefatos, com o conteúdo em base64 (até 20 MiB no total, para o arquivo caber na importação)."`
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
	Company Company        `json:"company" doc:"A empresa criada, ou a de destino no merge. Em dry-run para empresa nova, sem id (não existe ainda)."`
	Counts  map[string]int `json:"counts" doc:"Quantos itens de cada tipo foram criados (em dry-run, seriam criados). No merge, os que foram gravados (tudo menos pular)."`
	DryRun  bool           `json:"dry_run" doc:"true: nada foi gravado; é a prévia."`
	Merged  bool           `json:"merged" doc:"true: o destino era uma empresa existente (só a configuração entra)."`
	// Items e Warnings são a prévia/resultado detalhado (ADR-0057).
	Items    []ImportItem    `json:"items" doc:"Workspaces, projetos, assinaturas, skills, personas, fluxos e integrações do arquivo e o que acontece com cada um."`
	Warnings []ImportWarning `json:"warnings" doc:"Avisos: caminhos limpos, segredos a recriar, itens que entram desligados, histórico que não entra."`
}

// ImportItem é um item do arquivo e a ação aplicada.
type ImportItem struct {
	Kind   string `json:"kind" enum:"workspaces,projects,subscriptions,skills,personas,flows,integrations"`
	Key    string `json:"key" doc:"Slug no arquivo (nome, em assinaturas)."`
	Name   string `json:"name"`
	Action string `json:"action" enum:"create,skip,rename,replace"`
	Target string `json:"target" doc:"Chave final no destino (o novo slug quando renomeado)."`
}

// ImportWarning é um aviso da prévia; message é um formato pt-BR com %d para count.
type ImportWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func importedOut(res service.ImportResult) ImportedCompany {
	out := ImportedCompany{Company: companyOut(res.Company), Counts: res.Counts, DryRun: res.DryRun, Merged: res.Merged,
		Items: make([]ImportItem, len(res.Items)), Warnings: make([]ImportWarning, len(res.Warnings))}
	for i, it := range res.Items {
		out.Items[i] = ImportItem{Kind: it.Kind, Key: it.Key, Name: it.Name, Action: string(it.Action), Target: it.Target}
	}
	for i, w := range res.Warnings {
		out.Warnings[i] = ImportWarning{Code: w.Code, Message: w.Message, Count: w.Count}
	}
	return out
}

func (s *server) registerExport(api huma.API) {
	o := op("export-company", http.MethodGet, "/companies/{company}/export", "companies",
		"Exporta a empresa como arquivo portátil (hanko.company.v1), sem segredos (admin ou owner)")
	o.Responses = map[string]*huma.Response{"200": {Description: "Arquivo hanko.company.v1.", Content: map[string]*huma.MediaType{
		"application/json": {Schema: exportDoc{}.Schema(nil)}}},
		"503": {Description: "Exportações demais em andamento; tente de novo depois de Retry-After.",
			Headers: map[string]*huma.Param{"Retry-After": {Description: "Segundos até tentar de novo.", Schema: &huma.Schema{Type: "string"}}},
			Content: map[string]*huma.MediaType{"application/problem+json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(huma.ErrorModel{}), true, "")}}}}
	huma.Register(api, o, func(ctx context.Context, in *exportIn) (*huma.StreamResponse, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		if !s.exportGate.tryAcquire() {
			return busyStream("há exportações demais em andamento; tente de novo em instantes"), nil
		}
		// Teto absoluto de OpenExport+WriteTo: a transação de snapshot prende uma das conexões do pool (compartilhadas
		// com heartbeat, SSE, UI e API) enquanto a resposta é escrita. O cancel e a liberação da vaga ficam para o
		// fim do Body, depois do Close (rollback).
		ectx, cancel := context.WithTimeout(ctx, s.xfer.ExportTimeout)
		// OpenExport confere o papel e monta tudo menos os logs ANTES do primeiro byte: os erros ainda viram
		// status HTTP. Os eventos saem run a run, em páginas, durante a escrita.
		st, err := s.svc.OpenExport(ectx, sc.Company.ID, service.ExportOptions{WithRuns: in.WithRuns, WithLogs: in.WithLogs, WithArtifacts: in.WithArtifacts})
		if err != nil {
			cancel()
			s.exportGate.release()
			return nil, s.fail(ctx, err)
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer s.exportGate.release()
			defer cancel()
			defer st.Close()
			_, w := humago.Unwrap(hctx)
			rc := http.NewResponseController(w)
			defer func() { _ = rc.SetWriteDeadline(time.Time{}) }() // a conexão pode ser reaproveitada
			hctx.SetHeader("Content-Type", "application/json")
			hctx.SetStatus(http.StatusOK)
			if err := st.WriteTo(ectx, &renewingWriter{w: hctx.BodyWriter(), rc: rc, extend: s.xfer.ExportWriteIdle}); err != nil {
				if ctx.Err() == nil { // o cliente que sumiu não é erro do servidor
					s.log.ErrorContext(ctx, "export: escrita interrompida", "err", err, "request_id", requestIDFrom(ctx))
				}
				// O status 200 já foi enviado: sair normalmente fecharia a resposta como completa e o cliente
				// receberia um JSON truncado como se fosse o arquivo. Abortar rompe a conexão (erro de transporte)
				// e o Close libera a transação de snapshot.
				panic(http.ErrAbortHandler)
			}
		}}, nil
	})
}

// renewingWriter renova o prazo de escrita antes de cada Write (o bufio do export descarrega em blocos): um
// cliente que lê devagar mas lê continua; um que para de ler estoura o prazo, a escrita falha e a conexão do
// banco é liberada.
type renewingWriter struct {
	w      io.Writer
	rc     *http.ResponseController
	extend time.Duration
}

func (r *renewingWriter) Write(p []byte) (int, error) {
	_ = r.rc.SetWriteDeadline(time.Now().Add(r.extend)) // ResponseWriter sem suporte (testes): sem prazo
	return r.w.Write(p)
}

// gate é um semáforo de vagas não bloqueante.
type gate chan struct{}

func newGate(n int) gate { return make(gate, n) }

func (g gate) tryAcquire() bool {
	select {
	case g <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g gate) release() { <-g }

// busyStream é a resposta 503 (com Retry-After) de quando o teto de transferências simultâneas está cheio.
func busyStream(detail string) *huma.StreamResponse {
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		hctx.SetHeader("Retry-After", busyRetryAfter)
		hctx.SetHeader("Content-Type", "application/problem+json")
		hctx.SetStatus(http.StatusServiceUnavailable)
		b, _ := json.Marshal(problem(http.StatusServiceUnavailable, detail))
		_, _ = hctx.BodyWriter().Write(b)
	}}
}

// registerImportDoc descreve a importação no OpenAPI (a rota em si não passa pelo huma: o corpo é lido
// em fluxo e nunca é ecoado nos erros).
func (s *server) registerImportDoc(api huma.API) {
	reg := api.OpenAPI().Components.Schemas // huma.Registry
	errBody := func(d string) *huma.Response {
		return &huma.Response{Description: d, Content: map[string]*huma.MediaType{
			"application/problem+json": {Schema: reg.Schema(reflect.TypeOf(huma.ErrorModel{}), true, "")}}}
	}
	busy := errBody("Importações demais em andamento; tente de novo depois de Retry-After.")
	busy.Headers = map[string]*huma.Param{"Retry-After": {Description: "Segundos até tentar de novo.", Schema: &huma.Schema{Type: "string"}}}
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "import-company",
		Method:      http.MethodPost,
		Path:        importPath,
		Tags:        []string{"companies"},
		Summary:     "Importa um arquivo exportado: cria uma empresa nova ou, com `into`, mescla a configuração numa existente; `dry_run` mostra a prévia; tudo ou nada (só humanos)",
		Description: fmt.Sprintf("Por padrão a empresa entra PAUSADA (personas pausadas, fluxos desligados); `paused=false` liga. "+
			"Corpo de até %d MiB (Content-Length maior é recusado com 413 sem ler o corpo), lido com prazo renovado por inatividade "+
			"e teto absoluto; um corpo inválido nunca é devolvido na resposta. No máximo %d importações simultâneas por servidor.",
			maxImportBytes>>20, s.xfer.MaxImports),
		Parameters: []*huma.Param{
			{Name: "slug", In: "query", Description: "Slug da empresa nova; vazio = o do arquivo.",
				Schema: &huma.Schema{Type: "string", Pattern: slugParamRe.String()}},
			{Name: "paused", In: "query", Description: "Importa personas pausadas e fluxos desligados (padrão). `false` as deixa como no arquivo.",
				Schema: &huma.Schema{Type: "boolean", Default: true}},
			{Name: "dry_run", In: "query", Description: "Só valida e devolve a prévia (contagens, itens, avisos); não grava nada.",
				Schema: &huma.Schema{Type: "boolean", Default: false}},
			{Name: "into", In: "query", Description: "Id ou slug de uma empresa EXISTENTE: importa só a configuração (merge; admin da empresa, não arquivada). Vazio cria uma empresa nova.",
				Schema: &huma.Schema{Type: "string"}},
			{Name: "on_conflict", In: "query", Description: "Com `into`: ação padrão para o que já existe no destino.",
				Schema: &huma.Schema{Type: "string", Enum: []any{"skip", "rename", "replace"}, Default: "skip"}},
			{Name: "resolve", In: "query", Style: "form", Explode: boolPtr(true), Description: "Com `into`: exceção por item, `tipo/chave=ação` (ex.: `personas/dev=replace`); repetível.",
				Schema: &huma.Schema{Type: "array", Items: &huma.Schema{Type: "string"}}},
		},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: exportDoc{}.Schema(nil)}}},
		Responses: map[string]*huma.Response{
			"200": {Description: "Prévia (dry_run) ou merge em empresa existente (into).", Content: map[string]*huma.MediaType{
				"application/json": {Schema: reg.Schema(reflect.TypeOf(ImportedCompany{}), true, "")}}},
			"201": {Description: "Empresa criada.", Content: map[string]*huma.MediaType{
				"application/json": {Schema: reg.Schema(reflect.TypeOf(ImportedCompany{}), true, "")}}},
			"400": errBody("JSON inválido."),
			"401": errBody("Não autenticado."),
			"403": errBody("Sem permissão (run token, ou usuário comum da instalação)."),
			"408": errBody("Tempo esgotado ao receber o arquivo (sem progresso por muito tempo, ou acima do teto absoluto do upload)."),
			"409": errBody("Slug em uso."),
			"413": errBody("Arquivo maior que o limite."),
			"422": errBody("Arquivo inválido; nada foi gravado."),
			"500": errBody("Erro interno."),
			"503": busy,
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

	dryRun := false
	if v := q.Get("dry_run"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeProblem(w, problem(http.StatusUnprocessableEntity, "entrada inválida: dry_run",
				&huma.ErrorDetail{Location: "query.dry_run", Message: "expected boolean"}))
			return
		}
		dryRun = b
	}
	opts := service.ImportOptions{Slug: slug, Active: !paused, DryRun: dryRun, OnConflict: domain.ConflictAction(q.Get("on_conflict"))}
	if opts.OnConflict != "" && !opts.OnConflict.Valid() {
		writeProblem(w, problem(http.StatusUnprocessableEntity, "entrada inválida: on_conflict",
			&huma.ErrorDetail{Location: "query.on_conflict", Message: "expected skip, rename or replace"}))
		return
	}
	for _, rv := range q["resolve"] {
		kind, key, act, err := domain.ParseResolution(rv)
		if err != nil {
			p, _ := toProblem(err)
			writeProblem(w, p)
			return
		}
		if opts.Resolve == nil {
			opts.Resolve = map[string]domain.ConflictAction{}
		}
		opts.Resolve[kind+"/"+key] = act
	}
	if into := q.Get("into"); into != "" {
		co, err := s.svc.GetCompany(ctx, into)
		if err != nil {
			p, _ := toProblem(err)
			writeProblem(w, p)
			return
		}
		if cl := principalFrom(ctx).Claims; cl != nil && cl.CompanyID != co.ID.String() {
			writeProblem(w, problem(http.StatusForbidden, "o token não pertence a esta empresa"))
			return
		}
		id := co.ID
		opts.Into = &id
	}

	// Content-Length acima do limite: 413 sem ler nada do corpo.
	if r.ContentLength > maxImportBytes {
		writeProblem(w, importBodyProblem(&http.MaxBytesError{Limit: maxImportBytes}))
		return
	}
	if !s.importGate.tryAcquire() {
		w.Header().Set("Retry-After", busyRetryAfter)
		writeProblem(w, problem(http.StatusServiceUnavailable, "há importações demais em andamento; tente de novo em instantes"))
		return
	}
	defer s.importGate.release()

	// Upload longo: o prazo de leitura é só do corpo, renovado por inatividade (a cada importRenewEvery bytes
	// lidos) com teto absoluto, e o limite é aplicado durante a leitura (413 para quem não declarou o tamanho).
	rc := http.NewResponseController(w)
	body := newIdleDeadlineReader(http.MaxBytesReader(w, r.Body, maxImportBytes), rc, s.xfer.ImportIdle, s.xfer.ImportMax, importRenewEvery)
	dec := json.NewDecoder(body)
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
	_ = rc.SetReadDeadline(time.Time{})
	if err != nil {
		writeProblem(w, importBodyProblem(err))
		return
	}

	res, err := s.svc.ImportCompany(ctx, principalFrom(ctx).Actor, doc, opts)
	if err != nil {
		p, known := toProblem(err)
		if !known {
			s.log.ErrorContext(ctx, "erro inesperado", "err", err, "request_id", requestIDFrom(ctx))
		}
		writeProblem(w, p)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if res.DryRun || res.Merged {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	_ = json.NewEncoder(w).Encode(importedOut(res))
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

// idleDeadlineReader renova o prazo de leitura da conexão por inatividade: o prazo vale idle a partir da última
// renovação, que acontece a cada `every` bytes lidos, e nunca passa de hard (teto absoluto do upload). Um
// cliente precisa, portanto, enviar pelo menos `every` bytes a cada `idle` (16 KiB por minuto no padrão).
type idleDeadlineReader struct {
	r     io.Reader
	rc    *http.ResponseController
	idle  time.Duration
	hard  time.Time
	every int
	since int
}

func newIdleDeadlineReader(r io.Reader, rc *http.ResponseController, idle, max time.Duration, every int) *idleDeadlineReader {
	d := &idleDeadlineReader{r: r, rc: rc, idle: idle, hard: time.Now().Add(max), every: every}
	d.renew()
	return d
}

func (d *idleDeadlineReader) renew() {
	d.since = 0
	dl := time.Now().Add(d.idle)
	if dl.After(d.hard) {
		dl = d.hard
	}
	_ = d.rc.SetReadDeadline(dl) // ResponseWriter sem suporte (testes): sem prazo
}

func (d *idleDeadlineReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if d.since += n; d.since >= d.every {
		d.renew()
	}
	return n, err
}
