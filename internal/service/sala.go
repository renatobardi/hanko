package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Sala (ADR-0033): uma conversa com várias personas. Reaproveita o chat (ADR-0027) — a mesma conversa, a
// mesma run de chat, os mesmos orçamentos, trava semanal, ledger e log — com três diferenças: quem responde é
// decidido por mensagem (menções, moderadora ou todas), cada persona tem a sua run e a sua fila na sala, e a
// resposta de uma persona que menciona outra acorda essa outra, com teto de saltos e de runs por mensagem
// humana para duas personas não conversarem para sempre.

// RoomView é a configuração e os participantes de uma sala.
type RoomView struct {
	Settings domain.RoomSettings `json:"settings"`
	// ModeratorPersonaID é a moderadora efetiva: a configurada, se ainda está na sala, ou a primeira participante.
	ModeratorPersonaID *domain.ID `json:"moderator_persona_id,omitempty"`
	// MaxRunsPerMessage é o teto efetivo (o configurado, ou o número de participantes ativos).
	MaxRunsPerMessage int                   `json:"max_runs_per_message"`
	Participants      []RoomParticipantView `json:"participants"`
}

// RoomParticipantView é uma persona da sala com os dados de exibição.
type RoomParticipantView struct {
	domain.RoomParticipant
	Name   string               `json:"name"`
	Slug   string               `json:"slug"`
	Title  string               `json:"title,omitempty"`
	Icon   string               `json:"icon,omitempty"`
	Status domain.PersonaStatus `json:"status"`
	// ActiveRunID é a run da persona nesta sala (na fila ou rodando); nil quando está ociosa.
	ActiveRunID *domain.ID `json:"active_run_id,omitempty"`
	// Pending são as mensagens que esperam a persona terminar a run em andamento.
	Pending int `json:"pending"`
}

// Active devolve só as personas que estão na sala, na ordem em que entraram.
func (r RoomView) Active() []RoomParticipantView {
	var out []RoomParticipantView
	for _, p := range r.Participants {
		if p.Active() {
			out = append(out, p)
		}
	}
	return out
}

// Participant devolve a persona da sala (ativa ou não).
func (r RoomView) Participant(id domain.ID) (RoomParticipantView, bool) {
	for _, p := range r.Participants {
		if p.PersonaID == id {
			return p, true
		}
	}
	return RoomParticipantView{}, false
}

// RoomContext é o que a run de uma persona na sala lê além do chat: quem está na sala, o modo e o salto.
type RoomContext struct {
	Participants []RoomParticipantView
	Mode         domain.RoomMode
	ModeratorID  *domain.ID
	// Hop é o salto desta run a partir da mensagem humana (0 = respondendo a um humano).
	Hop     int
	MaxHops int
}

// CreateRoomInput abre uma sala. Title vazio deriva da primeira mensagem; ModeratorID vazio = a primeira persona.
type CreateRoomInput struct {
	Title             string
	PersonaIDs        []domain.ID
	ModeratorID       *domain.ID
	Mode              domain.RoomMode
	MaxHops           int // 0 = padrão (2)
	MaxRunsPerMessage int // 0 = número de participantes
	Message           string
}

// RoomSettingsPatch muda a configuração de uma sala; campos nil ficam como estão.
type RoomSettingsPatch struct {
	Mode              *domain.RoomMode
	ModeratorID       *domain.ID
	MaxHops           *int
	MaxRunsPerMessage *int // 0 = número de participantes
}

// ---------- leitura ----------

func roomSettingsOf(raw []byte) domain.RoomSettings {
	var st domain.RoomSettings
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &st) // configuração ilegível volta aos padrões
	}
	return st.Normalized()
}

// roomViewOf semeia a parte da sala de uma conversa (só a configuração; os participantes vêm de attachRooms).
func roomViewOf(c store.Conversation) *RoomView {
	if domain.ConversationKind(c.Kind) != domain.ConversationRoom {
		return nil
	}
	return &RoomView{Settings: roomSettingsOf(c.RoomSettings)}
}

func participantFromRow(r store.ListRoomParticipantsRow) RoomParticipantView {
	return RoomParticipantView{
		RoomParticipant: domain.RoomParticipant{PersonaID: r.PersonaID, AddedAt: r.AddedAt, AddedBy: actorOf(r.AddedByType, r.AddedByID), RemovedAt: r.RemovedAt},
		Name:            r.PersonaName, Slug: r.PersonaSlug, Title: r.PersonaTitle, Icon: r.PersonaIcon,
		Status: domain.PersonaStatus(r.PersonaStatus), ActiveRunID: r.ActiveRunID, Pending: int(r.Pending),
	}
}

// finish completa a moderadora e o teto efetivos depois de os participantes chegarem.
func (r *RoomView) finish() {
	active := r.Active()
	r.MaxRunsPerMessage = r.Settings.EffectiveMaxRuns(len(active))
	r.ModeratorPersonaID = nil
	if m := r.Settings.ModeratorPersonaID; m != nil {
		for _, p := range active {
			if p.PersonaID == *m {
				id := p.PersonaID
				r.ModeratorPersonaID = &id
			}
		}
	}
	if r.ModeratorPersonaID == nil && len(active) > 0 {
		id := active[0].PersonaID
		r.ModeratorPersonaID = &id
	}
}

