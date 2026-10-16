package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/store"
)

// ExportOptions escolhe o que mais entra no arquivo além da configuração da empresa.
type ExportOptions struct {
	WithRuns bool // execuções encerradas
	WithLogs bool // e os eventos (log) delas; implica WithRuns
	// WithArtifacts inclui todas as versões dos artefatos com o conteúdo (ADR-0028). O conteúdo sai em fluxo, um
	// artefato por vez; o total é limitado por MaxExportArtifactBytes para o arquivo caber na importação.
	WithArtifacts bool
}

// exportStatus: estados transitórios de persona não são configuração e voltam como idle.
func exportStatus(s string) string {
	switch domain.PersonaStatus(s) {
	case domain.PersonaRunning, domain.PersonaError, domain.PersonaAwaitingGate:
		return string(domain.PersonaIdle)
	}
	return s
}

// defaultExportPageSize é quantos eventos de uma execução o export lê por vez.
const defaultExportPageSize = 1000

const (
	// DefaultExportIdleTxTimeout é o idle_in_transaction_session_timeout da transação de snapshot do export:
	// entre duas páginas a transação fica ociosa enquanto a resposta é escrita para um cliente lento, e uma
	// conexão do pool (20, compartilhadas com heartbeat, SSE, UI e API) não pode ficar presa por um leitor
	// parado. Precisa ser maior que o prazo de escrita por flush da API (60 s).
	DefaultExportIdleTxTimeout = 2 * time.Minute
	// DefaultExportStatementTimeout limita cada consulta do export.
	DefaultExportStatementTimeout = 2 * time.Minute
	// rollbackTimeout limita o ROLLBACK que libera a conexão (o ctx da requisição pode já estar cancelado).
	rollbackTimeout = 2 * time.Second
)

// red mascara credenciais que tenham ido parar em texto livre (instruções, descrições, comentários, logs):
// o arquivo é feito para ser compartilhado (ADR-0022). Não o torna anônimo.
func red(s string) string { return adapter.Redact(s) }

