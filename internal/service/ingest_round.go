package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
)

// Rodada externa (ADR-0080 itens 2, 3 e 7): nasce dos eventos `oute.swarm.*` da ingestão, na regra de `state.py` e `etapas.py`
// do agent-studio (`@845764f`). Tudo é derivado e idempotente: reenviar o lote, em qualquer ordem, dá o mesmo resultado.
// Cada atributo guarda a hora do fato que o trouxe (o mais antigo vence); a abertura é a mais antiga e o fechamento o mais
// novo; a revisão mais alta de uma etapa vence. O texto da etapa nunca é copiado: a linha aponta para o fato.

const (
	eventRoundOpened   = "oute.swarm.round.opened"
	eventRoundClosed   = "oute.swarm.round.closed"
	eventSessionSpawn  = "oute.swarm.session.spawned"
	eventSessionClosed = "oute.swarm.session.closed"
	eventStepPublished = "oute.swarm.step.published"

	maxRoundKey  = 200
	maxIssueRef  = 200
	maxStepRev   = 9999
	maxRoundSess = 1_000_000
)

var (
	roundNameRE = regexp.MustCompile(`^[A-Z][a-z]+_[A-Z][a-z]+(_[1-9][0-9]{2})?$`)
	cycleRefRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+#[0-9]{1,9}$`)
	stepKeyRE   = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	sha256RE    = regexp.MustCompile(`^[0-9a-f]{64}$`)

	// Os valores dos eventos são os do agent-studio (português); o Hanko guarda os nomes em inglês (ADR-0080 item 1).
	stepKinds   = map[string]string{"triagem": "triage", "merge": "merge", "kaizen": "kaizen", "fechamento": "closing", "ciclo": "cycle_summary"}
	stepReviews = map[string]string{"aprovado": "approved", "reprovado": "rejected", "sem-revisor": "no_reviewer"}
	stepChecks  = map[string]string{"ok": "ok", "falhou": "failed", "ausente": "missing"}
)

// swarmEvent é um evento `oute.swarm.*` com os atributos lidos.
type swarmEvent struct {
	name  string
	round string
	at    int64
	attrs map[string]string
	row   ingest.LogRow
}

// attrStrings lê o JSON de atributos de um log como texto; número vira o texto dele. Atributo que não é escalar fica de fora.
func attrStrings(raw string) map[string]string {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	var m map[string]any
	if dec.Decode(&m) != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case string:
			out[k] = x
		case json.Number:
			out[k] = x.String()
		}
	}
	return out
}

