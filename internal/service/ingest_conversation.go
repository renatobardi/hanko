package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/store"
)

// Conversa externa e chamadas ao modelo (ADR-0078, ADR-0082 itens 1 e 2; decisões Q41 e Q43 do Bardi). A conversa é o
// `session.id` do agente. Mantida na ingestão, na mesma transação do lote: cada atributo guarda a hora do fato que o trouxe
// (um fato mais antigo troca o valor); os contadores só somam a chamada que entrou de novo; o p95 e o contexto são refeitos
// a partir de `model_calls`, só para as conversas tocadas. O custo de lista é da fatia 2 (#126): aqui a chamada com custo
// real no span é `real` e as outras são `unpriced`.

const (
	maxConversationSession = 200 // external_conversation.session_id: 1 a 200
	maxInstanceText        = 200
	maxServiceText         = 200
	maxSubscriptionKey     = 32
	nameVariations         = domain.ExternalNameAttempts
	nameFallbacks          = 900 // os sufixos de 3 dígitos, de 100 a 999
)

// convFacts é o que um lote traz para uma conversa.
type convFacts struct {
	host, agent, instance, service, subscription, repo, task attr
	first, last                                              int64
}

func (c *convFacts) note(f ingest.Fixed, at int64) {
	offer := func(a *attr, p *string, max int) {
		if t, ok := entityText(textOf(p), max); ok {
			a.offer(t, at)
		}
	}
	offer(&c.host, f.HostName, maxEntityName)
	offer(&c.agent, f.OuteAgent, maxEntityName)
	offer(&c.instance, f.OuteInstance, maxInstanceText)
	offer(&c.service, f.ServiceName, maxServiceText)
	offer(&c.subscription, f.OuteSubscription, maxSubscriptionKey)
	offer(&c.repo, f.OuteRepo, maxRepoKey)
	offer(&c.task, f.OuteTaskID, maxSessionTask)
	c.first = min(c.first, at)
	c.last = max(c.last, at)
}

// entityCache guarda, durante um lote, o id de cada agente, host e workspace já resolvido.
type entityCache struct {
	companyID  domain.ID
	agents     map[string]domain.ID
	hosts      map[string]domain.ID
	workspaces map[string]domain.ID // repositório ("" = sem repositório) -> workspace
}

func newEntityCache(companyID domain.ID) *entityCache {
	return &entityCache{companyID: companyID, agents: map[string]domain.ID{}, hosts: map[string]domain.ID{}, workspaces: map[string]domain.ID{}}
}

func (e *entityCache) entity(t *txc, name string, ops entityOps, cache map[string]domain.ID) (domain.ID, error) {
	if id, ok := cache[name]; ok {
		return id, nil
	}
	id, err := resolveEntity(t, e.companyID, name, ops)
	if err == nil {
		cache[name] = id
	}
	return id, err
}

func (e *entityCache) agent(t *txc, name string) (domain.ID, error) {
	return e.entity(t, name, agentEntity, e.agents)
}
func (e *entityCache) host(t *txc, name string) (domain.ID, error) {
	return e.entity(t, name, hostEntity, e.hosts)
}

// workspace é o workspace do emparelhamento do repositório; sem repositório (ou sem emparelhamento), o `no-repo`.
func (e *entityCache) workspace(t *txc, repo string) (domain.ID, error) {
	if id, ok := e.workspaces[repo]; ok {
		return id, nil
	}
	var id domain.ID
	if repo != "" {
		ws, err := repoWorkspace(t, e.companyID, repo)
		if err != nil {
			return id, err
		}
		if ws != nil {
			e.workspaces[repo] = *ws
			return *ws, nil
		}
	}
	sys, ok, err := systemWorkspace(t, e.companyID)
	if err != nil || !ok {
		return id, errors.Join(err, errors.New("ingest: sem workspace para a chamada"))
	}
	e.workspaces[repo] = sys
	return sys, nil
}

