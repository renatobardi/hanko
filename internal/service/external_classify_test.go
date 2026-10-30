package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// convFor cria uma conversa externa na empresa dada, com o session_id dado, e devolve o id dela.
func convFor(t *testing.T, e *env, company domain.ID, session, name string) domain.ID {
	t.Helper()
	var id domain.ID
	if err := e.svc.Pool.QueryRow(context.Background(), `INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ($1, $2, $3, $4) RETURNING id`,
		domain.NewID(), company, session, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// convFixture cria uma conversa na empresa de teste.
func convFixture(t *testing.T, e *env, session, name string) domain.ID {
	t.Helper()
	return convFor(t, e, e.co.ID, session, name)
}

// spanIn grava um span `claude_code.tool` da empresa dada, com os atributos e os atributos de recurso dados.
func spanIn(t *testing.T, e *env, company domain.ID, session, task string, attrs, res map[string]any) {
	t.Helper()
	raw, err := json.Marshal(attrs)
	if err != nil {
		t.Fatal(err)
	}
	rawRes, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO telemetry_spans (company_id, dedupe_key, time_unix_nano, trace_id, span_id, name, session_id,
		oute_task_id, attributes, resource_attributes, received_unix_nano) VALUES ($1, $2, $3, 'tr', $2, 'claude_code.tool', $4, NULLIF($5, ''), $6::jsonb, $7::jsonb, $3)`,
		company, domain.NewID().String(), time.Now().UnixNano(), session, task, string(raw), string(rawRes)); err != nil {
		t.Fatal(err)
	}
}

// spanTool grava um span de ferramenta na empresa de teste.
func spanTool(t *testing.T, e *env, session, task string, attrs map[string]any) {
	t.Helper()
	spanIn(t, e, e.co.ID, session, task, attrs, nil)
}

// openLog grava o evento de abertura do oute-task de uma sessão.
func openLog(t *testing.T, e *env, session, task, phase, origin, repo, slug string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"oute.task.phase": phase, "oute.task.origin": origin, "oute.task.repo": repo, "oute.task.slug": slug})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO telemetry_logs (company_id, dedupe_key, time_unix_nano, received_unix_nano, event_name, session_id, oute_task_id, attributes)
		VALUES ($1, $2, $3, $3, 'oute.task.opened', $4, $5, $6::jsonb)`, e.co.ID, domain.NewID().String(), time.Now().UnixNano(), session, task, string(raw)); err != nil {
		t.Fatal(err)
	}
}

// classOf lê a classe gravada de uma conversa.
func classOf(t *testing.T, e *env, conv domain.ID) (class, origin, conf, classifier string, tags []string) {
	t.Helper()
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT class, class_origin, class_confidence, classifier, tags FROM external_conversation WHERE id = $1`,
		conv).Scan(&class, &origin, &conf, &classifier, &tags); err != nil {
		t.Fatal(err)
	}
	return
}

// passada roda a classificação e devolve quantas conversas mudaram.
func passada(t *testing.T, e *env) int {
	t.Helper()
	n, err := e.svc.ClassifyExternalConversations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Ação forte grava a classe, a origem `action` e a confiança alta; uma segunda passada não muda nada nem gera evento.
func TestClassifyStrongActionIsIdempotent(t *testing.T) {
	e := newEnv(t)
	conv := convFixture(t, e, "s-acao", "Alfa_Beta")
	for i := 0; i < 4; i++ {
		spanTool(t, e, "s-acao", "", map[string]any{"tool_name": "Bash", "full_command": "gh pr review 9"})
	}
	if n := passada(t, e); n != 1 {
		t.Fatalf("primeira passada = %d; quero 1 conversa mudada", n)
	}
	class, origin, conf, classifier, tags := classOf(t, e, conv)
	if class != "qa" || origin != "action" || conf != "high" || classifier != "aidlc" || len(tags) != 0 {
		t.Errorf("classe = %q, %q, %q, %q, %v; quero qa, action, high, aidlc, sem marca", class, origin, conf, classifier, tags)
	}
	if n := passada(t, e); n != 0 {
		t.Errorf("segunda passada = %d; quero 0", n)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'conversation.classified'`, e.co.ID); n != 1 {
		t.Errorf("eventos conversation.classified = %d, quero 1", n)
	}
}