// attachRooms preenche os participantes das salas da lista com uma consulta só.
func attachRooms(ctx context.Context, q *store.Queries, companyID domain.ID, vs []*ConversationView) error {
	var ids []domain.ID
	byID := map[domain.ID]*ConversationView{}
	for _, v := range vs {
		if v.Room != nil {
			ids = append(ids, v.ID)
			byID[v.ID] = v
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.ListRoomParticipants(ctx, store.ListRoomParticipantsParams{CompanyID: companyID, ConversationIds: ids})
	if err != nil {
		return fmt.Errorf("list room participants: %w", err)
	}
	for _, r := range rows {
		if v := byID[r.ConversationID]; v != nil {
			v.Room.Participants = append(v.Room.Participants, participantFromRow(r))
		}
	}
	for _, v := range byID {
		v.Room.finish()
		// a sala não tem uma run ativa única: ActiveRunID fica com a primeira, para quem só quer saber se há
		for _, p := range v.Room.Participants {
			if p.ActiveRunID != nil {
				v.ActiveRunID = p.ActiveRunID
				break
			}
		}
	}
	return nil
}

// loadRoom lê a configuração e os participantes de uma sala (dentro ou fora de uma transação).
func loadRoom(ctx context.Context, q *store.Queries, conv store.Conversation) (RoomView, error) {
	rv := roomViewOf(conv)
	if rv == nil {
		return RoomView{}, domain.Invalid("conversation", "não é uma sala")
	}
	rows, err := q.ListRoomParticipants(ctx, store.ListRoomParticipantsParams{CompanyID: conv.CompanyID, ConversationIds: []domain.ID{conv.ID}})
	if err != nil {
		return RoomView{}, fmt.Errorf("list room participants: %w", err)
	}
	for _, r := range rows {
		rv.Participants = append(rv.Participants, participantFromRow(r))
	}
	rv.finish()
	return *rv, nil
}

// attachRoomMessageInfo completa as mensagens de uma sala com o nome da persona e as entregas.
func attachRoomMessageInfo(ctx context.Context, q *store.Queries, companyID, convID domain.ID, msgs []domain.ConversationMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	rows, err := q.ListRoomParticipants(ctx, store.ListRoomParticipantsParams{CompanyID: companyID, ConversationIds: []domain.ID{convID}})
	if err != nil {
		return fmt.Errorf("list room participants: %w", err)
	}
	names := make(map[domain.ID]string, len(rows))
	for _, r := range rows {
		names[r.PersonaID] = r.PersonaName
	}
	ids := make([]domain.ID, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	dels, err := q.ListMessageDeliveries(ctx, store.ListMessageDeliveriesParams{CompanyID: companyID, MessageIds: ids})
	if err != nil {
		return fmt.Errorf("list message deliveries: %w", err)
	}
	byMsg := map[domain.ID][]domain.MessageDelivery{}
	for _, d := range dels {
		byMsg[d.MessageID] = append(byMsg[d.MessageID], domain.MessageDelivery{PersonaID: d.PersonaID, RunID: d.RunID})
	}
	for i := range msgs {
		if msgs[i].PersonaID != nil {
			msgs[i].PersonaName = names[*msgs[i].PersonaID]
		}
		msgs[i].Deliveries = byMsg[msgs[i].ID]
	}
	return nil
}

// ---------- escrita ----------

func validRoomLimits(maxHops, maxRuns int) error {
	if maxHops < 0 || maxHops > domain.MaxRoomHops {
		return domain.Invalid("max_hops", fmt.Sprintf("use de 1 a %d saltos", domain.MaxRoomHops))
	}
	if maxRuns < 0 || maxRuns > domain.MaxRoomRunsPerMessage {
		return domain.Invalid("max_runs_per_message", fmt.Sprintf("use de 1 a %d execuções (0 = número de participantes)", domain.MaxRoomRunsPerMessage))
	}
	return nil
}

func encodeRoomSettings(st domain.RoomSettings) ([]byte, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("encode room settings: %w", err)
	}
	return b, nil
}

// CreateRoom opens a room with 2..12 personas. With a message it also sends it; when no persona can answer it
// now nothing is created and the reason is the error (as in CreateConversation).
func (s *Service) CreateRoom(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateRoomInput) (ConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ConversationView{}, err
	}
	if err := requireHuman(actor); err != nil {
		return ConversationView{}, err
	}
	var msg string
	if strings.TrimSpace(in.Message) != "" {
		var err error
		if msg, err = cleanMessage(in.Message); err != nil {
			return ConversationView{}, err
		}
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = domain.TitleFromMessage(msg)
	}
	if title == "" {
		return ConversationView{}, domain.Invalid("title", "informe um título ou a primeira mensagem")
	}
	if n := len([]rune(title)); n > domain.MaxConversationTitle {
		return ConversationView{}, domain.Invalid("title", fmt.Sprintf("o título passa de %d caracteres", domain.MaxConversationTitle))
	}
	var ids []domain.ID
	seen := map[domain.ID]bool{}
	for _, id := range in.PersonaIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) < domain.MinRoomParticipants || len(ids) > domain.MaxRoomParticipants {
		return ConversationView{}, domain.Invalid("personas", fmt.Sprintf("uma sala tem de %d a %d personas", domain.MinRoomParticipants, domain.MaxRoomParticipants))
	}
	mode := in.Mode
	if mode == "" {
		mode = domain.RoomMentions
	}
	if !mode.Valid() {
		return ConversationView{}, domain.Invalid("mode", fmt.Sprintf("modo %q desconhecido; use mentions ou all", mode))
	}
	if err := validRoomLimits(in.MaxHops, in.MaxRunsPerMessage); err != nil {
		return ConversationView{}, err
	}
	moderator := ids[0]
	if in.ModeratorID != nil {
		if !seen[*in.ModeratorID] {
			return ConversationView{}, domain.Invalid("moderator", "a moderadora precisa ser uma das personas da sala")
		}
		moderator = *in.ModeratorID
	}
	st := domain.RoomSettings{Mode: mode, ModeratorPersonaID: &moderator, MaxHops: in.MaxHops, MaxRunsPerMessage: in.MaxRunsPerMessage}.Normalized()
	raw, err := encodeRoomSettings(st)
	if err != nil {
		return ConversationView{}, err
	}
	var (
		out     ConversationView
		blocked error
	)
	err = s.withTx(ctx, func(t *txc) error {
		for _, id := range ids {
			if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: id}); err != nil {
				return mapErr(err, "get persona")
			}
		}
		conv, err := t.q.CreateRoom(ctx, store.CreateRoomParams{ID: domain.NewID(), CompanyID: companyID, Title: title,
			CreatedByType: string(actor.Type), CreatedByID: actor.ID, RoomSettings: raw, Now: t.now})
		if err != nil {
			return mapErr(err, "create room")
		}
		for _, id := range ids {
			if _, err := t.q.AddRoomParticipant(ctx, store.AddRoomParticipantParams{CompanyID: companyID, ConversationID: conv.ID,
				PersonaID: id, Now: t.now, AddedByType: string(actor.Type), AddedByID: actor.ID}); err != nil {
				return mapErr(err, "add room participant")
			}
		}
		// Como no chat: com a primeira mensagem, ao menos uma persona precisa aceitar antes de a sala ser
		// anunciada; uma sala recusada é apagada e não deixa atividade. Os efeitos da recusa (pausa, alertas) ficam.
		var plan *roomPlan
		if msg != "" {
			room, err := loadRoom(ctx, t.q, conv)
			if err != nil {
				return err
			}
			p, berr, err := t.planRoomMessage(conv, room, msg)
			if err != nil {
				return err
			}
			if berr != nil {
				blocked = berr
				return t.q.DeleteConversation(ctx, store.DeleteConversationParams{CompanyID: companyID, ID: conv.ID})
			}
			plan = &p
		}
		if err := t.activity(actor, companyID, "conversation.created", "conversation", conv.ID, map[string]any{
			"kind": domain.ConversationRoom, "title": title, "personas": ids, "mode": st.Mode, "moderator_persona_id": moderator}, ""); err != nil {
			return err
		}
		if plan != nil {
			if _, err := t.storeRoomMessage(conv, actor, msg, *plan); err != nil {
				return err
			}
		}
		out, err = loadConversationView(ctx, t.q, companyID, conv.ID)
		return err
	})
	if err != nil {
		return ConversationView{}, err
	}
	if blocked != nil {
		return ConversationView{}, blocked
	}
	return out, nil
}