// ensureConversation acha a conversa do `session.id` ou a cria com o nome do gerador único (ADR-0084). O nome sai do id da
// conversa; se já existe na empresa, tenta a variação seguinte, e no fim o sufixo de 3 dígitos. `INSERT ... ON CONFLICT DO
// NOTHING` não deixa a transação abortar: sem linha de volta, ou outra transação criou a conversa ou o nome está tomado.
func ensureConversation(t *txc, companyID domain.ID, sessionID string) (domain.ID, error) {
	find := func() (domain.ID, error) {
		var id domain.ID
		err := t.tx.QueryRow(t.ctx, `SELECT id FROM external_conversation WHERE company_id = $1 AND session_id = $2`, companyID, sessionID).Scan(&id)
		return id, err
	}
	if id, err := find(); err == nil {
		return id, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ID{}, fmt.Errorf("ingest: buscar a conversa %q: %w", sessionID, err)
	}
	newID := domain.NewID()
	for i := 0; i < nameVariations+nameFallbacks; i++ {
		name := domain.ExternalName(newID, i)
		if i >= nameVariations {
			name = domain.ExternalNameFallback(newID, i-nameVariations)
		}
		if name == "" {
			return domain.ID{}, errors.New("ingest: as listas do gerador de nome estão vazias")
		}
		var created domain.ID
		err := t.tx.QueryRow(t.ctx, `INSERT INTO external_conversation (id, company_id, session_id, name, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5) ON CONFLICT DO NOTHING RETURNING id`, newID, companyID, sessionID, name, t.now).Scan(&created)
		if err == nil {
			return created, t.activity(domain.SystemActor, companyID, "external_conversation.created", "external_conversation", created,
				map[string]any{"session_id": sessionID, "name": name}, "")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.ID{}, fmt.Errorf("ingest: criar a conversa %q: %w", sessionID, err)
		}
		if id, err := find(); err == nil {
			return id, nil // outra transação criou a conversa antes
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return domain.ID{}, fmt.Errorf("ingest: buscar a conversa %q: %w", sessionID, err)
		}
	}
	return domain.ID{}, fmt.Errorf("ingest: sem nome livre para a conversa %q", sessionID)
}

// A regra de cada atributo está no UPDATE, sob o lock da linha: só troca o valor quando o fato é mais antigo que o guardado.
const updateConversationFactsSQL = `UPDATE external_conversation SET
  host_id = CASE WHEN $2::bigint IS NOT NULL AND (host_at IS NULL OR $2::bigint < host_at) THEN $3::uuid ELSE host_id END,
  host_at = CASE WHEN $2::bigint IS NOT NULL AND (host_at IS NULL OR $2::bigint < host_at) THEN $2::bigint ELSE host_at END,
  agent_id = CASE WHEN $4::bigint IS NOT NULL AND (agent_at IS NULL OR $4::bigint < agent_at) THEN $5::uuid ELSE agent_id END,
  agent_at = CASE WHEN $4::bigint IS NOT NULL AND (agent_at IS NULL OR $4::bigint < agent_at) THEN $4::bigint ELSE agent_at END,
  oute_instance = CASE WHEN $6::bigint IS NOT NULL AND (oute_instance_at IS NULL OR $6::bigint < oute_instance_at) THEN $7::text ELSE oute_instance END,
  oute_instance_at = CASE WHEN $6::bigint IS NOT NULL AND (oute_instance_at IS NULL OR $6::bigint < oute_instance_at) THEN $6::bigint ELSE oute_instance_at END,
  service_name = CASE WHEN $8::bigint IS NOT NULL AND (service_name_at IS NULL OR $8::bigint < service_name_at) THEN $9::text ELSE service_name END,
  service_name_at = CASE WHEN $8::bigint IS NOT NULL AND (service_name_at IS NULL OR $8::bigint < service_name_at) THEN $8::bigint ELSE service_name_at END,
  subscription_key = CASE WHEN $10::bigint IS NOT NULL AND (subscription_key_at IS NULL OR $10::bigint < subscription_key_at) THEN $11::text ELSE subscription_key END,
  subscription_key_at = CASE WHEN $10::bigint IS NOT NULL AND (subscription_key_at IS NULL OR $10::bigint < subscription_key_at) THEN $10::bigint ELSE subscription_key_at END,
  oute_repo = CASE WHEN $12::bigint IS NOT NULL AND (oute_repo_at IS NULL OR $12::bigint < oute_repo_at) THEN $13::text ELSE oute_repo END,
  workspace_id = CASE WHEN $12::bigint IS NOT NULL AND (oute_repo_at IS NULL OR $12::bigint < oute_repo_at) THEN $14::uuid ELSE workspace_id END,
  oute_repo_at = CASE WHEN $12::bigint IS NOT NULL AND (oute_repo_at IS NULL OR $12::bigint < oute_repo_at) THEN $12::bigint ELSE oute_repo_at END,
  external_session_id = COALESCE($15::uuid, external_session_id),
  first_at = LEAST(COALESCE(first_at, $16::bigint), $16::bigint),
  last_at = GREATEST(COALESCE(last_at, $17::bigint), $17::bigint),
  updated_at = $18
WHERE id = $1`

