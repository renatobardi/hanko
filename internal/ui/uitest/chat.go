package uitest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func (f *Fake) canWrite() bool {
	return f.MyRoleIs == "" || f.MyRoleIs.AtLeast(domain.MemberMember)
}

func (f *Fake) ListConversations(_ context.Context, _ domain.ID, fl service.ConversationFilter) ([]service.ConversationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListConversations"); err != nil {
		return nil, err
	}
	var out []service.ConversationView
	for _, c := range f.Conversations {
		if fl.PersonaID != nil && c.PersonaID != *fl.PersonaID {
			continue
		}
		if fl.Status != "" && c.Status != fl.Status {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastMessageAt.After(out[j].LastMessageAt) })
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, nil
}

func (f *Fake) GetConversation(_ context.Context, _, id domain.ID) (service.ConversationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetConversation"); err != nil {
		return service.ConversationView{}, err
	}
	for _, c := range f.Conversations {
		if c.ID == id {
			return c, nil
		}
	}
	return service.ConversationView{}, domain.ErrNotFound
}

func (f *Fake) ListConversationMessages(_ context.Context, _, id domain.ID, limit int) ([]domain.ConversationMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListConversationMessages"); err != nil {
		return nil, err
	}
	var out []domain.ConversationMessage
	for _, m := range f.ConvMessages {
		if m.ConversationID == id {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// blockedError is the refusal of a persona that cannot answer; Error() is the reason (as in the service).
type blockedError struct{ reason string }

func (e *blockedError) Error() string { return e.reason }
func (e *blockedError) Unwrap() error { return domain.ErrPersonaUnavailable }

// blockedReason mirrors the service: a persona that cannot work refuses the message with a reason.
func (f *Fake) blockedReason(personaID domain.ID) error {
	for _, p := range f.Personas {
		if p.ID == personaID && !p.Status.CanWork() && p.Status != domain.PersonaAwaitingGate {
			why := map[domain.PersonaStatus]string{domain.PersonaPaused: "pausada", domain.PersonaError: "em erro", domain.PersonaOff: "desligada"}[p.Status]
			return &blockedError{fmt.Sprintf("a persona %s está %s e não pode responder agora", p.Name, why)}
		}
	}
	return nil
}

func (f *Fake) CreateConversation(_ context.Context, actor domain.Actor, companyID domain.ID, in service.CreateConversationInput) (service.ConversationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateConversation")
	if err := f.fail("CreateConversation"); err != nil {
		return service.ConversationView{}, err
	}
	if !f.canWrite() {
		return service.ConversationView{}, domain.ErrForbidden
	}
	var persona *domain.Persona
	for i := range f.Personas {
		if f.Personas[i].ID == in.PersonaID {
			persona = &f.Personas[i]
		}
	}
	if persona == nil {
		return service.ConversationView{}, domain.ErrNotFound
	}
	msg := strings.TrimSpace(in.Message)
	if err := f.blockedReason(persona.ID); err != nil && msg != "" {
		return service.ConversationView{}, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = domain.TitleFromMessage(msg)
	}
	if title == "" {
		return service.ConversationView{}, domain.Invalid("title", "informe um título ou a primeira mensagem")
	}
	now := Now()
	c := service.ConversationView{
		Conversation: domain.Conversation{ID: domain.NewID(), CompanyID: companyID, PersonaID: persona.ID, Title: title,
			Status: domain.ConversationOpen, CreatedBy: actor, CreatedAt: now, UpdatedAt: now, LastMessageAt: now},
		PersonaName: persona.Name, PersonaSlug: persona.Slug,
	}
	if msg != "" {
		f.ConvMessages = append(f.ConvMessages, domain.ConversationMessage{ID: domain.NewID(), CompanyID: companyID, ConversationID: c.ID,
			Author: actor, Kind: domain.MessageHuman, Body: msg, CreatedAt: now})
		c.PendingMessages = 1
		runID := domain.NewID()
		c.ActiveRunID = &runID
	}
	f.Conversations = append(f.Conversations, c)
	return c, nil
}

func (f *Fake) SendMessage(_ context.Context, actor domain.Actor, companyID, id domain.ID, body string) (domain.ConversationMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SendMessage")
	if err := f.fail("SendMessage"); err != nil {
		return domain.ConversationMessage{}, err
	}
	if !f.canWrite() {
		return domain.ConversationMessage{}, domain.ErrForbidden
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return domain.ConversationMessage{}, domain.Invalid("message", "escreva algo antes de enviar")
	}
	for i := range f.Conversations {
		c := &f.Conversations[i]
		if c.ID != id {
			continue
		}
		if c.Status != domain.ConversationOpen {
			return domain.ConversationMessage{}, domain.Invalid("conversation", "a conversa está arquivada; reabra para continuar")
		}
		if err := f.blockedReason(c.PersonaID); err != nil {
			return domain.ConversationMessage{}, err
		}
		now := Now()
		m := domain.ConversationMessage{ID: domain.NewID(), CompanyID: companyID, ConversationID: id, Author: actor,
			Kind: domain.MessageHuman, Body: body, CreatedAt: now}
		f.ConvMessages = append(f.ConvMessages, m)
		c.PendingMessages++
		if c.ActiveRunID == nil {
			runID := domain.NewID()
			c.ActiveRunID = &runID
		}
		c.LastMessageAt, c.UpdatedAt = now, now
		return m, nil
	}
	return domain.ConversationMessage{}, domain.ErrNotFound
}

func (f *Fake) SetConversationStatus(_ context.Context, _ domain.Actor, _, id domain.ID, st domain.ConversationStatus) (service.ConversationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetConversationStatus")
	if err := f.fail("SetConversationStatus"); err != nil {
		return service.ConversationView{}, err
	}
	if !f.canWrite() {
		return service.ConversationView{}, domain.ErrForbidden
	}
	for i := range f.Conversations {
		if f.Conversations[i].ID == id {
			f.Conversations[i].Status = st
			return f.Conversations[i], nil
		}
	}
	return service.ConversationView{}, domain.ErrNotFound
}

// PersonaReplies plays the executor: the persona answers the waiting messages of the conversation with body
// (reply of run runID) and the conversation goes idle.
func (f *Fake) PersonaReplies(convID domain.ID, runID *domain.ID, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Conversations {
		c := &f.Conversations[i]
		if c.ID != convID {
			continue
		}
		now := Now().Add(time.Second)
		pid := c.PersonaID
		f.ConvMessages = append(f.ConvMessages, domain.ConversationMessage{ID: domain.NewID(), CompanyID: c.CompanyID, ConversationID: convID,
			Author: domain.Actor{Type: domain.ActorPersona, ID: &pid}, Kind: domain.MessagePersona, Body: body, RunID: runID, CreatedAt: now})
		c.PendingMessages, c.ActiveRunID = 0, nil
		c.LastMessageAt, c.UpdatedAt = now, now
	}
}

// Working marks the conversation as being answered by run runID.
func (f *Fake) Working(convID, runID domain.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Conversations {
		if f.Conversations[i].ID == convID {
			f.Conversations[i].ActiveRunID = &runID
		}
	}
}

// Stranded leaves n messages waiting with no run to answer them (the run was cancelled or interrupted).
func (f *Fake) Stranded(convID domain.ID, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Conversations {
		if f.Conversations[i].ID == convID {
			f.Conversations[i].PendingMessages, f.Conversations[i].ActiveRunID = n, nil
		}
	}
}
