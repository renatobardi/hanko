package ui

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Sala (ADR-0033): conversa com várias personas, com o Feed do time ao lado.

// roomFeedLimit é quantos fatos o Feed do time mostra.
const roomFeedLimit = 25

// roomSuggestions são os atalhos do estado vazio; cada um preenche a primeira mensagem.
var roomSuggestions = []struct{ label, message string }{
	{i18n.N("Planejar a semana"), i18n.N("Vamos planejar a semana: o que cada um tem de prioridade e o que pode travar?")},
	{i18n.N("Revisar o roadmap"), i18n.N("Revisem o roadmap atual: o que está atrasado, o que deveria mudar de ordem e por quê?")},
	{i18n.N("Destravar uma task"), i18n.N("Tem uma task travada. Quem pode ajudar a destravar e qual o próximo passo?")},
	{i18n.N("Montar um plano de contratação"), i18n.N("Precisamos montar um plano de contratação: que papéis faltam no time e em que ordem contratar?")},
}

func (s *server) roomRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	mux.HandleFunc("GET /c/{company}/sala", co(s.handleRooms))
	mux.HandleFunc("POST /c/{company}/sala/nova", post(co(s.handleCreateRoom)))
	mux.HandleFunc("GET /c/{company}/sala/{conversation}", co(s.handleRoom))
	mux.HandleFunc("GET /c/{company}/sala/{conversation}/mensagens", co(s.handleRoomThread))
	mux.HandleFunc("POST /c/{company}/sala/{conversation}/mensagens", post(co(s.handleRoomSend)))
	mux.HandleFunc("GET /c/{company}/sala/{conversation}/feed", co(s.handleRoomFeed))
	mux.HandleFunc("POST /c/{company}/sala/{conversation}/participantes", post(co(s.handleRoomAdd)))
	mux.HandleFunc("POST /c/{company}/sala/{conversation}/participantes/{persona}/remover", post(co(s.handleRoomRemove)))
	mux.HandleFunc("POST /c/{company}/sala/{conversation}/config", post(co(s.handleRoomConfig)))
	mux.HandleFunc("POST /c/{company}/sala/{conversation}/arquivar", post(co(s.handleRoomStatus(domain.ConversationArchived))))
	mux.HandleFunc("POST /c/{company}/sala/{conversation}/reabrir", post(co(s.handleRoomStatus(domain.ConversationOpen))))
}

func roomBadge(ctx context.Context, c service.ConversationView) views.Badge {
	if c.Room != nil {
		n := 0
		for _, p := range c.Room.Active() {
			if p.ActiveRunID != nil {
				n++
			}
		}
		if n > 0 {
			return views.Badge{Label: i18n.Tf(ctx, "Respondendo (%d)", n), Class: "run", Live: true}
		}
	}
	return chatBadge(ctx, c)
}

func roomRows(ctx context.Context, list []service.ConversationView, base string, now time.Time, loc *time.Location) []views.RoomRow {
	out := make([]views.RoomRow, 0, len(list))
	for _, c := range list {
		row := views.RoomRow{Href: base + "/sala/" + c.ID.String(), Title: c.Title, Badge: roomBadge(ctx, c),
			Last: relTimeCtx(ctx, c.LastMessageAt, now, loc), Cost: fmtUSDCtx(ctx, c.CostUSD)}
		if c.Room != nil {
			for _, p := range c.Room.Active() {
				row.Personas = append(row.Personas, p.Name)
			}
		}
		out = append(out, row)
	}
	return out
}

func roomModeLabel(ctx context.Context, m domain.RoomMode) string {
	if m == domain.RoomAll {
		return i18n.T(ctx, "Todas respondem")
	}
	return i18n.T(ctx, "Responde quem é citado")
}

// roomForm é o que o formulário de nova sala devolve num erro.
type roomForm struct {
	title, message, moderator, mode string
	personas                        map[string]bool
}

func (s *server) handleRooms(w http.ResponseWriter, r *http.Request, co domain.Company) {
	f := roomForm{message: r.URL.Query().Get("msg")}
	s.renderRooms(w, r, co, http.StatusOK, "", f)
}

