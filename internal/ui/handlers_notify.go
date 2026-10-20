package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
	"github.com/renatobardi/hanko/internal/webpush"
)

// Caixa por pessoa: estado, selo, toasts ao vivo e preferências de aviso (ADR-0042).

// notifyBackend é o que as telas de aviso pedem ao service; o Backend de teste pode não ter.
type notifyBackend interface {
	InboxUnread(ctx context.Context, companyID domain.ID) (int, error)
	NotifyPrefs(ctx context.Context, companyID domain.ID) ([]domain.NotifyPref, error)
	SetNotifyPref(ctx context.Context, companyID domain.ID, p domain.NotifyPref) error
	SendTestNotice(ctx context.Context, companyID domain.ID) error
	PushVapidKey(ctx context.Context) (string, error)
	PushAvailable() bool
	AddPushSubscription(ctx context.Context, in webpush.Subscription, userAgent string) (service.PushSubscriptionView, error)
	ListPushSubscriptions(ctx context.Context) ([]service.PushSubscriptionView, error)
	RemovePushSubscription(ctx context.Context, ref string) error
}

func (s *server) notifyBE() (notifyBackend, bool) {
	b, ok := s.be.(notifyBackend)
	return b, ok
}

// handleInboxState marca itens da Caixa (lido, não lido, arquivar, desarquivar) ou tudo como lido. Depois volta para
// a aba em que a pessoa estava (HX-Current-URL), se for da própria Caixa.
func (s *server) handleInboxState(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.be.(inboxBackend)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("body", "formulário inválido"))
		return
	}
	var refs []service.InboxRef
	for _, v := range r.PostForm["item"] {
		ref, err := service.ParseInboxRef(strings.TrimSpace(v))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		refs = append(refs, ref)
	}
	if _, err := b.SetInboxState(r.Context(), actorFrom(r.Context()), co.ID, domain.InboxAction(r.PostFormValue("action")), refs); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, backToInbox(r, co))
}

// backToInbox é a URL da Caixa de onde veio o pedido (mesma aba), ou a Caixa padrão.
func backToInbox(r *http.Request, co domain.Company) string {
	base := "/c/" + co.Slug + "/caixa"
	if cur := r.Header.Get("HX-Current-URL"); cur != "" {
		if u, err := url.Parse(cur); err == nil && (u.Path == base) {
			if u.RawQuery != "" {
				return base + "?" + u.RawQuery
			}
			return base
		}
	}
	return base
}