// lockRoom trava a linha da sala (o ponto de serialização da conversa, como no chat) e confere que é uma sala.
func (t *txc) lockRoom(companyID, id domain.ID) (store.Conversation, RoomView, error) {
	conv, err := t.q.GetConversationForUpdate(t.ctx, store.GetConversationForUpdateParams{CompanyID: companyID, ID: id})
	if err != nil {
		return conv, RoomView{}, mapErr(err, "get conversation")
	}
	if domain.ConversationKind(conv.Kind) != domain.ConversationRoom {
		return conv, RoomView{}, domain.Invalid("conversation", "esta conversa é um chat direto, não uma sala")
	}
	room, err := loadRoom(t.ctx, t.q, conv)
	return conv, room, err
}

// AddRoomParticipant puts a persona in the room (or back in it). Adding one who is already there changes nothing.
func (s *Service) AddRoomParticipant(ctx context.Context, actor domain.Actor, companyID, convID, personaID domain.ID) (ConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ConversationView{}, err
	}
	if err := requireHuman(actor); err != nil {
		return ConversationView{}, err
	}
	var out ConversationView
	err := s.withTx(ctx, func(t *txc) error {
		conv, room, err := t.lockRoom(companyID, convID)
		if err != nil {
			return err
		}
		p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		if cur, ok := room.Participant(p.ID); !ok || !cur.Active() {
			if len(room.Active()) >= domain.MaxRoomParticipants {
				return domain.Invalid("personas", fmt.Sprintf("a sala já tem %d personas (o máximo)", domain.MaxRoomParticipants))
			}
			if _, err := t.q.AddRoomParticipant(ctx, store.AddRoomParticipantParams{CompanyID: companyID, ConversationID: conv.ID,
				PersonaID: p.ID, Now: t.now, AddedByType: string(actor.Type), AddedByID: actor.ID}); err != nil {
				return mapErr(err, "add room participant")
			}
			if err := t.addRoomMessage(conv, domain.MessageSystem, nil, &p.ID, p.Name+" entrou na sala.", nil, nil, nil); err != nil {
				return err
			}
			if err := t.activity(actor, companyID, "conversation.participant_added", "conversation", conv.ID,
				map[string]any{"kind": domain.ConversationRoom, "persona_id": p.ID}, ""); err != nil {
				return err
			}
		}
		out, err = loadConversationView(ctx, t.q, companyID, conv.ID)
		return err
	})
	return out, err
}

