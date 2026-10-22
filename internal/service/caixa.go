package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Caixa por pessoa (ADR-0042 §A). Os itens são derivados do estado da empresa (Inbox, ADR-0020 e seguintes); o que é
// da pessoa — lido e arquivado — fica em inbox_state, com uma impressão digital que faz o item reaparecer quando muda.

const (
	// inboxMentionWindow é por quantos dias uma menção fica na Caixa.
	inboxMentionWindow = 30 * 24 * time.Hour
	maxInboxMentions   = 50
	// inboxStateRetention é a idade a partir da qual uma linha de inbox_state é podada na escrita.
	inboxStateRetention = 180 * 24 * time.Hour
	// maxInboxRefsPerRequest limita os itens de uma marcação.
	maxInboxRefsPerRequest = 500
)

// InboxMention é uma menção ao usuário num comentário.
type InboxMention struct {
	ID        domain.ID
	CommentID domain.ID
	TaskID    domain.ID
	TaskKey   string
	TaskTitle string
	Author    string
	Excerpt   string
	At        time.Time
}

// InboxRef identifica um item da Caixa: o tipo e o id da origem.
type InboxRef struct {
	Kind domain.InboxItemKind `json:"kind"`
	ID   domain.ID            `json:"id"`
}

// InboxItem é um item da Caixa achatado (API, CLI): o que é, para onde leva e o estado da pessoa.
type InboxItem struct {
	Kind   domain.InboxItemKind `json:"kind"`
	ID     domain.ID            `json:"id"`
	Title  string               `json:"title"`
	Detail string               `json:"detail,omitempty"`
	// TaskKey e Ref dão o contexto: a chave da task, ou o slug da persona, o id da execução...
	TaskKey  string    `json:"task_key,omitempty"`
	Ref      string    `json:"ref,omitempty"`
	At       time.Time `json:"at"`
	Read     bool      `json:"read"`
	Archived bool      `json:"archived"`
	// Informational: ninguém precisa agir (fora de Count e do selo).
	Informational bool `json:"informational,omitempty"`
	// Fingerprint é a "versão" do item; mudou, o estado guardado deixa de valer.
	Fingerprint string `json:"-"`
}

func micros(t time.Time) string { return strconv.FormatInt(t.UnixMicro(), 10) }

// itemFingerprint ... (um por tipo; ver ADR-0042 §A.2)
func gateFingerprint(g GateView) string          { return micros(g.CreatedAt) }
func blockedFingerprint(t TaskView) string       { return micros(t.UpdatedAt) }
func heldFingerprint(h HeldTask) string          { return micros(h.Hold.At) }
func runFailedFingerprint(r RunView) string      { return string(r.Status) }
func alertFingerprint(a domain.Alert) string     { return string(a.Severity) + "|" + micros(a.OpenedAt) }
func subFingerprint(g SubscriptionStatus) string { return micros(g.WeekStart) }
func personaFingerprint(a PersonaAlert) string {
	last := ""
	if a.LastErrorAt != nil {
		last = micros(*a.LastErrorAt)
	}
	return strconv.Itoa(a.ConsecutiveFailures) + "|" + last
}
func goalFingerprint(g GoalView) string {
	if g.AchievedAt != nil {
		return micros(*g.AchievedAt)
	}
	return micros(g.UpdatedAt)
}

