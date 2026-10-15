package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/store"
)

// Busca única da empresa (ADR-0024): full-text do Postgres em tasks, comentários, personas, execuções e fluxos, mais a
// memória da empresa quando há provider. Quem lê a empresa (viewer ou mais) busca em tudo — a matriz do ADR-0021 não
// esconde nada dos papéis de leitura — e toda consulta filtra por company_id.

// SearchType é o tipo de um resultado de busca.
type SearchType string

const (
	SearchTask    SearchType = "task"
	SearchComment SearchType = "comment"
	SearchPersona SearchType = "persona"
	SearchRun     SearchType = "run"
	SearchFlow    SearchType = "flow"
	SearchMemory  SearchType = "memory"
)

// SearchTypes lista os tipos na ordem em que os grupos aparecem.
var SearchTypes = []SearchType{SearchTask, SearchComment, SearchPersona, SearchRun, SearchFlow, SearchMemory}

// Valid diz se t é um tipo conhecido.
func (t SearchType) Valid() bool {
	for _, k := range SearchTypes {
		if k == t {
			return true
		}
	}
	return false
}

// ParseSearchTypes converte "task,comment" em tipos; vazio = todos. Tipo desconhecido é ErrInvalid.
func ParseSearchTypes(csv string) ([]SearchType, error) {
	var out []SearchType
	for _, p := range strings.Split(csv, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		t := SearchType(p)
		if !t.Valid() {
			return nil, domain.Invalid("types", fmt.Sprintf("tipo desconhecido %q; use task, comment, persona, run, flow ou memory", p))
		}
		out = append(out, t)
	}
	return out, nil
}

const (
	// DefaultSearchLimit e MaxSearchLimit valem por tipo (por grupo).
	DefaultSearchLimit = 10
	MaxSearchLimit     = 50

	snippetWidth  = 180
	memoryTimeout = 3 * time.Second

	// DefaultSearchStatementTimeout é o teto de cada consulta do banco da busca (SET LOCAL statement_timeout).
	DefaultSearchStatementTimeout = 2 * time.Second
	// commentMinPrefix: nos comentários (o texto mais denso) a última palavra só casa por prefixo com 3+ runas.
	commentMinPrefix = 3
)

// ErrSearchTooSlow: uma consulta da busca passou do teto de tempo do banco. É ErrInvalid (422): o remédio é do usuário
// (termos mais específicos), não um erro do servidor.
var ErrSearchTooSlow = domain.Invalid("q", "a busca demorou demais; use palavras mais específicas")

// searchTx executa fn numa transação somente leitura com SET LOCAL statement_timeout, para uma consulta patológica
// (prefixo curto num texto enorme) nunca prender uma conexão do pool. Estouro vira ErrSearchTooSlow.
func (s *Service) searchTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin search tx: %w", err)
	}
	defer rollbackTx(tx)
	d := s.SearchStatementTimeout
	if d <= 0 {
		d = DefaultSearchStatementTimeout
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('statement_timeout', $1, true)", strconv.FormatInt(d.Milliseconds(), 10)); err != nil {
		return fmt.Errorf("search statement_timeout: %w", err)
	}
	if err := fn(tx); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "57014" { // query_canceled: statement_timeout
			return ErrSearchTooSlow
		}
		return err
	}
	return nil
}

// SearchFilter restringe a busca. Types vazio = todos os tipos; Limit é por tipo.
type SearchFilter struct {
	Types []SearchType
	Limit int
}

// SearchHit é um resultado. Title/Snippet vêm em segmentos (texto + se casou): nunca HTML.
type SearchHit struct {
	Type SearchType `json:"type"`
	// ID é o da task/comentário/persona/execução/fluxo (zero na memória).
	ID domain.ID `json:"id"`
	// Ref é o que identifica o item para quem navega: chave da task, slug da persona/fluxo, id da execução, path na memória.
	Ref   string           `json:"ref"`
	Title []search.Segment `json:"title"`
	// Snippet é o trecho que casou (ou o começo do texto, sem marcas, se só o título casou).
	Snippet []search.Segment `json:"snippet,omitempty"`
	// TaskKey e TaskTitle dão o contexto de comentários e execuções.
	TaskKey   string `json:"task_key,omitempty"`
	TaskTitle string `json:"task_title,omitempty"`
	// Status: status da task/persona/execução; "enabled"/"disabled" no fluxo.
	Status string `json:"status,omitempty"`
	// Author: autor do comentário (nome da persona, ou human/system); Persona: persona da execução.
	Author  string     `json:"author,omitempty"`
	Persona string     `json:"persona,omitempty"`
	At      *time.Time `json:"at,omitempty"`
	Score   float64    `json:"score"`
	// Exact: a consulta é a chave exata da task (HNK-123).
	Exact bool `json:"exact,omitempty"`
}