// RemoveRoomParticipant takes a persona out of the room. A run of hers in progress still answers, but she is no
// longer woken and her waiting messages are dropped. The room keeps at least two personas; when the moderator
// leaves, the first remaining persona takes over.
func (s *Service) RemoveRoomParticipant(ctx context.Context, actor domain.Actor, companyID, convID, personaID domain.ID) (ConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ConversationView{}, err
	}
	if err := requireHuman(actor); err != nil {
		return ConversationView{}, err
	}
	var out ConversationView
	err := s.withTx(ctx, func(t *txc) error {
		conv, room, err := t.lockRoom(companyID, convID)
		if err != nil {
			return err
		}
		cur, ok := room.Participant(personaID)
		if !ok || !cur.Active() {
			return fmt.Errorf("persona is not in the room: %w", domain.ErrNotFound)
		}
		if len(room.Active()) <= domain.MinRoomParticipants {
			return domain.Invalid("personas", fmt.Sprintf("a sala precisa de pelo menos %d personas", domain.MinRoomParticipants))
		}
		if _, err := t.q.RemoveRoomParticipant(ctx, store.RemoveRoomParticipantParams{Now: &t.now, CompanyID: companyID,
			ConversationID: conv.ID, PersonaID: personaID}); err != nil {
			return mapErr(err, "remove room participant")
		}
		dropped, err := t.q.DeletePendingDeliveries(ctx, store.DeletePendingDeliveriesParams{ConversationID: conv.ID, PersonaID: personaID})
		if err != nil {
			return fmt.Errorf("drop pending deliveries: %w", err)
		}
		data := map[string]any{"kind": domain.ConversationRoom, "persona_id": personaID, "dropped_messages": dropped}
		if room.ModeratorPersonaID != nil && *room.ModeratorPersonaID == personaID {
			for _, p := range room.Active() {
				if p.PersonaID != personaID {
					next := p.PersonaID
					st := room.Settings
					st.ModeratorPersonaID = &next
					raw, err := encodeRoomSettings(st)
					if err != nil {
						return err
					}
					if _, err := t.q.SetRoomSettings(ctx, store.SetRoomSettingsParams{RoomSettings: raw, Now: t.now, CompanyID: companyID, ID: conv.ID}); err != nil {
						return mapErr(err, "set room settings")
					}
					data["moderator_persona_id"] = next
					break
				}
			}
		}
		if err := t.addRoomMessage(conv, domain.MessageSystem, nil, &personaID, cur.Name+" saiu da sala.", nil, nil, nil); err != nil {
			return err
		}
		if err := t.activity(actor, companyID, "conversation.participant_removed", "conversation", conv.ID, data, ""); err != nil {
			return err
		}
		out, err = loadConversationView(ctx, t.q, companyID, conv.ID)
		return err
	})
	return out, err
}

// UpdateRoomSettings changes the mode, the moderator or the limits of a room.
func (s *Service) UpdateRoomSettings(ctx context.Context, actor domain.Actor, companyID, convID domain.ID, in RoomSettingsPatch) (ConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ConversationView{}, err
	}
	if err := requireHuman(actor); err != nil {
		return ConversationView{}, err
	}
	var out ConversationView
	err := s.withTx(ctx, func(t *txc) error {
		conv, room, err := t.lockRoom(companyID, convID)
		if err != nil {
			return err
		}
		st := room.Settings
		if in.Mode != nil {
			if !in.Mode.Valid() {
				return domain.Invalid("mode", fmt.Sprintf("modo %q desconhecido; use mentions ou all", *in.Mode))
			}
			st.Mode = *in.Mode
		}
		if in.ModeratorID != nil {
			if p, ok := room.Participant(*in.ModeratorID); !ok || !p.Active() {
				return domain.Invalid("moderator", "a moderadora precisa ser uma das personas da sala")
			}
			id := *in.ModeratorID
			st.ModeratorPersonaID = &id
		}
		if in.MaxHops != nil {
			if *in.MaxHops < 1 || *in.MaxHops > domain.MaxRoomHops {
				return domain.Invalid("max_hops", fmt.Sprintf("use de 1 a %d saltos", domain.MaxRoomHops))
			}
			st.MaxHops = *in.MaxHops
		}
		if in.MaxRunsPerMessage != nil {
			if err := validRoomLimits(st.MaxHops, *in.MaxRunsPerMessage); err != nil {
				return err
			}
			st.MaxRunsPerMessage = *in.MaxRunsPerMessage
		}
		if !sameSettings(st, room.Settings) {
			raw, err := encodeRoomSettings(st)
			if err != nil {
				return err
			}
			if _, err := t.q.SetRoomSettings(ctx, store.SetRoomSettingsParams{RoomSettings: raw, Now: t.now, CompanyID: companyID, ID: conv.ID}); err != nil {
				return mapErr(err, "set room settings")
			}
			if err := t.activity(actor, companyID, "conversation.room_updated", "conversation", conv.ID, map[string]any{
				"kind": domain.ConversationRoom, "mode": st.Mode, "moderator_persona_id": st.ModeratorPersonaID,
				"max_hops": st.MaxHops, "max_runs_per_message": st.MaxRunsPerMessage}, ""); err != nil {
				return err
			}
		}
		out, err = loadConversationView(ctx, t.q, companyID, conv.ID)
		return err
	})
	return out, err
}

