package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Coordenação de tasks na UI (ADR-0040): breadcrumb, dependências, subtasks, pausa herdada, política de revisão
// e a aba Pausadas da Caixa.

func firstTwoTasks(t *testing.T, f *uitest.Fake) (service.TaskView, service.TaskView) {
	t.Helper()
	if len(f.Tasks) < 2 {
		t.Fatal("amostra sem tasks")
	}
	return f.Tasks[0], f.Tasks[1]
}

func TestTaskPageShowsDependenciesAndBlockedBadge(t *testing.T) {
	f := uitest.Sample()
	a, b := firstTwoTasks(t, f)
	e := newEnv(t, f)

	rec := e.post(t, "/c/hanko/tasks/"+b.Key+"/dependencias", url.Values{"dep": {a.Key}}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="task-side"`, `id="task-deps"`, a.Key, "Dependências", "remover")
	if !f.Called("AddTaskDependency") {
		t.Fatal("AddTaskDependency não foi chamado")
	}

	page := e.get(t, "/c/hanko/tasks/"+b.Key)
	wantStatus(t, page, http.StatusOK)
	if a.Status.Open() {
		wantContains(t, page, "Aguarda dependências", "nenhuma execução abre até todas fecharem")
	}
	// a lista também mostra o selo
	list := e.get(t, "/c/hanko/tasks")
	wantStatus(t, list, http.StatusOK)

	// remover
	rec = e.post(t, "/c/hanko/tasks/"+b.Key+"/dependencias/"+a.Key+"/remover", nil, true)
	wantStatus(t, rec, http.StatusOK)
	if !f.Called("RemoveTaskDependency") {
		t.Fatal("RemoveTaskDependency não foi chamado")
	}
	// chave que não existe vira erro de campo, sem escrever
	f2 := uitest.Sample()
	e2 := newEnv(t, f2)
	rec = e2.post(t, "/c/hanko/tasks/"+b.Key+"/dependencias", url.Values{"dep": {"HNK-99999"}}, true)
	if rec.Code < 400 {
		t.Fatalf("dependência inexistente deveria falhar: %d", rec.Code)
	}
	if f2.Called("AddTaskDependency") {
		t.Fatal("escreveu dependência inexistente")
	}
}

func TestTaskPageCycleIsRefused(t *testing.T) {
	f := uitest.Sample()
	a, b := firstTwoTasks(t, f)
	e := newEnv(t, f)
	wantStatus(t, e.post(t, "/c/hanko/tasks/"+b.Key+"/dependencias", url.Values{"dep": {a.Key}}, true), http.StatusOK)
	rec := e.post(t, "/c/hanko/tasks/"+a.Key+"/dependencias", url.Values{"dep": {b.Key}}, true)
	wantStatus(t, rec, http.StatusConflict)
}

func TestTaskPageHoldBannerAndResume(t *testing.T) {
	f := uitest.Sample()
	parent, kid := firstTwoTasks(t, f)
	// kid vira filha do pai
	for i := range f.Tasks {
		if f.Tasks[i].ID == kid.ID {
			f.Tasks[i].ParentID = &parent.ID
		}
	}
	e := newEnv(t, f)

	rec := e.post(t, "/c/hanko/tasks/"+parent.Key+"/pausar", url.Values{"reason": {"congelamento do release"}}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Pausada", "congelamento do release", "Retomar")

	// a página do pai e a da filha mostram o banner no thread; a da filha diz de onde herdou
	page := e.get(t, "/c/hanko/tasks/"+parent.Key)
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, "hold-banner", "congelamento do release", "Nenhuma execução abre enquanto durar", "/tasks/"+parent.Key+"/retomar")
	child := e.get(t, "/c/hanko/tasks/"+kid.Key)
	wantStatus(t, child, http.StatusOK)
	wantContains(t, child, "hold-banner", "herdada de", parent.Key, parent.Key+"</a>")
	wantNotContains(t, child, "/tasks/"+kid.Key+"/retomar")
	// breadcrumb da filha
	wantContains(t, child, `href="/c/hanko/tasks/`+parent.Key+`"`)

	// a Caixa mostra a raiz da pausa, com Retomar
	inbox := e.get(t, "/c/hanko/caixa")
	wantStatus(t, inbox, http.StatusOK)
	wantContains(t, inbox, "Tasks pausadas", "congelamento do release", "/caixa/tasks/"+parent.ID.String()+"/retomar")
	tab := e.get(t, "/c/hanko/caixa?aba=pausadas")
	wantStatus(t, tab, http.StatusOK)
	wantContains(t, tab, "Tasks pausadas")

	// retomar pela Caixa
	rec = e.post(t, "/c/hanko/caixa/tasks/"+parent.ID.String()+"/retomar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !f.Called("ResumeTask") {
		t.Fatal("ResumeTask não foi chamado")
	}
	page = e.get(t, "/c/hanko/tasks/"+parent.Key)
	wantNotContains(t, page, "hold-banner")

	// motivo vazio é erro e não escreve
	f2 := uitest.Sample()
	e2 := newEnv(t, f2)
	rec = e2.post(t, "/c/hanko/tasks/"+parent.Key+"/pausar", url.Values{"reason": {""}}, true)
	if rec.Code < 400 || f2.Called("HoldTask") && len(f2.Coord.Holds) != 0 {
		t.Fatalf("pausa sem motivo: %d", rec.Code)
	}
}

func TestTaskPageSubtasksParentAndForceClose(t *testing.T) {
	f := uitest.Sample()
	parent, kid := firstTwoTasks(t, f)
	for i := range f.Tasks {
		if f.Tasks[i].ID == kid.ID {
			f.Tasks[i].ParentID = &parent.ID
			f.Tasks[i].Status = domain.TaskTodo
		}
	}
	e := newEnv(t, f)
	page := e.get(t, "/c/hanko/tasks/"+parent.Key)
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, "Subtasks", kid.Key, "Nova subtask", "/tasks/nova?pai="+parent.Key, "Concluir mesmo com subtask aberta", "/forcar-conclusao")

	// concluir sem override é recusado pelo serviço; o override pede motivo
	rec := e.post(t, "/c/hanko/tasks/"+parent.Key+"/forcar-conclusao", url.Values{"reason": {"filha virou outra frente"}}, true)
	wantStatus(t, rec, http.StatusOK)
	got, _ := f.GetTask(context.Background(), domain.ID{}, parent.Key)
	if got.Status != domain.TaskDone {
		t.Fatalf("pai: %s", got.Status)
	}

	// mudar o pai
	rec = e.post(t, "/c/hanko/tasks/"+kid.Key+"/pai", url.Values{"parent": {""}}, true)
	wantStatus(t, rec, http.StatusOK)
	k, _ := f.GetTask(context.Background(), domain.ID{}, kid.Key)
	if k.ParentID != nil {
		t.Fatalf("pai não foi limpo: %v", k.ParentID)
	}
	rec = e.post(t, "/c/hanko/tasks/"+kid.Key+"/pai", url.Values{"parent": {"HNK-99999"}}, true)
	if rec.Code < 400 {
		t.Fatalf("pai inexistente deveria falhar: %d", rec.Code)
	}

	// nova subtask abre o formulário com o pai preenchido
	form := e.get(t, "/c/hanko/tasks/nova?pai="+parent.Key)
	wantStatus(t, form, http.StatusOK)
	wantContains(t, form, `name="parent"`, `value="`+parent.Key+`"`, `name="depends_on"`)
}

func TestTaskPageReviewPolicyForm(t *testing.T) {
	f := uitest.Sample()
	a, _ := firstTwoTasks(t, f)
	e := newEnv(t, f)
	page := e.get(t, "/c/hanko/tasks/"+a.Key)
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, "Revisão do fechamento", "Revisão ", "(origem: ", "/politica-revisao", "Exigir revisão")

	rec := e.post(t, "/c/hanko/tasks/"+a.Key+"/politica-revisao", url.Values{"require": {"yes"}, "reviewer": {"none"}}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Revisão obrigatória (origem: task)")
	pol := f.Coord.TaskPolicy[a.ID]
	if pol.RequireReview == nil || !*pol.RequireReview || pol.ReviewerMode != domain.ReviewerNone {
		t.Fatalf("política gravada: %+v", pol)
	}
	// valores inválidos não escrevem
	for _, form := range []url.Values{{"reviewer": {"nao-uuid"}}} {
		if rec := e.post(t, "/c/hanko/tasks/"+a.Key+"/politica-revisao", form, true); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", form, rec.Code)
		}
	}
}

func TestCoordinationInEnglish(t *testing.T) {
	f := uitest.Sample()
	a, _ := firstTwoTasks(t, f)
	e := newEnv(t, f)
	if _, err := f.HoldTask(context.Background(), domain.HumanActor, domain.ID{}, a.ID, "release freeze"); err != nil {
		t.Fatal(err)
	}
	rec := e.get(t, "/c/hanko/tasks/"+a.Key, "Cookie", "hanko_lang=en")
	wantStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"Paused", "Dependencies", "Subtasks", "Closing review", "No run opens while it lasts"} {
		if !strings.Contains(body, want) {
			t.Errorf("página em inglês sem %q", want)
		}
	}
	for _, pt := range []string{"Dependências", "Revisão do fechamento", "Nenhuma execução abre"} {
		if strings.Contains(body, pt) {
			t.Errorf("página em inglês com texto em português %q", pt)
		}
	}
}
