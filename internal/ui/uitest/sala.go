package uitest

import (
	"context"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Sala (ADR-0033): as salas ficam em f.Conversations com Kind room e Room preenchido.

func kindOf(c service.ConversationView) domain.ConversationKind {
	if c.Kind == "" {
		return domain.ConversationDirect
	}
	return c.Kind
}

func inRoom(c service.ConversationView, personaID domain.ID) bool {
	if c.Room == nil {
		return false
	}
	for _, p := range c.Room.Active() {
		if p.PersonaID == personaID {
			return true
		}
	}
	return false
}

func (f *Fake) participant(p domain.Persona, at time.Time) service.RoomParticipantView {
	return service.RoomParticipantView{RoomParticipant: domain.RoomParticipant{PersonaID: p.ID, AddedAt: at, AddedBy: domain.HumanActor},
		Name: p.Name, Slug: p.Slug, Title: p.Title, Icon: p.Icon, Status: p.Status}
}

func (f *Fake) personaByID(id domain.ID) (domain.Persona, bool) {
	for _, p := range f.Personas {
		if p.ID == id {
			return p, true
		}
	}
	return domain.Persona{}, false
}

// finishRoom recalcula a moderadora e o teto efetivos, como o service.
func finishRoom(r *service.RoomView) {
	active := r.Active()
	r.MaxRunsPerMessage = r.Settings.EffectiveMaxRuns(len(active))
	r.ModeratorPersonaID = nil
	for _, p := range active {
		if r.Settings.ModeratorPersonaID != nil && *r.Settings.ModeratorPersonaID == p.PersonaID {
			id := p.PersonaID
			r.ModeratorPersonaID = &id
		}
	}
	if r.ModeratorPersonaID == nil && len(active) > 0 {
		id := active[0].PersonaID
		r.ModeratorPersonaID = &id
	}
}

// AddRoom cria uma sala com as personas (pelo nome ou slug) e devolve a visão dela.
func (f *Fake) AddRoom(title string, personas ...string) service.ConversationView {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := Now()
	co := domain.ID{}
	if len(f.Companies) > 0 {
		co = f.Companies[0].ID
	}
	rv := &service.RoomView{Settings: domain.RoomSettings{Mode: domain.RoomMentions, MaxHops: domain.DefaultRoomHops}}
	for _, ref := range personas {
		for _, p := range f.Personas {
			if p.Name == ref || p.Slug == ref {
				rv.Participants = append(rv.Participants, f.participant(p, now))
			}
		}
	}
	finishRoom(rv)
	cv := service.ConversationView{Conversation: domain.Conversation{ID: domain.NewID(), CompanyID: co, Kind: domain.ConversationRoom, Title: title,
		Status: domain.ConversationOpen, CreatedBy: domain.HumanActor, CreatedAt: now, UpdatedAt: now, LastMessageAt: now}, Room: rv}
	f.Conversations = append(f.Conversations, cv)
	return cv
}

// RoomThinking marca a persona como respondendo (run ativa) na sala.
func (f *Fake) RoomThinking(convID, personaID domain.ID) domain.ID {
	f.mu.Lock()
	defer f.mu.Unlock()
	runID := domain.NewID()
	for i := range f.Conversations {
		c := &f.Conversations[i]
		if c.ID != convID || c.Room == nil {
			continue
		}
		for j := range c.Room.Participants {
			if c.Room.Participants[j].PersonaID == personaID {
				c.Room.Participants[j].ActiveRunID = &runID
				c.ActiveRunID = &runID
			}
		}
	}
	return runID
}

// RoomSays grava uma mensagem da persona na sala.
func (f *Fake) RoomSays(convID, personaID domain.ID, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, _ := f.personaByID(personaID)
	pid := personaID
	runID := domain.NewID()
	f.ConvMessages = append(f.ConvMessages, domain.ConversationMessage{ID: domain.NewID(), ConversationID: convID, Kind: domain.MessagePersona,
		Author: domain.PersonaActor(personaID), PersonaID: &pid, PersonaName: p.Name, Body: body, RunID: &runID, CreatedAt: Now().Add(time.Second)})
}

func (f *Fake) CreateRoom(_ context.Context, actor domain.Actor, companyID domain.ID, in service.CreateRoomInput) (service.ConversationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateRoom")
	if err := f.fail("CreateRoom"); err != nil {
		return service.ConversationView{}, err
	}
	if !f.canWrite() {
		return service.ConversationView{}, domain.ErrForbidden
	}
	if len(in.PersonaIDs) < domain.MinRoomParticipants || len(in.PersonaIDs) > domain.MaxRoomParticipants {
		return service.ConversationView{}, domain.Invalid("personas", "uma sala tem de 2 a 12 personas")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = domain.TitleFromMessage(in.Message)
	}
	if title == "" {
		return service.ConversationView{}, domain.Invalid("title", "informe um título ou a primeira mensagem")
	}
	now := Now()
	mode := in.Mode
	if mode == "" {
		mode = domain.RoomMentions
	}
	rv := &service.RoomView{Settings: domain.RoomSettings{Mode: mode, ModeratorPersonaID: in.ModeratorID, MaxHops: in.MaxHops,
		MaxRunsPerMessage: in.MaxRunsPerMessage}.Normalized()}
	for _, id := range in.PersonaIDs {
		p, ok := f.personaByID(id)
		if !ok {
			return service.ConversationView{}, domain.ErrNotFound
		}
		rv.Participants = append(rv.Participants, f.participant(p, now))
	}
	finishRoom(rv)
	cv := service.ConversationView{Conversation: domain.Conversation{ID: domain.NewID(), CompanyID: companyID, Kind: domain.ConversationRoom,
		Title: title, Status: domain.ConversationOpen, CreatedBy: actor, CreatedAt: now, UpdatedAt: now, LastMessageAt: now}, Room: rv}
	if msg := strings.TrimSpace(in.Message); msg != "" {
		f.ConvMessages = append(f.ConvMessages, domain.ConversationMessage{ID: domain.NewID(), CompanyID: companyID, ConversationID: cv.ID,
			Author: actor, Kind: domain.MessageHuman, Body: msg, CreatedAt: now})
	}
	f.Conversations = append(f.Conversations, cv)
	return cv, nil
}

func (f *Fake) roomByID(id domain.ID) (*service.ConversationView, error) {
	for i := range f.Conversations {
		if f.Conversations[i].ID == id {
			if f.Conversations[i].Room == nil {
				return nil, domain.Invalid("conversation", "esta conversa é um chat direto, não uma sala")
			}
			return &f.Conversations[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *Fake) AddRoomParticipant(_ context.Context, _ domain.Actor, _, convID, personaID domain.ID) (service.ConversationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("AddRoomParticipant")
	if err := f.fail("AddRoomParticipant"); err != nil {
		return service.ConversationView{}, err
	}
	if !f.canWrite() {
		return service.ConversationView{}, domain.ErrForbidden
	}
	cv, err := f.roomByID(convID)
	if err != nil {
		return service.ConversationView{}, err
	}
	p, ok := f.personaByID(personaID)
	if !ok {
		return service.ConversationView{}, domain.ErrNotFound
	}
	if !inRoom(*cv, personaID) {
		cv.Room.Participants = append(cv.Room.Participants, f.participant(p, Now()))
	}
	finishRoom(cv.Room)
	return *cv, nil
}

func (f *Fake) RemoveRoomParticipant(_ context.Context, _ domain.Actor, _, convID, personaID domain.ID) (service.ConversationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RemoveRoomParticipant")
	if err := f.fail("RemoveRoomParticipant"); err != nil {
		return service.ConversationView{}, err
	}
	if !f.canWrite() {
		return service.ConversationView{}, domain.ErrForbidden
	}
	cv, err := f.roomByID(convID)
	if err != nil {
		return service.ConversationView{}, err
	}
	if !inRoom(*cv, personaID) {
		return service.ConversationView{}, domain.ErrNotFound
	}
	if len(cv.Room.Active()) <= domain.MinRoomParticipants {
		return service.ConversationView{}, domain.Invalid("personas", "a sala precisa de pelo menos 2 personas")
	}
	now := Now()
	for i := range cv.Room.Participants {
		if cv.Room.Participants[i].PersonaID == personaID {
			cv.Room.Participants[i].RemovedAt = &now
		}
	}
	finishRoom(cv.Room)
	return *cv, nil
}

func (f *Fake) UpdateRoomSettings(_ context.Context, _ domain.Actor, _, convID domain.ID, in service.RoomSettingsPatch) (service.ConversationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateRoomSettings")
	if err := f.fail("UpdateRoomSettings"); err != nil {
		return service.ConversationView{}, err
	}
	if !f.canWrite() {
		return service.ConversationView{}, domain.ErrForbidden
	}
	cv, err := f.roomByID(convID)
	if err != nil {
		return service.ConversationView{}, err
	}
	st := &cv.Room.Settings
	if in.Mode != nil {
		if !in.Mode.Valid() {
			return service.ConversationView{}, domain.Invalid("mode", "modo desconhecido")
		}
		st.Mode = *in.Mode
	}
	if in.ModeratorID != nil {
		if !inRoom(*cv, *in.ModeratorID) {
			return service.ConversationView{}, domain.Invalid("moderator", "a moderadora precisa ser uma das personas da sala")
		}
		id := *in.ModeratorID
		st.ModeratorPersonaID = &id
	}
	if in.MaxHops != nil {
		if *in.MaxHops < 1 || *in.MaxHops > domain.MaxRoomHops {
			return service.ConversationView{}, domain.Invalid("max_hops", "use de 1 a 5 saltos")
		}
		st.MaxHops = *in.MaxHops
	}
	if in.MaxRunsPerMessage != nil {
		st.MaxRunsPerMessage = *in.MaxRunsPerMessage
	}
	finishRoom(cv.Room)
	return *cv, nil
}
