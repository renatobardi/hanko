package ui

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// chatListLimit is how many conversations the list shows.
const chatListLimit = 100

// chatThreadLimit is how many messages the thread page loads.
const chatThreadLimit = 200

type chatTab struct {
	key, label string
	status     domain.ConversationStatus
}

var chatTabs = []chatTab{
	{"abertas", i18n.N("Abertas"), domain.ConversationOpen},
	{"arquivadas", i18n.N("Arquivadas"), domain.ConversationArchived},
	{"todas", i18n.N("Todas"), ""},
}

func chatBadge(ctx context.Context, c service.ConversationView) views.Badge {
	switch {
	case c.ActiveRunID != nil:
		return views.Badge{Label: i18n.T(ctx, "Respondendo"), Class: "run", Live: true}
	case c.PendingMessages > 0:
		return views.Badge{Label: i18n.T(ctx, "Na fila"), Class: "warn"}
	case c.Status == domain.ConversationArchived:
		return views.Badge{Label: i18n.T(ctx, "Arquivada"), Class: "soft"}
	}
	return views.Badge{Label: i18n.T(ctx, "Aberta"), Class: "outline"}
}

func chatRow(ctx context.Context, c service.ConversationView, base string, now time.Time, loc *time.Location) views.ChatRow {
	return views.ChatRow{
		Href:        base + "/chat/" + c.ID.String(),
		Title:       c.Title,
		Persona:     c.PersonaName,
		PersonaHref: base + "/personas/" + c.PersonaSlug,
		Badge:       chatBadge(ctx, c),
		Last:        relTimeCtx(ctx, c.LastMessageAt, now, loc),
		Cost:        fmtUSDCtx(ctx, c.CostUSD),
		Archived:    c.Status == domain.ConversationArchived,
	}
}

func chatRows(ctx context.Context, list []service.ConversationView, base string, now time.Time, loc *time.Location) []views.ChatRow {
	out := make([]views.ChatRow, 0, len(list))
	for _, c := range list {
		out = append(out, chatRow(ctx, c, base, now, loc))
	}
	return out
}

func (s *server) handleChats(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderChats(w, r, co, http.StatusOK, "", "", r.URL.Query().Get("persona"))
}

// renderChats draws the list with the new-conversation form; errMsg/message/selected carry a failed submission.
func (s *server) renderChats(w http.ResponseWriter, r *http.Request, co domain.Company, status int, errMsg, message, selected string) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()

	cur := chatTabs[0]
	if k := r.URL.Query().Get("s"); k != "" {
		for _, t := range chatTabs {
			if t.key == k {
				cur = t
			}
		}
	}
	list, err := s.be.ListConversations(ctx, co.ID, service.ConversationFilter{Status: cur.status, Limit: chatListLimit})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := views.ChatsVM{
		Rows: chatRows(ctx, list, base, now, loc), NewAction: base + "/chat/nova",
		Message: message, Error: errMsg, Empty: len(personas) == 0,
		Subtitle: i18n.T(ctx, "Conversa direta com uma persona, fora de uma task."),
	}
	for _, t := range chatTabs {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), On: t.key == cur.key,
			Href: base + "/chat?s=" + t.key})
	}
	for _, p := range personas {
		vm.Personas = append(vm.Personas, views.Option{Value: p.ID.String(), Label: p.Name + " · " + p.Title,
			Selected: p.ID.String() == selected || p.Slug == selected})
	}
	sh := s.shell(r, co, "chat", i18n.T(ctx, "Chat"), views.Crumb{Label: i18n.T(ctx, "Chat")})
	s.render(w, r, status, views.ChatsPage(sh, vm))
}