// Cada regra de ação da tabela do phase.py decide a conversa com a sua fase (origem action, confiança alta).
func TestClassifyEachActionRule(t *testing.T) {
	cases := []struct {
		name  string
		phase string
		attrs []map[string]any
	}{
		{"abriu PR", "build", rep(4, map[string]any{"tool_name": "Bash", "full_command": "gh pr merge 3"})},
		{"commit", "build", rep(4, map[string]any{"tool_name": "Bash", "full_command": "git commit -m x"})},
		{"editou ADR", "arch", rep(2, map[string]any{"tool_name": "Edit", "file_path": "/repo/docs/adr/0099.md"})},
		{"auditou PR pelo oute-sonar", "qa", rep(2, map[string]any{"tool_name": "Bash", "full_command": "oute-sonar pr 9 --json"})},
		{"release", "ship", rep(2, map[string]any{"tool_name": "Bash", "full_command": "gh release create v1"})},
		{"telemetria", "ops", rep(2, map[string]any{"tool_name": "Bash", "full_command": "journalctl -u hanko"})},
		{"criou issue", "spec", rep(2, map[string]any{"tool_name": "Bash", "full_command": "gh issue create --title x"})},
		{"pesquisou", "strat", rep(4, map[string]any{"tool_name": "WebSearch"})},
	}
	nomes := []string{"Regra_Abriu", "Regra_Commit", "Regra_Adr", "Regra_Auditou", "Regra_Release", "Regra_Telemetria", "Regra_Issue", "Regra_Pesquisa"}
	for i, c := range cases {
		e := newEnv(t)
		session := "s-regra-" + nomes[i]
		conv := convFixture(t, e, session, nomes[i])
		for _, a := range c.attrs {
			spanTool(t, e, session, "", a)
		}
		passada(t, e)
		class, origin, conf, _, _ := classOf(t, e, conv)
		if class != c.phase || origin != "action" || conf != "high" {
			t.Errorf("%s: classe = %q, %q, %q; quero %q, action, high", c.name, class, origin, conf, c.phase)
		}
	}
}

// rep repete um atributo de ferramenta n vezes.
func rep(n int, attrs map[string]any) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = attrs
	}
	return out
}

// Outra empresa: a passada de uma empresa não lê nem grava a telemetria da outra, ainda que o session_id seja igual.
func TestClassifyOtherCompanyStaysApart(t *testing.T) {
	e := newEnv(t)
	outra, err := e.svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	minha := convFixture(t, e, "s-mesmo", "Mu_Nu")
	dela := convFor(t, e, outra.ID, "s-mesmo", "Xi_Omicron")
	for i := 0; i < 4; i++ {
		spanIn(t, e, outra.ID, "s-mesmo", "", map[string]any{"tool_name": "Bash", "full_command": "gh pr review 1"}, nil)
	}
	passada(t, e)
	if class, origin, _, _, _ := classOf(t, e, dela); class != "qa" || origin != "action" {
		t.Errorf("conversa da outra empresa = %q, %q; quero qa, action", class, origin)
	}
	if class, origin, conf, _, _ := classOf(t, e, minha); class != "build" || origin != "action" || conf != "low" {
		t.Errorf("conversa desta empresa = %q, %q, %q; quero o padrão build, baixa (sem telemetria aqui)", class, origin, conf)
	}
}