func swarmEvents(b ingest.Batch) []swarmEvent {
	var out []swarmEvent
	for _, r := range b.Logs {
		name := textOf(r.EventName)
		if !strings.HasPrefix(name, "oute.swarm.") && !strings.HasPrefix(name, "oute.task.") {
			continue
		}
		attrs := attrStrings(r.Attributes)
		round, ok := entityText(attrs["oute.swarm.round"], maxRoundKey)
		if !ok {
			continue
		}
		out = append(out, swarmEvent{name: name, round: round, at: r.TimeUnixNano, attrs: attrs, row: r})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out
}

// roundFacts é o que o lote traz para uma rodada.
type roundFacts struct {
	name           string
	repo           attr
	max            attr
	opened, closed *int64
	participants   map[string]*participantFacts
	stepSeen       bool
}

type participantFacts struct {
	issue          string
	spawned, close *int64
	taskIDs        []string
}

func minPtr(p *int64, v int64) *int64 {
	if p == nil || v < *p {
		return &v
	}
	return p
}

func maxPtr(p *int64, v int64) *int64 {
	if p == nil || v > *p {
		return &v
	}
	return p
}

func (f *roundFacts) participant(slug string) *participantFacts {
	if f.participants == nil {
		f.participants = map[string]*participantFacts{}
	}
	p := f.participants[slug]
	if p == nil {
		p = &participantFacts{}
		f.participants[slug] = p
	}
	return p
}

// note aplica um evento que não é de etapa aos fatos da rodada.
func (f *roundFacts) note(ev swarmEvent) {
	a := ev.attrs
	switch ev.name {
	case eventRoundOpened:
		f.opened = minPtr(f.opened, ev.at)
		if n := strings.TrimSpace(a["oute.swarm.round.name"]); roundNameRE.MatchString(n) && f.name == "" {
			f.name = n
		}
	case eventRoundClosed:
		f.closed = maxPtr(f.closed, ev.at)
	case eventSessionSpawn, eventSessionClosed:
		slug, ok := entityText(a["oute.swarm.session"], maxEntityName)
		if !ok {
			return
		}
		p := f.participant(slug)
		if ev.name == eventSessionSpawn {
			p.spawned = minPtr(p.spawned, ev.at)
			if is, ok := entityText(a["oute.swarm.issue"], maxIssueRef); ok && p.issue == "" {
				p.issue = is
			}
		} else {
			p.close = maxPtr(p.close, ev.at)
		}
	}
	if repo, ok := entityText(a["oute.swarm.repo"], maxRepoKey); ok && (ev.name == eventRoundOpened || ev.name == eventSessionSpawn) {
		f.repo.offer(repo, ev.at)
	}
	if ev.name == eventRoundOpened {
		if n, err := strconv.Atoi(strings.TrimSpace(a["oute.swarm.max"])); err == nil && n >= 0 && n <= maxRoundSess {
			f.max.offer(strconv.Itoa(n), ev.at)
		}
	}
	if strings.HasPrefix(ev.name, "oute.task.") {
		slug, ok1 := entityText(a["oute.swarm.session"], maxEntityName)
		task, ok2 := entityText(textOf(ev.row.OuteTaskID), maxSessionTask)
		if ok1 && ok2 {
			p := f.participant(slug)
			p.taskIDs = append(p.taskIDs, task)
		}
	}
}

// stepEvent é um `oute.swarm.step.published` válido, já em inglês.
type stepEvent struct {
	round, kind, key, review, check, sha, cycle string
	rev                                         int
	row                                         ingest.LogRow
	at                                          int64
}

// parseStep confere o formato do `oute-swarm step publish`; evento fora dele fica só no fato (state.py:step_valid).
func parseStep(ev swarmEvent) (stepEvent, bool) {
	a := ev.attrs
	kind, ok1 := stepKinds[a["oute.swarm.step.kind"]]
	review, ok2 := stepReviews[a["oute.swarm.step.review"]]
	rev, err := strconv.Atoi(a["oute.swarm.step.rev"])
	key := a["oute.swarm.step.key"]
	sha := a["oute.swarm.step.sha256"]
	if !ok1 || !ok2 || err != nil || rev < 1 || rev > maxStepRev || !sha256RE.MatchString(sha) || (kind == "merge") != (key != "") ||
		(key != "" && !stepKeyRE.MatchString(key)) {
		return stepEvent{}, false
	}
	check, ok := stepChecks[a["oute.swarm.step.refcheck"]]
	if !ok {
		check = "missing"
	}
	cycle := a["oute.swarm.cycle"]
	if !cycleRefRE.MatchString(cycle) {
		cycle = ""
	}
	return stepEvent{round: ev.round, kind: kind, key: key, review: review, check: check, sha: sha, cycle: cycle, rev: rev, row: ev.row, at: ev.at}, true
}

// deriveRounds liga o lote às rodadas externas, participantes, ciclos e etapas.
func deriveRounds(t *txc, companyID domain.ID, b ingest.Batch) error {
	events := swarmEvents(b)
	if len(events) == 0 {
		return nil
	}
	rounds := map[string]*roundFacts{}
	var steps []stepEvent
	for _, ev := range events {
		f := rounds[ev.round]
		if f == nil {
			f = &roundFacts{}
			rounds[ev.round] = f
		}
		if ev.name == eventStepPublished {
			if s, ok := parseStep(ev); ok {
				steps = append(steps, s)
			}
			continue
		}
		f.note(ev)
	}
	for _, s := range steps {
		if s.kind != "cycle_summary" {
			rounds[s.round].stepSeen = true
		}
	}
	keys := make([]string, 0, len(rounds))
	for k, f := range rounds {
		if f.opened != nil || f.closed != nil || f.stepSeen || len(f.participants) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	ids := map[string]domain.ID{}
	for _, k := range keys {
		id, err := upsertRound(t, companyID, k, rounds[k])
		if err != nil {
			return err
		}
		ids[k] = id
		if err := upsertParticipants(t, companyID, id, rounds[k]); err != nil {
			return err
		}
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].rev < steps[j].rev })
	for _, s := range steps {
		if err := upsertStep(t, companyID, ids, s); err != nil {
			return err
		}
	}
	return nil
}

