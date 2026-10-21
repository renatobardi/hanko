package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/webpush"
)

// Avisos por pessoa (ADR-0042 §A): toast ao vivo (SSE por usuário) e web push, conforme as preferências. A entrega é
// best-effort e assíncrona, depois do commit: um aviso perdido nunca desfaz nem atrasa a escrita (o item continua na Caixa).

const (
	// vapidSecretName é a chave em instance_secrets onde o par VAPID fica cifrado.
	vapidSecretName = "vapid"
	// pushMaxFailures: falhas seguidas de uma assinatura antes de ela ser apagada.
	pushMaxFailures = 5
	// notifyTimeout é o teto de uma entrega inteira (todos os alvos e canais).
	notifyTimeout = 30 * time.Second
	// maxPushSubscriptionsPerUser limita os aparelhos de uma pessoa.
	maxPushSubscriptionsPerUser = 10
	maxNoticeBodyRunes          = 100
)

// PushConfig é a configuração do web push do operador. O zero value serve: cliente com proteção SSRF e contato padrão.
type PushConfig struct {
	// Subject identifica o operador perante o serviço de push ("mailto:..." ou URL https); HANKO_PUSH_SUBJECT.
	Subject string
	// HTTP substitui o cliente de saída (testes). Nil usa o cliente com a proteção SSRF de netguard.
	HTTP *http.Client
	// AllowInsecureEndpoints aceita endpoints http e internos (só testes, com um httptest como serviço de push).
	AllowInsecureEndpoints bool
}

func (c PushConfig) subject() string {
	if c.Subject != "" {
		return c.Subject
	}
	return "mailto:admin@hanko.invalid"
}

func (c PushConfig) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return netguard.NewClient(netguard.Policy{}, 10*time.Second)
}

// Notice é o conteúdo de um aviso.
type Notice struct {
	Title   string
	Body    string
	URL     string // caminho relativo da UI
	TaskKey string
}

// notifyPayload é o que viaja ao navegador (toast SSE e push). Nada sensível: título, trecho curto e um caminho.
type notifyPayload struct {
	Kind  domain.NotifyKind `json:"kind"`
	Title string            `json:"title"`
	Body  string            `json:"body,omitempty"`
	URL   string            `json:"url,omitempty"`
	Key   string            `json:"key,omitempty"`
	Tag   string            `json:"tag,omitempty"`
}

var (
	notifyOnce    sync.Once
	notifyCounter metric.Int64Counter
)

func countNotify(ctx context.Context, kind domain.NotifyKind, channel, result string) {
	notifyOnce.Do(func() {
		c, err := otel.Meter("hanko/service").Int64Counter("hanko.notifications",
			metric.WithDescription("Avisos por pessoa: toast e web push, por tipo, canal e resultado"))
		if err == nil {
			notifyCounter = c
		}
	})
	if notifyCounter != nil {
		notifyCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", string(kind)),
			attribute.String("channel", channel), attribute.String("result", result)))
	}
}

// UserTopic é o tópico do broker com os avisos de uma pessoa numa empresa.
func UserTopic(companyID, userID domain.ID) string {
	return events.UserTopic(companyID.String(), userID.String())
}

// notify enfileira o aviso para depois do commit.
func (t *txc) notify(companyID domain.ID, kind domain.NotifyKind, users []domain.ID, n Notice) {
	if len(users) == 0 {
		return
	}
	s := t.s
	t.after = append(t.after, func() { s.dispatchNotice(companyID, kind, users, n) })
}

// notifyMembers avisa os membros que trabalham na empresa (member para cima), menos quem causou o fato.
func (t *txc) notifyMembers(companyID domain.ID, kind domain.NotifyKind, except *domain.ID, n Notice) error {
	ids, err := t.q.ListMemberIDsAtLeast(t.ctx, companyID)
	if err != nil {
		return fmt.Errorf("notify: list members: %w", err)
	}
	var users []domain.ID
	for _, id := range ids {
		if except == nil || id != *except {
			users = append(users, id)
		}
	}
	t.notify(companyID, kind, users, n)
	return nil
}

// notifyState é o estado mutável dos avisos, compartilhado pelas cópias do Service.
type notifyState struct {
	wg      sync.WaitGroup // entregas em andamento (WaitNotifications)
	vapidMu sync.Mutex
	vapid   webpush.VAPID // cache do par VAPID lido de instance_secrets
}

var fallbackNotifier notifyState

func (s *Service) ns() *notifyState {
	if s.notifier != nil {
		return s.notifier
	}
	return &fallbackNotifier
}

// dispatchNotice entrega em segundo plano; WaitNotifications espera (testes e desligamento).
func (s *Service) dispatchNotice(companyID domain.ID, kind domain.NotifyKind, users []domain.ID, n Notice) {
	s.ns().wg.Add(1)
	go func() {
		defer s.ns().wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()
		s.deliverNotice(ctx, companyID, kind, users, n)
	}()
}