func optAt(a attr) *int64 {
	if !a.ok {
		return nil
	}
	v := a.at
	return &v
}

func optText(a attr) *string {
	if !a.ok {
		return nil
	}
	v := a.text
	return &v
}

func updateConversationFacts(t *txc, companyID, id domain.ID, c *convFacts, ents *entityCache) error {
	var hostID, agentID, wsID, sessionID *domain.ID
	if c.host.ok {
		v, err := ents.host(t, c.host.text)
		if err != nil {
			return err
		}
		hostID = &v
	}
	if c.agent.ok {
		v, err := ents.agent(t, c.agent.text)
		if err != nil {
			return err
		}
		agentID = &v
	}
	if c.repo.ok {
		ws, err := repoWorkspace(t, companyID, c.repo.text)
		if err != nil {
			return err
		}
		wsID = ws
	}
	if c.task.ok {
		sid, err := t.q.ExternalSessionIDByTask(t.ctx, store.ExternalSessionIDByTaskParams{CompanyID: companyID, TaskID: c.task.text})
		if err == nil {
			sessionID = &sid
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("ingest: sessão da conversa: %w", err)
		}
	}
	_, err := t.tx.Exec(t.ctx, updateConversationFactsSQL, id,
		optAt(c.host), hostID, optAt(c.agent), agentID, optAt(c.instance), optText(c.instance), optAt(c.service), optText(c.service),
		optAt(c.subscription), optText(c.subscription), optAt(c.repo), optText(c.repo), wsID, sessionID, c.first, c.last, t.now)
	if err != nil {
		return fmt.Errorf("ingest: atualizar a conversa: %w", err)
	}
	return nil
}

// ---------- chamadas ao modelo ----------

// Spans que são uma chamada ao modelo (ADR-0082 item 1). O agente `router` fica fora.
var modelCallSpans = map[string]bool{"claude_code.llm_request": true, "session_task.turn": true, "ai_memory.llm_request": true, "jev.decision": true}

const routerAgent = "router"

func jsonMap(s string) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

func hasKey(m map[string]any, k string) bool { return m[k] != nil }

// callRole é o papel da chamada (ADR-0078 item 3; `usage.py:82-108@845764f`): subagente, revisor; depois o papel que a
// abertura da sessão deu, e sem ele o do resource da própria chamada.
func callRole(r ingest.SpanRow, sessionRole string) domain.CallRole {
	attrs, res := jsonMap(r.Attributes), jsonMap(r.ResourceAttributes)
	if textOf(r.Name) == "claude_code.llm_request" {
		qs, _ := attrs["query_source_safe"].(string)
		if strings.HasPrefix(qs, "agent.") || hasKey(attrs, "agent_id") {
			return domain.CallRoleSubagent
		}
	}
	round := textOf(r.OuteSwarmRound) != ""
	if hasKey(res, "oute.swarm.step") && round && !hasKey(res, "oute.swarm.session") {
		return domain.CallRoleReviewer
	}
	if sessionRole != "" {
		return domain.CallRole(sessionRole)
	}
	switch {
	case hasKey(res, "oute.swarm.session"):
		return domain.CallRoleWorker
	case round:
		return domain.CallRoleDispatcher
	}
	return domain.CallRoleStandalone
}

// sessionRole é o papel que os eventos de abertura da sessão (`oute.task.opened` e `oute.task.reopened`) dão: worker se
// algum traz a sessão de worker, dispatcher se algum está numa rodada, senão standalone. Vazio sem evento de abertura.
func sessionRole(t *txc, companyID domain.ID, task string) (string, error) {
	var worker, dispatcher bool
	var n int
	err := t.tx.QueryRow(t.ctx, `SELECT count(*), COALESCE(bool_or(attributes->>'oute.swarm.session' IS NOT NULL), false),
		COALESCE(bool_or(oute_swarm_round IS NOT NULL), false)
		FROM telemetry_logs WHERE company_id = $1 AND event_name IN ('oute.task.opened', 'oute.task.reopened') AND oute_task_id = $2`,
		companyID, task).Scan(&n, &worker, &dispatcher)
	if err != nil {
		return "", fmt.Errorf("ingest: papel da sessão %q: %w", task, err)
	}
	switch {
	case n == 0:
		return "", nil
	case worker:
		return string(domain.CallRoleWorker), nil
	case dispatcher:
		return string(domain.CallRoleDispatcher), nil
	}
	return string(domain.CallRoleStandalone), nil
}

