package uitest

import (
	"context"
	"strings"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/webpush"
)

// CaixaFake guarda o estado por pessoa da Caixa de entrada, os avisos e o web push (ADR-0042). O zero value serve.
type CaixaFake struct {
	// State é o lido/arquivado por item; Mentions, as menções à pessoa.
	State    map[service.InboxRef]domain.InboxItemState
	Mentions []service.InboxMention
	// Prefs são as preferências alteradas; o resto vale o padrão.
	Prefs map[domain.NotifyKind]domain.NotifyPref
	// PushOn liga o web push (há HANKO_SECRET_KEY); Devices são os aparelhos; TestSent conta os avisos de teste.
	PushOn   bool
	Devices  []service.PushSubscriptionView
	Added    []webpush.Subscription
	TestSent int
	// MineTasks (chave da task → motivos) e MineGates são o que "Minhas" devolve.
	MineTasks map[string][]string
	MineGates map[domain.ID]bool
	// LastAction e LastRefs guardam a última marcação feita.
	LastAction domain.InboxAction
	LastRefs   []service.InboxRef
}

func (f *Fake) InboxView(ctx context.Context, companyID domain.ID, view domain.InboxView) (service.Inbox, error) {
	in, err := f.Inbox(ctx, companyID)
	if err != nil {
		return in, err
	}
	f.mu.Lock()
	in.Mentions = append(in.Mentions, f.Caixa.Mentions...)
	state := map[service.InboxRef]domain.InboxItemState{}
	for k, v := range f.Caixa.State {
		state[k] = v
	}
	f.mu.Unlock()
	if _, ok := auth.IdentityFrom(ctx); ok { // sem pessoa (modo trusted) não há estado
		in.ApplyState(state)
	}
	if view == "" {
		view = domain.InboxViewAll
	}
	in.Filter(view)
	return in, nil
}

func (f *Fake) InboxUnread(ctx context.Context, companyID domain.ID) (int, error) {
	in, err := f.InboxView(ctx, companyID, domain.InboxViewAll)
	return in.Unread, err
}

func (f *Fake) SetInboxState(ctx context.Context, _ domain.Actor, companyID domain.ID, action domain.InboxAction, refs []service.InboxRef) (int, error) {
	if err := f.fail("SetInboxState"); err != nil {
		return 0, err
	}
	all, err := f.InboxView(ctx, companyID, domain.InboxViewAll)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Caixa.LastAction, f.Caixa.LastRefs = action, refs
	if f.Caixa.State == nil {
		f.Caixa.State = map[service.InboxRef]domain.InboxItemState{}
	}
	if action == domain.InboxReadAll {
		n := 0
		for _, it := range all.Items() {
			if !it.Read && !it.Archived {
				f.Caixa.State[service.InboxRef{Kind: it.Kind, ID: it.ID}] = domain.InboxItemState{Read: true}
				n++
			}
		}
		return n, nil
	}
	for _, r := range refs {
		st := f.Caixa.State[r]
		switch action {
		case domain.InboxRead:
			st.Read = true
		case domain.InboxUnread:
			st = domain.InboxItemState{}
		case domain.InboxArchive:
			st = domain.InboxItemState{Read: true, Archived: true}
		case domain.InboxUnarchive:
			st = domain.InboxItemState{Read: true}
		default:
			return 0, domain.Invalid("action", "ação desconhecida")
		}
		f.Caixa.State[r] = st
	}
	return len(refs), nil
}

func (f *Fake) NotifyPrefs(_ context.Context, _ domain.ID) ([]domain.NotifyPref, error) {
	if err := f.fail("NotifyPrefs"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.NotifyPref, 0, len(domain.NotifyKinds))
	for _, k := range domain.NotifyKinds {
		p := domain.DefaultNotifyPref(k)
		if v, ok := f.Caixa.Prefs[k]; ok {
			p = v
		}
		out = append(out, p)
	}
	return out, nil
}

func (f *Fake) SetNotifyPref(_ context.Context, _ domain.ID, p domain.NotifyPref) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Caixa.Prefs == nil {
		f.Caixa.Prefs = map[domain.NotifyKind]domain.NotifyPref{}
	}
	f.Caixa.Prefs[p.Kind] = p
	return nil
}

func (f *Fake) SendTestNotice(_ context.Context, _ domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Caixa.TestSent++
	return nil
}

func (f *Fake) PushAvailable() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Caixa.PushOn
}

func (f *Fake) PushVapidKey(_ context.Context) (string, error) {
	if !f.PushAvailable() {
		return "", service.ErrPushUnavailable
	}
	return "BFAKEVAPIDPUBLICKEY", nil
}

func (f *Fake) AddPushSubscription(_ context.Context, in webpush.Subscription, ua string) (service.PushSubscriptionView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.Caixa.PushOn {
		return service.PushSubscriptionView{}, service.ErrPushUnavailable
	}
	if !strings.HasPrefix(in.Endpoint, "https://") {
		return service.PushSubscriptionView{}, domain.Invalid("endpoint", "use https")
	}
	f.Caixa.Added = append(f.Caixa.Added, in)
	v := service.PushSubscriptionView{ID: ID("push:" + in.Endpoint), Endpoint: in.Endpoint, UserAgent: ua, CreatedAt: Now()}
	f.Caixa.Devices = append(f.Caixa.Devices, v)
	return v, nil
}

func (f *Fake) ListPushSubscriptions(_ context.Context) ([]service.PushSubscriptionView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]service.PushSubscriptionView(nil), f.Caixa.Devices...), nil
}

func (f *Fake) RemovePushSubscription(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, d := range f.Caixa.Devices {
		if d.ID.String() == ref || d.Endpoint == ref {
			f.Caixa.Devices = append(f.Caixa.Devices[:i], f.Caixa.Devices[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

// ResolveActor resolve "system", "persona:<slug>" e "human:<e-mail>" contra os dados do Fake.
func (f *Fake) ResolveActor(_ context.Context, _ domain.ID, ref string) (service.ActorFilter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	typ, val, _ := strings.Cut(ref, ":")
	switch typ {
	case "system":
		return service.ActorFilter{Type: "system"}, nil
	case "persona":
		if val == "" {
			return service.ActorFilter{Type: "persona"}, nil
		}
		for _, p := range f.Personas {
			if p.Slug == val {
				id := p.ID
				return service.ActorFilter{Type: "persona", ID: &id}, nil
			}
		}
	case "human":
		if val == "" {
			return service.ActorFilter{Type: "human"}, nil
		}
		for _, m := range f.Members {
			if strings.EqualFold(m.Email, val) {
				id := m.UserID
				return service.ActorFilter{Type: "human", ID: &id}, nil
			}
		}
	}
	return service.ActorFilter{}, domain.Invalid("actor", "ator não encontrado")
}
