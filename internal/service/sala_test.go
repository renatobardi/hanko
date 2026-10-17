package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// roomFixture: personas Ana (moderadora), Beto e Caio numa sala sem mensagem.
func roomFixture(t *testing.T, mod func(*service.CreateRoomInput)) (*env, []domain.Persona, service.ConversationView) {
	t.Helper()
	e := newEnv(t)
	ps := []domain.Persona{e.persona(t, "Ana", nil), e.persona(t, "Beto", nil), e.persona(t, "Caio", nil)}
	in := service.CreateRoomInput{Title: "Planejamento", PersonaIDs: []domain.ID{ps[0].ID, ps[1].ID, ps[2].ID}}
	if mod != nil {
		mod(&in)
	}
	e.jobs.reset()
	cv, err := e.svc.CreateRoom(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	return e, ps, cv
}

// activeRuns são as runs abertas da sala, por persona.
func (e *env) activeRuns(t *testing.T, convID domain.ID) map[domain.ID]domain.Run {
	t.Helper()
	runs, err := e.svc.ListRuns(context.Background(), e.co.ID, service.RunFilter{ConversationID: &convID,
		Statuses: []domain.RunStatus{domain.RunQueued, domain.RunRunning}})
	if err != nil {
		t.Fatal(err)
	}
	out := map[domain.ID]domain.Run{}
	for _, r := range runs {
		if _, dup := out[r.PersonaID]; dup {
			t.Fatalf("two active runs for persona %s", r.PersonaName)
		}
		out[r.PersonaID] = r.Run
	}
	return out
}

func (e *env) say(t *testing.T, convID domain.ID, body string) domain.ConversationMessage {
	t.Helper()
	m, err := e.svc.SendMessage(context.Background(), domain.HumanActor, e.co.ID, convID, body)
	if err != nil {
		t.Fatalf("send %q: %v", body, err)
	}
	return m
}

func (e *env) roomMessages(t *testing.T, convID domain.ID) []domain.ConversationMessage {
	t.Helper()
	ms, err := e.svc.ListConversationMessages(context.Background(), e.co.ID, convID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestRoomCreateAndView(t *testing.T) {
	ctx := context.Background()
	e, ps, cv := roomFixture(t, nil)
	if cv.Kind != domain.ConversationRoom || cv.PersonaID != (domain.ID{}) || cv.Room == nil {
		t.Fatalf("room: %+v", cv)
	}
	if len(cv.Room.Participants) != 3 || cv.Room.ModeratorPersonaID == nil || *cv.Room.ModeratorPersonaID != ps[0].ID ||
		cv.Room.Settings.Mode != domain.RoomMentions || cv.Room.Settings.MaxHops != domain.DefaultRoomHops || cv.Room.MaxRunsPerMessage != 3 {
		t.Fatalf("room view: %+v", cv.Room)
	}
	// a sala aparece na lista de salas e na de quem participa, não na de chats diretos
	rooms, _ := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{Kind: domain.ConversationRoom})
	direct, _ := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{Kind: domain.ConversationDirect})
	mine, _ := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{Kind: domain.ConversationRoom, PersonaID: &ps[1].ID})
	if len(rooms) != 1 || len(direct) != 0 || len(mine) != 1 || len(rooms[0].Room.Participants) != 3 {
		t.Fatalf("lists: rooms=%d direct=%d mine=%d", len(rooms), len(direct), len(mine))
	}
	if _, err := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{Kind: "grupo"}); err == nil {
		t.Fatal("unknown kind must fail")
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "conversation", Limit: 10})
	if len(acts) != 1 || acts[0].Verb != "conversation.created" || !strings.Contains(strings.ReplaceAll(string(acts[0].Data), " ", ""), `"kind":"room"`) {
		t.Fatalf("activity: %+v", acts)
	}
}