func (s *server) renderRooms(w http.ResponseWriter, r *http.Request, co domain.Company, status int, errMsg string, f roomForm) {
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
	list, err := s.be.ListConversations(ctx, co.ID, service.ConversationFilter{Status: cur.status, Kind: domain.ConversationRoom, Limit: chatListLimit})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := views.RoomsVM{
		Subtitle: i18n.T(ctx, "Uma conversa com várias personas ao mesmo tempo."), NewAction: base + "/sala/nova",
		Rows: roomRows(ctx, list, base, now, loc), Error: errMsg, Empty: len(personas) < domain.MinRoomParticipants,
		RoomTitle: f.title, Message: f.message, Mode: f.mode,
	}
	for _, t := range chatTabs {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), On: t.key == cur.key, Href: base + "/sala?s=" + t.key})
	}
	vm.Moderators = append(vm.Moderators, views.Option{Value: "", Label: i18n.T(ctx, "A primeira escolhida")})
	for _, p := range personas {
		pk := views.RoomPick{ID: p.ID.String(), Name: p.Name, Title: p.Title, Checked: f.personas[p.ID.String()]}
		if !p.Status.CanWork() && p.Status != domain.PersonaAwaitingGate {
			pk.Disabled = strings.ToLower(personaBadge(ctx, p.Status).Label)
		}
		vm.Picks = append(vm.Picks, pk)
		vm.Moderators = append(vm.Moderators, views.Option{Value: p.ID.String(), Label: p.Name, Selected: f.moderator == p.ID.String()})
	}
	for _, sg := range roomSuggestions {
		vm.Suggestions = append(vm.Suggestions, views.RoomSuggestion{Label: i18n.T(ctx, sg.label),
			Href: base + "/sala?msg=" + url.QueryEscape(i18n.T(ctx, sg.message)) + "#nova-sala"})
	}
	sh := s.shell(r, co, "sala", i18n.T(ctx, "Sala"), views.Crumb{Label: i18n.T(ctx, "Sala")})
	s.render(w, r, status, views.RoomsPage(sh, vm))
}