// SearchGroup são os resultados de um tipo, do mais ao menos relevante.
type SearchGroup struct {
	Type SearchType  `json:"type"`
	Hits []SearchHit `json:"hits"`
	// More: há mais resultados do que o limite mostrou.
	More bool `json:"more,omitempty"`
	// Unavailable: o tipo não pôde ser consultado agora (memória fora do ar); os demais valem.
	Unavailable bool `json:"unavailable,omitempty"`
}

// SearchResult é a resposta de Search: só os grupos que têm resultado (ou ficaram indisponíveis).
type SearchResult struct {
	Query  string        `json:"query"`
	Groups []SearchGroup `json:"groups"`
	Total  int           `json:"total"`
}

// Search busca q na empresa. Exige leitura da empresa (viewer ou mais); não-membro = ErrNotFound. Consulta vazia
// (sem nenhum termo) é ErrInvalid. A chave exata de uma task (HNK-123) sempre abre o grupo de tasks.
func (s *Service) Search(ctx context.Context, companyID domain.ID, q string, f SearchFilter) (SearchResult, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return SearchResult{}, err
	}
	q = strings.TrimSpace(q)
	pq, err := search.Parse(q)
	if err != nil {
		if errors.Is(err, search.ErrEmpty) {
			return SearchResult{}, domain.Invalid("q", "informe ao menos um termo")
		}
		return SearchResult{}, fmt.Errorf("search: %w", err)
	}
	want := map[SearchType]bool{}
	for _, t := range f.Types {
		if !t.Valid() {
			return SearchResult{}, domain.Invalid("types", fmt.Sprintf("tipo desconhecido %q", t))
		}
		want[t] = true
	}
	if len(want) == 0 {
		for _, t := range SearchTypes {
			want[t] = true
		}
	}
	lim := clamp(f.Limit, DefaultSearchLimit, MaxSearchLimit)

	co, err := s.read().GetCompanyByID(ctx, companyID)
	if err != nil {
		return SearchResult{}, mapErr(err, "search company")
	}
	now := s.Now()

	type out struct {
		g   SearchGroup
		err error
	}
	results := make([]out, len(SearchTypes))
	var wg sync.WaitGroup
	for i, t := range SearchTypes {
		if !want[t] {
			continue
		}
		if t == SearchMemory && !s.MemorySearchAvailable(ctx) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var g SearchGroup
			var err error
			if t == SearchMemory {
				results[i] = out{s.searchMemory(ctx, co, q, pq, lim), nil}
				return
			}
			err = s.searchTx(ctx, func(tx pgx.Tx) error {
				base := store.New(tx)
				var e error
				switch t {
				case SearchTask:
					g, e = s.searchTasks(ctx, base, co, q, pq, lim, now)
				case SearchComment:
					g, e = s.searchComments(ctx, base, co, pq.WithMinPrefix(commentMinPrefix), lim, now)
				case SearchPersona:
					g, e = s.searchPersonas(ctx, base, co, pq, lim, now)
				case SearchRun:
					g, e = s.searchRuns(ctx, base, co, pq, lim, now)
				case SearchFlow:
					g, e = s.searchFlows(ctx, base, co, pq, lim, now)
				}
				return e
			})
			results[i] = out{g, err}
		}()
	}
	wg.Wait()

	res := SearchResult{Query: q, Groups: []SearchGroup{}}
	for _, r := range results {
		if errors.Is(r.err, ErrSearchTooSlow) {
			return SearchResult{}, ErrSearchTooSlow
		}
		if r.err != nil {
			return SearchResult{}, fmt.Errorf("search: %w", r.err)
		}
		if len(r.g.Hits) == 0 && !r.g.Unavailable {
			continue
		}
		res.Total += len(r.g.Hits)
		res.Groups = append(res.Groups, r.g)
	}
	return res, nil
}