type dailyKey struct {
	day              time.Time
	host, agent, ws  domain.ID
	model, subscript string
}

type tally struct {
	calls, errors, input, output, cacheRead, cacheCreate, unpriced int64
	costReal                                                       float64
}

func (a *tally) add(r ingest.SpanRow, isErr bool, class domain.CallCostClass, real float64) {
	a.calls++
	if isErr {
		a.errors++
	}
	a.input += int64Of(r.InputTokens)
	a.output += int64Of(r.OutputTokens)
	a.cacheRead += int64Of(r.CacheReadTokens)
	a.cacheCreate += int64Of(r.CacheCreationTokens)
	if class == domain.CallCostUnpriced {
		a.unpriced++
	}
	a.costReal += real
}

func int64Of(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

const insertModelCallSQL = `INSERT INTO model_calls (company_id, dedupe_key, time_unix_nano, conversation_id, host_id, agent_id, workspace_id,
  role, model, subscription_key, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, duration_ns, is_error,
  cost_class, cost_real_usd, origin)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, 'ingested')
ON CONFLICT DO NOTHING RETURNING dedupe_key`

// recordModelCalls grava, em `model_calls`, as chamadas ao modelo do lote, e soma no `calls_daily` e na conversa só as que
// entraram de novo (a chave `(empresa, dedupe_key)` recusa o reenvio). Chamada sem host ou sem agente não tem onde entrar:
// o bruto a guarda, e a derivada a deixa de fora.
func recordModelCalls(t *txc, companyID domain.ID, b ingest.Batch, convs map[string]domain.ID, ents *entityCache) error {
	type pending struct {
		row  ingest.SpanRow
		args []any
		day  dailyKey
		conv *domain.ID
		isEr bool
		cls  domain.CallCostClass
		real float64
	}
	var list []pending
	roles := map[string]string{}
	for _, r := range b.Spans {
		agent := textOf(r.OuteAgent)
		if !modelCallSpans[textOf(r.Name)] || agent == routerAgent {
			continue
		}
		hostName, okH := entityText(textOf(r.HostName), maxEntityName)
		agentName, okA := entityText(agent, maxEntityName)
		if !okH || !okA {
			continue
		}
		hostID, err := ents.host(t, hostName)
		if err != nil {
			return err
		}
		agentID, err := ents.agent(t, agentName)
		if err != nil {
			return err
		}
		repo, _ := entityText(textOf(r.OuteRepo), maxRepoKey)
		wsID, err := ents.workspace(t, repo)
		if err != nil {
			return err
		}
		task := textOf(r.OuteTaskID)
		role, seen := roles[task]
		if !seen && task != "" {
			if role, err = sessionRole(t, companyID, task); err != nil {
				return err
			}
			roles[task] = role
		}
		var conv *domain.ID
		if id, ok := convs[textOf(r.SessionID)]; ok {
			conv = &id
		}
		sub, _ := entityText(textOf(r.OuteSubscription), maxSubscriptionKey)
		model := textOf(r.Model)
		cls, real := domain.CallCostUnpriced, 0.0
		// na assinatura zai o custo que o Claude Code calcula para o glm-* não é real (ADR-0082 item 1)
		if r.CostUSD != nil && sub != "zai" {
			cls, real = domain.CallCostReal, *r.CostUSD
		}
		isErr := r.StatusCode != nil && *r.StatusCode == 2
		var realArg any
		if cls == domain.CallCostReal {
			realArg = real
		}
		list = append(list, pending{row: r, conv: conv, isEr: isErr, cls: cls, real: real,
			day: dailyKey{day: time.Unix(0, r.TimeUnixNano).UTC().Truncate(24 * time.Hour), host: hostID, agent: agentID, ws: wsID, model: model, subscript: sub},
			args: []any{companyID, r.DedupeKey, r.TimeUnixNano, conv, hostID, agentID, wsID, string(callRole(r, role)), model, sub,
				int64Of(r.InputTokens), int64Of(r.OutputTokens), int64Of(r.CacheReadTokens), int64Of(r.CacheCreationTokens), r.DurationNs, isErr,
				string(cls), realArg}})
	}
	if len(list) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, p := range list {
		batch.Queue(insertModelCallSQL, p.args...)
	}
	res := t.tx.SendBatch(t.ctx, batch)
	daily := map[dailyKey]*tally{}
	perConv := map[domain.ID]*tally{}
	for _, p := range list {
		var key string
		err := res.QueryRow().Scan(&key)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // chamada que já estava gravada
		}
		if err != nil {
			_ = res.Close()
			return fmt.Errorf("ingest: gravar a chamada ao modelo: %w", err)
		}
		if daily[p.day] == nil {
			daily[p.day] = &tally{}
		}
		daily[p.day].add(p.row, p.isEr, p.cls, p.real)
		if p.conv != nil {
			if perConv[*p.conv] == nil {
				perConv[*p.conv] = &tally{}
			}
			perConv[*p.conv].add(p.row, p.isEr, p.cls, p.real)
		}
	}
	if err := res.Close(); err != nil {
		return fmt.Errorf("ingest: gravar as chamadas ao modelo: %w", err)
	}
	return addTallies(t, companyID, daily, perConv)
}