func TestRoomValidation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.persona(t, "Ana", nil), e.persona(t, "Beto", nil)
	c := e.persona(t, "Caio", nil)
	for name, in := range map[string]service.CreateRoomInput{
		"uma persona":         {Title: "x", PersonaIDs: []domain.ID{a.ID, a.ID}},
		"sem título":          {PersonaIDs: []domain.ID{a.ID, b.ID}},
		"moderadora de fora":  {Title: "x", PersonaIDs: []domain.ID{a.ID, b.ID}, ModeratorID: &c.ID},
		"modo inválido":       {Title: "x", PersonaIDs: []domain.ID{a.ID, b.ID}, Mode: "todos"},
		"saltos demais":       {Title: "x", PersonaIDs: []domain.ID{a.ID, b.ID}, MaxHops: 6},
		"execuções demais":    {Title: "x", PersonaIDs: []domain.ID{a.ID, b.ID}, MaxRunsPerMessage: 25},
		"persona inexistente": {Title: "x", PersonaIDs: []domain.ID{a.ID, domain.NewID()}},
	} {
		if _, err := e.svc.CreateRoom(ctx, domain.HumanActor, e.co.ID, in); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	ids := make([]domain.ID, 13)
	for i := range ids {
		ids[i] = domain.NewID()
	}
	_, err := e.svc.CreateRoom(ctx, domain.HumanActor, e.co.ID, service.CreateRoomInput{Title: "x", PersonaIDs: ids})
	wantErr(t, err, domain.ErrInvalid)
	// uma persona nunca escreve na sala pelo service
	cv, err := e.svc.CreateRoom(ctx, domain.HumanActor, e.co.ID, service.CreateRoomInput{Title: "x", PersonaIDs: []domain.ID{a.ID, b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.SendMessage(ctx, domain.PersonaActor(a.ID), e.co.ID, cv.ID, "@beto oi")
	wantErr(t, err, domain.ErrNotAllowed)
	// operações de sala não valem para um chat direto
	direct, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: a.ID, Title: "direto"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.AddRoomParticipant(ctx, domain.HumanActor, e.co.ID, direct.ID, b.ID)
	wantErr(t, err, domain.ErrInvalid)
}

func TestRoomRoutingByMentionModeratorAndAll(t *testing.T) {
	e, ps, cv := roomFixture(t, nil)
	ana, beto, caio := ps[0], ps[1], ps[2]

	// com menção: só quem foi citado (e-mail e código não contam)
	m := e.say(t, cv.ID, "@Beto e @caio, vejam isto (cc ana@x.com, `@ana`)")
	runs := e.activeRuns(t, cv.ID)
	if len(runs) != 2 || runs[beto.ID].ID == (domain.ID{}) || runs[caio.ID].ID == (domain.ID{}) {
		t.Fatalf("mentioned runs: %+v", runs)
	}
	if len(m.Deliveries) != 2 || len(m.Mentions) != 2 {
		t.Fatalf("deliveries/mentions: %+v", m)
	}
	if len(e.jobs.execs) != 2 {
		t.Fatalf("execute_run: %v", e.jobs.execs)
	}
	e.answer(t, runs[beto.ID].ID, domain.RunDone, adapter.Result{Summary: "Visto."})
	e.answer(t, runs[caio.ID].ID, domain.RunDone, adapter.Result{Summary: "Ok."})

	// sem menção, no modo mentions: a moderadora
	e.say(t, cv.ID, "Alguém pode resumir?")
	runs = e.activeRuns(t, cv.ID)
	if len(runs) != 1 || runs[ana.ID].ID == (domain.ID{}) {
		t.Fatalf("moderator run: %+v", runs)
	}
	e.answer(t, runs[ana.ID].ID, domain.RunDone, adapter.Result{Summary: "Resumo."})

	// modo all: todas respondem
	all := domain.RoomAll
	if _, err := e.svc.UpdateRoomSettings(context.Background(), domain.HumanActor, e.co.ID, cv.ID, service.RoomSettingsPatch{Mode: &all}); err != nil {
		t.Fatal(err)
	}
	e.say(t, cv.ID, "Bom dia, time")
	if runs = e.activeRuns(t, cv.ID); len(runs) != 3 {
		t.Fatalf("all mode runs: %d", len(runs))
	}

	// a resposta entra na thread com a persona
	ms := e.roomMessages(t, cv.ID)
	var replies int
	for _, x := range ms {
		if x.Kind == domain.MessagePersona {
			replies++
			if x.PersonaID == nil || x.PersonaName == "" || x.ReplyToID == nil {
				t.Fatalf("persona reply without persona: %+v", x)
			}
		}
	}
	if replies != 3 {
		t.Fatalf("replies: %d", replies)
	}
}

func TestRoomPartialRefusal(t *testing.T) {
	ctx := context.Background()
	e, ps, cv := roomFixture(t, nil)
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, ps[1].ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaPaused)}); err != nil {
		t.Fatal(err)
	}
	// Beto pausado não derruba Caio: a mensagem é gravada e a recusa vira aviso
	m := e.say(t, cv.ID, "@beto @caio prontos?")
	if len(m.Deliveries) != 1 || m.Deliveries[0].PersonaID != ps[2].ID {
		t.Fatalf("deliveries: %+v", m.Deliveries)
	}
	ms := e.roomMessages(t, cv.ID)
	if len(ms) != 2 || ms[1].Kind != domain.MessageSystem || !strings.Contains(ms[1].Body, "Beto não vai responder") || !strings.Contains(ms[1].Body, "pausada") {
		t.Fatalf("notice: %+v", ms)
	}
	// ninguém aceita: a mensagem não é gravada e o erro diz por quê
	_, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "@beto e você?")
	wantErr(t, err, domain.ErrPersonaUnavailable)
	if !strings.Contains(err.Error(), "Beto") {
		t.Fatalf("reason: %v", err)
	}
	if got := e.roomMessages(t, cv.ID); len(got) != 2 {
		t.Fatalf("refused message was stored: %d", len(got))
	}
	// sala nova com primeira mensagem que ninguém aceita não existe
	_, err = e.svc.CreateRoom(ctx, domain.HumanActor, e.co.ID, service.CreateRoomInput{PersonaIDs: []domain.ID{ps[0].ID, ps[1].ID},
		ModeratorID: &ps[1].ID, Message: "Começando"})
	wantErr(t, err, domain.ErrPersonaUnavailable)
	rooms, _ := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{Kind: domain.ConversationRoom})
	if len(rooms) != 1 {
		t.Fatalf("refused room left behind: %d", len(rooms))
	}
}

