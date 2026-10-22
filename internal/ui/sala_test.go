package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// roomEnv é a empresa de exemplo com uma sala de Ada, Hana e Kenji em que Hana já respondeu.
func roomEnv(t *testing.T) (*env, service.ConversationView) {
	t.Helper()
	f := uitest.Sample()
	e := newEnv(t, f)
	cv := f.AddRoom("Planejamento da semana", "ada", "hana", "kenji")
	if _, err := f.SendMessage(t.Context(), domain.HumanActor, f.Companies[0].ID, cv.ID, "@hana como está a pesquisa?"); err != nil {
		t.Fatal(err)
	}
	f.RoomSays(cv.ID, uitest.ID("persona:hana"), "Quase pronta: **3 fontes** confirmadas.")
	f.Stranded(cv.ID, 0)
	return e, cv
}

func TestRoomNavigationAndList(t *testing.T) {
	e, cv := roomEnv(t)
	rec := e.get(t, "/c/hanko/sala")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Uma conversa com várias personas ao mesmo tempo.", "Nova sala", `name="personas"`, `name="moderator"`,
		`name="mode"`, "Planejamento da semana", `href="/c/hanko/sala/`+cv.ID.String()+`"`, "Ada, Hana, Kenji", "Abertas")
	wantNotContains(t, rec, "Em breve")
	// o menu leva à sala, não ao "Em breve"
	wantContains(t, e.get(t, "/c/hanko/"), `href="/c/hanko/sala"`)
	// o chat continua só com conversas diretas
	wantNotContains(t, e.get(t, "/c/hanko/chat"), "Planejamento da semana")
	// a persona mostra as salas em que participa
	wantContains(t, e.get(t, "/c/hanko/personas/hana"), "Salas em que participa", "Planejamento da semana")
	wantContains(t, e.get(t, "/c/hanko/personas/mika"), "Esta persona não está em nenhuma sala.")
	// estado vazio com atalhos que preenchem a primeira mensagem
	empty := newEnv(t, uitest.Sample()).get(t, "/c/hanko/sala")
	wantContains(t, empty, "Nenhuma sala ainda — junte personas numa conversa só.", "Planejar a semana", "Revisar o roadmap",
		"Destravar uma task", "Montar um plano de contratação", "/c/hanko/sala?msg=")
	wantContains(t, newEnv(t, uitest.Sample()).get(t, "/c/hanko/sala?msg="+url.QueryEscape("Vamos planejar")), ">Vamos planejar</textarea>")
	// sala de uma empresa sem personas suficientes
	wantContains(t, newEnv(t, uitest.Empty()).get(t, "/c/hanko/sala"), "A sala precisa de pelo menos duas personas.")
	// /chat/{id} de uma sala leva à página da sala
	rec = e.get(t, "/c/hanko/chat/"+cv.ID.String())
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/sala/"+cv.ID.String() {
		t.Errorf("Location = %q", loc)
	}
}

func TestRoomPage(t *testing.T) {
	e, cv := roomEnv(t)
	path := "/c/hanko/sala/" + cv.ID.String()
	rec := e.get(t, path)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Planejamento da semana", "@hana como está a pesquisa?", "<strong>3 fontes</strong>", `id="room-thread"`,
		`hx-post="`+path+`/mensagens"`, "@ para chamar uma persona", `data-mention="@kenji"`, "Feed do time", `id="room-feed"`,
		`hx-get="`+path+`/feed"`, "moderadora", "Responde quem é citado", "Arquivar", `data-draft="hanko-sala-`+cv.ID.String()+`"`,
		`action="`+path+`/participantes"`, `action="`+path+`/config"`, path+"/participantes/"+uitest.ID("persona:ada").String()+"/remover")
	wantNotContains(t, rec, "está pensando…", `hx-trigger="every 2s`)
	// resposta da persona à esquerda com o nome dela; humano à direita
	body := rec.Body.String()
	if !strings.Contains(body, `class="msg me"`) || !strings.Contains(body, "<b>Hana</b>") {
		t.Fatalf("bubbles: %s", body)
	}
	wantStatus(t, e.get(t, "/c/hanko/sala/nao-e-id"), http.StatusNotFound)
	wantStatus(t, e.get(t, "/c/hanko/sala/"+uitest.ID("conv:nope").String()), http.StatusNotFound)
	// o feed é o fragmento da atividade
	wantContains(t, e.get(t, path+"/feed", "HX-Request", "true"), `id="room-feed"`, `hx-trigger="every 10s"`)
}

