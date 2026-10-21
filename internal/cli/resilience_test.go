package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

const resilRun = "0198f2a0-0000-7000-8000-0000000000bb"

func resilAPI(t *testing.T) (map[string]string, *fakeAPI) {
	t.Helper()
	stopped := api.Run{ID: resilRun, Name: "Hábil_Corrente", Status: "interrupted", Agent: "claude", TaskKey: "HNK-1", PersonaName: "Marina",
		SteerMode: "inject", DecisionPending: true, ContinuationOfRunID: "0198f2a0-0000-7000-8000-0000000000aa", ContinuationAttempt: 2, ContinuationReason: "recovery",
		CreatedAt: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC),
		Diagnosis: &api.RunDiagnosis{Cause: "process_restart", CauseDetail: "o processo do Hanko foi reiniciado", Attempt: 2, MaxAttempts: 2, NextAction: "human_decision", Owner: "human", Exhausted: true,
			Uncertain: &api.UncertainTool{Name: "Bash", Input: "git push origin main", Seq: 9}}}
	fu := api.Followup{ID: "0198f2a0-0000-7000-8000-0000000000f1", Kind: "task", Body: "depois, atualize o changelog", Delivery: "queue", Position: 1, WaitReason: "run_active", WaitDetail: "Hábil_Corrente"}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/runs/" + resilRun: jsonResp(200, stopped),
		"POST /api/v1/companies/acme/runs/" + resilRun + "/actions": func(w http.ResponseWriter, r *http.Request) {
			out := stopped // the request body was already recorded by the fake server; the answer is a fixed decision
			out.Decision, out.DecisionNote, out.DecisionPending = "continue", "conferi o remoto", false
			jsonResp(200, out)(w, r)
		},
		"POST /api/v1/companies/acme/runs/" + resilRun + "/steer":                                 jsonResp(201, api.SteerResult{Mode: "inject", Followup: api.Followup{ID: fu.ID, Delivery: "steer"}}),
		"GET /api/v1/companies/acme/tasks/HNK-1/followups":                                        jsonResp(200, list(fu)),
		"POST /api/v1/companies/acme/tasks/HNK-1/followups":                                       jsonResp(201, fu),
		"PATCH /api/v1/companies/acme/followups/" + fu.ID:                                         jsonResp(200, fu),
		"DELETE /api/v1/companies/acme/followups/" + fu.ID:                                        func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) },
		"GET /api/v1/companies/acme/conversations/0198f2a0-0000-7000-8000-0000000000c1/followups": jsonResp(200, list(api.Followup{ID: "x", Kind: "chat", Body: "e o plano B?", Position: 1, WaitReason: "starting"})),
	})
	return map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}, f
}

func TestRunShowPrintsTheDiagnosisCard(t *testing.T) {
	env, _ := resilAPI(t)
	code, out, _ := exec(t, Runtime{}, env, "", "run", "show", resilRun)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"Diagnóstico:", "o processo do Hanko caiu", "o processo do Hanko foi reiniciado", "continuação 2 de 2",
		"espera a sua decisão: Tentar de novo, Continuar ou Encerrar", "operador", "Desfecho incerto: a tool Bash", "git push origin main",
		"Continuação de: 0198f2a0-0000-7000-8000-0000000000aa (recovery, tentativa 2)"} {
		if !strings.Contains(out, want) {
			t.Errorf("run show sem %q:\n%s", want, out)
		}
	}
	_, out, _ = exec(t, Runtime{}, merge(env, envLang("en")), "", "run", "show", resilRun)
	for _, want := range []string{"Diagnosis:", "the Hanko process went down", "waiting for your decision: Retry, Continue or Close", "Uncertain outcome: the tool Bash"} {
		if !strings.Contains(out, want) {
			t.Errorf("run show em inglês sem %q:\n%s", want, out)
		}
	}
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func TestRunDecisionCommands(t *testing.T) {
	env, f := resilAPI(t)
	for _, action := range []string{"retry", "continue", "close"} {
		code, out, errOut := exec(t, Runtime{}, env, "", "run", action, resilRun, "--note", "conferi o remoto")
		if code != 0 || !strings.Contains(out, "decisão registrada (continue)") {
			t.Fatalf("%s: %d %q %q", action, code, out, errOut)
		}
		h := f.last()
		if h.Method != "POST" || !strings.HasSuffix(h.Path, "/actions") || !strings.Contains(h.Body, `"action":"`+action+`"`) || !strings.Contains(h.Body, `"note":"conferi o remoto"`) {
			t.Fatalf("%s: %+v", action, h)
		}
	}
	code, out, _ := exec(t, Runtime{}, env, "", "run", "continue", resilRun, "--json")
	if code != 0 || !strings.Contains(out, `"decision": "continue"`) {
		t.Fatalf("json: %d %q", code, out)
	}
}