// Items achata a Caixa em itens, na ordem das seções da tela. O estado vem de st (nil = tudo não lido, não arquivado).
func (i Inbox) Items() []InboxItem {
	var out []InboxItem
	add := func(it InboxItem) {
		if st, ok := i.State[InboxRef{Kind: it.Kind, ID: it.ID}]; ok {
			it.Read, it.Archived = st.Read, st.Archived
		}
		out = append(out, it)
	}
	for _, g := range i.Gates {
		add(InboxItem{Kind: domain.InboxGate, ID: g.ID, Title: g.Title, TaskKey: g.TaskKey, Ref: g.ID.String(), At: g.CreatedAt, Fingerprint: gateFingerprint(g)})
	}
	for _, t := range i.BlockedTasks {
		add(InboxItem{Kind: domain.InboxTaskBlocked, ID: t.ID, Title: t.Title, TaskKey: t.Key, Ref: t.Key, At: t.UpdatedAt, Fingerprint: blockedFingerprint(t)})
	}
	for _, r := range i.FailedRuns {
		add(InboxItem{Kind: domain.InboxRunFailed, ID: r.ID, Title: r.TaskTitle, Detail: r.Error, TaskKey: r.TaskKey, Ref: r.ID.String(), At: r.CreatedAt, Fingerprint: runFailedFingerprint(r)})
	}
	for _, p := range i.ErrorPersonas {
		at := time.Time{}
		if p.LastErrorAt != nil {
			at = *p.LastErrorAt
		}
		add(InboxItem{Kind: domain.InboxPersonaError, ID: p.Persona.ID, Title: p.Persona.Name, Detail: p.LastError, Ref: p.Persona.Slug, At: at, Fingerprint: personaFingerprint(p)})
	}
	for _, a := range i.Alerts {
		add(InboxItem{Kind: domain.InboxAlert, ID: a.ID, Title: a.Message, Ref: a.SubjectID.String(), At: a.OpenedAt, Fingerprint: alertFingerprint(a)})
	}
	for _, g := range i.GuardedSubscriptions {
		add(InboxItem{Kind: domain.InboxSubscription, ID: g.ID, Title: g.Name, Ref: g.ID.String(), At: g.WeekStart, Fingerprint: subFingerprint(g)})
	}
	for _, m := range i.Mentions {
		add(InboxItem{Kind: domain.InboxMention, ID: m.ID, Title: m.TaskTitle, Detail: m.Excerpt, TaskKey: m.TaskKey, Ref: m.TaskKey, At: m.At})
	}
	for _, h := range i.HeldTasks {
		add(InboxItem{Kind: domain.InboxTaskHeld, ID: h.Task.ID, Title: h.Task.Title, Detail: h.Hold.Reason, TaskKey: h.Task.Key, Ref: h.Task.Key, At: h.Hold.At,
			Informational: true, Fingerprint: heldFingerprint(h)})
	}
	for _, g := range i.AchievedGoals {
		add(InboxItem{Kind: domain.InboxGoalAchieved, ID: g.ID, Title: g.Title, Ref: g.Slug, At: g.UpdatedAt, Informational: true, Fingerprint: goalFingerprint(g)})
	}
	return out
}

// StateOf é o estado da pessoa para um item (zero se nunca marcou, ou se o item mudou desde então).
func (i Inbox) StateOf(kind domain.InboxItemKind, id domain.ID) domain.InboxItemState {
	return i.State[InboxRef{Kind: kind, ID: id}]
}

// callerUser é o usuário autenticado do contexto; sem identidade (modo trusted) não há Caixa pessoal.
func callerUser(ctx context.Context) (domain.ID, bool) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return domain.ID{}, false
	}
	return id.User.ID, true
}

// Inbox monta a Caixa de entrada da pessoa na visão padrão (tudo que não está arquivado).
func (s *Service) Inbox(ctx context.Context, companyID domain.ID) (Inbox, error) {
	return s.InboxView(ctx, companyID, domain.InboxViewAll)
}

// InboxView monta a Caixa filtrada pela visão: all (não arquivados), unread (não arquivados e não lidos) ou archived.
// Count e as seções refletem a visão; Unread e ArchivedCount são totais da pessoa, qualquer que seja a visão.
func (s *Service) InboxView(ctx context.Context, companyID domain.ID, view domain.InboxView) (Inbox, error) {
	if view == "" {
		view = domain.InboxViewAll
	}
	if !view.Valid() {
		return Inbox{}, domain.Invalid("view", "use all, unread ou archived")
	}
	in, err := s.inboxFull(ctx, companyID)
	if err != nil {
		return in, err
	}
	in.Filter(view)
	return in, nil
}

// inboxFull é a Caixa inteira (arquivados inclusive) com o estado da pessoa aplicado e os totais calculados.
func (s *Service) inboxFull(ctx context.Context, companyID domain.ID) (Inbox, error) {
	in, err := s.buildInbox(ctx, companyID)
	if err != nil {
		return in, err
	}
	uid, personal := callerUser(ctx)
	if personal {
		if in.Mentions, err = s.inboxMentions(ctx, companyID, uid); err != nil {
			return in, err
		}
		rows, err := s.read().ListInboxStates(ctx, store.ListInboxStatesParams{CompanyID: companyID, UserID: uid})
		if err != nil {
			return in, fmt.Errorf("list inbox state: %w", err)
		}
		stored := make(map[InboxRef]store.InboxState, len(rows))
		for _, r := range rows {
			stored[InboxRef{Kind: domain.InboxItemKind(r.ItemKind), ID: r.ItemID}] = r
		}
		state := map[InboxRef]domain.InboxItemState{}
		for _, it := range in.Items() {
			ref := InboxRef{Kind: it.Kind, ID: it.ID}
			if r, ok := stored[ref]; ok {
				state[ref] = domain.EffectiveInboxState(r.Fingerprint, it.Fingerprint, r.ReadAt != nil, r.ArchivedAt != nil)
			}
		}
		in.ApplyState(state)
	} else { // sem pessoa (modo trusted): tudo não lido, só os totais
		in.ApplyState(nil)
		in.Personal = false
	}
	return in, nil
}