// Reclassificação pelo recebimento: um lote novo da sessão (updated_at) reabre a conversa, mesmo com relógio do fato
// atrasado (last_at não sobe); sem lote novo ela não é relida. Simula o recebimento direto no banco (ver #125, Falta).
func TestClassifyRereadsOnlyWhenTheSessionReceivesABatch(t *testing.T) {
	e := newEnv(t)
	conv := convFixture(t, e, "s-novo", "Pi_Rho")
	var sess domain.ID
	if err := e.svc.Pool.QueryRow(context.Background(), `INSERT INTO external_session (id, company_id, task_id, last_at, updated_at)
		VALUES ($1, $2, 'task-novo', 100, now() - interval '1 hour') RETURNING id`, domain.NewID(), e.co.ID).Scan(&sess); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(context.Background(), `UPDATE external_conversation SET external_session_id = $1 WHERE id = $2`, sess, conv); err != nil {
		t.Fatal(err)
	}
	if n := passada(t, e); n != 1 {
		t.Fatalf("primeira passada = %d; quero 1", n)
	}
	for i := 0; i < 4; i++ {
		spanTool(t, e, "s-novo", "", map[string]any{"tool_name": "Bash", "full_command": "gh pr review 2"})
	}
	if n := passada(t, e); n != 0 {
		t.Fatalf("sem lote novo, a passada releu a conversa: %d; quero 0", n)
	}
	// o fato chega com relógio atrasado (last_at continua 100), mas o lote entra agora: updated_at avança
	if _, err := e.svc.Pool.Exec(context.Background(), `UPDATE external_session SET updated_at = now() WHERE id = $1`, sess); err != nil {
		t.Fatal(err)
	}
	if n := passada(t, e); n != 1 {
		t.Fatalf("com lote novo = %d mudadas; quero 1", n)
	}
	if class, _, _, _, _ := classOf(t, e, conv); class != "qa" {
		t.Errorf("classe depois do lote novo = %q; quero qa", class)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_conversation c JOIN external_session s ON s.id = c.external_session_id
		WHERE c.id = $1 AND c.classified_session_at = s.updated_at`, conv); n != 1 {
		t.Errorf("janela da conversa não acompanhou o recebimento da sessão")
	}
}

// Conversa sem nenhum fato: cai no padrão build, com confiança baixa, e não volta a ser relida.
func TestClassifyConversationWithoutFacts(t *testing.T) {
	e := newEnv(t)
	conv := convFixture(t, e, "s-sem-fato", "Sigma_Tau")
	if n := passada(t, e); n != 1 {
		t.Fatalf("primeira passada = %d; quero 1", n)
	}
	if class, origin, conf, _, _ := classOf(t, e, conv); class != "build" || origin != "action" || conf != "low" {
		t.Errorf("sem fato = %q, %q, %q; quero build, action, low", class, origin, conf)
	}
	if n := passada(t, e); n != 0 {
		t.Errorf("segunda passada = %d; quero 0", n)
	}
}

// Papel pelo resource_attributes: rodada sem worker nem passo é dispatcher (plan); com passo é revisor (qa).
func TestClassifyRoleFromResourceAttributes(t *testing.T) {
	e := newEnv(t)
	disp := convFixture(t, e, "s-disp", "Upsilon_Phi")
	rev := convFixture(t, e, "s-rev", "Chi_Psi")
	spanIn(t, e, e.co.ID, "s-disp", "", map[string]any{"tool_name": "Read"}, map[string]any{"oute.swarm.round": "r1"})
	spanIn(t, e, e.co.ID, "s-rev", "", map[string]any{"tool_name": "Read"}, map[string]any{"oute.swarm.round": "r1", "oute.swarm.step": "s1"})
	passada(t, e)
	if class, origin, conf, _, _ := classOf(t, e, disp); class != "plan" || origin != "role" || conf != "high" {
		t.Errorf("dispatcher = %q, %q, %q; quero plan, role, high", class, origin, conf)
	}
	if class, origin, conf, _, _ := classOf(t, e, rev); class != "qa" || origin != "role" || conf != "high" {
		t.Errorf("revisor = %q, %q, %q; quero qa, role, high", class, origin, conf)
	}
}

// Label entre sessões: a sessão sem fase na abertura herda a fase de label da mesma issue, aberta em outra sessão.
func TestClassifyLabelAcrossSessionsOfTheSameIssue(t *testing.T) {
	e := newEnv(t)
	openLog(t, e, "s-label-a", "task-a", "spec", "label", "oute", "12-x")
	conv := convFixture(t, e, "s-label-b", "Omega_Alfa")
	openLog(t, e, "s-label-b", "task-b", "", "opening", "oute", "12-y")
	passada(t, e)
	if class, origin, conf, _, _ := classOf(t, e, conv); class != "spec" || origin != "label" || conf != "high" {
		t.Errorf("label entre sessões = %q, %q, %q; quero spec, label, high", class, origin, conf)
	}
}

// O comando com segredo é só testado: o valor não entra no evento nem na classe.
func TestClassifyDoesNotStoreSecretFromCommand(t *testing.T) {
	e := newEnv(t)
	const valor = "abcdefghijklmnopqrstuvwxyz"
	conv := convFixture(t, e, "s-segredo", "Psi_Omega")
	for i := 0; i < 4; i++ {
		spanTool(t, e, "s-segredo", "", map[string]any{"tool_name": "Bash", "full_command": "curl -H 'Authorization: Bearer " + valor + "' && gh pr review 1"})
	}
	passada(t, e)
	if class, origin, _, _, _ := classOf(t, e, conv); class != "qa" || origin != "action" {
		t.Fatalf("classe = %q, %q; quero qa, action", class, origin)
	}
	var evento string
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(data::text, ' '), '') FROM activity_events WHERE company_id = $1`, e.co.ID).Scan(&evento); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(evento, valor) {
		t.Errorf("o valor do segredo apareceu num evento de atividade")
	}
}

