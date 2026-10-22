package ui

import (
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Os compositores (chat, sala, comentário da task) não podem se limpar no autosave do rascunho: a limpeza mora no
// hanko.js e só vale para o envio do próprio form[data-composer]; o autosave cede ao envio e as conversas não se
// atualizam durante ele.
func TestComposersClearOnlyOnTheirOwnSend(t *testing.T) {
	e, cv := chatEnv(t)
	re, room := roomEnv(t)
	pages := map[*env][]string{e: {"/c/hanko/chat/" + cv.ID.String(), "/c/hanko/tasks/HNK-221"}, re: {"/c/hanko/sala/" + room.ID.String()}}
	for e, paths := range pages {
		for _, path := range paths {
			rec := e.get(t, path)
			wantContains(t, rec, `data-composer`, `hx-disabled-elt="find button[type=submit]"`)
			wantNotContains(t, rec, "hx-on::after-request", `hx-sync="this:replace"`)
		}
	}
}

func TestTaskThreadPollsWhileAwaitingThePersona(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	task := e.f.Tasks[0]
	for i := range e.f.Tasks {
		if e.f.Tasks[i].Key == "HNK-221" {
			task = e.f.Tasks[i]
		}
	}
	frag := "/c/hanko/tasks/HNK-221/conversa"
	quiet := e.get(t, frag, "HX-Request", "true")
	wantStatus(t, quiet, 200)
	wantContains(t, quiet, `id="thread"`)
	if task.ActiveRunID == nil {
		wantNotContains(t, quiet, `hx-trigger="every 3s`)
	}

	// um comentário humano recente: a conversa passa a se atualizar até a persona responder
	e.f.Comments = append(e.f.Comments, domain.Comment{ID: domain.NewID(), CompanyID: task.CompanyID, TaskID: task.ID,
		Author: domain.HumanActor, Body: "e agora?", CreatedAt: uitest.Now().Add(-10 * time.Second)})
	rec := e.get(t, frag, "HX-Request", "true")
	wantContains(t, rec, "e agora?", `hx-get="`+frag+`"`, `hx-trigger="every 3s [!document.querySelector('form[data-composer].htmx-request')]"`)
	wantNotContains(t, rec, "<html")
}

func TestAwaitingReply(t *testing.T) {
	now := uitest.Now()
	human := func(ago time.Duration) domain.Comment {
		return domain.Comment{Author: domain.HumanActor, CreatedAt: now.Add(-ago)}
	}
	pid := domain.NewID()
	persona := domain.Comment{Author: domain.Actor{Type: domain.ActorPersona, ID: &pid}, CreatedAt: now}
	cases := []struct {
		name string
		in   []domain.Comment
		want bool
	}{
		{"sem comentários", nil, false},
		{"humano recente", []domain.Comment{human(30 * time.Second)}, true},
		{"humano antigo", []domain.Comment{human(threadLiveWindow + time.Second)}, false},
		{"a persona já respondeu", []domain.Comment{human(30 * time.Second), persona}, false},
	}
	for _, c := range cases {
		if got := awaitingReply(c.in, now); got != c.want {
			t.Errorf("%s: awaitingReply = %v, want %v", c.name, got, c.want)
		}
	}
}