func more[T any](rows []T, lim int) ([]T, bool) {
	if len(rows) > lim {
		return rows[:lim], true
	}
	return rows, false
}

func taskKeyOf(prefix string, number int64) string {
	return prefix + "-" + strconv.FormatInt(number, 10)
}

func (s *Service) searchTasks(ctx context.Context, q *store.Queries, co store.Company, raw string, pq search.Query, lim int, now time.Time) (SearchGroup, error) {
	g := SearchGroup{Type: SearchTask, Hits: []SearchHit{}}
	var exact *SearchHit
	if prefix, num, ok := domain.ParseTaskKey(raw); ok && prefix == co.TaskPrefix {
		r, err := q.GetTaskViewByNumber(ctx, store.GetTaskViewByNumberParams{CompanyID: co.ID, Number: num})
		if err == nil {
			t := r.Task
			at := t.UpdatedAt
			exact = &SearchHit{Type: SearchTask, ID: t.ID, Ref: taskKeyOf(r.TaskPrefix, t.Number), Title: search.Mark(t.Title, pq),
				Snippet: search.Snippet(left(t.Description, 600), pq, snippetWidth), Status: string(t.Status), At: &at, Score: 1e9, Exact: true}
			g.Hits = append(g.Hits, *exact)
		} else if !isNoRows(err) {
			return g, err
		}
	}
	rows, err := q.SearchTasks(ctx, store.SearchTasksParams{CompanyID: co.ID, Query: pq.TSQuery, Now: now, Lim: i32(lim + 1)})
	if err != nil {
		return g, err
	}
	rows, g.More = more(rows, lim)
	for _, r := range rows {
		ref := taskKeyOf(r.TaskPrefix, r.Number)
		if exact != nil && exact.Ref == ref {
			continue
		}
		at := r.UpdatedAt
		g.Hits = append(g.Hits, SearchHit{Type: SearchTask, ID: r.ID, Ref: ref, Title: search.Mark(r.Title, pq),
			Snippet: search.Snippet(r.Description, pq, snippetWidth), Status: r.Status, At: &at, Score: r.Score})
	}
	return g, nil
}