// ApplyState aplica o estado da pessoa (lido/arquivado por item) à Caixa e calcula os totais Unread e ArchivedCount
// (só itens que pedem ação: o selo não conta os informativos).
func (i *Inbox) ApplyState(state map[InboxRef]domain.InboxItemState) {
	i.Personal, i.State = true, state
	i.Unread, i.ArchivedCount = 0, 0
	for _, it := range i.Items() {
		if it.Informational {
			continue
		}
		switch {
		case it.Archived:
			i.ArchivedCount++
		case !it.Read:
			i.Unread++
		}
	}
}

// Filter deixa em cada seção só os itens visíveis na visão (all, unread ou archived).
func (i *Inbox) Filter(view domain.InboxView) {
	keep := func(kind domain.InboxItemKind, id domain.ID) bool {
		return domain.InboxVisible(view, i.State[InboxRef{Kind: kind, ID: id}])
	}
	var gates []GateView
	for _, g := range i.Gates {
		if keep(domain.InboxGate, g.ID) {
			gates = append(gates, g)
		}
	}
	i.Gates = gates
	var blocked []TaskView
	for _, t := range i.BlockedTasks {
		if keep(domain.InboxTaskBlocked, t.ID) {
			blocked = append(blocked, t)
		}
	}
	i.BlockedTasks = blocked
	var failed []RunView
	for _, r := range i.FailedRuns {
		if keep(domain.InboxRunFailed, r.ID) {
			failed = append(failed, r)
		}
	}
	i.FailedRuns = failed
	var personas []PersonaAlert
	for _, p := range i.ErrorPersonas {
		if keep(domain.InboxPersonaError, p.Persona.ID) {
			personas = append(personas, p)
		}
	}
	i.ErrorPersonas = personas
	var alerts []domain.Alert
	for _, a := range i.Alerts {
		if keep(domain.InboxAlert, a.ID) {
			alerts = append(alerts, a)
		}
	}
	i.Alerts = alerts
	var subs []SubscriptionStatus
	for _, g := range i.GuardedSubscriptions {
		if keep(domain.InboxSubscription, g.ID) {
			subs = append(subs, g)
		}
	}
	i.GuardedSubscriptions = subs
	var ments []InboxMention
	for _, m := range i.Mentions {
		if keep(domain.InboxMention, m.ID) {
			ments = append(ments, m)
		}
	}
	i.Mentions = ments
	var held []HeldTask
	for _, h := range i.HeldTasks {
		if keep(domain.InboxTaskHeld, h.Task.ID) {
			held = append(held, h)
		}
	}
	i.HeldTasks = held
	var goals []GoalView
	for _, g := range i.AchievedGoals {
		if keep(domain.InboxGoalAchieved, g.ID) {
			goals = append(goals, g)
		}
	}
	i.AchievedGoals = goals
	// a revisão em andamento não é item (só informa): fica como está
}

func (s *Service) inboxMentions(ctx context.Context, companyID, userID domain.ID) ([]InboxMention, error) {
	rows, err := s.read().ListUserMentions(ctx, store.ListUserMentionsParams{CompanyID: companyID, UserID: &userID,
		Since: s.Now().UTC().Add(-inboxMentionWindow), Lim: maxInboxMentions})
	if err != nil {
		return nil, fmt.Errorf("list mentions: %w", err)
	}
	out := make([]InboxMention, len(rows))
	for i, r := range rows {
		out[i] = InboxMention{ID: r.ID, CommentID: r.CommentID, TaskID: r.TaskID, TaskKey: domain.TaskKey(r.TaskPrefix, r.TaskNumber),
			TaskTitle: r.TaskTitle, Author: r.AuthorName, Excerpt: r.Excerpt, At: r.CreatedAt}
	}
	return out, nil
}

// InboxUnread é o número do selo: itens que pedem ação, não lidos e não arquivados.
func (s *Service) InboxUnread(ctx context.Context, companyID domain.ID) (int, error) {
	in, err := s.InboxView(ctx, companyID, domain.InboxViewAll)
	if err != nil {
		return 0, err
	}
	return in.Unread, nil
}

// ListInboxItems devolve os itens da Caixa na visão pedida, do mais novo para o mais antigo.
func (s *Service) ListInboxItems(ctx context.Context, companyID domain.ID, view domain.InboxView) ([]InboxItem, error) {
	in, err := s.InboxView(ctx, companyID, view)
	if err != nil {
		return nil, err
	}
	items := in.Items()
	sort.SliceStable(items, func(a, b int) bool { return items[a].At.After(items[b].At) })
	return items, nil
}

