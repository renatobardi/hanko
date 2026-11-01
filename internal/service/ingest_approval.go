package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
)

// Aprovação externa (ADR-0081 itens 2, 4 e 5): o pedido do canal nasce dos eventos `oute.canal.*`. O Hanko só observa. Tudo é
// derivado e idempotente (`state.py` e `proposals.py` do agent-studio `@845764f`): o `proposed` mais antigo vale para o título e
// o script; cada script diferente que chega é uma versão; a decisão vale sobre o pendente em qualquer ordem de chegada; a
// lista da outbox mais nova de cada host vence.

const (
	eventCanalProposed = "oute.canal.proposed"
	eventCanalDecided  = "oute.canal.decided"
	eventCanalSnapshot = "oute.canal.snapshot"

	approveReadLimit = 65536 // o `oute approve` lê no máximo isto do arquivo do pedido
	snapshotMax      = 1000  // o oute-emit não manda lista maior
	snapshotSkew     = 300   // folga do relógio do host, em segundos
	maxApprovalText  = 300
	maxApprovalAs    = 200
	maxApprovalName  = 200
)

var canalIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-z0-9-]+$`)

// approveSHA refaz o arquivo do pedido como o `oute-propose` o escreve e devolve o sha256 inteiro dele, o que o `oute approve`
// confere (a tela mostra os 12 primeiros). Pedido que não nasceu do `oute-propose` dá outro valor (`proposals.py:approve_sha`).
func approveSHA(title, as, agent, session, script string, at int64) string {
	if agent == "" {
		agent = "desconhecido"
	}
	if as == "" {
		as = "user"
	}
	created := time.Unix(at/int64(time.Second), 0).UTC().Format("2006-01-02T15:04:05Z")
	sess := ""
	if session != "" {
		sess = "# sessao: " + session + "\n"
	}
	text := "# oute-propose\n# titulo: " + title + "\n# como: " + as + "\n# agente: " + agent + "\n# criado: " + created + "\n" + sess + "\n" + script
	b := []byte(text)
	if len(b) > approveReadLimit {
		b = b[:approveReadLimit]
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type canalEvent struct {
	kind  string
	id    string
	at    int64
	attrs map[string]string
	row   ingest.LogRow
}

func canalEvents(b ingest.Batch) []canalEvent {
	var out []canalEvent
	for _, r := range b.Logs {
		name := textOf(r.EventName)
		if name != eventCanalProposed && name != eventCanalDecided && name != eventCanalSnapshot {
			continue
		}
		a := attrStrings(r.Attributes)
		ev := canalEvent{kind: name, id: a["oute.canal.id"], at: r.TimeUnixNano, attrs: a, row: r}
		if name != eventCanalSnapshot && !canalIDRE.MatchString(ev.id) {
			continue
		}
		out = append(out, ev)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out
}

// deriveApprovals liga o lote às aprovações externas e às listas da outbox.
func deriveApprovals(t *txc, companyID domain.ID, b ingest.Batch) error {
	for _, ev := range canalEvents(b) {
		var err error
		switch ev.kind {
		case eventCanalProposed:
			err = applyProposed(t, companyID, ev)
		case eventCanalDecided:
			err = applyDecided(t, companyID, ev)
		default:
			err = applySnapshot(t, companyID, ev)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func hostIDOf(t *txc, companyID domain.ID, name string) (*domain.ID, string, error) {
	n, ok := entityText(name, maxApprovalName)
	if !ok {
		return nil, "", nil
	}
	id, err := resolveEntity(t, companyID, n, hostEntity)
	if err != nil {
		return nil, "", err
	}
	return &id, n, nil
}

func agentIDOf(t *txc, companyID domain.ID, name string) (*domain.ID, error) {
	n, ok := entityText(name, maxEntityName)
	if !ok {
		return nil, nil
	}
	id, err := resolveEntity(t, companyID, n, agentEntity)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func sessionIDOf(t *txc, companyID domain.ID, task string) (*domain.ID, error) {
	n, ok := entityText(task, maxSessionTask)
	if !ok {
		return nil, nil
	}
	var id domain.ID
	err := t.tx.QueryRow(t.ctx, `SELECT id FROM external_session WHERE company_id = $1 AND task_id = $2`, companyID, n).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ingest: sessão do pedido: %w", err)
	}
	return &id, nil
}

// ensureApproval acha o pedido do canal ou o cria pendente. `ON CONFLICT DO NOTHING` não deixa a transação abortar.
func ensureApproval(t *txc, companyID domain.ID, canal string, at int64) (domain.ID, error) {
	var id domain.ID
	err := t.tx.QueryRow(t.ctx, `INSERT INTO external_approval (id, company_id, canal_id, state, created_at, updated_at)
		VALUES ($1, $2, $3, 'pending', $4, $4) ON CONFLICT (company_id, canal_id) DO NOTHING RETURNING id`, domain.NewID(), companyID, canal, t.now).Scan(&id)
	if err == nil {
		return id, t.activity(domain.SystemActor, companyID, "external_approval.created", "external_approval", id, map[string]any{"canal_id": canal}, "")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return id, fmt.Errorf("ingest: criar o pedido %q: %w", canal, err)
	}
	if err := t.tx.QueryRow(t.ctx, `SELECT id FROM external_approval WHERE company_id = $1 AND canal_id = $2`, companyID, canal).Scan(&id); err != nil {
		return id, fmt.Errorf("ingest: buscar o pedido %q: %w", canal, err)
	}
	return id, nil
}

const proposedSQL = `UPDATE external_approval SET
  title = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $3::text ELSE title END,
  requested_as = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $4::text ELSE requested_as END,
  host_id = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $5::uuid ELSE host_id END,
  host_name = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $6::text ELSE host_name END,
  agent_id = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $7::uuid ELSE agent_id END,
  external_session_id = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $8::uuid ELSE external_session_id END,
  script_sha256 = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $9::text ELSE script_sha256 END,
  fact_dedupe_key = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $10::text ELSE fact_dedupe_key END,
  requested_at = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $2::bigint ELSE requested_at END,
  fact_time_unix_nano = CASE WHEN fact_time_unix_nano IS NULL OR $2::bigint < fact_time_unix_nano THEN $2::bigint ELSE fact_time_unix_nano END,
  updated_at = $11