func sameSettings(a, b domain.RoomSettings) bool {
	sameMod := (a.ModeratorPersonaID == nil && b.ModeratorPersonaID == nil) ||
		(a.ModeratorPersonaID != nil && b.ModeratorPersonaID != nil && *a.ModeratorPersonaID == *b.ModeratorPersonaID)
	return sameMod && a.Mode == b.Mode && a.MaxHops == b.MaxHops && a.MaxRunsPerMessage == b.MaxRunsPerMessage
}

// ---------- núcleo transacional ----------

// roomTarget é a decisão de despacho para uma persona: run aberta agora, entrega na fila (a persona já está
// rodando nesta sala) ou recusa com o motivo.
type roomTarget struct {
	persona RoomParticipantView
	run     *store.Run
	queued  bool
	refusal error
}

func (r roomTarget) accepted() bool { return r.run != nil || r.queued }

// roomPlan é o despacho de uma mensagem humana: quem responde, as menções e quem ficou de fora pelo teto.
type roomPlan struct {
	targets  []roomTarget
	mentions []domain.ID
	limited  []RoomParticipantView
	maxRuns  int
}

// resolveMentions acha, entre as personas ativas da sala, as citadas com @slug (exceto exclude).
func resolveMentions(body string, active []RoomParticipantView, exclude *domain.ID) []RoomParticipantView {
	bySlug := make(map[string]RoomParticipantView, len(active))
	for _, p := range active {
		bySlug[p.Slug] = p
	}
	var out []RoomParticipantView
	for _, slug := range domain.ParseMentions(body) {
		p, ok := bySlug[slug]
		if !ok || (exclude != nil && p.PersonaID == *exclude) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// dispatchRoom decide o que acontece com uma entrega para a persona p: com run da persona ativa na sala, a
// entrega espera (até maxPendingMessages); sem, abre uma run (status, orçamentos, trava semanal). Uma recusa
// volta em refusal; err é falha inesperada.
func (t *txc) dispatchRoom(conv store.Conversation, p RoomParticipantView) (roomTarget, error) {
	tg := roomTarget{persona: p}
	_, aerr := t.q.ActiveRunForConversationPersona(t.ctx, store.ActiveRunForConversationPersonaParams{ConversationID: &conv.ID, PersonaID: p.PersonaID})
	switch {
	case aerr == nil:
		n, err := t.q.CountPendingDeliveries(t.ctx, store.CountPendingDeliveriesParams{ConversationID: conv.ID, PersonaID: p.PersonaID})
		if err != nil {
			return tg, fmt.Errorf("count pending deliveries: %w", err)
		}
		if n >= maxPendingMessages {
			tg.refusal = domain.Invalid("message", fmt.Sprintf("já há %d mensagens esperando %s; aguarde a persona", n, p.Name))
			return tg, nil
		}
		tg.queued = true
	case isNoRows(aerr):
		run, berr, err := t.openPersonaRun(conv, p.PersonaID, nil)
		if err != nil {
			return tg, err
		}
		if berr != nil {
			tg.refusal = berr
			return tg, nil
		}
		tg.run = &run
	default:
		return tg, fmt.Errorf("active run for room persona: %w", aerr)
	}
	return tg, nil
}

// planRoomMessage resolve quem responde a uma mensagem humana e despacha cada persona. Se nenhuma aceitou,
// blocked traz o motivo e nada foi gravado além dos efeitos das recusas (pausa, alertas).
func (t *txc) planRoomMessage(conv store.Conversation, room RoomView, body string) (plan roomPlan, blocked, err error) {
	active := room.Active()
	if len(active) == 0 {
		return plan, nil, domain.Invalid("conversation", "a sala não tem personas")
	}
	mentioned := resolveMentions(body, active, nil)
	for _, p := range mentioned {
		plan.mentions = append(plan.mentions, p.PersonaID)
	}
	var targets []RoomParticipantView
	switch {
	case room.Settings.Mode == domain.RoomAll:
		targets = active
	case len(mentioned) > 0:
		targets = mentioned
	default:
		for _, p := range active {
			if room.ModeratorPersonaID != nil && p.PersonaID == *room.ModeratorPersonaID {
				targets = append(targets, p)
			}
		}
	}
	plan.maxRuns = room.MaxRunsPerMessage
	if len(targets) > plan.maxRuns {
		plan.limited = targets[plan.maxRuns:]
		targets = targets[:plan.maxRuns]
	}
	var refusals []error
	for _, p := range targets {
		tg, err := t.dispatchRoom(conv, p)
		if err != nil {
			return plan, nil, err
		}
		plan.targets = append(plan.targets, tg)
		if tg.refusal != nil {
			refusals = append(refusals, tg.refusal)
		}
	}
	for _, tg := range plan.targets {
		if tg.accepted() {
			return plan, nil, nil
		}
	}
	return plan, joinRefusals(refusals), nil
}

// joinRefusals devolve a recusa única como está (mantém o tipo: orçamento, trava, persona indisponível) ou junta
// várias numa recusa de persona indisponível com todos os motivos.
func joinRefusals(rs []error) error {
	switch len(rs) {
	case 0:
		return &personaBlockedError{reason: "nenhuma persona da sala pode responder agora"}
	case 1:
		return rs[0]
	}
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = r.Error()
	}
	return &personaBlockedError{reason: "nenhuma persona da sala pode responder agora: " + strings.Join(parts, "; ")}
}

// storeRoomMessage grava a mensagem humana do plano, as entregas, lança as runs abertas e avisa na sala quem
// recusou ou ficou de fora pelo teto.
func (t *txc) storeRoomMessage(conv store.Conversation, actor domain.Actor, body string, plan roomPlan) (domain.ConversationMessage, error) {
	row, err := t.q.InsertRoomMessage(t.ctx, store.InsertRoomMessageParams{ID: domain.NewID(), CompanyID: conv.CompanyID,
		ConversationID: conv.ID, AuthorType: string(domain.MessageHuman), AuthorID: actor.ID, Body: body, Mentions: plan.mentions, Now: t.now})
	if err != nil {
		return domain.ConversationMessage{}, fmt.Errorf("insert message: %w", err)
	}
	if err := t.q.TouchConversation(t.ctx, store.TouchConversationParams{ID: conv.ID, Now: t.now}); err != nil {
		return domain.ConversationMessage{}, fmt.Errorf("touch conversation: %w", err)
	}
	var personas, queued, refused []domain.ID
	for _, tg := range plan.targets {
		if !tg.accepted() {
			refused = append(refused, tg.persona.PersonaID)
			continue
		}
		personas = append(personas, tg.persona.PersonaID)
		if tg.queued {
			queued = append(queued, tg.persona.PersonaID)
		}
		if err := t.q.InsertMessageDelivery(t.ctx, store.InsertMessageDeliveryParams{CompanyID: conv.CompanyID, ConversationID: conv.ID,
			MessageID: row.ID, PersonaID: tg.persona.PersonaID, OriginMessageID: row.ID, Hop: 0, Now: t.now}); err != nil {
			return domain.ConversationMessage{}, fmt.Errorf("insert delivery: %w", err)
		}
	}
	if err := t.activity(actor, conv.CompanyID, "conversation.message", "conversation", conv.ID, map[string]any{
		"message_id": row.ID, "kind": domain.ConversationRoom, "personas": personas, "queued": queued, "refused": refused,
		"mentions": plan.mentions}, ""); err != nil {
		return domain.ConversationMessage{}, err
	}
	for _, tg := range plan.targets {
		if tg.run != nil {
			if err := t.launchRoomRun(conv, *tg.run); err != nil {
				return domain.ConversationMessage{}, err
			}
		}
	}
	if err := t.noteRefusals(conv, plan.targets, &row.ID); err != nil {
		return domain.ConversationMessage{}, err
	}
	if err := t.noteLimited(conv, plan.limited, plan.maxRuns, &row.ID); err != nil {
		return domain.ConversationMessage{}, err
	}
	m := messageFromRow(row)
	for _, tg := range plan.targets {
		if tg.accepted() {
			d := domain.MessageDelivery{PersonaID: tg.persona.PersonaID}
			if tg.run != nil {
				d.RunID = &tg.run.ID
			}
			m.Deliveries = append(m.Deliveries, d)
		}
	}
	return m, nil
}

// sendRoomMessage é o SendMessage de uma sala: a linha da sala já está travada pelo chamador.
func (t *txc) sendRoomMessage(conv store.Conversation, actor domain.Actor, body string) (msg domain.ConversationMessage, blocked, err error) {
	room, err := loadRoom(t.ctx, t.q, conv)
	if err != nil {
		return msg, nil, err
	}
	plan, blocked, err := t.planRoomMessage(conv, room, body)
	if err != nil || blocked != nil {
		return msg, blocked, err
	}
	msg, err = t.storeRoomMessage(conv, actor, body, plan)
	return msg, nil, err
}

func (t *txc) noteRefusals(conv store.Conversation, targets []roomTarget, replyTo *domain.ID) error {
	for _, tg := range targets {
		if tg.refusal == nil {
			continue
		}
		pid := tg.persona.PersonaID
		reason := tg.refusal.Error()
		var verr *domain.ValidationError
		if errors.As(tg.refusal, &verr) {
			reason = verr.Reason
		}
		if err := t.addRoomMessage(conv, domain.MessageSystem, nil, &pid, fmt.Sprintf("%s não vai responder: %s.", tg.persona.Name, strings.TrimSuffix(reason, ".")), nil, replyTo, nil); err != nil {
			return err
		}
	}
	return nil
}

func (t *txc) noteLimited(conv store.Conversation, limited []RoomParticipantView, maxRuns int, replyTo *domain.ID) error {
	if len(limited) == 0 {
		return nil
	}
	names := make([]string, len(limited))
	for i, p := range limited {
		names[i] = p.Name
	}
	return t.addRoomMessage(conv, domain.MessageSystem, nil, nil,
		fmt.Sprintf("Limite de %d execuções por mensagem atingido: %s não foi acordada.", maxRuns, strings.Join(names, ", ")), nil, replyTo, nil)
}

// launchRoomRun entrega à run tudo o que espera a persona dela na sala e enfileira a execução.
func (t *txc) launchRoomRun(conv store.Conversation, run store.Run) error {
	if _, err := t.q.ClaimPendingDeliveries(t.ctx, store.ClaimPendingDeliveriesParams{RunID: &run.ID, ConversationID: conv.ID, PersonaID: run.PersonaID}); err != nil {
		return fmt.Errorf("claim deliveries: %w", err)
	}
	if t.s.Jobs != nil {
		if err := t.s.Jobs.ExecuteRunTx(t.ctx, t.tx, conv.CompanyID, run.ID); err != nil {
			return fmt.Errorf("enqueue execute_run: %w", err)
		}
	}
	t.publishRunStatus(runFromRow(run))
	return nil
}

// addRoomMessage grava uma mensagem de persona ou de sistema na sala e sobe a sala na lista.
func (t *txc) addRoomMessage(conv store.Conversation, kind domain.MessageAuthor, author, persona *domain.ID, body string, runID, replyTo *domain.ID, mentions []domain.ID) error {
	if _, err := t.insertRoomMessage(conv, kind, author, persona, body, runID, replyTo, mentions); err != nil {
		return err
	}
	return nil
}

func (t *txc) insertRoomMessage(conv store.Conversation, kind domain.MessageAuthor, author, persona *domain.ID, body string, runID, replyTo *domain.ID, mentions []domain.ID) (store.ConversationMessage, error) {
	row, err := t.q.InsertRoomMessage(t.ctx, store.InsertRoomMessageParams{ID: domain.NewID(), CompanyID: conv.CompanyID,
		ConversationID: conv.ID, AuthorType: string(kind), AuthorID: author, Body: body, RunID: runID, PersonaID: persona,
		ReplyToID: replyTo, Mentions: mentions, Now: t.now})
	if err != nil {
		return row, fmt.Errorf("insert message: %w", err)
	}
	if err := t.q.TouchConversation(t.ctx, store.TouchConversationParams{ID: conv.ID, Now: t.now}); err != nil {
		return row, fmt.Errorf("touch conversation: %w", err)
	}
	return row, nil
}

// runScope é de onde vem uma run da sala: a mensagem humana que abriu a cadeia, o salto e a última mensagem
// recolhida (a que a resposta responde).
type runScope struct {
	origin  *domain.ID
	hop     int
	replyTo *domain.ID
	inputs  map[domain.ID]bool
}

func runScopeOf(ctx context.Context, q *store.Queries, runID domain.ID) (runScope, error) {
	rows, err := q.ListRunDeliveries(ctx, &runID)
	if err != nil {
		return runScope{}, fmt.Errorf("list run deliveries: %w", err)
	}
	sc := runScope{inputs: map[domain.ID]bool{}}
	for i, r := range rows {
		if i == 0 {
			o := r.OriginMessageID
			sc.origin = &o
		}
		sc.hop = max(sc.hop, int(r.Hop))
		id := r.MessageID
		sc.replyTo = &id
		sc.inputs[r.MessageID] = true
	}
	return sc, nil
}

// roomRunEnded fecha uma run da sala: o resumo vira a resposta da persona (ou um aviso diz por que não há),
// as personas citadas na resposta são acordadas (dentro dos tetos) e as mensagens que chegaram para ela durante
// a run abrem a próxima.
func (t *txc) roomRunEnded(conv store.Conversation, run store.Run, status domain.RunStatus, summary, errMsg string) error {
	room, err := loadRoom(t.ctx, t.q, conv)
	if err != nil {
		return err
	}
	sc, err := runScopeOf(t.ctx, t.q, run.ID)
	if err != nil {
		return err
	}
	me, inRoom := room.Participant(run.PersonaID)
	name := me.Name
	if !inRoom {
		p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: run.CompanyID, ID: run.PersonaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		name = p.Name
	}
	active := inRoom && me.Active()
	reply := status == domain.RunDone && strings.TrimSpace(summary) != ""
	pid := run.PersonaID
	var (
		mentioned []RoomParticipantView
		msg       store.ConversationMessage
	)
	if reply {
		body := clipText(strings.TrimSpace(summary), 90000)
		// persona que saiu da sala ainda tem a resposta publicada, mas não acorda ninguém
		if active {
			mentioned = resolveMentions(body, room.Active(), &pid)
		}
		ids := make([]domain.ID, len(mentioned))
		for i, p := range mentioned {
			ids[i] = p.PersonaID
		}
		if msg, err = t.insertRoomMessage(conv, domain.MessagePersona, &pid, &pid, body, &run.ID, sc.replyTo, ids); err != nil {
			return err
		}
	} else {
		notice := name + ": " + chatNotice(status, summary, errMsg)
		if err := t.addRoomMessage(conv, domain.MessageSystem, nil, &pid, notice, &run.ID, sc.replyTo, nil); err != nil {
			return err
		}
	}
	stop := status == domain.RunCancelled || status == domain.RunInterrupted ||
		domain.ConversationStatus(conv.Status) != domain.ConversationOpen
	var woke []domain.ID
	if reply && !stop && len(mentioned) > 0 && sc.origin != nil {
		if woke, err = t.wakeMentioned(conv, room, msg.ID, mentioned, *sc.origin, sc.hop+1); err != nil {
			return err
		}
	}
	if reply {
		if err := t.activity(domain.PersonaActor(run.PersonaID), run.CompanyID, "conversation.replied", "conversation", conv.ID, map[string]any{
			"run_id": run.ID, "kind": domain.ConversationRoom, "persona_id": run.PersonaID, "message_id": msg.ID,
			"hop": sc.hop, "woke": woke}, ""); err != nil {
			return err
		}
	}
	if stop || !active {
		return nil
	}
	// coalescência por (sala, persona): o que chegou para ela durante a run
	n, err := t.q.CountPendingDeliveries(t.ctx, store.CountPendingDeliveriesParams{ConversationID: conv.ID, PersonaID: run.PersonaID})
	if err != nil {
		return fmt.Errorf("count pending deliveries: %w", err)
	}
	if n == 0 {
		return nil
	}
	next, berr, err := t.openPersonaRun(conv, run.PersonaID, nil)
	if err != nil {
		return err
	}
	if berr != nil {
		return t.addRoomMessage(conv, domain.MessageSystem, nil, &pid,
			fmt.Sprintf("%s não conseguiu responder às %d mensagem(ns) em espera: %s. Elas seguem na fila e serão respondidas quando ela for chamada de novo.", name, n, berr.Error()), &run.ID, nil, nil)
	}
	return t.launchRoomRun(conv, next)
}

// wakeMentioned acorda as personas citadas numa resposta, com a resposta como entrada, respeitando o teto de
// saltos e o de runs por mensagem humana. Devolve quem foi acordado (ou entrou na fila).
func (t *txc) wakeMentioned(conv store.Conversation, room RoomView, msgID domain.ID, mentioned []RoomParticipantView, origin domain.ID, hop int) ([]domain.ID, error) {
	if hop > room.Settings.MaxHops {
		names := make([]string, len(mentioned))
		for i, p := range mentioned {
			names[i] = "@" + p.Slug
		}
		return nil, t.addRoomMessage(conv, domain.MessageSystem, nil, nil,
			fmt.Sprintf("Limite de %d saltos entre personas atingido: %s não foi acordada. Mencione de novo numa mensagem sua se quiser continuar.", room.Settings.MaxHops, strings.Join(names, ", ")), nil, &msgID, nil)
	}
	var (
		woke    []domain.ID
		targets []roomTarget
		limited []RoomParticipantView
	)
	for _, p := range mentioned {
		used, err := t.q.CountOriginRuns(t.ctx, origin)
		if err != nil {
			return nil, fmt.Errorf("count origin runs: %w", err)
		}
		if int(used) >= room.MaxRunsPerMessage {
			limited = append(limited, p)
			continue
		}
		tg, err := t.dispatchRoom(conv, p)
		if err != nil {
			return nil, err
		}
		targets = append(targets, tg)
		if !tg.accepted() {
			continue
		}
		if err := t.q.InsertMessageDelivery(t.ctx, store.InsertMessageDeliveryParams{CompanyID: conv.CompanyID, ConversationID: conv.ID,
			MessageID: msgID, PersonaID: p.PersonaID, OriginMessageID: origin, Hop: i32(hop), Now: t.now}); err != nil {
			return nil, fmt.Errorf("insert delivery: %w", err)
		}
		if tg.run != nil {
			if err := t.launchRoomRun(conv, *tg.run); err != nil {
				return nil, err
			}
		}
		woke = append(woke, p.PersonaID)
	}
	if err := t.noteRefusals(conv, targets, &msgID); err != nil {
		return nil, err
	}
	return woke, t.noteLimited(conv, limited, room.MaxRunsPerMessage, &msgID)
}

// loadRoomContext é o loadChatContext de uma run da sala: o histórico com quem falou, o que esta run recolheu
// (mensagens humanas e respostas que citaram a persona) e a sessão da persona nesta sala a retomar.
func loadRoomContext(ctx context.Context, q *store.Queries, conv store.Conversation, run domain.Run, persona domain.Persona) (*ChatContext, error) {
	room, err := loadRoom(ctx, q, conv)
	if err != nil {
		return nil, err
	}
	sc, err := runScopeOf(ctx, q, run.ID)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListConversationMessages(ctx, store.ListConversationMessagesParams{CompanyID: run.CompanyID,
		ConversationID: conv.ID, Lim: chatContextMessages})
	if err != nil {
		return nil, fmt.Errorf("list conversation messages: %w", err)
	}
	msgs := make([]domain.ConversationMessage, len(rows))
	for i, r := range rows {
		msgs[i] = messageFromRow(r)
	}
	if err := attachRoomMessageInfo(ctx, q, run.CompanyID, conv.ID, msgs); err != nil {
		return nil, err
	}
	cc := &ChatContext{Conversation: conversationFromRow(conv), Room: &RoomContext{Participants: room.Participants,
		Mode: room.Settings.Mode, ModeratorID: room.ModeratorPersonaID, Hop: sc.hop, MaxHops: room.Settings.MaxHops}}
	for _, m := range msgs {
		switch {
		case sc.inputs[m.ID]:
			cc.New = append(cc.New, m)
		case m.CreatedAt.After(run.CreatedAt):
			// chegou depois de a run abrir: entra no histórico da próxima
		default:
			cc.History = append(cc.History, m)
		}
	}
	prev, err := q.LatestRunOfConversationPersona(ctx, store.LatestRunOfConversationPersonaParams{ConversationID: &conv.ID,
		PersonaID: run.PersonaID, ExceptID: run.ID})
	switch {
	case err == nil:
		if prev.Status == string(domain.RunDone) && prev.SessionRef != "" && prev.Agent == persona.Agent && prev.CreatedAt.Before(run.CreatedAt) {
			cc.ResumeSession = prev.SessionRef
		}
	case !isNoRows(err):
		return nil, fmt.Errorf("latest run of room persona: %w", err)
	}
	return cc, nil
}
