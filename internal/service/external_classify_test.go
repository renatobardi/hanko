package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// convFixture cria uma conversa externa com o session_id dado e devolve o id dela.
func convFixture(t *testing.T, e *env, session, name string) domain.ID {
	t.Helper()
	var id domain.ID
	if err := e.svc.Pool.QueryRow(context.Background(), `INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ($1, $2, $3, $4) RETURNING id`,
		domain.NewID(), e.co.ID, session, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// spanTool põe um span `claude_code.tool` com os atributos dados (vazio = ausente) e a tarefa oute.task.id dada.
func spanTool(t *testing.T, e *env, session, task string, attrs map[string]any) {
	t.Helper()
	raw, err := json.Marshal(attrs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO telemetry_spans (company_id, dedupe_key, time_unix_nano, trace_id, span_id, name, session_id,
		oute_task_id, attributes, received_unix_nano) VALUES ($1, $2, $3, 'tr', $2, 'claude_code.tool', $4, NULLIF($5, ''), $6::jsonb, $3)`,
		e.co.ID, domain.NewID().String(), time.Now().UnixNano(), session, task, string(raw)); err != nil {
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

// Ação forte grava a classe, a origem `action` e a confiança alta; uma segunda passada não muda nada nem gera evento.
func TestClassifyStrongActionIsIdempotent(t *testing.T) {
	e := newEnv(t)
	conv := convFixture(t, e, "s-acao", "Alfa_Beta")
	for i := 0; i < 4; i++ {
		spanTool(t, e, "s-acao", "", map[string]any{"tool_name": "Bash", "full_command": "gh pr review 9"})
	}
	if n, err := e.svc.ClassifyExternalConversations(context.Background()); err != nil || n != 1 {
		t.Fatalf("primeira passada = %d, %v; quero 1 conversa mudada", n, err)
	}
	class, origin, conf, classifier, tags := classOf(t, e, conv)
	if class != "qa" || origin != "action" || conf != "high" || classifier != "aidlc" || len(tags) != 0 {
		t.Errorf("classe = %q, %q, %q, %q, %v; quero qa, action, high, aidlc, sem marca", class, origin, conf, classifier, tags)
	}
	if n, err := e.svc.ClassifyExternalConversations(context.Background()); err != nil || n != 0 {
		t.Errorf("segunda passada = %d, %v; quero 0", n, err)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'conversation.classified'`, e.co.ID); n != 1 {
		t.Errorf("eventos conversation.classified = %d, quero 1", n)
	}
}

// A troca manual vale sobre a telemetria: a conversa fica com a classe marcada pelo humano.
func TestClassifyManualMarkWinsOverTelemetry(t *testing.T) {
	e := newEnv(t)
	conv := convFixture(t, e, "s-manual", "Gama_Delta")
	for i := 0; i < 4; i++ {
		spanTool(t, e, "s-manual", "", map[string]any{"tool_name": "Bash", "full_command": "gh pr review 9"})
	}
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO conversation_class_input (id, company_id, conversation_id, kind, class, confidence, actor_type)
		VALUES ($1, $2, $3, 'manual', 'ship', 'high', 'human')`, domain.NewID(), e.co.ID, conv); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ClassifyExternalConversations(context.Background()); err != nil {
		t.Fatal(err)
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
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO telemetry_logs (company_id, dedupe_key, time_unix_nano, received_unix_nano, event_name, session_id, oute_task_id, attributes)
		VALUES ($1, 'ab1', $2, $2, 'oute.task.opened', 's-abre', 'regression-7', $3::jsonb)`, e.co.ID, time.Now().UnixNano(),
		`{"oute.task.phase":"ops","oute.task.origin":"opening","oute.task.repo":"oute","oute.task.slug":"7-x"}`); err != nil {
		t.Fatal(err)
	}
	spanTool(t, e, "s-abre", "regression-7", map[string]any{"tool_name": "Read"})
	if _, err := e.svc.ClassifyExternalConversations(context.Background()); err != nil {
		t.Fatal(err)
	}
	class, origin, conf, _, tags := classOf(t, e, conv)
	if class != "ops" || origin != "opening" || conf != "high" {
		t.Errorf("abertura = %q, %q, %q; quero ops, opening, high", class, origin, conf)
	}
	if len(tags) != 1 || tags[0] != "regression" {
		t.Errorf("marcas = %v; quero [regression]", tags)
	}
}

// Skill de fase no tool_parameters (texto JSON) decide; conversa sem nenhum sinal cai no padrão build, com confiança baixa.
func TestClassifySkillAndDefault(t *testing.T) {
	e := newEnv(t)
	skill := convFixture(t, e, "s-skill", "Iota_Kappa")
	none := convFixture(t, e, "s-vazia", "Lambda_Mu")
	spanTool(t, e, "s-skill", "", map[string]any{"tool_name": "Skill", "tool_parameters": `{"skill_name":"oute-aidlc-spec-criar"}`})
	spanTool(t, e, "s-vazia", "", map[string]any{"tool_name": "Read", "file_path": "/repo/main.go"})
	if _, err := e.svc.ClassifyExternalConversations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if class, origin, conf, _, _ := classOf(t, e, skill); class != "spec" || origin != "skill" || conf != "high" {
		t.Errorf("skill = %q, %q, %q; quero spec, skill, high", class, origin, conf)
	}
	if class, origin, conf, _, _ := classOf(t, e, none); class != "build" || origin != "action" || conf != "low" {
		t.Errorf("sem sinal = %q, %q, %q; quero build, action, low", class, origin, conf)
	}
}
