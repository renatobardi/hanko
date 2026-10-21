package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// ---- steer ----

func TestRunPageShowsSteerBoxWithTheHarnessMode(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	ada := e.f.Runs[0] // claude: injects
	rec := e.get(t, "/c/hanko/execucoes/"+ada.ID.String())
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="run-actions"`, "Mudar o rumo", "injeta ao vivo", `data-steer-mode="inject"`,
		`action="/c/hanko/execucoes/`+ada.ID.String()+`/steer"`, "A mensagem entra na execução em andamento")

	hana := e.f.Runs[1] // codex: interrupts and continues
	rec = e.get(t, "/c/hanko/execucoes/"+hana.ID.String())
	wantContains(t, rec, "Mudar o rumo", "interrompe e continua", `data-steer-mode="restart"`, "a execução é interrompida e continua com a sua mensagem")

	// a finished run takes no steer
	done := e.f.Runs[2]
	rec = e.get(t, "/c/hanko/execucoes/"+done.ID.String())
	wantNotContains(t, rec, "Mudar o rumo", `/steer"`)
}

func TestRunSteerPost(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	run := e.f.Runs[0]
	path := "/c/hanko/execucoes/" + run.ID.String() + "/steer"

	rec := e.post(t, path, url.Values{"message": {"foque nos testes"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/execucoes/"+run.ID.String() {
		t.Errorf("Location = %q", loc)
	}
	if len(e.f.Steers) != 1 || e.f.Steers[0].Body != "foque nos testes" || e.f.Steers[0].RunID != run.ID {
		t.Fatalf("steers = %+v", e.f.Steers)
	}
	// empty message: friendly 422; finished run: refused
	wantStatus(t, e.post(t, path, url.Values{"message": {"  "}}, false), http.StatusUnprocessableEntity)
	done := e.f.Runs[2]
	wantStatus(t, e.post(t, "/c/hanko/execucoes/"+done.ID.String()+"/steer", url.Values{"message": {"x"}}, false), http.StatusUnprocessableEntity)
	wantStatus(t, e.post(t, "/c/hanko/execucoes/nao-e-id/steer", url.Values{"message": {"x"}}, false), http.StatusNotFound)
}

// ---- diagnosis card and the operator's actions ----

func stoppedEnv(t *testing.T) (*env, domain.ID) {
	t.Helper()
	e := newEnv(t, uitest.Sample())
	run := e.f.Runs[0]
	e.f.StopRun(run.ID, domain.RunDiagnosis{Cause: domain.CauseProcessRestart, CauseDetail: "o processo do Hanko foi reiniciado com a execução em andamento",
		Attempt: 1, MaxAttempts: 2, NextAction: domain.ActionHumanDecision, Owner: domain.OwnerHuman,
		Uncertain: &domain.UncertainTool{Name: "Bash", Input: "git push origin main", Seq: 9}})
	return e, run.ID
}

func TestInboxShowsStoppedRunWithDiagnosisAndActions(t *testing.T) {
	e, id := stoppedEnv(t)
	rec := e.get(t, "/c/hanko/caixa")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Execuções paradas esperando decisão", "Diagnóstico da execução",
		"o processo do Hanko caiu", "continuação 1 de 2", "espera a sua decisão", "operador",
		"Desfecho incerto", "Bash: git push origin main",
		`action="/c/hanko/execucoes/`+id.String()+`/acao"`, `value="retry"`, `value="continue"`, `value="close"`,
		"Tentar de novo", "Continuar", "Encerrar", `name="voltar" value="/c/hanko/caixa"`)

	// the run page carries the same card (and no steer box: it is not active anymore)
	rec = e.get(t, "/c/hanko/execucoes/"+id.String())
	wantContains(t, rec, "Diagnóstico da execução", `value="continue"`, `name="voltar" value="/c/hanko/execucoes/`+id.String()+`"`)
	wantNotContains(t, rec, "Mudar o rumo")
}

func TestRunActionPost(t *testing.T) {
	e, id := stoppedEnv(t)
	path := "/c/hanko/execucoes/" + id.String() + "/acao"

	rec := e.post(t, path, url.Values{"action": {"continue"}, "note": {"conferi o remoto"}, "voltar": {"/c/hanko/caixa"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/caixa" {
		t.Errorf("Location = %q", loc)
	}
	if len(e.f.RunActions) != 1 || e.f.RunActions[0].Action != domain.DecisionContinue || e.f.RunActions[0].Note != "conferi o remoto" || e.f.RunActions[0].RunID != id {
		t.Fatalf("actions = %+v", e.f.RunActions)
	}
	// decided: it left the inbox
	wantNotContains(t, e.get(t, "/c/hanko/caixa"), "Execuções paradas esperando decisão")

	// "voltar" only accepts paths inside the company
	for _, bad := range []string{"https://evil.example/x", "//evil.example/x", "/outra/pagina", "/c/hanko//evil", "/c/outra/caixa", "/c/hanko/\\evil"} {
		rec := e.post(t, path, url.Values{"action": {"close"}, "voltar": {bad}}, false)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/execucoes/"+id.String() {
			t.Errorf("voltar %q -> Location %q", bad, loc)
		}
	}
	wantStatus(t, e.post(t, path, url.Values{"action": {"explodir"}}, false), http.StatusUnprocessableEntity)
	wantStatus(t, e.post(t, "/c/hanko/execucoes/nao-e-id/acao", url.Values{"action": {"retry"}}, false), http.StatusNotFound)
}

func TestFailedRunInInboxCarriesTheCardToo(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	// the sample failed run has no stored diagnosis; the service computes it on read, the fake gets it set
	failed := e.f.Runs[4]
	e.f.Runs[4].Diagnosis = &domain.RunDiagnosis{Cause: domain.CauseAgentError, CauseDetail: "bloqueada por orçamento do projeto LinkedIn",
		MaxAttempts: 2, NextAction: domain.ActionHumanDecision, Owner: domain.OwnerHuman}
	rec := e.get(t, "/c/hanko/caixa?aba=falhas")
	wantContains(t, rec, "Execuções que falharam", "erro do agente", "bloqueada por orçamento do projeto LinkedIn",
		`action="/c/hanko/execucoes/`+failed.ID.String()+`/acao"`, "Acordar de novo")
}

func TestViewerSeesTheCardButNoActions(t *testing.T) {
	e, _ := stoppedEnv(t)
	e.f.MyRoleIs = domain.MemberViewer
	rec := e.get(t, "/c/hanko/caixa")
	wantContains(t, rec, "Diagnóstico da execução", "Desfecho incerto")
	wantNotContains(t, rec, `value="continue"`, `/acao"`)
	rec = e.get(t, "/c/hanko/execucoes/"+e.f.Runs[1].ID.String())
	wantNotContains(t, rec, `/steer"`)
}

// ---- follow-up queue ----

func TestTaskPageFollowupQueue(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	task := e.f.Tasks[0] // HNK-221, with a running run
	path := "/c/hanko/tasks/" + task.Key

	wantContains(t, e.get(t, path), "Fila de follow-ups", "Nada na fila", `action="`+path+`/followups"`, "Mudar o rumo", "injeta ao vivo")

	rec := e.post(t, path+"/followups", url.Values{"message": {"depois, atualize o changelog"}, "voltar": {path + "#followups"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != path+"#followups" {
		t.Errorf("Location = %q", loc)
	}
	rec = e.get(t, path)
	wantContains(t, rec, "depois, atualize o changelog", "espera a execução terminar", "na fila", `data-wait="run_active"`, "Editar", "Descartar")
	wantNotContains(t, rec, "Nada na fila")
	if len(e.f.Followups) != 1 {
		t.Fatalf("followups = %+v", e.f.Followups)
	}
	id := e.f.Followups[0].ID.String()

	rec = e.post(t, "/c/hanko/followups/"+id+"/editar", url.Values{"message": {"depois, atualize o changelog e o README"}, "voltar": {path}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, path), "e o README", "editado")

	wantStatus(t, e.post(t, "/c/hanko/followups/"+id+"/descartar", url.Values{"voltar": {path}}, false), http.StatusSeeOther)
	if len(e.f.Followups) != 0 {
		t.Fatalf("followups = %+v", e.f.Followups)
	}
	wantStatus(t, e.post(t, "/c/hanko/followups/"+id+"/descartar", nil, false), http.StatusNotFound)
	wantStatus(t, e.post(t, "/c/hanko/followups/lixo/editar", url.Values{"message": {"x"}}, false), http.StatusNotFound)
	wantStatus(t, e.post(t, path+"/followups", url.Values{"message": {" "}}, false), http.StatusUnprocessableEntity)
}

func TestSteeredMessageAppearsInTheQueueWithItsMode(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	hana := e.f.Runs[1] // codex run: restart
	task := "HNK-219"
	wantStatus(t, e.post(t, "/c/hanko/execucoes/"+hana.ID.String()+"/steer", url.Values{"message": {"troque a abordagem"}, "voltar": {"/c/hanko/tasks/" + task + "#followups"}}, false), http.StatusSeeOther)
	rec := e.get(t, "/c/hanko/tasks/"+task)
	wantContains(t, rec, "troque a abordagem", "interromper e continuar", "a execução será interrompida e continuada", `data-wait="restart_pending"`)
}

func TestChatQueuePanelAndFragment(t *testing.T) {
	e, cv := chatEnv(t)
	cid := cv.ID
	e.f.Followups = append(e.f.Followups, domain.Followup{ID: uitest.ID("fu:chat1"), Kind: domain.FollowupChat, ConversationID: &cid, Body: "e o plano B?",
		Delivery: domain.DeliveryQueue, WaitReason: domain.WaitRunActive, CreatedAt: uitest.Now()})
	path := "/c/hanko/chat/" + cid.String()

	rec := e.get(t, path)
	wantContains(t, rec, `id="chat-queue"`, "Fila de follow-ups", "e o plano B?", "espera a execução terminar", "Descartar")

	rec = e.get(t, path+"/fila", "HX-Request", "true")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "e o plano B?")
	wantNotContains(t, rec, "<html")

	// a viewer sees the queue but cannot edit it
	e.f.MyRoleIs = domain.MemberViewer
	rec = e.get(t, path)
	wantContains(t, rec, "e o plano B?")
	wantNotContains(t, rec, "/descartar")
}

func TestSafeBack(t *testing.T) {
	co := domain.Company{Slug: "acme"}
	for raw, want := range map[string]string{
		"":                                  "/f",
		"/c/acme/caixa":                     "/c/acme/caixa",
		"/c/acme/tasks/ACM-1#followups":     "/c/acme/tasks/ACM-1#followups",
		"/c/acme/execucoes/x?aba=falhas":    "/c/acme/execucoes/x?aba=falhas",
		"https://evil.example/c/acme/x":     "/f",
		"//evil.example":                    "/f",
		"/c/acme//evil.example":             "/f",
		"/c/other/caixa":                    "/f",
		"/c/acme/x\nSet-Cookie: a=b":        "/f",
		"/c/acme/x\\y":                      "/f",
		"javascript:alert(1)":               "/f",
		"/c/acme/" + strings.Repeat("a", 5): "/c/acme/aaaaa",
	} {
		if got := safeBack(co, raw, "/f"); got != want {
			t.Errorf("safeBack(%q) = %q, want %q", raw, got, want)
		}
	}
}