func TestRoomCoalescesPerPersona(t *testing.T) {
	ctx := context.Background()
	e, ps, cv := roomFixture(t, nil)
	beto, caio := ps[1], ps[2]
	e.say(t, cv.ID, "@beto primeira")
	runs := e.activeRuns(t, cv.ID)
	first := runs[beto.ID]
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, first.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	// Beto ocupado: as próximas para ele esperam; Caio roda ao mesmo tempo na mesma sala
	e.say(t, cv.ID, "@beto segunda")
	e.say(t, cv.ID, "@beto terceira e @caio também")
	runs = e.activeRuns(t, cv.ID)
	if len(runs) != 2 || runs[beto.ID].ID != first.ID {
		t.Fatalf("runs during beto's run: %+v", runs)
	}
	got, _ := e.svc.GetConversation(ctx, e.co.ID, cv.ID)
	if p, _ := got.Room.Participant(beto.ID); p.Pending != 2 || p.ActiveRunID == nil || got.PendingMessages != 2 {
		t.Fatalf("beto pending: %+v (conv %d)", p, got.PendingMessages)
	}
	e.answer(t, runs[caio.ID].ID, domain.RunDone, adapter.Result{Summary: "Feito."})
	// fim da run de Beto: a próxima recolhe as duas que esperavam
	if _, err := e.svc.FinishRun(ctx, e.co.ID, first.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "Primeira ok.", SessionRef: "s-beto"}}); err != nil {
		t.Fatal(err)
	}
	runs = e.activeRuns(t, cv.ID)
	next := runs[beto.ID]
	if len(runs) != 1 || next.ID == first.ID {
		t.Fatalf("next run: %+v", runs)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Chat == nil || rc.Chat.Room == nil || len(rc.Chat.New) != 2 || rc.Chat.New[0].Body != "@beto segunda" || rc.Chat.ResumeSession != "s-beto" {
		t.Fatalf("coalesced context: %+v", rc.Chat)
	}
	// o histórico tem quem falou
	var named bool
	for _, m := range rc.Chat.History {
		if m.Kind == domain.MessagePersona && m.PersonaName == "Caio" {
			named = true
		}
	}
	if !named {
		t.Fatalf("history without names: %+v", rc.Chat.History)
	}
}