// redMax é red com teto em runas: o mascaramento pode ALONGAR o texto (uma senha curta de URL de banco vira
// "[REDACTED]"), e o arquivo exportado tem de continuar dentro dos limites que a importação valida (título de task
// 500, comentário 100000). Acima do teto, corta.
func redMax(s string, max int) string {
	s = red(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// Limites de texto validados na importação (veja import.go).
const (
	maxTaskTitleRunes   = 500
	maxCommentBodyRunes = 100000
)

// ExportStream é uma exportação aberta: a configuração e o histórico de execuções (sem logs) já estão montados
// em memória, numa transação de snapshot que fica aberta até Close; os logs saem run a run, em páginas, durante
// WriteTo, então o log inteiro da empresa nunca fica na memória. Chame Close sempre.
type ExportStream struct {
	tx        pgx.Tx
	companyID domain.ID
	q         *store.Queries
	doc       export.Document
	runIDs    []domain.ID // paralelo a doc.Runs
	artIDs    []domain.ID // paralelo a doc.Artifacts (o conteúdo é lido na hora de escrever)
	withLogs  bool
	pageSize  int
	pages     int
	hook      func(page int) error
}

// Pages devolve quantas páginas de eventos foram lidas (para testes e métricas).
func (e *ExportStream) Pages() int { return e.pages }

// Close encerra a transação de snapshot.
func (e *ExportStream) Close() {
	if e.tx != nil {
		rollbackTx(e.tx)
		e.tx = nil
	}
}

// OpenExport confere o papel (admin: as instruções das personas e o histórico não são para qualquer membro) e
// monta o arquivo portátil da empresa (ADR-0022) numa leitura consistente (uma única transação de snapshot).
// Os erros de autorização e de montagem saem aqui, antes de qualquer byte da resposta.
func (s *Service) OpenExport(ctx context.Context, companyID domain.ID, opts ExportOptions) (*ExportStream, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	if opts.WithLogs {
		opts.WithRuns = true
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin export tx: %w", err)
	}
	st := &ExportStream{tx: tx, q: store.New(tx), companyID: companyID, withLogs: opts.WithLogs, pageSize: s.ExportPageSize, hook: s.ExportPageHook}
	if err := s.limitExportTx(ctx, tx); err != nil {
		st.Close()
		return nil, err
	}
	if st.pageSize <= 0 {
		st.pageSize = defaultExportPageSize
	}
	if err := s.buildExport(ctx, st, companyID, opts); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

// limitExportTx põe os tetos de tempo na transação de snapshot (SET LOCAL): o servidor do banco derruba a
// conexão se a transação ficar ociosa demais (leitor lento ou parado) e cancela consultas longas demais.
func (s *Service) limitExportTx(ctx context.Context, tx pgx.Tx) error {
	idle, stmt := s.ExportIdleTxTimeout, s.ExportStatementTimeout
	if idle <= 0 {
		idle = DefaultExportIdleTxTimeout
	}
	if stmt <= 0 {
		stmt = DefaultExportStatementTimeout
	}
	ms := func(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) + "ms" }
	if _, err := tx.Exec(ctx, `SELECT set_config('idle_in_transaction_session_timeout', $1, true), set_config('statement_timeout', $2, true)`,
		ms(idle), ms(stmt)); err != nil {
		return fmt.Errorf("export tx limits: %w", err)
	}
	return nil
}

// ExportCompany devolve o arquivo inteiro em memória, logs incluídos (uso programático e testes; a API usa
// OpenExport e escreve em fluxo).
func (s *Service) ExportCompany(ctx context.Context, companyID domain.ID, opts ExportOptions) (export.Document, error) {
	st, err := s.OpenExport(ctx, companyID, opts)
	if err != nil {
		return export.Document{}, err
	}
	defer st.Close()
	for i, id := range st.artIDs {
		b, err := st.q.GetArtifactContent(ctx, store.GetArtifactContentParams{CompanyID: companyID, ArtifactID: id})
		if err != nil {
			return export.Document{}, fmt.Errorf("export artifact content: %w", err)
		}
		st.doc.Artifacts[i].Content = b
	}
	if st.withLogs {
		for i, id := range st.runIDs {
			err := st.eachEvent(ctx, id, func(e export.RunEvent) error {
				st.doc.Runs[i].Events = append(st.doc.Runs[i].Events, e)
				return nil
			})
			if err != nil {
				return export.Document{}, err
			}
		}
	}
	return st.doc, nil
}

// eachEvent percorre os eventos de uma execução em páginas por seq.
func (e *ExportStream) eachEvent(ctx context.Context, runID domain.ID, fn func(export.RunEvent) error) error {
	var after int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.hook != nil {
			if err := e.hook(e.pages + 1); err != nil {
				return err
			}
		}
		rows, err := e.q.ExportRunEventsPage(ctx, store.ExportRunEventsPageParams{RunID: runID, AfterSeq: after, PageSize: i32(e.pageSize)})
		if err != nil {
			return fmt.Errorf("export run events: %w", err)
		}
		e.pages++
		for _, r := range rows {
			if err := fn(export.RunEvent{Seq: r.Seq, Kind: r.Kind, Text: red(r.Text), Payload: adapter.RedactJSON(r.Payload),
				OccurredAt: r.OccurredAt}); err != nil {
				return err
			}
			after = r.Seq
		}
		if len(rows) < e.pageSize {
			return nil
		}
	}
}

// WriteTo escreve o JSON do arquivo: o documento (sem artefatos nem execuções), depois os artefatos um a um (o
// conteúdo é lido na hora), depois as execuções uma a uma, e os eventos de cada uma em páginas. A saída é a mesma
// de json.Marshal(Document) com o conteúdo e os eventos dentro de cada item.
func (e *ExportStream) WriteTo(ctx context.Context, w io.Writer) error {
	bw := bufio.NewWriterSize(w, 64<<10)
	runs, arts := e.doc.Runs, e.doc.Artifacts
	head := e.doc
	head.Runs, head.Artifacts = nil, nil
	b, err := json.Marshal(head)
	if err != nil {
		return fmt.Errorf("marshal export: %w", err)
	}
	if len(runs) == 0 && len(arts) == 0 {
		_, _ = bw.Write(b)
		return flushAfter(bw, nil)
	}
	_, _ = bw.Write(b[:len(b)-1]) // sem o "}" final
	if len(arts) > 0 {
		_, _ = bw.WriteString(`,"artifacts":[`)
		for i, a := range arts {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i > 0 {
				_ = bw.WriteByte(',')
			}
			if a.Content == nil {
				if a.Content, err = e.q.GetArtifactContent(ctx, store.GetArtifactContentParams{CompanyID: e.companyID, ArtifactID: e.artIDs[i]}); err != nil {
					return fmt.Errorf("export artifact content: %w", err)
				}
			}
			ab, err := json.Marshal(a)
			if err != nil {
				return fmt.Errorf("marshal artifact: %w", err)
			}
			if _, err := bw.Write(ab); err != nil {
				return err
			}
		}
		_ = bw.WriteByte(']')
	}
	if len(runs) > 0 {
		_, _ = bw.WriteString(`,"runs":[`)
		for i, r := range runs {
			if i > 0 {
				_ = bw.WriteByte(',')
			}
			r.Events = nil
			rb, err := json.Marshal(r)
			if err != nil {
				return fmt.Errorf("marshal run: %w", err)
			}
			if !e.withLogs {
				_, _ = bw.Write(rb)
				continue
			}
			_, _ = bw.Write(rb[:len(rb)-1])
			first := true
			err = e.eachEvent(ctx, e.runIDs[i], func(ev export.RunEvent) error {
				eb, err := json.Marshal(ev)
				if err != nil {
					return fmt.Errorf("marshal run event: %w", err)
				}
				if first {
					_, _ = bw.WriteString(`,"events":[`)
					first = false
				} else {
					_ = bw.WriteByte(',')
				}
				_, err = bw.Write(eb)
				return err
			})
			if err != nil {
				return err
			}
			if !first {
				_ = bw.WriteByte(']')
			}
			_ = bw.WriteByte('}')
		}
		_ = bw.WriteByte(']')
	}
	_ = bw.WriteByte('}')
	return flushAfter(bw, nil)
}

func flushAfter(bw *bufio.Writer, err error) error {
	if err != nil {
		return err
	}
	return bw.Flush()
}

// buildExport lê tudo menos os eventos das execuções.
func (s *Service) buildExport(ctx context.Context, st *ExportStream, companyID domain.ID, opts ExportOptions) error {
	doc, err := s.buildDoc(ctx, st.q, companyID, opts, st)
	if err != nil {
		return err
	}
	st.doc = doc
	return nil
}

func (s *Service) buildDoc(ctx context.Context, q *store.Queries, companyID domain.ID, opts ExportOptions, st *ExportStream) (export.Document, error) {
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
		doc.Workspaces = append(doc.Workspaces, export.Workspace{Slug: w.Slug, Name: w.Name, Path: w.Path, RepoURL: w.RepoUrl, DefaultBranch: w.DefaultBranch, CreatedAt: w.CreatedAt})
	}
	prRows, err := q.ListProjects(ctx, companyID)
	if err != nil {
		return doc, fmt.Errorf("export projects: %w", err)
	}
	prSlug := map[domain.ID]string{}
	for _, p := range prRows {
		prSlug[p.ID] = p.Slug
		doc.Projects = append(doc.Projects, export.Project{Slug: p.Slug, Name: p.Name, Description: red(p.Description),
			Workspace: refSlug(wsSlug, p.WorkspaceID), Status: p.Status, BudgetMonthlyCents: p.BudgetMonthlyCents, CreatedAt: p.CreatedAt})
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
		doc.Personas = append(doc.Personas, export.Persona{Slug: p.Slug, Name: p.Name, Title: red(p.Title), Icon: p.Icon, Instructions: red(p.Instructions),
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
			Body: redMax(c.Body, maxCommentBodyRunes), Run: refSlug(runKey, c.RunID), CreatedAt: c.CreatedAt})
	}
	for _, t := range taskRows {
		et := export.Task{Number: t.Number, Title: redMax(t.Title, maxTaskTitleRunes), Description: red(t.Description), Status: t.Status, Priority: t.Priority,
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
		doc.Flows = append(doc.Flows, export.Flow{Slug: f.Slug, Name: f.Name, Description: red(f.Description), Cron: f.Cron,
			Timezone: f.Timezone, Enabled: f.Enabled, Persona: peSlug[f.PersonaID], Project: refSlug(prSlug, f.ProjectID),
			Priority: f.Priority, TaskTitle: red(f.TaskTitle), TaskDescription: red(f.TaskDescription), CreatedAt: f.CreatedAt})
	}

	if err := exportSkillsIntegrations(ctx, q, companyID, &doc, peSlug); err != nil {
		return doc, err
	}

	if opts.WithArtifacts {
		if err := s.exportArtifacts(ctx, q, companyID, st, &doc, peSlug, runKey); err != nil {
			return doc, err
		}
	}

	if opts.WithRuns {
		for _, r := range runRows {
			var exit *int
			if r.ExitCode != nil {
				v := int(*r.ExitCode)
				exit = &v
			}
			st.runIDs = append(st.runIDs, r.ID)
			doc.Runs = append(doc.Runs, export.Run{Key: runKey[r.ID], Name: r.Name, Task: taskNum[idOrZero(r.TaskID)], Persona: peSlug[r.PersonaID],
				Status: r.Status, Trigger: r.Trigger, Agent: r.Agent, Model: r.Model, ModelSource: r.ModelSource,
				Subscription: refSlug(subName, r.SubscriptionID), ExitCode: exit, Error: red(r.Error), Summary: red(r.Summary),
				CostUSD: r.CostUsd, CostSource: r.CostSource, ListCostUSD: r.ListCostUsd,
				Usage: export.Usage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheReadTokens: r.CacheReadTokens,
					CacheCreationTokens: r.CacheCreationTokens},
				CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt})
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