// ensureRound acha a rodada da chave ou a cria com o nome do evento (se válido e livre) ou do gerador único (ADR-0084).
func ensureRound(t *txc, companyID domain.ID, key, hint string) (domain.ID, error) {
	find := func() (domain.ID, error) {
		var id domain.ID
		err := t.tx.QueryRow(t.ctx, `SELECT id FROM external_round WHERE company_id = $1 AND round_key = $2`, companyID, key).Scan(&id)
		return id, err
	}
	if id, err := find(); err == nil {
		return id, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ID{}, fmt.Errorf("ingest: buscar a rodada %q: %w", key, err)
	}
	newID := domain.NewID()
	for i := -1; i < nameVariations+nameFallbacks; i++ {
		name := hint
		switch {
		case i < 0 && hint == "":
			continue
		case i >= nameVariations:
			name = domain.ExternalNameFallback(newID, i-nameVariations)
		case i >= 0:
			name = domain.ExternalName(newID, i)
		}
		var created domain.ID
		err := t.tx.QueryRow(t.ctx, `INSERT INTO external_round (id, company_id, round_key, name, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5) ON CONFLICT DO NOTHING RETURNING id`, newID, companyID, key, name, t.now).Scan(&created)
		if err == nil {
			return created, t.activity(domain.SystemActor, companyID, "external_round.created", "external_round", created,
				map[string]any{"round_key": key, "name": name}, "")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.ID{}, fmt.Errorf("ingest: criar a rodada %q: %w", key, err)
		}
		if id, err := find(); err == nil {
			return id, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return domain.ID{}, fmt.Errorf("ingest: buscar a rodada %q: %w", key, err)
		}
	}
	return domain.ID{}, fmt.Errorf("ingest: sem nome livre para a rodada %q", key)
}

const updateRoundFactsSQL = `UPDATE external_round SET
  repo = CASE WHEN $2::bigint IS NOT NULL AND (repo_at IS NULL OR $2::bigint < repo_at) THEN $3::text ELSE repo END,
  workspace_id = CASE WHEN $2::bigint IS NOT NULL AND (repo_at IS NULL OR $2::bigint < repo_at) THEN $4::uuid ELSE workspace_id END,
  repo_at = CASE WHEN $2::bigint IS NOT NULL AND (repo_at IS NULL OR $2::bigint < repo_at) THEN $2::bigint ELSE repo_at END,
  max_sessions = CASE WHEN $5::bigint IS NOT NULL AND (max_at IS NULL OR $5::bigint < max_at) THEN $6::int ELSE max_sessions END,
  max_at = CASE WHEN $5::bigint IS NOT NULL AND (max_at IS NULL OR $5::bigint < max_at) THEN $5::bigint ELSE max_at END,
  opened_at = CASE WHEN $7::bigint IS NULL THEN opened_at ELSE LEAST(COALESCE(opened_at, $7::bigint), $7::bigint) END,
  closed_at = CASE WHEN $8::bigint IS NULL THEN closed_at ELSE GREATEST(COALESCE(closed_at, $8::bigint), $8::bigint) END,
  updated_at = $9
WHERE id = $1`

func upsertRound(t *txc, companyID domain.ID, key string, f *roundFacts) (domain.ID, error) {
	id, err := ensureRound(t, companyID, key, f.name)
	if err != nil {
		return id, err
	}
	var repoAt, maxAt *int64
	var repo string
	var ws *domain.ID
	if f.repo.ok {
		repoAt, repo = &f.repo.at, f.repo.text
		if err := ensurePairing(t, companyID, repo); err != nil {
			return id, err
		}
		if ws, err = repoWorkspace(t, companyID, repo); err != nil {
			return id, err
		}
	}
	var maxVal *int
	if f.max.ok {
		maxAt = &f.max.at
		n, _ := strconv.Atoi(f.max.text)
		maxVal = &n
	}
	if _, err := t.tx.Exec(t.ctx, updateRoundFactsSQL, id, repoAt, repo, ws, maxAt, maxVal, f.opened, f.closed, t.now); err != nil {
		return id, fmt.Errorf("ingest: atualizar a rodada %q: %w", key, err)
	}
	return id, nil
}