func TestRoomMentionsWakeOthersWithinLimits(t *testing.T) {
	ctx := context.Background()
	e, ps, cv := roomFixture(t, func(in *service.CreateRoomInput) { in.MaxHops = 2 })
	ana, beto, caio := ps[0], ps[1], ps[2]
	e.say(t, cv.ID, "@ana comece")
	a1 := e.activeRuns(t, cv.ID)[ana.ID]
	// Ana cita Beto (e a si mesma, o que não conta): Beto acorda no salto 1
	e.answer(t, a1.ID, domain.RunDone, adapter.Result{Summary: "Beleza. @beto, levanta os números? (eu, @ana, reviso)"})
	runs := e.activeRuns(t, cv.ID)
	if len(runs) != 1 || runs[beto.ID].ID == (domain.ID{}) {
		t.Fatalf("hop 1: %+v", runs)
	}
	b1 := runs[beto.ID]
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, b1.ID)
	if err != nil || rc.Chat.Room.Hop != 1 || len(rc.Chat.New) != 1 || rc.Chat.New[0].PersonaName != "Ana" {
		t.Fatalf("hop-1 context: %+v %v", rc.Chat, err)
	}
	// Beto cita Caio (salto 2) e alguém de fora da sala (não acorda)
	e.persona(t, "Duda", nil)
	e.answer(t, b1.ID, domain.RunDone, adapter.Result{Summary: "Números prontos. @caio publica? @duda pode ver também"})
	runs = e.activeRuns(t, cv.ID)
	if len(runs) != 1 || runs[caio.ID].ID == (domain.ID{}) {
		t.Fatalf("hop 2: %+v", runs)
	}
	// Caio cita Ana: salto 3 passa do teto, ninguém acorda e a sala avisa
	e.answer(t, runs[caio.ID].ID, domain.RunDone, adapter.Result{Summary: "Publicado. @ana confere?"})
	if runs = e.activeRuns(t, cv.ID); len(runs) != 0 {
		t.Fatalf("hop 3 must not wake: %+v", runs)
	}
	ms := e.roomMessages(t, cv.ID)
	last := ms[len(ms)-1]
	if last.Kind != domain.MessageSystem || !strings.Contains(last.Body, "Limite de 2 saltos") {
		t.Fatalf("hop notice: %+v", last)
	}
	all, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{ConversationID: &cv.ID})
	if len(all) != 3 {
		t.Fatalf("total runs: %d", len(all))
	}
}