func left(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (s *Service) searchComments(ctx context.Context, q *store.Queries, co store.Company, pq search.Query, lim int, now time.Time) (SearchGroup, error) {
	g := SearchGroup{Type: SearchComment, Hits: []SearchHit{}}
	rows, err := q.SearchComments(ctx, store.SearchCommentsParams{CompanyID: co.ID, Query: pq.TSQuery, Now: now, Lim: i32(lim + 1)})
	if err != nil {
		return g, err
	}
	rows, g.More = more(rows, lim)
	for _, r := range rows {
		author := r.AuthorName
		if author == "" {
			author = r.AuthorType
		}
		at := r.CreatedAt
		key := taskKeyOf(r.TaskPrefix, r.Number)
		g.Hits = append(g.Hits, SearchHit{Type: SearchComment, ID: r.ID, Ref: key, Title: []search.Segment{{Text: r.TaskTitle}},
			Snippet: search.Snippet(r.Body, pq, snippetWidth), TaskKey: key, TaskTitle: r.TaskTitle, Author: author, At: &at, Score: r.Score})
	}
	return g, nil
}

func (s *Service) searchPersonas(ctx context.Context, q *store.Queries, co store.Company, pq search.Query, lim int, now time.Time) (SearchGroup, error) {
	g := SearchGroup{Type: SearchPersona, Hits: []SearchHit{}}
	rows, err := q.SearchPersonas(ctx, store.SearchPersonasParams{CompanyID: co.ID, Query: pq.TSQuery, Now: now, Lim: i32(lim + 1)})
	if err != nil {
		return g, err
	}
	rows, g.More = more(rows, lim)
	for _, r := range rows {
		// O trecho vem das instruções se algo casou lá; senão do cargo.
		sn := search.Snippet(r.Instructions, pq, snippetWidth)
		if !anyMatch(sn) {
			if t := search.Snippet(r.Title, pq, snippetWidth); len(t) > 0 {
				sn = t
			}
		}
		at := r.UpdatedAt
		g.Hits = append(g.Hits, SearchHit{Type: SearchPersona, ID: r.ID, Ref: r.Slug, Title: search.Mark(r.Name, pq), Snippet: sn,
			Status: r.Status, At: &at, Score: r.Score})
	}
	return g, nil
}

func anyMatch(segs []search.Segment) bool {
	for _, s := range segs {
		if s.Match {
			return true
		}
	}
	return false
}

func (s *Service) searchRuns(ctx context.Context, q *store.Queries, co store.Company, pq search.Query, lim int, now time.Time) (SearchGroup, error) {
	g := SearchGroup{Type: SearchRun, Hits: []SearchHit{}}
	rows, err := q.SearchRuns(ctx, store.SearchRunsParams{CompanyID: co.ID, Query: pq.TSQuery, Now: now, Lim: i32(lim + 1)})
	if err != nil {
		return g, err
	}
	rows, g.More = more(rows, lim)
	for _, r := range rows {
		name := r.Name
		if name == "" {
			name = r.ID.String()[:8]
		}
		at := r.CreatedAt
		// a chat run has no task (ADR-0027): no key or title
		var key, title string
		if r.Number != nil {
			key = taskKeyOf(r.TaskPrefix, *r.Number)
		}
		if r.TaskTitle != nil {
			title = *r.TaskTitle
		}
		g.Hits = append(g.Hits, SearchHit{Type: SearchRun, ID: r.ID, Ref: r.ID.String(), Title: search.Mark(name, pq),
			Snippet: search.Snippet(r.Summary, pq, snippetWidth), TaskKey: key, TaskTitle: title,
			Status: r.Status, Persona: r.PersonaName, At: &at, Score: r.Score})
	}
	return g, nil
}

func (s *Service) searchFlows(ctx context.Context, q *store.Queries, co store.Company, pq search.Query, lim int, now time.Time) (SearchGroup, error) {
	g := SearchGroup{Type: SearchFlow, Hits: []SearchHit{}}
	rows, err := q.SearchFlows(ctx, store.SearchFlowsParams{CompanyID: co.ID, Query: pq.TSQuery, Now: now, Lim: i32(lim + 1)})
	if err != nil {
		return g, err
	}
	rows, g.More = more(rows, lim)
	for _, r := range rows {
		st := "disabled"
		if r.Enabled {
			st = "enabled"
		}
		at := r.UpdatedAt
		g.Hits = append(g.Hits, SearchHit{Type: SearchFlow, ID: r.ID, Ref: r.Slug, Title: search.Mark(r.Name, pq),
			Snippet: search.Snippet(r.Description, pq, snippetWidth), Status: st, At: &at, Score: r.Score})
	}
	return g, nil
}

// searchMemory nunca devolve erro: a memória é dependência degradável (ADR-0015). Falha ou lentidão marcam o grupo
// como indisponível e a busca segue com o resto.
func (s *Service) searchMemory(ctx context.Context, co store.Company, raw string, pq search.Query, lim int) SearchGroup {
	g := SearchGroup{Type: SearchMemory, Hits: []SearchHit{}}
	mctx, cancel := context.WithTimeout(ctx, memoryTimeout)
	defer cancel()
	hits, err := s.Memory.Search(mctx, memory.Scope{Workspace: co.Slug}, raw, lim+1)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("busca na memória falhou", "company", co.Slug, "err", err.Error())
		}
		g.Unavailable = true
		return g
	}
	hits, g.More = more(hits, lim)
	for _, h := range hits {
		title := h.Title
		if title == "" {
			title = h.Path
		}
		g.Hits = append(g.Hits, SearchHit{Type: SearchMemory, Ref: h.Path, Title: search.Mark(title, pq),
			Snippet: search.Snippet(h.Snippet, pq, snippetWidth), Score: h.Score})
	}
	return g
}