const upsertDailySQL = `INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key, calls, errors,
  input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_real_usd, unpriced_calls)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (company_id, day, host_id, agent_id, workspace_id, model, subscription_key) DO UPDATE SET
  calls = calls_daily.calls + EXCLUDED.calls, errors = calls_daily.errors + EXCLUDED.errors,
  input_tokens = calls_daily.input_tokens + EXCLUDED.input_tokens, output_tokens = calls_daily.output_tokens + EXCLUDED.output_tokens,
  cache_read_tokens = calls_daily.cache_read_tokens + EXCLUDED.cache_read_tokens,
  cache_creation_tokens = calls_daily.cache_creation_tokens + EXCLUDED.cache_creation_tokens,
  cost_real_usd = calls_daily.cost_real_usd + EXCLUDED.cost_real_usd, unpriced_calls = calls_daily.unpriced_calls + EXCLUDED.unpriced_calls`

const addConversationSQL = `UPDATE external_conversation SET calls = calls + $2, errors = errors + $3, input_tokens = input_tokens + $4,
  output_tokens = output_tokens + $5, cache_read_tokens = cache_read_tokens + $6, cache_creation_tokens = cache_creation_tokens + $7,
  cost_real_usd = cost_real_usd + $8, unpriced_calls = unpriced_calls + $9, updated_at = $10 WHERE id = $1`

// refreshConversationSQL refaz o p95 da duração e o contexto (entrada + cache lido) da conversa, a partir das chamadas.
const refreshConversationSQL = `UPDATE external_conversation c SET p95_ms = s.p95, context_avg = s.avg_ctx, context_max = s.max_ctx
FROM (SELECT conversation_id,
    round((percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ns) FILTER (WHERE duration_ns IS NOT NULL)) / 1000000.0)::int AS p95,
    avg(input_tokens + cache_read_tokens)::bigint AS avg_ctx, max(input_tokens + cache_read_tokens) AS max_ctx
  FROM model_calls WHERE conversation_id = ANY($1) GROUP BY conversation_id) s
WHERE c.id = s.conversation_id`

func addTallies(t *txc, companyID domain.ID, daily map[dailyKey]*tally, perConv map[domain.ID]*tally) error {
	for k, v := range daily {
		if _, err := t.tx.Exec(t.ctx, upsertDailySQL, companyID, k.day, k.host, k.agent, k.ws, k.model, k.subscript, v.calls, v.errors,
			v.input, v.output, v.cacheRead, v.cacheCreate, v.costReal, v.unpriced); err != nil {
			return fmt.Errorf("ingest: somar no calls_daily: %w", err)
		}
	}
	ids := make([]domain.ID, 0, len(perConv))
	for id, v := range perConv {
		if _, err := t.tx.Exec(t.ctx, addConversationSQL, id, v.calls, v.errors, v.input, v.output, v.cacheRead, v.cacheCreate, v.costReal,
			v.unpriced, t.now); err != nil {
			return fmt.Errorf("ingest: somar na conversa: %w", err)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err := t.tx.Exec(t.ctx, refreshConversationSQL, ids); err != nil {
		return fmt.Errorf("ingest: refazer o p95 e o contexto: %w", err)
	}
	return nil
}

// deriveConversations liga o lote às conversas: cria as que faltam (com nome), aplica os atributos pela hora do fato e
// registra as chamadas ao modelo.
func deriveConversations(t *txc, companyID domain.ID, b ingest.Batch, d derived) error {
	ents := newEntityCache(companyID)
	convs := map[string]domain.ID{}
	for _, sid := range sortedTexts(d.conversations) {
		id, err := ensureConversation(t, companyID, sid)
		if err != nil {
			return err
		}
		convs[sid] = id
		if err := updateConversationFacts(t, companyID, id, d.conversations[sid], ents); err != nil {
			return err
		}
	}
	return recordModelCalls(t, companyID, b, convs, ents)
}
