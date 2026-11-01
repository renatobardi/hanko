package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/ingest"
)

// swarmLog monta um registro de log com o nome do evento e os atributos (pares chave, valor) mais o id do evento.
func swarmLog(id, event string, at int64, kv ...string) string {
	return swarmLogBody(id, event, "texto", at, kv...)
}

func swarmLogBody(id, event, body string, at int64, kv ...string) string {
	attrs := []string{fmt.Sprintf(`{"key":"oute.event.id","value":{"stringValue":%q}}`, id),
		`{"key":"host.name","value":{"stringValue":"oute-server"}}`, `{"key":"oute.swarm.round","value":{"stringValue":"swarm-1"}}`}
	for i := 0; i < len(kv); i += 2 {
		attrs = append(attrs, fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, kv[i], kv[i+1]))
	}
	return fmt.Sprintf(`{"timeUnixNano":"%d","eventName":%q,"body":{"stringValue":%q},"attributes":[%s]}`, at, event, body, strings.Join(attrs, ","))
}

const stepSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// A rodada, os participantes, o ciclo e as etapas nascem dos eventos `oute.swarm.*`; a revisão mais alta vence em qualquer
// ordem de chegada e o reenvio não muda nada (ADR-0080 itens 2, 3 e 7).
func TestWriteTelemetryDerivesRounds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	actor := ingestActor(t, e)
	token, _, err := e.svc.CreateIngestKey(ctx, actor, e.co.ID, "oute-server")
	if err != nil {
		t.Fatal(err)
	}
	key, err := e.svc.ResolveIngestKey(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UnixNano()
	step := func(id string, at int64, kind, rev, skey, review string) string {
		body := "texto"
		if kind == "triagem" && rev == "2" {
			body = "## Ações\n- a1: rodar o deploy\n- a2: ver `pedido:20261031-101500-deploy`\n"
		}
		kv := []string{"oute.swarm.step.kind", kind, "oute.swarm.step.rev", rev, "oute.swarm.step.sha256", stepSHA,
			"oute.swarm.step.review", review, "oute.swarm.step.refcheck", "ok", "oute.swarm.cycle", "renatobardi/hanko#126"}
		if skey != "" {
			kv = append(kv, "oute.swarm.step.key", skey)
		}
		return swarmLogBody(id, "oute.swarm.step.published", body, at, kv...)
	}
	send := func(recs ...string) {
		t.Helper()
		doc, err := ingest.DecodeJSON([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + strings.Join(recs, ",") + `]}]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := ingest.LogRows(doc, time.Now().UnixNano()+1_000_000)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: rows}); err != nil {
			t.Fatal(err)
		}
	}
	// a revisão 2 chega antes da 1; o fechamento antes da abertura
	batch := []string{
		step("s-triage-2", base+10, "triagem", "2", "", "aprovado"),
		swarmLog("r-closed", "oute.swarm.round.closed", base+100),
		swarmLog("r-opened", "oute.swarm.round.opened", base+1, "oute.swarm.repo", "renatobardi/hanko", "oute.swarm.max", "4", "oute.swarm.round.name", "Brave_Otter"),
		swarmLog("w-spawn", "oute.swarm.session.spawned", base+2, "oute.swarm.session", "w1", "oute.swarm.issue", "renatobardi/hanko#126"),
		step("s-triage-1", base+5, "triagem", "1", "", "reprovado"),
		step("s-merge-1", base+20, "merge", "1", "42", "aprovado"),
		step("s-cycle-1", base+30, "ciclo", "1", "", "aprovado"),
		step("s-invalid", base+40, "foo", "1", "", "aprovado"),
	}
	send(batch...)
	send(batch...) // reenvio: nada muda

	var name, repo string
	var maxSess *int
	var opened, closed *int64
	var cycle *string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT r.name, r.repo, r.max_sessions, r.opened_at, r.closed_at, c.issue_ref
		FROM external_round r LEFT JOIN round_cycle c ON c.id = r.cycle_id WHERE r.company_id = $1 AND r.round_key = 'swarm-1'`, e.co.ID).
		Scan(&name, &repo, &maxSess, &opened, &closed, &cycle); err != nil {
		t.Fatal(err)
	}
	if name != "Brave_Otter" || repo != "renatobardi/hanko" || maxSess == nil || *maxSess != 4 || opened == nil || *opened != base+1 || closed == nil || *closed != base+100 {
		t.Errorf("rodada: %q %q max=%v aberta=%v fechada=%v", name, repo, maxSess, opened, closed)
	}
	if cycle == nil || *cycle != "renatobardi/hanko#126" {
		t.Errorf("ciclo da rodada = %v, quer renatobardi/hanko#126", cycle)
	}
	var steps, triageRev, cycleSteps, participants int
	var triageReview string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM round_step WHERE company_id = $1`, e.co.ID).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Pool.QueryRow(ctx, `SELECT revision, review FROM round_step WHERE company_id = $1 AND kind = 'triage'`, e.co.ID).Scan(&triageRev, &triageReview); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM round_step WHERE company_id = $1 AND kind = 'cycle_summary' AND cycle_id IS NOT NULL AND round_id IS NULL`, e.co.ID).Scan(&cycleSteps); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM round_participant WHERE company_id = $1 AND slug = 'w1' AND issue_ref = 'renatobardi/hanko#126' AND spawned_at = $2`, e.co.ID, base+2).Scan(&participants); err != nil {
		t.Fatal(err)
	}
	if steps != 3 || triageRev != 2 || triageReview != "approved" || cycleSteps != 1 || participants != 1 {
		t.Errorf("etapas=%d (quer 3: triagem, merge e resumo), triagem rev=%d %s (quer 2 approved), resumo do ciclo=%d, participante=%d", steps, triageRev, triageReview, cycleSteps, participants)
	}
	var actions, withApproval int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE approval_key = '20261031-101500-deploy') FROM step_action WHERE company_id = $1`, e.co.ID).Scan(&actions, &withApproval); err != nil {
		t.Fatal(err)
	}
	if actions != 2 || withApproval != 1 {
		t.Errorf("ações da revisão vigente = %d (quer 2), com pedido = %d (quer 1); a revisão 1 chegou depois e não vale", actions, withApproval)
	}
	var rounds int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM external_round WHERE company_id = $1`, e.co.ID).Scan(&rounds); err != nil || rounds != 1 {
		t.Errorf("rodadas = %d (%v), quer 1", rounds, err)
	}
}