func TestRunSteerCommand(t *testing.T) {
	env, f := resilAPI(t)
	code, out, _ := exec(t, Runtime{}, env, "", "run", "steer", resilRun, "foque nos testes")
	if code != 0 || !strings.Contains(out, "Mensagem enviada (injeta ao vivo)") {
		t.Fatalf("%d %q", code, out)
	}
	if h := f.last(); !strings.HasSuffix(h.Path, "/steer") || !strings.Contains(h.Body, `"message":"foque nos testes"`) {
		t.Fatalf("hit %+v", h)
	}
	code, _, errOut := exec(t, Runtime{}, env, "", "run", "steer", resilRun, "   ")
	if code == 0 || !strings.Contains(errOut, "vazia") {
		t.Fatalf("empty: %d %q", code, errOut)
	}
}

func TestFollowupCommands(t *testing.T) {
	env, f := resilAPI(t)
	code, out, _ := exec(t, Runtime{}, env, "", "followup", "list", "--task", "HNK-1")
	if code != 0 || !strings.Contains(out, "depois, atualize o changelog") || !strings.Contains(out, "espera a execução terminar (Hábil_Corrente)") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "followup", "list", "--conversation", "0198f2a0-0000-7000-8000-0000000000c1")
	if code != 0 || !strings.Contains(out, "e o plano B?") || !strings.Contains(out, "a próxima execução está abrindo") {
		t.Fatalf("list conversation: %d %q", code, out)
	}
	if code, _, errOut := exec(t, Runtime{}, env, "", "followup", "list"); code == 0 || !strings.Contains(errOut, "--task ou --conversation") {
		t.Fatalf("list sem alvo: %d %q", code, errOut)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "followup", "add", "--task", "HNK-1", "depois, atualize o changelog")
	if code != 0 || !strings.Contains(out, "na fila (posição 1)") {
		t.Fatalf("add: %d %q", code, out)
	}
	if h := f.last(); h.Method != "POST" || !strings.Contains(h.Body, "depois, atualize o changelog") {
		t.Fatalf("add hit %+v", h)
	}
	if code, _, errOut := exec(t, Runtime{}, env, "", "followup", "add", "x"); code == 0 || !strings.Contains(errOut, "--task") {
		t.Fatalf("add sem task: %d %q", code, errOut)
	}
	id := "0198f2a0-0000-7000-8000-0000000000f1"
	code, out, _ = exec(t, Runtime{}, env, "", "followup", "edit", id, "novo texto")
	if code != 0 || !strings.Contains(out, "editado") || f.last().Method != "PATCH" {
		t.Fatalf("edit: %d %q %+v", code, out, f.last())
	}
	code, out, _ = exec(t, Runtime{}, env, "", "followup", "discard", id)
	if code != 0 || !strings.Contains(out, "descartado") || f.last().Method != "DELETE" {
		t.Fatalf("discard: %d %q %+v", code, out, f.last())
	}
}