func upsertParticipants(t *txc, companyID, roundID domain.ID, f *roundFacts) error {
	slugs := make([]string, 0, len(f.participants))
	for s := range f.participants {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		p := f.participants[slug]
		var sessionID *domain.ID
		for _, task := range p.taskIDs {
			var id domain.ID
			err := t.tx.QueryRow(t.ctx, `SELECT id FROM external_session WHERE company_id = $1 AND task_id = $2`, companyID, task).Scan(&id)
			if err == nil {
				sessionID = &id
				break
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("ingest: sessão da rodada: %w", err)
			}
		}
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO round_participant (id, company_id, round_id, slug, issue_ref, external_session_id, spawned_at, closed_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
			ON CONFLICT (round_id, slug) DO UPDATE SET
			  issue_ref = CASE WHEN round_participant.issue_ref = '' THEN EXCLUDED.issue_ref ELSE round_participant.issue_ref END,
			  external_session_id = COALESCE(EXCLUDED.external_session_id, round_participant.external_session_id),
			  spawned_at = CASE WHEN EXCLUDED.spawned_at IS NULL THEN round_participant.spawned_at ELSE LEAST(COALESCE(round_participant.spawned_at, EXCLUDED.spawned_at), EXCLUDED.spawned_at) END,
			  closed_at = CASE WHEN EXCLUDED.closed_at IS NULL THEN round_participant.closed_at ELSE GREATEST(COALESCE(round_participant.closed_at, EXCLUDED.closed_at), EXCLUDED.closed_at) END,
			  updated_at = EXCLUDED.updated_at`,
			domain.NewID(), companyID, roundID, slug, p.issue, sessionID, p.spawned, p.close, t.now); err != nil {
			return fmt.Errorf("ingest: participante %q: %w", slug, err)
		}
	}
	return nil
}

func ensureCycle(t *txc, companyID domain.ID, issue string) (domain.ID, error) {
	var id domain.ID
	err := t.tx.QueryRow(t.ctx, `INSERT INTO round_cycle (id, company_id, issue_ref, created_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (company_id, issue_ref) DO UPDATE SET issue_ref = round_cycle.issue_ref RETURNING id`, domain.NewID(), companyID, issue, t.now).Scan(&id)
	if err != nil {
		return id, fmt.Errorf("ingest: ciclo %q: %w", issue, err)
	}
	return id, nil
}

// upsertStep grava a etapa só se a revisão do evento é maior ou igual à guardada (a mais alta vence, em qualquer ordem de
// chegada). O ciclo da rodada é o da revisão vigente da triagem.
func upsertStep(t *txc, companyID domain.ID, rounds map[string]domain.ID, s stepEvent) error {
	var roundID, cycleID *domain.ID
	scopeCol := "round_id"
	if s.kind == "cycle_summary" {
		if s.cycle == "" {
			return nil // o resumo do ciclo sem o ciclo não tem onde aparecer
		}
		id, err := ensureCycle(t, companyID, s.cycle)
		if err != nil {
			return err
		}
		cycleID, scopeCol = &id, "cycle_id"
	} else {
		id, ok := rounds[s.round]
		if !ok {
			return nil
		}
		roundID = &id
	}
	var stepID domain.ID
	err := t.tx.QueryRow(t.ctx, `INSERT INTO round_step (id, company_id, round_id, cycle_id, kind, step_key, revision, sha256, review, reference_check,
		fact_dedupe_key, fact_time_unix_nano, published_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12, $13, $13)
		ON CONFLICT (`+scopeCol+`, kind, step_key) WHERE `+scopeCol+` IS NOT NULL DO UPDATE SET
		  revision = EXCLUDED.revision, sha256 = EXCLUDED.sha256, review = EXCLUDED.review, reference_check = EXCLUDED.reference_check,
		  fact_dedupe_key = EXCLUDED.fact_dedupe_key, fact_time_unix_nano = EXCLUDED.fact_time_unix_nano, published_at = EXCLUDED.published_at,
		  updated_at = EXCLUDED.updated_at
		WHERE round_step.revision <= EXCLUDED.revision RETURNING id`,
		domain.NewID(), companyID, roundID, cycleID, s.kind, s.key, s.rev, s.sha, s.review, s.check, s.row.DedupeKey, s.row.TimeUnixNano, t.now).Scan(&stepID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // há uma revisão mais alta guardada
	}
	if err != nil {
		return fmt.Errorf("ingest: etapa %s da rodada %q: %w", s.kind, s.round, err)
	}
	if s.kind != "triage" {
		return nil
	}
	var cycle *domain.ID
	if s.cycle != "" {
		id, err := ensureCycle(t, companyID, s.cycle)
		if err != nil {
			return err
		}
		cycle = &id
	}
	if _, err := t.tx.Exec(t.ctx, `UPDATE external_round SET cycle_id = $2, cycle_at = $3, updated_at = $4 WHERE id = $1`, *roundID, cycle, s.at, t.now); err != nil {
		return fmt.Errorf("ingest: ciclo da rodada %q: %w", s.round, err)
	}
	return nil
}
