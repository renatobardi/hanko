package uitest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Dublê dos canais externos (ADR-0046).

func (f *Fake) channelIndex(ref string) int {
	for i, c := range f.Channels {
		if c.Name == ref || c.ID.String() == ref {
			return i
		}
	}
	return -1
}

func (f *Fake) ListChannels(_ context.Context, _ domain.ID) ([]service.ChannelView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListChannels"); err != nil {
		return nil, err
	}
	return append([]service.ChannelView(nil), f.Channels...), nil
}

func (f *Fake) GetChannel(_ context.Context, _ domain.ID, ref string) (service.ChannelView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetChannel"); err != nil {
		return service.ChannelView{}, err
	}
	if i := f.channelIndex(ref); i >= 0 {
		return f.Channels[i], nil
	}
	return service.ChannelView{}, domain.ErrNotFound
}

func (f *Fake) CreateChannel(_ context.Context, _ domain.Actor, companyID domain.ID, in service.CreateChannelInput) (service.ChannelView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateChannel")
	if err := f.fail("CreateChannel"); err != nil {
		return service.ChannelView{}, err
	}
	if !in.Kind.Valid() {
		return service.ChannelView{}, domain.Invalid("kind", "use telegram ou github")
	}
	name, err := domain.ValidChannelName(in.Name)
	if err != nil {
		return service.ChannelView{}, err
	}
	cfg, err := domain.NormalizeChannelConfig(in.Kind, in.Config)
	if err != nil {
		return service.ChannelView{}, err
	}
	if f.channelIndex(name) >= 0 {
		return service.ChannelView{}, fmt.Errorf("channel %q: %w", name, domain.ErrConflict)
	}
	id := domain.NewID()
	c := domain.Channel{ID: id, CompanyID: companyID, Kind: in.Kind, Name: name, PersonaID: in.PersonaID, TokenRef: in.TokenRef,
		WebhookSecretRef: in.WebhookSecretRef, Config: cfg, InboundToken: "tok-" + id.String()[:8], CreatedAt: Now(), UpdatedAt: Now()}
	v := service.ChannelView{Channel: c, HookPath: c.ChannelHookPath()}
	for _, p := range f.Personas {
		if in.PersonaID != nil && p.ID == *in.PersonaID {
			v.PersonaName = p.Name
		}
	}
	f.Channels = append(f.Channels, v)
	return v, nil
}

func (f *Fake) UpdateChannel(_ context.Context, _ domain.Actor, _, channelID domain.ID, in service.UpdateChannelInput) (service.ChannelView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateChannel")
	if err := f.fail("UpdateChannel"); err != nil {
		return service.ChannelView{}, err
	}
	for i, c := range f.Channels {
		if c.ID != channelID {
			continue
		}
		if in.Name != nil {
			c.Name = *in.Name
		}
		if in.TokenRef != nil {
			c.TokenRef = *in.TokenRef
		}
		if in.WebhookSecretRef != nil {
			c.WebhookSecretRef = *in.WebhookSecretRef
		}
		if in.Config != nil {
			c.Config = *in.Config
		}
		if in.Enabled != nil {
			if *in.Enabled && (c.TokenRef == "" || c.WebhookSecretRef == "") {
				return service.ChannelView{}, domain.Invalid("token_ref", "informe o segredo com o token para ligar o canal")
			}
			c.Enabled = *in.Enabled
		}
		if in.ClearPersona {
			c.PersonaID, c.PersonaName = nil, ""
		} else if in.PersonaID != nil {
			c.PersonaID = in.PersonaID
			for _, p := range f.Personas {
				if p.ID == *in.PersonaID {
					c.PersonaName = p.Name
				}
			}
		}
		f.Channels[i] = c
		return c, nil
	}
	return service.ChannelView{}, domain.ErrNotFound
}

