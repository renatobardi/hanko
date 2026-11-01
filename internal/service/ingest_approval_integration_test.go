package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
)

const (
	canalA = "20261031-101500-deploy"
	canalB = "20261031-101600-limpeza"
)

func canalLog(id, event, body string, at int64, kv ...string) string {
	attrs := []string{fmt.Sprintf(`{"key":"oute.event.id","value":{"stringValue":%q}}`, id),
		`{"key":"host.name","value":{"stringValue":"oute-server"}}`, `{"key":"oute.agent","value":{"stringValue":"claude"}}`}
	for i := 0; i < len(kv); i += 2 {
		attrs = append(attrs, fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, kv[i], kv[i+1]))
	}
	return fmt.Sprintf(`{"timeUnixNano":"%d","eventName":%q,"body":{"stringValue":%q},"attributes":[%s]}`, at, event, body, strings.Join(attrs, ","))
}

// O pedido do canal nasce dos eventos `oute.canal.*`: o `proposed` mais antigo vale, versões diferentes contam, a decisão vale em
// qualquer ordem, a lista mais nova da outbox tira o pendente que ela não tem, e só owner e admin leem o script (ADR-0081).
func TestWriteTelemetryDerivesApprovals(t *testing.T) {
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
	base := time.Now().Add(-10 * time.Minute).UnixNano()
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
	prop := func(id, canal, body string, at int64) string {
		return canalLog(id, "oute.canal.proposed", body, at, "oute.canal.id", canal, "oute.canal.title", "Deploy", "oute.canal.as", "oute")
	}
	// A: a versão forjada (script diferente) chega antes da verdadeira no lote, mas a de hora mais antiga vale; a decisão chega antes dos dois
	send(
		canalLog("d-a", "oute.canal.decided", "", base+500, "oute.canal.id", canalA, "oute.canal.decision", "executado", "oute.canal.approver", "human-dark",
			"oute.canal.rc", "0", "oute.canal.duration_s", "12", "oute.canal.output_bytes", "300"),
		prop("p-a-fake", canalA, "rm -rf /", base+300),
		prop("p-a", canalA, "echo oi\n", base+100),
		prop("p-b", canalB, "echo limpeza\n", base+200),
	)
	send(prop("p-a", canalA, "echo oi\n", base+100)) // reenvio

	list, err := e.svc.ListExternalApprovals(ctx, e.co.ID, "", 50, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListExternalApprovals = %d (%v), quer 2", len(list), err)
	}
	var a, b *domainApproval
	for i := range list {
		v := domainApproval{id: list[i].ID, canal: list[i].CanalID, state: list[i].State, versions: list[i].Versions, title: list[i].Title}
		if v.decision = list[i].Decision; v.decision != nil {
			v.approver = *list[i].Approver
		}
		switch v.canal {
		case canalA:
			a = &v
		case canalB:
			b = &v
		}
	}
	if a == nil || b == nil {
		t.Fatalf("pedidos = %+v", list)
	}
	if a.state != "decided" || a.decision == nil || *a.decision != "executed" || a.approver != "human_dark" || a.versions != 2 || a.title != "Deploy" {
		t.Errorf("pedido A = %+v, quer decidido (executed, human_dark) com 2 versões", a)
	}
	if b.state != "pending" || b.versions != 1 {
		t.Errorf("pedido B = %+v, quer pendente com 1 versão", b)
	}
	pending, _ := e.svc.ListExternalApprovals(ctx, e.co.ID, "pending", 50, 0)
	if len(pending) != 1 || pending[0].CanalID != canalB {
		t.Errorf("pendentes = %+v, quer só o B", pending)
	}

	// o script é o da versão mais antiga e a leitura gera activity_event
	sc, err := e.svc.ApprovalScript(ctx, actor, e.co.ID, a.id)
	if err != nil || sc.Script != "echo oi\n" || len(sc.Versions) != 2 {
		t.Errorf("ApprovalScript = %+v (%v), quer o script da versão mais antiga e 2 versões", sc, err)
	}
	var reads int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'external_approval.script_read'`, e.co.ID).Scan(&reads); err != nil || reads != 1 {
		t.Errorf("leituras do script no activity_event = %d (%v), quer 1", reads, err)
	}

	// lista da outbox: tirada mais de 60 s depois do pedido e sem o B, ele sai das listas (mas não é apagado)
	ids := fmt.Sprintf(`["%s"]`, canalA)
	send(canalLog("snap-1", "oute.canal.snapshot", "", base+int64(5*time.Minute), "oute.canal.ids", ids))
	if pending, _ = e.svc.ListExternalApprovals(ctx, e.co.ID, "pending", 50, 0); len(pending) != 0 {
		t.Errorf("pendente fora da lista da outbox continua na lista: %+v", pending)
	}
	if v, err := e.svc.GetExternalApproval(ctx, e.co.ID, b.id); err != nil || v.State != "pending" {
		t.Errorf("o pedido que saiu das listas segue por id: %+v (%v)", v, err)
	}
	// lista forjada (hora à frente do servidor) é recusada: a anterior segue valendo
	send(canalLog("snap-2", "oute.canal.snapshot", "", time.Now().Add(time.Hour).UnixNano(), "oute.canal.ids", fmt.Sprintf(`["%s","%s"]`, canalA, canalB)))
	if pending, _ = e.svc.ListExternalApprovals(ctx, e.co.ID, "pending", 50, 0); len(pending) != 0 {
		t.Errorf("lista forjada trocou a lista da outbox: %+v", pending)
	}
	if _, err := e.svc.ApprovalScript(ctx, actor, e.co.ID, domain.NewID()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("pedido inexistente: %v, quer ErrNotFound", err)
	}
}

type domainApproval struct {
	id       domain.ID
	canal    string
	state    string
	decision *string
	approver string
	versions int
	title    string
}