// SetInboxState muda o estado de itens da Caixa para a pessoa autenticada: read, unread, archive, unarchive; ou
// read_all (todos os itens visíveis agora; refs ignorados). Arquivar e marcar tudo geram fato; ler/não ler um item
// não (é navegação pessoal). Um item que não está na Caixa agora é ErrNotFound (nada é gravado).
func (s *Service) SetInboxState(ctx context.Context, actor domain.Actor, companyID domain.ID, action domain.InboxAction, refs []InboxRef) (int, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return 0, err
	}
	if actor.Type != domain.ActorHuman {
		return 0, domain.ErrForbidden
	}
	if !action.Valid() {
		return 0, domain.Invalid("action", "use read, unread, archive, unarchive ou read_all")
	}
	uid, ok := callerUser(ctx)
	if !ok {
		return 0, domain.Invalid("user", "a Caixa pessoal exige uma pessoa autenticada")
	}
	if len(refs) > maxInboxRefsPerRequest {
		return 0, domain.Invalid("items", fmt.Sprintf("no máximo %d itens por pedido", maxInboxRefsPerRequest))
	}
	ctx, span := otel.Tracer("hanko/inbox").Start(ctx, "hanko.inbox.set_state", trace.WithAttributes(
		attribute.String("hanko.inbox.action", string(action))))
	defer span.End()

	// a Caixa inteira, com os arquivados: um item arquivado pode ser desarquivado
	full, err := s.inboxFull(ctx, companyID)
	if err != nil {
		return 0, err
	}
	byRef := map[InboxRef]InboxItem{}
	for _, it := range full.Items() {
		byRef[InboxRef{Kind: it.Kind, ID: it.ID}] = it
	}
	var targets []InboxItem
	if action == domain.InboxReadAll {
		for _, it := range full.Items() {
			if !it.Read && !it.Archived {
				targets = append(targets, it)
			}
		}
	} else {
		if len(refs) == 0 {
			return 0, domain.Invalid("items", "informe ao menos um item")
		}
		seen := map[InboxRef]bool{}
		for _, r := range refs {
			if !r.Kind.Valid() {
				return 0, domain.Invalid("kind", fmt.Sprintf("tipo de item desconhecido %q", r.Kind))
			}
			it, ok := byRef[r]
			if !ok {
				return 0, fmt.Errorf("item da Caixa %s/%s: %w", r.Kind, r.ID, domain.ErrNotFound)
			}
			if !seen[r] {
				seen[r] = true
				targets = append(targets, it)
			}
		}
	}
	if len(targets) == 0 {
		return 0, nil
	}
	err = s.withTx(ctx, func(t *txc) error {
		for _, it := range targets {
			var read, archived *time.Time
			now := t.now
			switch action {
			case domain.InboxRead, domain.InboxReadAll:
				read = &now
				if it.Archived { // ler um item arquivado não o tira do arquivo
					archived = &now
				}
			case domain.InboxUnread:
				// não lido: sem marca de leitura e sem arquivo
			case domain.InboxArchive:
				read, archived = &now, &now
			case domain.InboxUnarchive:
				read = &now // desarquivar devolve à Caixa como lido: quem arquivou já tinha visto
			}
			if err := t.q.UpsertInboxState(ctx, store.UpsertInboxStateParams{CompanyID: companyID, UserID: uid, ItemKind: string(it.Kind),
				ItemID: it.ID, Fingerprint: it.Fingerprint, ReadAt: read, ArchivedAt: archived, Now: now}); err != nil {
				return fmt.Errorf("upsert inbox state: %w", err)
			}
			if action == domain.InboxArchive || action == domain.InboxUnarchive {
				verb := "inbox.archived"
				if action == domain.InboxUnarchive {
					verb = "inbox.unarchived"
				}
				if err := t.activity(actor, companyID, verb, "inbox", it.ID, map[string]any{"kind": it.Kind}, ""); err != nil {
					return err
				}
			}
		}
		if action == domain.InboxReadAll {
			if err := t.activity(actor, companyID, "inbox.read_all", "company", companyID, map[string]any{"items": len(targets)}, ""); err != nil {
				return err
			}
		}
		if err := t.q.PruneInboxState(ctx, store.PruneInboxStateParams{CompanyID: companyID, UserID: uid, Before: t.now.Add(-inboxStateRetention)}); err != nil {
			return fmt.Errorf("prune inbox state: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(targets), nil
}

// ParseInboxRef lê "kind:uuid" (CLI, formulários da UI).
func ParseInboxRef(v string) (InboxRef, error) {
	for i := 0; i < len(v); i++ {
		if v[i] != ':' {
			continue
		}
		kind, idStr := domain.InboxItemKind(v[:i]), v[i+1:]
		id, err := uuid.Parse(idStr)
		if err != nil || !kind.Valid() {
			break
		}
		return InboxRef{Kind: kind, ID: id}, nil
	}
	return InboxRef{}, domain.Invalid("item", "use tipo:uuid, como gate:5c1e…")
}