func (f *Fake) DeleteChannel(_ context.Context, _ domain.Actor, _, channelID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeleteChannel")
	if err := f.fail("DeleteChannel"); err != nil {
		return err
	}
	for i, c := range f.Channels {
		if c.ID == channelID {
			f.Channels = append(f.Channels[:i], f.Channels[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Fake) ConnectChannel(_ context.Context, _ domain.Actor, _, channelID domain.ID) (service.ChannelConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ConnectChannel")
	if err := f.fail("ConnectChannel"); err != nil {
		return service.ChannelConnection{}, err
	}
	for _, c := range f.Channels {
		if c.ID == channelID {
			if c.Kind == domain.ChannelTelegram {
				return service.ChannelConnection{URL: "https://hanko.example.com" + c.HookPath, Registered: true}, nil
			}
			return service.ChannelConnection{URL: "https://hanko.example.com" + c.HookPath, Note: "Configure o webhook no repositório."}, nil
		}
	}
	return service.ChannelConnection{}, domain.ErrNotFound
}

func (f *Fake) CreateChannelLink(_ context.Context, _ domain.Actor, _, channelID domain.ID) (service.ChannelLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateChannelLink")
	if err := f.fail("CreateChannelLink"); err != nil {
		return service.ChannelLink{}, err
	}
	for _, c := range f.Channels {
		if c.ID == channelID {
			return service.ChannelLink{Code: "K7QM-2XPA", ExpiresAt: Now().Add(15 * time.Minute), Channel: c.Name, Kind: string(c.Kind), Hint: "Envie ao bot: /vincular K7QM-2XPA"}, nil
		}
	}
	return service.ChannelLink{}, domain.ErrNotFound
}

func (f *Fake) MyChannelIdentity(_ context.Context, _, channelID domain.ID) (*domain.ChannelIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.ChannelIdents {
		if f.ChannelIdents[i].ChannelID == channelID && f.ChannelIdents[i].UserID == (domain.ID{}) { // UserID zero = a pessoa logada no dublê
			x := f.ChannelIdents[i]
			return &x, nil
		}
	}
	return nil, nil
}

func (f *Fake) ListChannelIdentities(_ context.Context, _, channelID domain.ID) ([]domain.ChannelIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListChannelIdentities"); err != nil {
		return nil, err
	}
	var out []domain.ChannelIdentity
	for _, i := range f.ChannelIdents {
		if i.ChannelID == channelID && i.Status != domain.IdentityRevoked {
			out = append(out, i)
		}
	}
	return out, nil
}

func (f *Fake) RevokeChannelIdentity(_ context.Context, _ domain.Actor, _, _, identityID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RevokeChannelIdentity")
	if err := f.fail("RevokeChannelIdentity"); err != nil {
		return err
	}
	for i := range f.ChannelIdents {
		if f.ChannelIdents[i].ID == identityID {
			f.ChannelIdents[i].Status = domain.IdentityRevoked
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Fake) ListChannelMessages(_ context.Context, _, channelID domain.ID, limit int) ([]domain.ChannelMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListChannelMessages"); err != nil {
		return nil, err
	}
	var out []domain.ChannelMessage
	for _, m := range f.ChannelMsgs {
		if m.ChannelID == channelID {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *Fake) ResendChannelMessage(_ context.Context, _ domain.Actor, _, messageID domain.ID) (domain.ChannelMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ResendChannelMessage")
	if err := f.fail("ResendChannelMessage"); err != nil {
		return domain.ChannelMessage{}, err
	}
	for i := range f.ChannelMsgs {
		if f.ChannelMsgs[i].ID == messageID {
			if f.ChannelMsgs[i].Status != domain.MsgFailed {
				return domain.ChannelMessage{}, fmt.Errorf("%w: só uma mensagem que falhou", domain.ErrConflict)
			}
			f.ChannelMsgs[i].Status = domain.MsgPending
			return f.ChannelMsgs[i], nil
		}
	}
	return domain.ChannelMessage{}, domain.ErrNotFound
}

var _ = strings.TrimSpace