WHERE id = $1`

func applyProposed(t *txc, companyID domain.ID, ev canalEvent) error {
	script := textOf(ev.row.Body)
	if script == "" {
		return nil // sem script não há versão a conferir; o fato fica na telemetria
	}
	id, err := ensureApproval(t, companyID, ev.id, ev.at)
	if err != nil {
		return err
	}
	title := trimRunes(ev.attrs["oute.canal.title"], maxApprovalText)
	as := trimRunes(ev.attrs["oute.canal.as"], maxApprovalAs)
	sha := approveSHA(title, as, textOf(ev.row.OuteAgent), ev.attrs["oute.canal.sessao"], script, ev.at)
	hostID, hostName, err := hostIDOf(t, companyID, textOf(ev.row.HostName))
	if err != nil {
		return err
	}
	agentID, err := agentIDOf(t, companyID, textOf(ev.row.OuteAgent))
	if err != nil {
		return err
	}
	sessID, err := sessionIDOf(t, companyID, textOf(ev.row.OuteTaskID))
	if err != nil {
		return err
	}
	if _, err := t.tx.Exec(t.ctx, `INSERT INTO external_approval_version (approval_id, sha256, fact_dedupe_key, fact_time_unix_nano, seen_at) VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (approval_id, sha256) DO UPDATE SET fact_dedupe_key = EXCLUDED.fact_dedupe_key, fact_time_unix_nano = EXCLUDED.fact_time_unix_nano, seen_at = EXCLUDED.seen_at
		WHERE EXCLUDED.fact_time_unix_nano < external_approval_version.fact_time_unix_nano`, id, sha, ev.row.DedupeKey, ev.at); err != nil {
		return fmt.Errorf("ingest: versão do pedido %q: %w", ev.id, err)
	}
	if _, err := t.tx.Exec(t.ctx, proposedSQL, id, ev.at, title, as, hostID, hostName, agentID, sessID, sha, ev.row.DedupeKey, t.now); err != nil {
		return fmt.Errorf("ingest: pedido %q: %w", ev.id, err)
	}
	return nil
}

func trimRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}

var (
	decisions = map[string]string{"recusado": "rejected", "executado": "executed"}
	approvers = map[string]string{"human": "human", "human-dark": "human_dark"}
	shaRE     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func intAttr(a map[string]string, k string) *int {
	n, err := strconv.Atoi(strings.TrimSpace(a[k]))
	if err != nil || n < 0 || n > 1<<31-2 {
		return nil
	}
	return &n
}

// applyDecided grava a decisão: vale sobre o pendente em qualquer ordem de chegada e a mais antiga não é trocada por outra.
func applyDecided(t *txc, companyID domain.ID, ev canalEvent) error {
	decision, ok1 := decisions[ev.attrs["oute.canal.decision"]]
	approver, ok2 := approvers[ev.attrs["oute.canal.approver"]]
	if !ok1 || !ok2 {
		return nil // fora do formato do `oute-emit`: o fato fica na telemetria
	}
	id, err := ensureApproval(t, companyID, ev.id, ev.at)
	if err != nil {
		return err
	}
	var sha *string
	if v := ev.attrs["oute.canal.sha256"]; shaRE.MatchString(v) {
		sha = &v
	}
	var rc *int
	if n, err := strconv.Atoi(strings.TrimSpace(ev.attrs["oute.canal.rc"])); err == nil && n >= -1<<31+1 && n < 1<<31-1 {
		rc = &n
	}
	by := trimRunes(textOf(ev.row.OuteAgent), maxApprovalName)
	if _, err := t.tx.Exec(t.ctx, `UPDATE external_approval SET state = 'decided', decision = $2, approver = $3, decided_sha256 = $4, rc = $5, duration_s = $6,
		output_bytes = $7, approved_by = $8, decided_at = $9, updated_at = $10
		WHERE id = $1 AND (decided_at IS NULL OR $9::bigint < decided_at)`,
		id, decision, approver, sha, rc, intAttr(ev.attrs, "oute.canal.duration_s"), intAttr(ev.attrs, "oute.canal.output_bytes"), by, ev.at, t.now); err != nil {
		return fmt.Errorf("ingest: decisão do pedido %q: %w", ev.id, err)
	}
	return nil
}

// applySnapshot guarda a lista da outbox do host: a de hora mais nova vence; lista com hora à frente do relógio do servidor é
// forjada e recusada; lista fora do formato não troca a anterior (`state.py:snapshot_statements`).
func applySnapshot(t *txc, companyID domain.ID, ev canalEvent) error {
	var ids []string
	if json.Unmarshal([]byte(ev.attrs["oute.canal.ids"]), &ids) != nil || len(ids) > snapshotMax {
		return nil
	}
	if ev.at/int64(time.Second) > t.now.Unix()+snapshotSkew {
		return nil
	}
	for _, i := range ids {
		if !canalIDRE.MatchString(i) {
			return nil
		}
	}
	hostID, _, err := hostIDOf(t, companyID, textOf(ev.row.HostName))
	if err != nil || hostID == nil {
		return err
	}
	if ids == nil {
		ids = []string{}
	}
	if _, err := t.tx.Exec(t.ctx, `INSERT INTO external_outbox_snapshot (host_id, company_id, snapshot_at, canal_ids, received_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (host_id) DO UPDATE SET snapshot_at = EXCLUDED.snapshot_at, canal_ids = EXCLUDED.canal_ids, received_at = EXCLUDED.received_at
		WHERE EXCLUDED.snapshot_at > external_outbox_snapshot.snapshot_at`, hostID, companyID, ev.at, ids, ev.row.ReceivedUnixNano); err != nil {
		return fmt.Errorf("ingest: lista da outbox: %w", err)
	}
	if _, err := t.tx.Exec(t.ctx, `UPDATE external_approval SET listed_at = GREATEST(COALESCE(listed_at, $3::bigint), $3::bigint)
		WHERE company_id = $1 AND host_id = $2 AND canal_id = ANY($4) `, companyID, hostID, ev.at, ids); err != nil {
		return fmt.Errorf("ingest: pedidos na lista da outbox: %w", err)
	}
	return nil
}