// handleInboxBadge é o fragmento do selo do menu: o número de itens que pedem ação e não foram lidos.
func (s *server) handleInboxBadge(w http.ResponseWriter, r *http.Request, co domain.Company) {
	n := 0
	if b, ok := s.notifyBE(); ok {
		var err error
		if n, err = b.InboxUnread(r.Context(), co.ID); err != nil {
			n = 0
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, views.InboxBadge(n))
}

// handleInboxEvents é o stream SSE dos avisos da pessoa (toasts). Só quem está autenticado tem tópico; o acesso à
// empresa é reverificado periodicamente e o stream acaba quando ele cai.
func (s *server) handleInboxEvents(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	idn, ok := auth.IdentityFrom(ctx)
	b, hasBE := s.notifyBE()
	if !ok || !hasBE {
		w.WriteHeader(http.StatusNoContent) // sem pessoa não há tópico; 204 faz o navegador desistir de reconectar
		return
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "keep-alive")
	sub, cancel := s.broker.Subscribe(service.UserTopic(co.ID, idn.User.ID), 32)
	defer cancel()
	if _, err := w.Write([]byte("retry: 5000\n: conectado\n\n")); err != nil || rc.Flush() != nil {
		return
	}
	check := time.NewTicker(15 * s.sseTick)
	defer check.Stop()
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m, open := <-sub:
			if !open {
				return
			}
			raw, err := json.Marshal(m.Data)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: notify\ndata: %s\n\n", raw); err != nil || rc.Flush() != nil {
				return
			}
		case <-check.C:
			if _, err := b.NotifyPrefs(ctx, co.ID); err != nil && accessLost(err) {
				return
			}
		case <-keep.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

var notifyNotices = map[string]string{
	"salvo":   i18n.N("Preferências salvas."),
	"teste":   i18n.N("Aviso de teste enviado conforme as suas preferências de menção."),
	"removed": i18n.N("Aparelho removido."),
}

// notifyKindTexts traduz o rótulo e a ajuda de cada tipo de aviso.
var notifyKindTexts = map[domain.NotifyKind][2]string{
	domain.NotifyMention:   {i18n.N("Menções"), i18n.N("Alguém citou você com @ num comentário.")},
	domain.NotifyGate:      {i18n.N("Gates para decidir"), i18n.N("Um gate novo espera por um humano.")},
	domain.NotifyRunFailed: {i18n.N("Execuções que falharam"), i18n.N("Uma persona não conseguiu terminar o trabalho.")},
	domain.NotifyAlert:     {i18n.N("Alertas operacionais"), i18n.N("Persona silenciosa, execução presa, orçamento perto do teto.")},
}

func (s *server) notifyVM(r *http.Request, co domain.Company) (views.NotifyVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.NotifyVM{Sections: settingsSections(ctx, base, "notificacoes"), Notice: i18n.T(ctx, notifyNotices[r.URL.Query().Get("ok")]),
		Action: base + "/configuracoes/notificacoes", TestURL: base + "/configuracoes/notificacoes/teste",
		SubscribeURL: base + "/configuracoes/notificacoes/aparelhos", RemoveURL: base + "/configuracoes/notificacoes/aparelhos/remover"}
	b, ok := s.notifyBE()
	if _, authed := auth.IdentityFrom(ctx); !ok || !authed {
		return vm, nil // sem pessoa autenticada não há preferências
	}
	vm.Personal, vm.Available = true, b.PushAvailable()
	prefs, err := b.NotifyPrefs(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	for _, p := range prefs {
		t := notifyKindTexts[p.Kind]
		vm.Prefs = append(vm.Prefs, views.NotifyPrefRow{Kind: string(p.Kind), Label: i18n.T(ctx, t[0]), Help: i18n.T(ctx, t[1]), Toast: p.Toast, Push: p.Push})
	}
	if vm.Available {
		if vm.VapidKey, err = b.PushVapidKey(ctx); err != nil {
			return vm, err
		}
	}
	devs, err := b.ListPushSubscriptions(ctx)
	if err != nil {
		return vm, err
	}
	loc, now := loadLoc(co.Timezone), s.now()
	for _, d := range devs {
		row := views.NotifyDevice{ID: d.ID.String(), Name: deviceName(d.UserAgent, d.Endpoint), Since: agoCtx(ctx, d.CreatedAt, now, loc)}
		if d.LastSuccessAt != nil {
			row.Last = agoCtx(ctx, *d.LastSuccessAt, now, loc)
		}
		vm.Devices = append(vm.Devices, row)
	}
	return vm, nil
}

// deviceName é um nome curto para o aparelho: o navegador do user agent, ou o host do serviço de push.
func deviceName(ua, endpoint string) string {
	for _, b := range []string{"Edg", "Firefox", "Chrome", "Safari"} {
		if strings.Contains(ua, b) {
			if b == "Edg" {
				return "Edge"
			}
			return b
		}
	}
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return "—"
}

func (s *server) handleNotifySettings(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm, err := s.notifyVM(r, co)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	sh := s.settingsShell(r, co, i18n.T(r.Context(), "Notificações"))
	s.render(w, r, http.StatusOK, views.NotifyPage(sh, vm))
}

// handleNotifySave grava os canais de cada tipo de aviso (checkboxes toast_<tipo> e push_<tipo>).
func (s *server) handleNotifySave(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.notifyBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	for _, k := range domain.NotifyKinds {
		p := domain.NotifyPref{Kind: k, Toast: r.PostFormValue("toast_"+string(k)) != "", Push: r.PostFormValue("push_"+string(k)) != ""}
		if err := b.SetNotifyPref(r.Context(), co.ID, p); err != nil {
			s.fail(w, r, &co, err)
			return
		}
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/notificacoes?ok=salvo")
}

func (s *server) handleNotifyTest(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.notifyBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	if err := b.SendTestNotice(r.Context(), co.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/notificacoes?ok=teste")
}

// handlePushSubscribe recebe a assinatura de push que o navegador acabou de criar (JSON do script da tela).
func (s *server) handlePushSubscribe(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.notifyBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil {
		s.fail(w, r, &co, domain.Invalid("body", "JSON inválido"))
		return
	}
	if _, err := b.AddPushSubscription(r.Context(), webpush.Subscription{Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth}, r.UserAgent()); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handlePushRemove(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.notifyBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	if err := b.RemovePushSubscription(r.Context(), strings.TrimSpace(r.PostFormValue("id"))); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/notificacoes?ok=removed")
}