func (s *server) handleCreateChat(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	message := strings.TrimSpace(r.PostFormValue("message"))
	selected := trimForm(r, "persona")
	pid, err := domain.ParseID(selected)
	if err != nil {
		s.renderChats(w, r, co, http.StatusUnprocessableEntity, i18n.T(ctx, "Escolha a persona."), message, "")
		return
	}
	if message == "" {
		s.renderChats(w, r, co, http.StatusUnprocessableEntity, i18n.T(ctx, "Escreva a primeira mensagem."), "", selected)
		return
	}
	cv, err := s.be.CreateConversation(ctx, actorFrom(ctx), co.ID, service.CreateConversationInput{PersonaID: pid, Message: message})
	if err != nil {
		if st, title, msg := friendly(ctx, err); st < 500 && st != http.StatusForbidden && st != http.StatusNotFound {
			s.renderChats(w, r, co, st, title+": "+msg, message, selected)
			return
		}
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/chat/"+cv.ID.String())
}

// chatConversation loads the conversation of the request path.
func (s *server) chatConversation(w http.ResponseWriter, r *http.Request, co domain.Company) (service.ConversationView, bool) {
	id, err := domain.ParseID(r.PathValue("conversation"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return service.ConversationView{}, false
	}
	cv, err := s.be.GetConversation(r.Context(), co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return service.ConversationView{}, false
	}
	return cv, true
}

// buildChatVM loads the messages and everything the conversation page and its fragments need.
func (s *server) buildChatVM(r *http.Request, co domain.Company, cv service.ConversationView) (views.ChatVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	msgs, err := s.be.ListConversationMessages(ctx, co.ID, cv.ID, chatThreadLimit)
	if err != nil {
		return views.ChatVM{}, err
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return views.ChatVM{}, err
	}
	names := s.lookup(ctx, personas, nil)
	vm := views.ChatVM{
		Title: cv.Title, Persona: cv.PersonaName, PersonaHref: base + "/personas/" + cv.PersonaSlug,
		Badge: chatBadge(ctx, cv), Archived: cv.Status == domain.ConversationArchived,
		Busy:       cv.ActiveRunID != nil || cv.PendingMessages > 0,
		ThreadHref: base + "/chat/" + cv.ID.String() + "/mensagens", SendHref: base + "/chat/" + cv.ID.String() + "/mensagens",
		ListHref: base + "/chat", Cost: fmtUSDCtx(ctx, cv.CostUSD),
	}
	if cv.Status == domain.ConversationArchived {
		vm.StatusHref = base + "/chat/" + cv.ID.String() + "/reabrir"
	} else {
		vm.StatusHref = base + "/chat/" + cv.ID.String() + "/arquivar"
	}
	if cv.ActiveRunID != nil {
		vm.RunHref = base + "/execucoes/" + cv.ActiveRunID.String()
	}
	for _, m := range msgs {
		cm := views.ChatMsg{Kind: string(m.Kind), Author: names.actorName(m.Author), When: fmtDateTime(ctx, m.CreatedAt, loc)}
		switch m.Kind {
		case domain.MessageSystem:
			cm.Body = views.HTML(m.Body) // shown as escaped plain text
			cm.Author = i18n.T(ctx, "Sistema")
		case domain.MessageHuman:
			cm.Body = renderMD(m.Body)
		default:
			cm.Body = renderMD(m.Body)
			cm.Author = cv.PersonaName
		}
		if m.RunID != nil && m.Kind != domain.MessageHuman {
			cm.RunHref = base + "/execucoes/" + m.RunID.String()
		}
		vm.Messages = append(vm.Messages, cm)
	}
	if p, err := s.be.GetPersona(ctx, co.ID, cv.PersonaSlug); err == nil && !p.Status.CanWork() && p.Status != domain.PersonaAwaitingGate {
		vm.Blocked = i18n.Tf(ctx, "A persona %s está %s e não pode responder agora.", p.Name, strings.ToLower(personaBadge(ctx, p.Status).Label))
	}
	return vm, nil
}

func (s *server) handleChat(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.chatConversation(w, r, co)
	if !ok {
		return
	}
	vm, err := s.buildChatVM(r, co, cv)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	sh := s.shell(r, co, "chat", cv.Title, views.Crumb{Label: i18n.T(r.Context(), "Chat"), Href: "/c/" + co.Slug + "/chat"}, views.Crumb{Label: cv.Title})
	s.render(w, r, http.StatusOK, views.ChatPage(sh, vm))
}

// handleChatThread is the htmx fragment that polls while the persona answers.
func (s *server) handleChatThread(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.chatConversation(w, r, co)
	if !ok {
		return
	}
	vm, err := s.buildChatVM(r, co, cv)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.ChatThread(vm))
}

func (s *server) handleChatSend(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cv, ok := s.chatConversation(w, r, co)
	if !ok {
		return
	}
	body := strings.TrimSpace(r.PostFormValue("message"))
	if body == "" {
		s.fail(w, r, &co, domain.Invalid(i18n.T(ctx, "mensagem"), i18n.T(ctx, "escreva algo antes de enviar")))
		return
	}
	if _, err := s.be.SendMessage(ctx, actorFrom(ctx), co.ID, cv.ID, body); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	if !isHX(r) {
		redirect(w, r, base+"/chat/"+cv.ID.String()+"#chat-thread")
		return
	}
	fresh, err := s.be.GetConversation(ctx, co.ID, cv.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.buildChatVM(r, co, fresh)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.ChatThread(vm))
}

func (s *server) handleChatStatus(st domain.ConversationStatus) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		ctx := r.Context()
		cv, ok := s.chatConversation(w, r, co)
		if !ok {
			return
		}
		if _, err := s.be.SetConversationStatus(ctx, actorFrom(ctx), co.ID, cv.ID, st); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		base := "/c/" + co.Slug
		if st == domain.ConversationArchived {
			redirect(w, r, base+"/chat?s=abertas")
			return
		}
		redirect(w, r, base+"/chat/"+cv.ID.String())
	}
}