// Troca manual vale sobre a telemetria; uma troca nova depois da classificação é aplicada na passada seguinte.
func TestClassifyManualMarkWinsOverTelemetry(t *testing.T) {
	e := newEnv(t)
	conv := convFixture(t, e, "s-manual", "Gama_Delta")
	for i := 0; i < 4; i++ {
		spanTool(t, e, "s-manual", "", map[string]any{"tool_name": "Bash", "full_command": "gh pr review 9"})
	}
	passada(t, e)
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO conversation_class_input (id, company_id, conversation_id, kind, class, confidence, actor_type)
		VALUES ($1, $2, $3, 'manual', 'ship', 'high', 'human')`, domain.NewID(), e.co.ID, conv); err != nil {
		t.Fatal(err)
	}
	if n := passada(t, e); n != 1 {
		t.Fatalf("passada com troca manual = %d mudadas; quero 1", n)
	}
	class, origin, conf, _, _ := classOf(t, e, conv)
	if class != "ship" || origin != "manual" || conf != "high" {
		t.Errorf("com troca manual = %q, %q, %q; quero ship, manual, high", class, origin, conf)
	}
}

// Abertura do oute-task vale; a marca `regression` sai do oute.task.id da sessão.
func TestClassifyOpeningAndRegressionTag(t *testing.T) {
	e := newEnv(t)
	conv := convFixture(t, e, "s-abre", "Eta_Theta")
	openLog(t, e, "s-abre", "regression-7", "ops", "opening", "oute", "7-x")
	spanTool(t, e, "s-abre", "regression-7", map[string]any{"tool_name": "Read"})
	passada(t, e)
	class, origin, conf, _, tags := classOf(t, e, conv)
	if class != "ops" || origin != "opening" || conf != "high" {
		t.Errorf("abertura = %q, %q, %q; quero ops, opening, high", class, origin, conf)
	}
	if len(tags) != 1 || tags[0] != "regression" {
		t.Errorf("marcas = %v; quero [regression]", tags)
	}
}

// Skill de fase no tool_parameters (texto JSON) decide; a conversa sem sinal cai no padrão build.
func TestClassifySkillAndDefault(t *testing.T) {
	e := newEnv(t)
	skill := convFixture(t, e, "s-skill", "Iota_Kappa")
	none := convFixture(t, e, "s-vazia", "Lambda_Mu")
	spanTool(t, e, "s-skill", "", map[string]any{"tool_name": "Skill", "tool_parameters": `{"skill_name":"oute-aidlc-spec-criar"}`})
	spanTool(t, e, "s-vazia", "", map[string]any{"tool_name": "Read", "file_path": "/repo/main.go"})
	passada(t, e)
	if class, origin, conf, _, _ := classOf(t, e, skill); class != "spec" || origin != "skill" || conf != "high" {
		t.Errorf("skill = %q, %q, %q; quero spec, skill, high", class, origin, conf)
	}
	if class, origin, conf, _, _ := classOf(t, e, none); class != "build" || origin != "action" || conf != "low" {
		t.Errorf("sem sinal = %q, %q, %q; quero build, action, low", class, origin, conf)
	}
}