func TestRoomMaxRunsPerMessage(t *testing.T) {
	e, ps, cv := roomFixture(t, func(in *service.CreateRoomInput) { in.MaxRunsPerMessage = 2; in.MaxHops = 5 })
	ana, beto := ps[0], ps[1]
	// três citadas, teto 2: a terceira fica de fora com aviso
	e.say(t, cv.ID, "@ana @beto @caio todos")
	runs := e.activeRuns(t, cv.ID)
	if len(runs) != 2 {
		t.Fatalf("runs: %d", len(runs))
	}
	ms := e.roomMessages(t, cv.ID)
	if !strings.Contains(ms[len(ms)-1].Body, "Limite de 2 execuções") {
		t.Fatalf("limit notice: %+v", ms[len(ms)-1])
	}
	// as duas já gastaram o teto da cadeia: a citação na resposta não acorda ninguém
	e.answer(t, runs[ana.ID].ID, domain.RunDone, adapter.Result{Summary: "@caio sua vez"})
	if r := e.activeRuns(t, cv.ID); len(r) != 1 || r[beto.ID].ID == (domain.ID{}) {
		t.Fatalf("over the per-message ceiling: %+v", r)
	}
}

func TestRoomFailureAndCancel(t *testing.T) {
	ctx := context.Background()
	e, ps, cv := roomFixture(t, nil)
	e.say(t, cv.ID, "@beto @caio vai")
	runs := e.activeRuns(t, cv.ID)
	e.answer(t, runs[ps[1].ID].ID, domain.RunFailed, adapter.Result{IsError: true, Error: "quebrou"})
	if _, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, runs[ps[2].ID].ID); err != nil {
		t.Fatal(err)
	}
	ms := e.roomMessages(t, cv.ID)
	var failed, cancelled bool
	for _, m := range ms {
		if m.Kind == domain.MessageSystem && strings.HasPrefix(m.Body, "Beto: A execução falhou") {
			failed = true
		}
		if m.Kind == domain.MessageSystem && strings.HasPrefix(m.Body, "Caio: A resposta foi cancelada") {
			cancelled = true
		}
	}
	if !failed || !cancelled {
		t.Fatalf("notices: %+v", ms)
	}
	if r := e.activeRuns(t, cv.ID); len(r) != 0 {
		t.Fatalf("runs left: %+v", r)
	}
}

