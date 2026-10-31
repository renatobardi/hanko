package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
)

// Leitura da fonte externa (ADR-0075 a 0079; fatia 1 da v0.8, #125): sessões, conversas, entradas da classificação e uso.
// Leitura é de qualquer membro; a troca manual de classe é de owner ou admin.

func nsTime(ns *int64) *time.Time {
	if ns == nil {
		return nil
	}
	t := time.Unix(0, *ns).UTC()
	return &t
}

func nsOrNil(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := t.UnixNano()
	return &v
}

// ---------- sessões ----------

// ExternalSessionView é uma sessão externa com o número de conversas.
type ExternalSessionView struct {
	ID            domain.ID
	TaskID        string
	OuteRepo      string
	WorkspaceID   *domain.ID
	Slug          string
	AgentID       *domain.ID
	State         string
	Round         string
	Worker        string
	Conversations int
	FirstAt       *time.Time
	LastAt        *time.Time
}

const sessionSelect = `SELECT s.id, s.task_id, s.oute_repo, s.workspace_id, s.slug, s.agent_id, s.state, s.round, s.worker,
  (SELECT count(*) FROM external_conversation c WHERE c.external_session_id = s.id), s.first_at, s.last_at
FROM external_session s`

func scanSession(row pgx.Row) (ExternalSessionView, error) {
	var v ExternalSessionView
	var first, last *int64
	if err := row.Scan(&v.ID, &v.TaskID, &v.OuteRepo, &v.WorkspaceID, &v.Slug, &v.AgentID, &v.State, &v.Round, &v.Worker,
		&v.Conversations, &first, &last); err != nil {
		return v, err
	}
	v.FirstAt, v.LastAt = nsTime(first), nsTime(last)
	return v, nil
}

