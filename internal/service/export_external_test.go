package service_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// O export leva só a configuração externa (agentes, hosts, emparelhamentos, chave externa e histórico de mensalidade das
// assinaturas, limites de alerta e o workspace `no-repo`), nunca o que a ingestão guardou nem chave; o round trip export →
// import → export dá a mesma configuração (ADR-0088).
func TestExportImportExternalConfig(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	root := t.TempDir()
	e.svc.WorkspacesRoot = root
	ws := e.workspace(t, "Hanko", func(in *service.CreateWorkspaceInput) { in.Path = root + "/" + e.co.Slug + "/hanko" })
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.svc.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	agent, host, sys := domain.NewID(), domain.NewID(), domain.NewID()
	exec(`INSERT INTO external_agent (id, company_id, name) VALUES ($1, $2, 'claude')`, agent, e.co.ID)
	exec(`INSERT INTO external_agent_alias (company_id, alias, agent_id) VALUES ($1, 'Claude Code', $2), ($1, 'claude-code', $2)`, e.co.ID, agent)
	exec(`INSERT INTO external_host (id, company_id, name, always_on) VALUES ($1, $2, 'oute-server', true)`, host, e.co.ID)
	exec(`INSERT INTO external_host_alias (company_id, alias, host_id) VALUES ($1, 'oute-server.local', $2)`, e.co.ID, host)
	exec(`INSERT INTO workspaces (id, company_id, slug, name, path, is_system) VALUES ($1, $2, 'no-repo', 'Sem repositório', '', true)`, sys, e.co.ID)
	exec(`INSERT INTO repo_pairing (id, company_id, repo_key, key_kind, workspace_id) VALUES ($1, $2, 'renatobardi/hanko', 'name', $3), ($4, $2, 'https://github.com/o/x', 'url', $3)`,
		domain.NewID(), e.co.ID, ws.ID, domain.NewID())
	// dados que NÃO viajam: conversa, sessão, rodada, chamada e alerta
	exec(`INSERT INTO external_session (id, company_id, task_id) VALUES ($1, $2, 'task-1')`, domain.NewID(), e.co.ID)
	exec(`INSERT INTO external_round (id, company_id, round_key, name) VALUES ($1, $2, 'swarm-1', 'Brave_Otter')`, domain.NewID(), e.co.ID)

	sub := e.sub(t, func(in *service.CreateSubscriptionInput) {
		in.Name = "Claude"
		in.Agent = "claude"
		in.MonthlyUSD = 300
		in.ExternalKey = "claude"
	})
	if _, err := e.svc.AddSubscriptionMonthlyValue(ctx, domain.HumanActor, e.co.ID, sub.ID.String(), 400, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.queue_max_ratio", 0.8); err != nil {
		t.Fatal(err)
	}

	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.ExternalAgents) != 1 || len(doc.ExternalAgents[0].Aliases) != 2 || len(doc.ExternalHosts) != 1 || !doc.ExternalHosts[0].AlwaysOn ||
		len(doc.RepoPairings) != 2 || len(doc.AlertSettings) != 1 {
		t.Fatalf("configuração externa = %+v %+v %+v %+v", doc.ExternalAgents, doc.ExternalHosts, doc.RepoPairings, doc.AlertSettings)
	}
	if len(doc.Subscriptions) != 1 || doc.Subscriptions[0].ExternalKey != "claude" || len(doc.Subscriptions[0].MonthlyHistory) != 2 {
		t.Fatalf("assinatura = %+v", doc.Subscriptions)
	}
	var noRepo bool
	for _, w := range doc.Workspaces {
		noRepo = noRepo || (w.Slug == "no-repo" && w.System)
	}
	if !noRepo {
		t.Error("o workspace no-repo devia viajar, marcado como de sistema")
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"task-1", "swarm-1", "Brave_Otter", "api_key", "ingest", "token"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("o arquivo não devia conter %q", banned)
		}
	}

	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "copia"})
	if err != nil || res.Counts["external_agents"] != 1 || res.Counts["repo_pairings"] != 2 {
		t.Fatalf("import = %+v (%v)", res.Counts, err)
	}
	back, err := e.svc.ExportCompany(ctx, res.Company.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.ExternalAgents, back.ExternalAgents) || !reflect.DeepEqual(doc.ExternalHosts, back.ExternalHosts) ||
		!reflect.DeepEqual(doc.RepoPairings, back.RepoPairings) || !reflect.DeepEqual(doc.AlertSettings, back.AlertSettings) {
		t.Errorf("round trip mudou a configuração externa:\n%+v\n%+v", doc, back)
	}
	if !reflect.DeepEqual(doc.Subscriptions[0].MonthlyHistory, back.Subscriptions[0].MonthlyHistory) || back.Subscriptions[0].ExternalKey != "claude" {
		t.Errorf("round trip mudou o histórico ou a chave da assinatura: %+v", back.Subscriptions)
	}
	var hasData int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM external_session WHERE company_id = $1) + (SELECT count(*) FROM external_round WHERE company_id = $1)`, res.Company.ID).Scan(&hasData); err != nil || hasData != 0 {
		t.Errorf("a empresa importada começa sem histórico externo: %d (%v)", hasData, err)
	}

	// o arquivo com configuração externa inválida é recusado antes de gravar
	bad := doc
	bad.RepoPairings = append([]export.RepoPairing{}, doc.RepoPairings...)
	bad.RepoPairings[0].Workspace = "inexistente"
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, bad, service.ImportOptions{Slug: "ruim"}); err == nil {
		t.Error("emparelhamento com workspace desconhecido deveria ser recusado")
	}
	bad2 := doc
	bad2.AlertSettings = []export.AlertSetting{{Key: "limit.nao_existe", Value: 1}}
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, bad2, service.ImportOptions{Slug: "ruim2"}); err == nil {
		t.Error("limite de alerta desconhecido deveria ser recusado")
	}
}