func TestRoomParticipants(t *testing.T) {
	ctx := context.Background()
	e, ps, cv := roomFixture(t, nil)
	ana, beto := ps[0], ps[1]
	duda := e.persona(t, "Duda", nil)
	got, err := e.svc.AddRoomParticipant(ctx, domain.HumanActor, e.co.ID, cv.ID, duda.ID)
	if err != nil || len(got.Room.Active()) != 4 {
		t.Fatalf("add: %v %+v", err, got.Room)
	}
	// adicionar de novo não muda nada
	if again, err := e.svc.AddRoomParticipant(ctx, domain.HumanActor, e.co.ID, cv.ID, duda.ID); err != nil || len(again.Room.Active()) != 4 {
		t.Fatalf("idempotent add: %v", err)
	}
	// a moderadora sai: a próxima assume; a fila dela some
	e.say(t, cv.ID, "@ana oi")
	e.say(t, cv.ID, "@ana de novo")
	got, err = e.svc.RemoveRoomParticipant(ctx, domain.HumanActor, e.co.ID, cv.ID, ana.ID)
	if err != nil || got.Room.ModeratorPersonaID == nil || *got.Room.ModeratorPersonaID != beto.ID || got.PendingMessages != 0 {
		t.Fatalf("remove moderator: %v %+v", err, got.Room)
	}
	// quem saiu não é acordada por menção; sua run em andamento ainda responde, sem acordar ninguém
	run := e.activeRuns(t, cv.ID)[ana.ID]
	e.answer(t, run.ID, domain.RunDone, adapter.Result{Summary: "Saindo. @beto fica com isso"})
	if r := e.activeRuns(t, cv.ID); len(r) != 0 {
		t.Fatalf("removed persona woke someone: %+v", r)
	}
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "@ana volta?"); err != nil {
		t.Fatal(err) // ninguém citado na sala: vai para a moderadora (Beto)
	}
	if r := e.activeRuns(t, cv.ID); len(r) != 1 || r[beto.ID].ID == (domain.ID{}) {
		t.Fatalf("mention of a removed persona: %+v", r)
	}
	// volta: a mesma linha
	if got, err = e.svc.AddRoomParticipant(ctx, domain.HumanActor, e.co.ID, cv.ID, ana.ID); err != nil || len(got.Room.Active()) != 4 {
		t.Fatalf("re-add: %v", err)
	}
	// mínimo de duas
	for _, p := range []domain.Persona{ana, duda} {
		if _, err := e.svc.RemoveRoomParticipant(ctx, domain.HumanActor, e.co.ID, cv.ID, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	_, err = e.svc.RemoveRoomParticipant(ctx, domain.HumanActor, e.co.ID, cv.ID, beto.ID)
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.RemoveRoomParticipant(ctx, domain.HumanActor, e.co.ID, cv.ID, ana.ID)
	wantErr(t, err, domain.ErrNotFound)
	// moderadora tem de estar na sala
	_, err = e.svc.UpdateRoomSettings(ctx, domain.HumanActor, e.co.ID, cv.ID, service.RoomSettingsPatch{ModeratorID: &ana.ID})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.UpdateRoomSettings(ctx, domain.HumanActor, e.co.ID, cv.ID, service.RoomSettingsPatch{MaxHops: ptr(0)})
	wantErr(t, err, domain.ErrInvalid)
}

func TestRoomRoles(t *testing.T) {
	e := newEnv(t)
	a, b := e.persona(t, "Ana", nil), e.persona(t, "Beto", nil)
	viewer := mkUser(t, e, "v@x.com", domain.RoleMember)
	member := mkUser(t, e, "m@x.com", domain.RoleMember)
	join(t, e, viewer, domain.MemberViewer)
	join(t, e, member, domain.MemberMember)
	outsider := mkUser(t, e, "o@x.com", domain.RoleMember)
	in := service.CreateRoomInput{Title: "Sala", PersonaIDs: []domain.ID{a.ID, b.ID}}
	cv, err := e.svc.CreateRoom(as(member), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("member creates: %v", err)
	}
	if _, err := e.svc.GetConversation(as(viewer), e.co.ID, cv.ID); err != nil {
		t.Fatalf("viewer reads: %v", err)
	}
	_, err = e.svc.CreateRoom(as(viewer), domain.HumanActor, e.co.ID, in)
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.AddRoomParticipant(as(viewer), domain.HumanActor, e.co.ID, cv.ID, a.ID)
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.RemoveRoomParticipant(as(viewer), domain.HumanActor, e.co.ID, cv.ID, a.ID)
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.UpdateRoomSettings(as(viewer), domain.HumanActor, e.co.ID, cv.ID, service.RoomSettingsPatch{})
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.SendMessage(as(viewer), domain.HumanActor, e.co.ID, cv.ID, "@ana oi")
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.GetConversation(as(outsider), e.co.ID, cv.ID)
	wantErr(t, err, domain.ErrNotFound)
}

func TestRoomDoesNotChangeDirectChat(t *testing.T) {
	// o chat direto continua com uma run ativa por conversa e as mensagens recolhidas por run_id
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)
	if cv.Kind != domain.ConversationDirect || cv.Room != nil {
		t.Fatalf("direct: %+v", cv)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	e.say(t, cv.ID, "mais uma")
	if r := e.activeRuns(t, cv.ID); len(r) != 1 {
		t.Fatalf("direct runs: %d", len(r))
	}
	got, _ := e.svc.GetConversation(ctx, e.co.ID, cv.ID)
	if got.PendingMessages != 1 || got.ActiveRunID == nil || *got.ActiveRunID != run.ID {
		t.Fatalf("direct view: %+v", got)
	}
	list, _ := e.svc.ListConversations(ctx, e.co.ID, service.ConversationFilter{})
	if len(list) != 1 || list[0].PersonaName != "Ana" {
		t.Fatalf("direct list: %+v", list)
	}
}