// WaitNotifications espera as entregas em andamento (ou ctx acabar). Os testes a chamam antes de conferir o push.
func (s *Service) WaitNotifications(ctx context.Context) {
	done := make(chan struct{})
	go func() { s.ns().wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// effectivePrefs resolve as preferências de cada usuário para o tipo: o padrão do código, sobrescrito pelo que a pessoa guardou.
func effectivePrefs(kind domain.NotifyKind, users []domain.ID, rows []store.NotificationPref) map[domain.ID]domain.NotifyPref {
	out := make(map[domain.ID]domain.NotifyPref, len(users))
	for _, u := range users {
		out[u] = domain.DefaultNotifyPref(kind)
	}
	for _, r := range rows {
		if domain.NotifyKind(r.Kind) == kind {
			if _, ok := out[r.UserID]; ok {
				out[r.UserID] = domain.NotifyPref{Kind: kind, Toast: r.Toast, Push: r.Push}
			}
		}
	}
	return out
}

func (s *Service) deliverNotice(ctx context.Context, companyID domain.ID, kind domain.NotifyKind, users []domain.ID, n Notice) {
	ctx, span := otel.Tracer("hanko/notify").Start(ctx, "hanko.notify.dispatch", trace.WithAttributes(
		attribute.String("hanko.notify.kind", string(kind)), attribute.Int("hanko.notify.users", len(users))))
	defer span.End()
	q := s.read()
	rows, err := q.ListNotificationPrefsForUsers(ctx, store.ListNotificationPrefsForUsersParams{CompanyID: companyID, UserIds: users})
	if err != nil {
		s.logf("notify: preferências: %v", err)
		return
	}
	prefs := effectivePrefs(kind, users, rows)
	payload := notifyPayload{Kind: kind, Title: n.Title, Body: clipRunes(n.Body, maxNoticeBodyRunes), URL: n.URL, Key: n.TaskKey, Tag: string(kind) + ":" + n.TaskKey}

	var pushUsers []domain.ID
	for _, u := range users {
		p := prefs[u]
		if p.Toast && s.Broker != nil {
			s.Broker.Publish(events.Message{Topic: UserTopic(companyID, u), Type: "notify", Data: payload})
			countNotify(ctx, kind, "toast", "sent")
		}
		if p.Push {
			pushUsers = append(pushUsers, u)
		}
	}
	if len(pushUsers) > 0 {
		s.pushNotice(ctx, kind, pushUsers, payload)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(fmt.Sprintf(format, args...))
	}
}

// pushNotice envia o aviso a todos os aparelhos dos usuários. Sem chave VAPID (sem HANKO_SECRET_KEY) não envia.
func (s *Service) pushNotice(ctx context.Context, kind domain.NotifyKind, users []domain.ID, payload notifyPayload) {
	q := s.read()
	subs, err := q.ListPushSubscriptionsForUsers(ctx, users)
	if err != nil {
		s.logf("notify: assinaturas: %v", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	vapid, err := s.vapidKey(ctx)
	if err != nil {
		countNotify(ctx, kind, "push", "disabled")
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	sender := &webpush.Sender{HTTP: s.Push.client(), VAPID: vapid, Subject: s.Push.subject(), Now: s.Now}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // no máximo 8 envios simultâneos
	for _, sub := range subs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			err := sender.Send(ctx, webpush.Subscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}, body)
			switch {
			case err == nil:
				_ = q.RecordPushSuccess(ctx, store.RecordPushSuccessParams{ID: sub.ID, Now: ptr(s.Now().UTC())})
				countNotify(ctx, kind, "push", "sent")
			case errors.Is(err, webpush.ErrGone):
				_ = q.DeletePushSubscriptionByID(ctx, sub.ID)
				countNotify(ctx, kind, "push", "gone")
			default:
				countNotify(ctx, kind, "push", "failed")
				s.logf("notify: push falhou: %v", err)
				if n, ferr := q.RecordPushFailure(ctx, sub.ID); ferr == nil && n >= pushMaxFailures {
					_ = q.DeletePushSubscriptionByID(ctx, sub.ID)
				}
			}
		}()
	}
	wg.Wait()
}

// ---------- chave VAPID ----------

// vapidKey devolve o par VAPID da instalação; gera e guarda cifrado na primeira vez. Sem chave de cifragem do servidor
// (HANKO_SECRET_KEY) o web push fica desligado: ErrPushUnavailable.
func (s *Service) vapidKey(ctx context.Context) (webpush.VAPID, error) {
	st := s.ns()
	st.vapidMu.Lock()
	defer st.vapidMu.Unlock()
	if st.vapid.Valid() {
		return st.vapid, nil
	}
	box := s.secretBox()
	if box == nil || !box.Enabled() {
		return webpush.VAPID{}, ErrPushUnavailable
	}
	aad := "instance_secret/" + vapidSecretName
	q := s.read()
	load := func() (webpush.VAPID, bool, error) {
		row, err := q.GetInstanceSecret(ctx, vapidSecretName)
		if err != nil {
			if isNoRows(err) {
				return webpush.VAPID{}, false, nil
			}
			return webpush.VAPID{}, false, fmt.Errorf("get instance secret: %w", err)
		}
		plain, err := box.Open(row.ValueEnc, aad)
		if err != nil {
			return webpush.VAPID{}, false, fmt.Errorf("open vapid key: %w", err)
		}
		v, err := webpush.ParseVAPID(plain)
		return v, err == nil, err
	}
	if v, ok, err := load(); err != nil || ok {
		if err == nil {
			st.vapid = v
		}
		return v, err
	}
	v, err := webpush.GenerateVAPID()
	if err != nil {
		return webpush.VAPID{}, err
	}
	raw, err := v.Marshal()
	if err != nil {
		return webpush.VAPID{}, err
	}
	enc, err := box.Seal(raw, aad)
	if err != nil {
		return webpush.VAPID{}, fmt.Errorf("seal vapid key: %w", err)
	}
	now := s.Now().UTC()
	if _, err := q.InsertInstanceSecret(ctx, store.InsertInstanceSecretParams{Name: vapidSecretName, ValueEnc: enc, Now: now}); err != nil {
		return webpush.VAPID{}, fmt.Errorf("insert vapid key: %w", err)
	}
	// outra instância pode ter gravado primeiro (DO NOTHING): vale o que está no banco
	v, ok, err := load()
	if err != nil || !ok {
		return webpush.VAPID{}, fmt.Errorf("reload vapid key: %v", err)
	}
	st.vapid = v
	return v, nil
}

// ErrPushUnavailable: o web push está desligado nesta instalação (sem HANKO_SECRET_KEY para guardar a chave VAPID).
var ErrPushUnavailable = domain.Invalid("push", "o web push precisa de HANKO_SECRET_KEY no servidor para guardar a chave VAPID")

// PushVapidKey devolve a chave pública que o navegador usa em pushManager.subscribe. Exige usuário autenticado.
func (s *Service) PushVapidKey(ctx context.Context) (string, error) {
	if _, ok := callerUser(ctx); !ok {
		return "", domain.Invalid("user", "o web push exige uma pessoa autenticada")
	}
	v, err := s.vapidKey(ctx)
	if err != nil {
		return "", err
	}
	return v.PublicKey(), nil
}

// PushAvailable diz se esta instalação pode enviar push (há chave de cifragem).
func (s *Service) PushAvailable() bool {
	b := s.secretBox()
	return b != nil && b.Enabled()
}

// ---------- assinaturas ----------

// PushSubscriptionView é uma assinatura sem as chaves (nada sensível volta).
type PushSubscriptionView struct {
	ID            domain.ID  `json:"id"`
	Endpoint      string     `json:"endpoint"`
	UserAgent     string     `json:"user_agent,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
}

// AddPushSubscription guarda a assinatura de push do aparelho da pessoa autenticada. O endpoint tem de ser https e
// não pode apontar para a rede interna (SSRF); reassinar o mesmo endpoint atualiza as chaves.
func (s *Service) AddPushSubscription(ctx context.Context, in webpush.Subscription, userAgent string) (PushSubscriptionView, error) {
	uid, ok := callerUser(ctx)
	if !ok {
		return PushSubscriptionView{}, domain.Invalid("user", "o web push exige uma pessoa autenticada")
	}
	if !s.PushAvailable() {
		return PushSubscriptionView{}, ErrPushUnavailable
	}
	u, err := netguard.ValidateURL(in.Endpoint, s.Push.AllowInsecureEndpoints, netguard.Policy{AllowPrivate: s.Push.AllowInsecureEndpoints})
	if err != nil || len(u.String()) > 2048 {
		return PushSubscriptionView{}, domain.Invalid("endpoint", "use o endereço https que o navegador entregou")
	}
	if _, err := webpush.Encrypt(webpush.Subscription{Endpoint: u.String(), P256dh: in.P256dh, Auth: in.Auth}, []byte("x")); err != nil {
		return PushSubscriptionView{}, domain.Invalid("keys", "chaves p256dh/auth inválidas")
	}
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	var out PushSubscriptionView
	err = s.withTx(ctx, func(t *txc) error {
		n, err := t.q.ListPushSubscriptions(ctx, uid)
		if err != nil {
			return fmt.Errorf("list push subscriptions: %w", err)
		}
		exists := false
		for _, r := range n {
			exists = exists || r.Endpoint == u.String()
		}
		if !exists && len(n) >= maxPushSubscriptionsPerUser {
			return domain.Invalid("endpoint", fmt.Sprintf("no máximo %d aparelhos por pessoa; remova um", maxPushSubscriptionsPerUser))
		}
		row, err := t.q.UpsertPushSubscription(ctx, store.UpsertPushSubscriptionParams{ID: domain.NewID(), UserID: uid, Endpoint: u.String(),
			P256dh: in.P256dh, Auth: in.Auth, UserAgent: userAgent, Now: t.now})
		if err != nil {
			return mapErr(err, "save push subscription")
		}
		out = PushSubscriptionView{ID: row.ID, Endpoint: row.Endpoint, UserAgent: row.UserAgent, CreatedAt: row.CreatedAt, LastSuccessAt: row.LastSuccessAt}
		return nil
	})
	return out, err
}

// ListPushSubscriptions lista os aparelhos da pessoa autenticada.
func (s *Service) ListPushSubscriptions(ctx context.Context) ([]PushSubscriptionView, error) {
	uid, ok := callerUser(ctx)
	if !ok {
		return nil, domain.Invalid("user", "o web push exige uma pessoa autenticada")
	}
	rows, err := s.read().ListPushSubscriptions(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	out := make([]PushSubscriptionView, len(rows))
	for i, r := range rows {
		out[i] = PushSubscriptionView{ID: r.ID, Endpoint: r.Endpoint, UserAgent: r.UserAgent, CreatedAt: r.CreatedAt, LastSuccessAt: r.LastSuccessAt}
	}
	return out, nil
}

// RemovePushSubscription apaga um aparelho da pessoa (por id ou endpoint). Não existir é ErrNotFound.
func (s *Service) RemovePushSubscription(ctx context.Context, ref string) error {
	uid, ok := callerUser(ctx)
	if !ok {
		return domain.Invalid("user", "o web push exige uma pessoa autenticada")
	}
	n, err := s.read().DeletePushSubscription(ctx, store.DeletePushSubscriptionParams{UserID: uid, Ref: ref})
	if err != nil {
		return fmt.Errorf("delete push subscription: %w", err)
	}
	if n == 0 {
		return notFound("push subscription")
	}
	return nil
}

// ---------- preferências ----------

// NotifyPrefs devolve as preferências efetivas (padrão + o que a pessoa mudou) de todos os tipos, na ordem da tela.
func (s *Service) NotifyPrefs(ctx context.Context, companyID domain.ID) ([]domain.NotifyPref, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	uid, ok := callerUser(ctx)
	if !ok {
		return nil, domain.Invalid("user", "as preferências de aviso exigem uma pessoa autenticada")
	}
	rows, err := s.read().ListNotificationPrefs(ctx, store.ListNotificationPrefsParams{CompanyID: companyID, UserID: uid})
	if err != nil {
		return nil, fmt.Errorf("list notification prefs: %w", err)
	}
	out := make([]domain.NotifyPref, 0, len(domain.NotifyKinds))
	for _, k := range domain.NotifyKinds {
		out = append(out, effectivePrefs(k, []domain.ID{uid}, rows)[uid])
	}
	return out, nil
}

// SetNotifyPref grava a preferência de um tipo para a pessoa autenticada.
func (s *Service) SetNotifyPref(ctx context.Context, companyID domain.ID, p domain.NotifyPref) error {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return err
	}
	uid, ok := callerUser(ctx)
	if !ok {
		return domain.Invalid("user", "as preferências de aviso exigem uma pessoa autenticada")
	}
	if !p.Kind.Valid() {
		return domain.Invalid("kind", "use mention, gate, run_failed ou alert")
	}
	return s.withTx(ctx, func(t *txc) error {
		if err := t.q.UpsertNotificationPref(ctx, store.UpsertNotificationPrefParams{CompanyID: companyID, UserID: uid, Kind: string(p.Kind),
			Toast: p.Toast, Push: p.Push, Now: t.now}); err != nil {
			return fmt.Errorf("upsert notification pref: %w", err)
		}
		return nil
	})
}

// SendTestNotice manda um aviso de teste à própria pessoa pelos canais que ela ligou para o tipo mention.
func (s *Service) SendTestNotice(ctx context.Context, companyID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return err
	}
	uid, ok := callerUser(ctx)
	if !ok {
		return domain.Invalid("user", "o aviso de teste exige uma pessoa autenticada")
	}
	co, err := s.read().GetCompanyByID(ctx, companyID)
	if err != nil {
		return mapErr(err, "company")
	}
	s.dispatchNotice(companyID, domain.NotifyMention, []domain.ID{uid}, Notice{Title: "Aviso de teste do Hanko", Body: "Se você está lendo isto, os avisos funcionam.", URL: "/c/" + co.Slug + "/caixa"})
	return nil
}