// ListExternalSessions lista as sessões externas da empresa, da mais recente para a mais antiga.
func (s *Service) ListExternalSessions(ctx context.Context, companyID domain.ID, workspace, agent *domain.ID, limit, offset int) ([]ExternalSessionView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, sessionSelect+` WHERE s.company_id = $1 AND ($2::uuid IS NULL OR s.workspace_id = $2)
		AND ($3::uuid IS NULL OR s.agent_id = $3) ORDER BY s.last_at DESC NULLS LAST, s.id LIMIT $4 OFFSET $5`,
		companyID, workspace, agent, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list external sessions: %w", err)
	}
	defer rows.Close()
	out := []ExternalSessionView{}
	for rows.Next() {
		v, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("list external sessions: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetExternalSession devolve uma sessão externa.
func (s *Service) GetExternalSession(ctx context.Context, companyID, id domain.ID) (ExternalSessionView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ExternalSessionView{}, err
	}
	v, err := scanSession(s.Pool.QueryRow(ctx, sessionSelect+` WHERE s.company_id = $1 AND s.id = $2`, companyID, id))
	if err != nil {
		return v, mapErr(err, "external session")
	}
	return v, nil
}

// ---------- conversas ----------

// ExternalConversationView é uma conversa externa.
type ExternalConversationView struct {
	ID                  domain.ID
	SessionID           string
	Name                string
	ExternalSessionID   *domain.ID
	HostID              *domain.ID
	AgentID             *domain.ID
	OuteInstance        string
	ServiceName         string
	SubscriptionKey     string
	OuteRepo            string
	WorkspaceID         *domain.ID
	InferredAttribution bool
	FirstAt, LastAt     *time.Time
	Calls, Errors       int64
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	CostRealUSD         float64
	CostListUSD         float64
	UnpricedCalls       int64
	P95Ms               *int32
	ContextAvg          *int64
	ContextMax          *int64
	Class               string
	ClassOrigin         string
	ClassConfidence     string
	Classifier          string
	Tags                []string
}

const conversationSelect = `SELECT c.id, c.session_id, c.name, c.external_session_id, c.host_id, c.agent_id, c.oute_instance, c.service_name,
  c.subscription_key, c.oute_repo, c.workspace_id, c.inferred_attribution, c.first_at, c.last_at, c.calls, c.errors, c.input_tokens,
  c.output_tokens, c.cache_read_tokens, c.cache_creation_tokens, c.cost_real_usd::float8, c.cost_list_usd::float8, c.unpriced_calls,
  c.p95_ms, c.context_avg, c.context_max, c.class, c.class_origin, c.class_confidence, c.classifier, c.tags
FROM external_conversation c`

func scanConversation(row pgx.Row) (ExternalConversationView, error) {
	var v ExternalConversationView
	var first, last *int64
	if err := row.Scan(&v.ID, &v.SessionID, &v.Name, &v.ExternalSessionID, &v.HostID, &v.AgentID, &v.OuteInstance, &v.ServiceName,
		&v.SubscriptionKey, &v.OuteRepo, &v.WorkspaceID, &v.InferredAttribution, &first, &last, &v.Calls, &v.Errors, &v.InputTokens,
		&v.OutputTokens, &v.CacheReadTokens, &v.CacheCreationTokens, &v.CostRealUSD, &v.CostListUSD, &v.UnpricedCalls,
		&v.P95Ms, &v.ContextAvg, &v.ContextMax, &v.Class, &v.ClassOrigin, &v.ClassConfidence, &v.Classifier, &v.Tags); err != nil {
		return v, err
	}
	v.FirstAt, v.LastAt = nsTime(first), nsTime(last)
	if v.Tags == nil {
		v.Tags = []string{}
	}
	return v, nil
}

// ExternalConversationFilter são os filtros da lista de conversas externas.
type ExternalConversationFilter struct {
	Host, Agent, Workspace, Session *domain.ID
	Class, Tag                      string
	Since, Until                    time.Time
	ByCost                          bool
	Limit, Offset                   int
}

// ListExternalConversations lista as conversas externas da empresa, com filtro por host, agente, workspace, sessão, classe,
// marca e janela; a ordem é a da última atividade ou a do custo.
func (s *Service) ListExternalConversations(ctx context.Context, companyID domain.ID, f ExternalConversationFilter) ([]ExternalConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, conversationSelect+` WHERE c.company_id = $1
		AND ($2::uuid IS NULL OR c.host_id = $2) AND ($3::uuid IS NULL OR c.agent_id = $3) AND ($4::uuid IS NULL OR c.workspace_id = $4)
		AND ($5::uuid IS NULL OR c.external_session_id = $5) AND ($6::text = '' OR c.class = $6) AND ($7::text = '' OR $7 = ANY(c.tags))
		AND ($8::bigint IS NULL OR c.last_at >= $8) AND ($9::bigint IS NULL OR c.first_at < $9)
		ORDER BY CASE WHEN $10::bool THEN c.cost_real_usd + c.cost_list_usd END DESC NULLS LAST, c.last_at DESC NULLS LAST, c.id
		LIMIT $11 OFFSET $12`,
		companyID, f.Host, f.Agent, f.Workspace, f.Session, f.Class, f.Tag, nsOrNil(f.Since), nsOrNil(f.Until), f.ByCost, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list external conversations: %w", err)
	}
	defer rows.Close()
	out := []ExternalConversationView{}
	for rows.Next() {
		v, err := scanConversation(rows)
		if err != nil {
			return nil, fmt.Errorf("list external conversations: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetExternalConversation devolve uma conversa externa.
func (s *Service) GetExternalConversation(ctx context.Context, companyID, id domain.ID) (ExternalConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ExternalConversationView{}, err
	}
	v, err := scanConversation(s.Pool.QueryRow(ctx, conversationSelect+` WHERE c.company_id = $1 AND c.id = $2`, companyID, id))
	if err != nil {
		return v, mapErr(err, "external conversation")
	}
	return v, nil
}

// ---------- classificação ----------

// ClassInputView é uma entrada da classificação que não se refaz: a resposta do decision model ou a troca manual.
type ClassInputView struct {
	ID         domain.ID
	Kind       string
	Class      string
	Confidence string
	ActorType  string
	ActorID    *domain.ID
	Migrated   bool
	CreatedAt  time.Time
}

// ListConversationClassInputs lista as entradas da classificação de uma conversa, da mais nova para a mais antiga.
func (s *Service) ListConversationClassInputs(ctx context.Context, companyID, conversationID domain.ID) ([]ClassInputView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	if _, err := s.GetExternalConversation(ctx, companyID, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, kind, class, confidence, actor_type, actor_id, migrated, created_at FROM conversation_class_input
		WHERE company_id = $1 AND conversation_id = $2 ORDER BY created_at DESC, id`, companyID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list class inputs: %w", err)
	}
	defer rows.Close()
	out := []ClassInputView{}
	for rows.Next() {
		var v ClassInputView
		if err := rows.Scan(&v.ID, &v.Kind, &v.Class, &v.Confidence, &v.ActorType, &v.ActorID, &v.Migrated, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("list class inputs: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ClassifyExternalConversation troca a classe de uma conversa à mão (owner ou admin). A troca vale sobre tudo (ADR-0079 item
// 1): entra como entrada da classificação, com o autor, e a classe atual passa a ser a dela, de origem `manual`.
func (s *Service) ClassifyExternalConversation(ctx context.Context, actor domain.Actor, companyID, conversationID domain.ID, class string) (ExternalConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ExternalConversationView{}, err
	}
	class = strings.TrimSpace(class)
	if err := domain.ValidateClass(class); err != nil {
		return ExternalConversationView{}, err
	}
	if actor.Type != domain.ActorHuman || actor.ID == nil {
		return ExternalConversationView{}, domain.ErrForbidden
	}
	err := s.withTx(ctx, func(t *txc) error {
		var current string
		if err := t.tx.QueryRow(ctx, `SELECT class FROM external_conversation WHERE company_id = $1 AND id = $2 FOR UPDATE`,
			companyID, conversationID).Scan(&current); err != nil {
			return mapErr(err, "external conversation")
		}
		if _, err := t.tx.Exec(ctx, `INSERT INTO conversation_class_input (id, company_id, conversation_id, kind, class, confidence, actor_type, actor_id, created_at)
			VALUES ($1, $2, $3, 'manual', $4, 'high', 'human', $5, $6)`, domain.NewID(), companyID, conversationID, class, *actor.ID, t.now); err != nil {
			return mapErr(err, "class input")
		}
		if _, err := t.tx.Exec(ctx, `UPDATE external_conversation SET class = $3, class_origin = 'manual', class_confidence = 'high', updated_at = $4
			WHERE company_id = $1 AND id = $2`, companyID, conversationID, class, t.now); err != nil {
			return mapErr(err, "external conversation")
		}
		return t.activity(actor, companyID, "external_conversation.classified", "external_conversation", conversationID,
			map[string]any{"from": current, "to": class}, "")
	})
	if err != nil {
		return ExternalConversationView{}, err
	}
	return s.GetExternalConversation(ctx, companyID, conversationID)
}

// ---------- uso ----------

// ExternalUsageGroup são as dimensões do agrupamento do uso externo.
var externalUsageGroups = map[string]string{"day": "day", "host": "host_id", "agent": "agent_id", "model": "model", "workspace": "workspace_id", "subscription": "subscription_key"}

// ExternalUsageRowView é uma linha do uso externo; só as dimensões pedidas vêm preenchidas.
type ExternalUsageRowView struct {
	Day                                                                      *time.Time
	HostID, AgentID, WorkspaceID                                             *domain.ID
	Model, SubscriptionKey                                                   string
	Calls, Errors, InputTokens, OutputTokens, CacheReadTokens, CacheCreation int64
	CostRealUSD, CostListUSD, CostPaidUSD                                    float64
	UnpricedCalls                                                            int64
}

// ExternalUsage soma o uso externo por dia, host, agente, modelo, workspace e assinatura. Sem filtro de rodada lê o
// agregado diário; com ele (ADR-0080 item 8, o teto de custo do modo dark) soma as chamadas da rodada, pela sessão da
// conversa. O "pago" é o rateio único (ADR-0082 item 5): a mensalidade do dia de cada assinatura, dividida entre os runs e
// as chamadas externas que ela cobre, na proporção do custo de lista; a chamada sem assinatura não tem pago.
func (s *Service) ExternalUsage(ctx context.Context, companyID domain.ID, since, until time.Time, groupBy []string, round *domain.ID) ([]ExternalUsageRowView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q, args, dims, err := externalUsageSQL(companyID, since, until, groupBy, round)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("external usage: %w", err)
	}
	type raw struct {
		row    ExternalUsageRowView
		day    string
		subKey string
	}
	var rawRows []raw
	for rows.Next() {
		var r raw
		dest := make([]any, 0, len(dims)+12)
		for _, d := range dims {
			switch d {
			case "day":
				dest = append(dest, &r.row.Day)
			case "host":
				dest = append(dest, &r.row.HostID)
			case "agent":
				dest = append(dest, &r.row.AgentID)
			case "workspace":
				dest = append(dest, &r.row.WorkspaceID)
			case "model":
				dest = append(dest, &r.row.Model)
			case "subscription":
				dest = append(dest, &r.row.SubscriptionKey)
			}
		}
		dest = append(dest, &r.day, &r.subKey, &r.row.Calls, &r.row.Errors, &r.row.InputTokens, &r.row.OutputTokens, &r.row.CacheReadTokens,
			&r.row.CacheCreation, &r.row.CostRealUSD, &r.row.CostListUSD, &r.row.UnpricedCalls)
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, fmt.Errorf("external usage: %w", err)
		}
		rawRows = append(rawRows, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rates, subByKey, err := s.externalPaidRates(ctx, companyID, since, until, rawRows2Days(len(rawRows), func(i int) string { return rawRows[i].day }))
	if err != nil {
		return nil, err
	}
	// o pago de cada linha (dia × assinatura) soma na linha do agrupamento pedido
	merged := map[string]*ExternalUsageRowView{}
	var order []string
	for _, r := range rawRows {
		row := r.row
		if id, ok := subByKey[r.subKey]; ok {
			row.CostPaidUSD = rates[paidKey{id, r.day}] * row.CostListUSD
		}
		k := usageGroupKey(dims, row)
		if m, ok := merged[k]; ok {
			m.Calls += row.Calls
			m.Errors += row.Errors
			m.InputTokens += row.InputTokens
			m.OutputTokens += row.OutputTokens
			m.CacheReadTokens += row.CacheReadTokens
			m.CacheCreation += row.CacheCreation
			m.CostRealUSD += row.CostRealUSD
			m.CostListUSD += row.CostListUSD
			m.CostPaidUSD += row.CostPaidUSD
			m.UnpricedCalls += row.UnpricedCalls
			continue
		}
		cp := row
		merged[k] = &cp
		order = append(order, k)
	}
	out := make([]ExternalUsageRowView, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	if len(dims) == 0 && len(out) == 0 {
		out = append(out, ExternalUsageRowView{}) // sem agrupamento a soma é uma linha, mesmo vazia
	}
	return out, nil
}

func rawRows2Days(n int, day func(int) string) []string {
	seen := map[string]bool{}
	var out []string
	for i := 0; i < n; i++ {
		if d := day(i); !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func usageGroupKey(dims []string, r ExternalUsageRowView) string {
	var b strings.Builder
	for _, d := range dims {
		switch d {
		case "day":
			if r.Day != nil {
				b.WriteString(r.Day.Format("2006-01-02"))
			}
		case "host":
			fmt.Fprint(&b, r.HostID)
		case "agent":
			fmt.Fprint(&b, r.AgentID)
		case "workspace":
			fmt.Fprint(&b, r.WorkspaceID)
		case "model":
			b.WriteString(r.Model)
		case "subscription":
			b.WriteString(r.SubscriptionKey)
		}
		b.WriteByte(0)
	}
	return b.String()
}

// externalPaidRates devolve, por assinatura e dia, quantos USD pagos há por USD de lista (o rateio único), e a assinatura de
// cada chave externa. Os dias vêm das linhas lidas; sem linha, não há o que ratear.
func (s *Service) externalPaidRates(ctx context.Context, companyID domain.ID, since, until time.Time, days []string) (map[paidKey]float64, map[string]domain.ID, error) {
	subByKey := map[string]domain.ID{}
	if len(days) == 0 {
		return map[paidKey]float64{}, subByKey, nil
	}
	sort.Strings(days)
	from, err := time.Parse("2006-01-02", days[0])
	if err != nil {
		return nil, nil, fmt.Errorf("external usage: %w", err)
	}
	last, err := time.Parse("2006-01-02", days[len(days)-1])
	if err != nil {
		return nil, nil, fmt.Errorf("external usage: %w", err)
	}
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil, nil, mapErr(err, "get company")
	}
	subs, err := q.ListSubscriptions(ctx, companyID)
	if err != nil {
		return nil, nil, fmt.Errorf("list subscriptions: %w", err)
	}
	for _, sub := range subs {
		if sub.ExternalKey != nil {
			subByKey[*sub.ExternalKey] = sub.ID
		}
	}
	if len(subByKey) == 0 {
		return map[paidKey]float64{}, subByKey, nil
	}
	alloc, err := s.allocatePaid(ctx, q, co, from, last.AddDate(0, 0, 1))
	if err != nil {
		return nil, nil, err
	}
	return alloc.rates, subByKey, nil
}

func externalUsageSQL(companyID domain.ID, since, until time.Time, groupBy []string, round *domain.ID) (string, []any, []string, error) {
	seen := map[string]bool{}
	var dims []string
	for _, g := range groupBy {
		if _, ok := externalUsageGroups[g]; !ok {
			return "", nil, nil, domain.Invalid("group_by", "want day, host, agent, model, workspace or subscription")
		}
		if !seen[g] {
			seen[g] = true
			dims = append(dims, g)
		}
	}
	// com a rodada, a fonte são as chamadas (cada uma é uma linha); sem ela, o agregado diário. O dia e a assinatura entram
	// sempre no agrupamento (além do pedido): o pago é por dia e por assinatura e soma depois na linha pedida.
	from, prefix, dayExpr := "calls_daily d", "d.", "d.day"
	sums := `COALESCE(sum(d.calls),0)::bigint, COALESCE(sum(d.errors),0)::bigint, COALESCE(sum(d.input_tokens),0)::bigint,
		COALESCE(sum(d.output_tokens),0)::bigint, COALESCE(sum(d.cache_read_tokens),0)::bigint, COALESCE(sum(d.cache_creation_tokens),0)::bigint,
		COALESCE(sum(d.cost_real_usd),0)::float8, COALESCE(sum(d.cost_list_usd),0)::float8, COALESCE(sum(d.unpriced_calls),0)::bigint`
	where := "d.company_id = $1 AND ($2::date IS NULL OR d.day >= $2::date) AND ($3::date IS NULL OR d.day < $3::date)"
	args := []any{companyID, dateOrNil(since), dateOrNil(until)}
	if round != nil {
		from = `model_calls d JOIN external_conversation c ON c.id = d.conversation_id JOIN external_session es ON es.id = c.external_session_id
			JOIN external_round r ON r.company_id = d.company_id AND r.round_key = es.round`
		dayExpr = "(to_timestamp(d.time_unix_nano / 1000000000.0) AT TIME ZONE 'UTC')::date"
		sums = `count(*)::bigint, count(*) FILTER (WHERE d.is_error)::bigint, COALESCE(sum(d.input_tokens),0)::bigint,
			COALESCE(sum(d.output_tokens),0)::bigint, COALESCE(sum(d.cache_read_tokens),0)::bigint, COALESCE(sum(d.cache_creation_tokens),0)::bigint,
			COALESCE(sum(d.cost_real_usd),0)::float8, COALESCE(sum(d.cost_list_usd),0)::float8, count(*) FILTER (WHERE d.cost_class = 'unpriced')::bigint`
		where = fmt.Sprintf("d.company_id = $1 AND ($2::date IS NULL OR %[1]s >= $2::date) AND ($3::date IS NULL OR %[1]s < $3::date) AND r.id = $4", dayExpr)
		args = append(args, *round)
	}
	cols := make([]string, 0, len(dims))
	for _, d := range dims {
		if d == "day" {
			cols = append(cols, dayExpr)
		} else {
			cols = append(cols, prefix+externalUsageGroups[d])
		}
	}
	extra := []string{"to_char(" + dayExpr + ", 'YYYY-MM-DD')", prefix + "subscription_key"}
	group := append(append([]string{}, cols...), extra...)
	sel := strings.Join(append(append(append([]string{}, cols...), extra...), sums), ", ")
	q := "SELECT " + sel + " FROM " + from + " WHERE " + where + " GROUP BY " + strings.Join(group, ", ") + " ORDER BY " + strings.Join(group, ", ")
	return q, args, dims, nil
}

func dateOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	d := t.UTC().Truncate(24 * time.Hour)
	return &d
}

var _ = errors.Is

// ---------- conteúdo ----------

// ConversationContentItem é um trecho do conteúdo completo de uma conversa.
type ConversationContentItem struct {
	Time time.Time
	Role string
	Text string
}

// maxContentItems limita a resposta: uma conversa pode ter milhares de pedidos.
const maxContentItems = 1000

// GetExternalConversationContent devolve o conteúdo completo da conversa (owner ou admin). Cada leitura gera
// `activity_event` (ADR-0076 item 9, decisão 10 da #66). Hoje o conteúdo são os pedidos (`user_prompt`, atributo `prompt`,
// o mesmo que o classificador lê); o texto das respostas do assistente espera a conferência do nome do evento contra o
// bucket real (ADR-04 do oute-agent: "não verificado").
func (s *Service) GetExternalConversationContent(ctx context.Context, actor domain.Actor, companyID, conversationID domain.ID) ([]ConversationContentItem, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	var items []ConversationContentItem
	err := s.withTx(ctx, func(t *txc) error {
		var sessionID string
		if err := t.tx.QueryRow(ctx, `SELECT session_id FROM external_conversation WHERE company_id = $1 AND id = $2`, companyID, conversationID).
			Scan(&sessionID); err != nil {
			return mapErr(err, "external conversation")
		}
		rows, err := t.tx.Query(ctx, `SELECT time_unix_nano, attributes->>'prompt' FROM telemetry_logs
			WHERE company_id = $1 AND session_id = $2 AND event_name IN ('user_prompt', 'claude_code.user_prompt') AND attributes->>'prompt' IS NOT NULL
			ORDER BY time_unix_nano LIMIT $3`, companyID, sessionID, maxContentItems)
		if err != nil {
			return fmt.Errorf("conversation content: %w", err)
		}
		defer rows.Close()
		items = []ConversationContentItem{}
		for rows.Next() {
			var at int64
			var text string
			if err := rows.Scan(&at, &text); err != nil {
				return fmt.Errorf("conversation content: %w", err)
			}
			items = append(items, ConversationContentItem{Time: time.Unix(0, at).UTC(), Role: "prompt", Text: text})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		return t.activity(actor, companyID, "external_conversation.content_read", "external_conversation", conversationID,
			map[string]any{"items": len(items)}, "")
	})
	return items, err
}