func TestRoomThinkingPolls(t *testing.T) {
	e, cv := roomEnv(t)
	path := "/c/hanko/sala/" + cv.ID.String()
	run := e.f.RoomThinking(cv.ID, uitest.ID("persona:kenji"))
	rec := e.get(t, path+"/mensagens", "HX-Request", "true")
	wantContains(t, rec, "Kenji", "está pensando…", `hx-trigger="every 2s`, "/c/hanko/execucoes/"+run.String())
	wantNotContains(t, rec, "<html")

	// enviar pelo htmx devolve a thread
	rec = e.post(t, path+"/mensagens", url.Values{"message": {"@kenji e o deploy?"}}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "@kenji e o deploy?", `id="room-thread"`)
	// sem htmx, volta para a página
	rec = e.post(t, path+"/mensagens", url.Values{"message": {"Obrigado"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != path+"#room-thread" {
		t.Errorf("Location = %q", loc)
	}
	wantStatus(t, e.post(t, path+"/mensagens", url.Values{"message": {"  "}}, true), http.StatusUnprocessableEntity)
}

func TestRoomCreate(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	ada, hana := uitest.ID("persona:ada").String(), uitest.ID("persona:hana").String()
	rec := e.post(t, "/c/hanko/sala/nova", url.Values{"personas": {ada, hana}, "moderator": {hana}, "mode": {"all"},
		"title": {"Daily"}, "message": {"Bom dia"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(f.Conversations) != 1 || !strings.HasPrefix(rec.Header().Get("Location"), "/c/hanko/sala/"+f.Conversations[0].ID.String()) {
		t.Fatalf("rooms=%d location=%q", len(f.Conversations), rec.Header().Get("Location"))
	}
	r := f.Conversations[0]
	if r.Title != "Daily" || r.Room == nil || len(r.Room.Participants) != 2 || r.Room.Settings.Mode != domain.RoomAll ||
		r.Room.ModeratorPersonaID == nil || *r.Room.ModeratorPersonaID != uitest.ID("persona:hana") {
		t.Fatalf("room: %+v", r.Room)
	}
	// uma persona só, ou moderadora fora da seleção: o formulário volta com o erro e o que foi digitado
	rec = e.post(t, "/c/hanko/sala/nova", url.Values{"personas": {ada}, "message": {"texto digitado"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Escolha pelo menos duas personas.", "texto digitado")
	rec = e.post(t, "/c/hanko/sala/nova", url.Values{"personas": {ada, hana}, "moderator": {uitest.ID("persona:kenji").String()}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "A moderadora precisa ser uma das personas escolhidas.")
	// erro do service (sem título nem mensagem) também volta no formulário
	rec = e.post(t, "/c/hanko/sala/nova", url.Values{"personas": {ada, hana}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if len(f.Conversations) != 1 {
		t.Errorf("rooms = %d", len(f.Conversations))
	}
	// persona pausada aparece marcada no formulário
	wantContains(t, e.get(t, "/c/hanko/sala"), "rp-warn")
}

func TestRoomParticipantsAndConfig(t *testing.T) {
	e, cv := roomEnv(t)
	path := "/c/hanko/sala/" + cv.ID.String()
	mika := uitest.ID("persona:mika")
	wantStatus(t, e.post(t, path+"/participantes", url.Values{"persona": {mika.String()}}, false), http.StatusSeeOther)
	got, _ := e.f.GetConversation(t.Context(), cv.CompanyID, cv.ID)
	if len(got.Room.Active()) != 4 {
		t.Fatalf("add: %+v", got.Room)
	}
	wantStatus(t, e.post(t, path+"/participantes", url.Values{"persona": {"x"}}, false), http.StatusUnprocessableEntity)
	wantStatus(t, e.post(t, path+"/participantes/"+mika.String()+"/remover", url.Values{}, false), http.StatusSeeOther)
	got, _ = e.f.GetConversation(t.Context(), cv.CompanyID, cv.ID)
	if len(got.Room.Active()) != 3 {
		t.Fatalf("remove: %+v", got.Room)
	}
	rec := e.post(t, path+"/config", url.Values{"mode": {"all"}, "moderator": {uitest.ID("persona:kenji").String()}, "max_hops": {"3"}, "max_runs": {"6"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	got, _ = e.f.GetConversation(t.Context(), cv.CompanyID, cv.ID)
	if got.Room.Settings.Mode != domain.RoomAll || got.Room.Settings.MaxHops != 3 || got.Room.Settings.MaxRunsPerMessage != 6 ||
		*got.Room.ModeratorPersonaID != uitest.ID("persona:kenji") {
		t.Fatalf("config: %+v", got.Room.Settings)
	}
	wantContains(t, e.get(t, path), "Todas respondem")
	wantStatus(t, e.post(t, path+"/config", url.Values{"max_hops": {"muitos"}}, false), http.StatusUnprocessableEntity)
	wantStatus(t, e.post(t, path+"/config", url.Values{"max_hops": {"9"}}, false), http.StatusUnprocessableEntity)

	// com duas personas não há botão de tirar
	two := e.f.AddRoom("Dupla", "ada", "hana")
	wantNotContains(t, e.get(t, "/c/hanko/sala/"+two.ID.String()), "/remover")
	// as operações de sala não valem num chat direto
	direct, err := e.f.CreateConversation(t.Context(), domain.HumanActor, cv.CompanyID, service.CreateConversationInput{PersonaID: uitest.ID("persona:ada"), Title: "Direto"})
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.post(t, "/c/hanko/sala/"+direct.ID.String()+"/config", url.Values{"mode": {"all"}}, false), http.StatusNotFound)
}

func TestRoomArchive(t *testing.T) {
	e, cv := roomEnv(t)
	path := "/c/hanko/sala/" + cv.ID.String()
	wantStatus(t, e.post(t, path+"/arquivar", url.Values{}, false), http.StatusSeeOther)
	wantContains(t, e.get(t, "/c/hanko/sala?s=arquivadas"), "Planejamento da semana")
	wantNotContains(t, e.get(t, "/c/hanko/sala"), "Planejamento da semana")
	page := e.get(t, path)
	wantContains(t, page, "Sala arquivada: reabra para continuar.", "Reabrir")
	wantNotContains(t, page, `name="message"`, "Personas e regras")
	wantStatus(t, e.post(t, path+"/reabrir", url.Values{}, false), http.StatusSeeOther)
	wantContains(t, e.get(t, path), `name="message"`)
}

func TestRoomRoles(t *testing.T) {
	for _, role := range allRoles {
		f := uitest.Sample()
		cv := f.AddRoom("Sala", "ada", "hana", "kenji")
		f.MyRoleIs = role
		e := newEnv(t, f)
		p := "/c/hanko/sala/" + cv.ID.String()
		canWrite := role == domain.MemberMember || role == domain.MemberAdmin || role == domain.MemberOwner
		page, list := e.get(t, p), e.get(t, "/c/hanko/sala")
		wantStatus(t, page, http.StatusOK)
		for name, marker := range map[string]string{"composer": `name="message"`, "remover": "/remover", "config": "Personas e regras", "arquivar": p + "/arquivar"} {
			if got := strings.Contains(page.Body.String(), marker); got != canWrite {
				t.Errorf("%s: %s = %v, want %v", role, name, got, canWrite)
			}
		}
		if got := strings.Contains(list.Body.String(), `action="/c/hanko/sala/nova"`); got != canWrite {
			t.Errorf("%s: new-room form = %v, want %v", role, got, canWrite)
		}
		rec := e.post(t, p+"/participantes", url.Values{"persona": {uitest.ID("persona:mika").String()}}, false)
		if canWrite {
			wantStatus(t, rec, http.StatusSeeOther)
		} else {
			wantStatus(t, rec, http.StatusForbidden)
		}
	}
}

func TestRoomEnglish(t *testing.T) {
	e, cv := roomEnv(t)
	for _, path := range []string{"/c/hanko/sala", "/c/hanko/sala/" + cv.ID.String()} {
		rec := e.get(t, path, "Accept-Language", "en")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, i18n.English()["Sala"])
		wantNotContains(t, rec, "Feed do time", "Nova sala", "Personas e regras")
	}
}