func (s *server) handleCreateRoom(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", err.Error()))
		return
	}
	f := roomForm{title: trimForm(r, "title"), message: trimForm(r, "message"), moderator: trimForm(r, "moderator"),
		mode: trimForm(r, "mode"), personas: map[string]bool{}}
	in := service.CreateRoomInput{Title: f.title, Message: f.message, Mode: domain.RoomMode(f.mode)}
	for _, raw := range r.PostForm["personas"] {
		id, err := domain.ParseID(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		f.personas[id.String()] = true
		in.PersonaIDs = append(in.PersonaIDs, id)
	}
	if len(in.PersonaIDs) < domain.MinRoomParticipants {
		s.renderRooms(w, r, co, http.StatusUnprocessableEntity, i18n.T(ctx, "Escolha pelo menos duas personas."), f)
		return
	}
	if f.moderator != "" {
		id, err := domain.ParseID(f.moderator)
		if err != nil || !f.personas[id.String()] {
			s.renderRooms(w, r, co, http.StatusUnprocessableEntity, i18n.T(ctx, "A moderadora precisa ser uma das personas escolhidas."), f)
			return
		}
		in.ModeratorID = &id
	}
	cv, err := s.be.CreateRoom(ctx, actorFrom(ctx), co.ID, in)
	if err != nil {
		if st, title, msg := friendly(ctx, err); st < 500 && st != http.StatusForbidden && st != http.StatusNotFound {
			s.renderRooms(w, r, co, st, title+": "+msg, f)
			return
		}
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/sala/"+cv.ID.String())
}

// roomConversation carrega a sala do caminho; um chat direto vai para a página dele.
func (s *server) roomConversation(w http.ResponseWriter, r *http.Request, co domain.Company) (service.ConversationView, bool) {
	id, err := domain.ParseID(r.PathValue("conversation"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return service.ConversationView{}, false
	}
	cv, err := s.be.GetConversation(r.Context(), co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return cv, false
	}
	if cv.Room == nil {
		if r.Method == http.MethodGet && !isHX(r) {
			http.Redirect(w, r, "/c/"+co.Slug+"/chat/"+cv.ID.String(), http.StatusSeeOther)
			return cv, false
		}
		s.fail(w, r, &co, domain.ErrNotFound)
		return cv, false
	}
	return cv, true
}

// buildRoomVM carrega as mensagens, os participantes e o feed da sala.
func (s *server) buildRoomVM(r *http.Request, co domain.Company, cv service.ConversationView, withFeed bool) (views.RoomVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	room := base + "/sala/" + cv.ID.String()
	msgs, err := s.be.ListConversationMessages(ctx, co.ID, cv.ID, chatThreadLimit)
	if err != nil {
		return views.RoomVM{}, err
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return views.RoomVM{}, err
	}
	names := s.lookup(ctx, personas, nil)
	atts, err := s.messageAttachments(ctx, co, cv.ID, base, loc)
	if err != nil {
		return views.RoomVM{}, err
	}
	usage, err := s.be.GetArtifactUsage(ctx, co.ID)
	if err != nil {
		return views.RoomVM{}, err
	}
	byID := make(map[domain.ID]domain.Persona, len(personas))
	for _, p := range personas {
		byID[p.ID] = p
	}
	vm := views.RoomVM{
		Title: cv.Title, Badge: roomBadge(ctx, cv), Archived: cv.Status == domain.ConversationArchived,
		ThreadHref: room + "/mensagens", SendHref: room + "/mensagens", FeedHref: room + "/feed", ListHref: base + "/sala",
		AddHref: room + "/participantes", ConfigHref: room + "/config", Cost: fmtUSDCtx(ctx, cv.CostUSD),
		DraftKey:   "hanko-sala-" + cv.ID.String(),
		DraftF:     s.draftFields(ctx, co, room+"/rascunho", domain.DraftConversation, cv.ID),
		UploadHint: uploadHint(ctx, usage.MaxFileBytes),
	}
	if vm.Archived {
		vm.StatusHref = room + "/reabrir"
	} else {
		vm.StatusHref = room + "/arquivar"
	}
	rv := cv.Room
	if rv == nil {
		rv = &service.RoomView{}
	}
	vm.Mode, vm.ModeLabel = string(rv.Settings.Mode), roomModeLabel(ctx, rv.Settings.Mode)
	vm.MaxHops, vm.MaxRuns = rv.Settings.MaxHops, rv.Settings.MaxRunsPerMessage
	active := rv.Active()
	canEdit := views.RoleFrom(ctx).AtLeast(domain.MemberMember) && !vm.Archived
	in := map[domain.ID]bool{}
	partName := map[domain.ID]string{}
	for _, p := range rv.Participants {
		partName[p.PersonaID] = p.Name
	}
	for _, p := range active {
		in[p.PersonaID] = true
		m := views.RoomMember{ID: p.PersonaID.String(), Name: p.Name, Slug: p.Slug, Href: base + "/personas/" + p.Slug,
			Moderator: rv.ModeratorPersonaID != nil && *rv.ModeratorPersonaID == p.PersonaID, Pending: p.Pending}
		if p.ActiveRunID != nil {
			m.Thinking, m.RunHref = true, base+"/execucoes/"+p.ActiveRunID.String()
			vm.Busy = true
		} else if p.Pending > 0 {
			vm.Waiting += p.Pending
		}
		if !p.Status.CanWork() && p.Status != domain.PersonaAwaitingGate {
			m.Blocked = i18n.Tf(ctx, "A persona %s está %s e não pode responder agora.", p.Name, strings.ToLower(personaBadge(ctx, p.Status).Label))
		}
		if canEdit && len(active) > domain.MinRoomParticipants {
			m.RemoveURL = room + "/participantes/" + p.PersonaID.String() + "/remover"
		}
		vm.Members = append(vm.Members, m)
		vm.ModOptions = append(vm.ModOptions, views.Option{Value: p.PersonaID.String(), Label: p.Name,
			Selected: rv.ModeratorPersonaID != nil && *rv.ModeratorPersonaID == p.PersonaID})
	}
	if len(active) < domain.MaxRoomParticipants {
		ps := append([]domain.Persona(nil), personas...)
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
		for _, p := range ps {
			if !in[p.ID] {
				vm.AddOptions = append(vm.AddOptions, views.Option{Value: p.ID.String(), Label: p.Name + " · " + p.Title})
			}
		}
		vm.CanAdd = len(vm.AddOptions) > 0
	}
	for _, m := range msgs {
		rm := views.RoomMsg{Kind: string(m.Kind), Author: names.actorName(m.Author), When: fmtDateTime(ctx, m.CreatedAt, loc),
			Marker: m.Marker, Attachments: atts[m.ID]}
		switch m.Kind {
		case domain.MessageSystem:
			rm.Body = views.HTML(m.Body) // texto puro, escapado na view
		case domain.MessageHuman:
			rm.Body = renderMD(m.Body)
			for _, d := range m.Deliveries {
				if d.RunID == nil {
					n := partName[d.PersonaID]
					if n == "" {
						n = byID[d.PersonaID].Name
					}
					rm.Waiting = append(rm.Waiting, n)
				}
			}
		default:
			rm.Body = renderMD(m.Body)
			rm.Author = m.PersonaName
			if rm.Author == "" && m.PersonaID != nil {
				rm.Author = byID[*m.PersonaID].Name
			}
		}
		if m.RunID != nil && m.Kind != domain.MessageHuman {
			rm.RunHref = base + "/execucoes/" + m.RunID.String()
		}
		vm.Messages = append(vm.Messages, rm)
	}
	if withFeed {
		if vm.Feed, err = s.roomFeed(ctx, co, personas); err != nil {
			return views.RoomVM{}, err
		}
	}
	return vm, nil
}

// roomFeed é a atividade recente da empresa, como na página Atividade.
func (s *server) roomFeed(ctx context.Context, co domain.Company, personas []domain.Persona) ([]views.RoomFeedItem, error) {
	acts, err := s.be.ListActivity(ctx, co.ID, service.ActivityFilter{Limit: roomFeedLimit})
	if err != nil {
		return nil, err
	}
	names := s.lookup(ctx, personas, nil)
	sort.SliceStable(acts, func(i, j int) bool { return acts[i].OccurredAt.After(acts[j].OccurredAt) })
	base, loc, now := "/c/"+co.Slug, loadLoc(co.Timezone), s.now()
	out := make([]views.RoomFeedItem, 0, len(acts))
	for _, a := range acts {
		out = append(out, s.activityItem(ctx, a, base, names, now, loc, true))
	}
	return out, nil
}

func (s *server) handleRoom(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.roomConversation(w, r, co)
	if !ok {
		return
	}
	vm, err := s.buildRoomVM(r, co, cv, true)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	ctx := r.Context()
	sh := s.shell(r, co, "sala", cv.Title, views.Crumb{Label: i18n.T(ctx, "Sala"), Href: "/c/" + co.Slug + "/sala"}, views.Crumb{Label: cv.Title})
	s.render(w, r, http.StatusOK, views.RoomPage(sh, vm))
}

// handleRoomThread é o fragmento htmx que faz polling enquanto alguma persona responde.
func (s *server) handleRoomThread(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.roomConversation(w, r, co)
	if !ok {
		return
	}
	vm, err := s.buildRoomVM(r, co, cv, false)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.RoomThread(vm))
}

// handleRoomFeed é o fragmento do Feed do time.
func (s *server) handleRoomFeed(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.roomConversation(w, r, co)
	if !ok {
		return
	}
	personas, err := s.be.ListPersonas(r.Context(), co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	feed, err := s.roomFeed(r.Context(), co, personas)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.RoomFeed(views.RoomVM{FeedHref: "/c/" + co.Slug + "/sala/" + cv.ID.String() + "/feed", Feed: feed}))
}

func (s *server) handleRoomSend(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cv, ok := s.roomConversation(w, r, co)
	if !ok {
		return
	}
	if !s.sendFromForm(w, r, co, cv) {
		return
	}
	room := "/c/" + co.Slug + "/sala/" + cv.ID.String()
	if !isHX(r) {
		redirect(w, r, room+"#room-thread")
		return
	}
	fresh, err := s.be.GetConversation(ctx, co.ID, cv.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.buildRoomVM(r, co, fresh, false)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.RoomThread(vm))
}

func (s *server) handleRoomAdd(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cv, ok := s.roomConversation(w, r, co)
	if !ok {
		return
	}
	pid, err := domain.ParseID(trimForm(r, "persona"))
	if err != nil {
		s.fail(w, r, &co, domain.Invalid("persona", i18n.T(ctx, "Escolha a persona.")))
		return
	}
	if _, err := s.be.AddRoomParticipant(ctx, actorFrom(ctx), co.ID, cv.ID, pid); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/sala/"+cv.ID.String())
}

func (s *server) handleRoomRemove(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cv, ok := s.roomConversation(w, r, co)
	if !ok {
		return
	}
	pid, err := domain.ParseID(r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.be.RemoveRoomParticipant(ctx, actorFrom(ctx), co.ID, cv.ID, pid); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/sala/"+cv.ID.String())
}

func (s *server) handleRoomConfig(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cv, ok := s.roomConversation(w, r, co)
	if !ok {
		return
	}
	var p service.RoomSettingsPatch
	if v := trimForm(r, "mode"); v != "" {
		m := domain.RoomMode(v)
		p.Mode = &m
	}
	if v := trimForm(r, "moderator"); v != "" {
		id, err := domain.ParseID(v)
		if err != nil {
			s.fail(w, r, &co, domain.Invalid("moderator", i18n.T(ctx, "A moderadora precisa ser uma das personas da sala.")))
			return
		}
		p.ModeratorID = &id
	}
	for _, f := range []struct {
		key string
		dst **int
	}{{"max_hops", &p.MaxHops}, {"max_runs", &p.MaxRunsPerMessage}} {
		if v := trimForm(r, f.key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				s.fail(w, r, &co, domain.Invalid(f.key, i18n.T(ctx, "use um número inteiro")))
				return
			}
			*f.dst = &n
		}
	}
	if _, err := s.be.UpdateRoomSettings(ctx, actorFrom(ctx), co.ID, cv.ID, p); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/sala/"+cv.ID.String())
}

func (s *server) handleRoomStatus(st domain.ConversationStatus) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		ctx := r.Context()
		cv, ok := s.roomConversation(w, r, co)
		if !ok {
			return
		}
		if _, err := s.be.SetConversationStatus(ctx, actorFrom(ctx), co.ID, cv.ID, st); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		base := "/c/" + co.Slug
		if st == domain.ConversationArchived {
			redirect(w, r, base+"/sala?s=abertas")
			return
		}
		redirect(w, r, base+"/sala/"+cv.ID.String())
	}
}
