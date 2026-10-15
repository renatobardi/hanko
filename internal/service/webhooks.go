package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/webhook"
)

// Webhooks de saída (ADR-0029). Cada fato que o usuário pode assinar vira, na MESMA transação que o grava
// (txc.activity), uma linha em webhook_deliveries por webhook interessado e um job da fila; o worker faz o
// POST assinado e o retry. Ou a entrega fica registrada junto com o fato, ou nenhuma fica.

const (
	webhookTimeout = 10 * time.Second
	// WebhookMaxAttempts é o total de tentativas de uma entrega (a primeira e as de retry).
	WebhookMaxAttempts = 7
	// webhookKeep é quantas entregas por integração o histórico mantém.
	webhookKeep = 100
)

// WebhookJobs é a parte de Service.Jobs que enfileira entregas de webhook. É opcional: um Jobs que não a
// implementa simplesmente não entrega webhooks (testes de outras áreas continuam valendo sem ela).
type WebhookJobs interface {
	DeliverWebhookTx(ctx context.Context, tx pgx.Tx, deliveryID domain.ID) error
}

// WebhookBackoff é a espera antes da tentativa seguinte, depois da tentativa `attempt` (1-based) que falhou:
// 30 s, 2 min, 8 min, 32 min, ~2 h, 6 h (teto). Cresce por 4 a cada falha.
func WebhookBackoff(attempt int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 4
		if d >= 6*time.Hour {
			return 6 * time.Hour
		}
	}
	return d
}

// webhookEventFor mapeia um fato (verbo da atividade) no evento assinável, se houver.
func webhookEventFor(verb string, data json.RawMessage) (string, bool) {
	var d struct {
		Status string `json:"status"`
	}
	switch verb {
	case "task.created":
		return domain.EventTaskCreated, true
	case "task.closed":
		_ = json.Unmarshal(data, &d)
		return domain.EventTaskCompleted, d.Status == string(domain.TaskDone)
	case "gate.requested":
		return domain.EventGateOpened, true
	case "gate.decided":
		return domain.EventGateDecided, true
	case "run.finished":
		_ = json.Unmarshal(data, &d)
		return domain.EventRunFailed, d.Status == string(domain.RunFailed) || d.Status == string(domain.RunExpired)
	case "alert.opened":
		return domain.EventAlertOpened, true
	}
	return "", false
}

// WebhookEnvelope é o corpo JSON de toda entrega. data é o mesmo conteúdo do feed de atividade, com os
// padrões conhecidos de credencial mascarados.
type WebhookEnvelope struct {
	ID         string          `json:"id" doc:"Id do fato (estável entre tentativas)."`
	Event      string          `json:"event"`
	OccurredAt time.Time       `json:"occurred_at"`
	Company    EnvelopeCompany `json:"company"`
	Subject    EnvelopeSubject `json:"subject"`
	Data       json.RawMessage `json:"data"`
}

type EnvelopeCompany struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

type EnvelopeSubject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// enqueueWebhooks registra as entregas do fato que acabou de ser gravado. Chamada por txc.activity.
func (t *txc) enqueueWebhooks(companyID domain.ID, verb string, activityID domain.ID, subjectType string, subjectID domain.ID, data json.RawMessage) error {
	ev, ok := webhookEventFor(verb, data)
	if !ok {
		return nil
	}
	wj, ok := t.s.Jobs.(WebhookJobs)
	if !ok {
		return nil
	}
	hooks, err := t.q.ListWebhooksForEvent(t.ctx, store.ListWebhooksForEventParams{CompanyID: companyID, Event: ev})
	if err != nil {
		return fmt.Errorf("list webhooks: %w", err)
	}
	if len(hooks) == 0 {
		return nil
	}
	co, err := t.q.GetCompanyByID(t.ctx, companyID)
	if err != nil {
		return mapErr(err, "get company")
	}
	body, err := json.Marshal(WebhookEnvelope{ID: activityID.String(), Event: ev, OccurredAt: t.now,
		Company: EnvelopeCompany{ID: companyID.String(), Slug: co.Slug},
		Subject: EnvelopeSubject{Type: subjectType, ID: subjectID.String()},
		Data:    adapter.RedactJSON(data)})
	if err != nil {
		return fmt.Errorf("marshal webhook envelope: %w", err)
	}
	for _, h := range hooks {
		row, err := t.q.CreateWebhookDelivery(t.ctx, store.CreateWebhookDeliveryParams{ID: domain.NewID(), CompanyID: companyID,
			IntegrationID: h.ID, Event: ev, ActivityID: &activityID, Payload: string(body), Now: t.now})
		if errors.Is(err, pgx.ErrNoRows) {
			continue // já registrada para este fato
		}
		if err != nil {
			return fmt.Errorf("create webhook delivery: %w", err)
		}
		if err := wj.DeliverWebhookTx(t.ctx, t.tx, row.ID); err != nil {
			return fmt.Errorf("enqueue webhook delivery: %w", err)
		}
	}
	return nil
}

// WebhookOutcome é o que o worker faz depois de uma tentativa.
type WebhookOutcome int

const (
	// WebhookDone: nada mais a fazer (entregue, ou a entrega/integração não existe mais).
	WebhookDone WebhookOutcome = iota
	// WebhookRetry: falhou; a fila deve tentar de novo.
	WebhookRetry
	// WebhookGiveUp: falhou de forma definitiva (sem mais tentativas, ou erro que repetir não conserta).
	WebhookGiveUp
)

func deliveryFromRow(r store.WebhookDelivery) domain.WebhookDelivery {
	d := domain.WebhookDelivery{ID: r.ID, IntegrationID: r.IntegrationID, Event: r.Event, Test: r.Test, Status: r.Status,
		Attempts: int(r.Attempts), Error: r.Error, NextAttemptAt: r.NextAttemptAt, CreatedAt: r.CreatedAt, DeliveredAt: r.DeliveredAt}
	if r.HttpStatus != nil {
		v := int(*r.HttpStatus)
		d.HTTPStatus = &v
	}
	if r.LatencyMs != nil {
		v := int(*r.LatencyMs)
		d.LatencyMs = &v
	}
	return d
}

// DeliverWebhook faz uma tentativa de entrega (job deliver_webhook). attempt é o número desta tentativa
// (1-based) e maxAttempts o teto da fila. O resultado fica registrado na linha da entrega.
func (s *Service) DeliverWebhook(ctx context.Context, deliveryID domain.ID, attempt, maxAttempts int) (WebhookOutcome, error) {
	q := s.read()
	d, err := q.GetWebhookDelivery(ctx, deliveryID)
	if isNoRows(err) {
		return WebhookDone, nil
	}
	if err != nil {
		return WebhookRetry, fmt.Errorf("get webhook delivery: %w", err)
	}
	if d.Status == domain.DeliverySuccess || d.Status == domain.DeliveryFailed {
		return WebhookDone, nil
	}
	integ, err := q.GetIntegrationAny(ctx, d.IntegrationID)
	if isNoRows(err) {
		return WebhookDone, nil
	}
	if err != nil {
		return WebhookRetry, fmt.Errorf("get integration: %w", err)
	}
	_, out, err := s.attemptWebhook(ctx, d, integ, attempt, maxAttempts)
	return out, err
}

// attemptWebhook envia o corpo guardado e registra o resultado. É a mesma rotina da fila e do teste.
func (s *Service) attemptWebhook(ctx context.Context, d store.WebhookDelivery, integ store.Integration, attempt, maxAttempts int) (store.WebhookDelivery, WebhookOutcome, error) {
	record := func(status string, res webhook.Result, errText string, next *time.Time) (store.WebhookDelivery, error) {
		now := s.Now().UTC()
		p := store.RecordWebhookAttemptParams{ID: d.ID, Status: status, Error: errText, NextAttemptAt: next}
		if res.Status > 0 {
			v := int32(res.Status) //nolint:gosec // código HTTP (100-599)
			p.HttpStatus = &v
		}
		if res.Latency > 0 {
			v := i32(int(res.Latency / time.Millisecond))
			p.LatencyMs = &v
		}
		if status == domain.DeliverySuccess {
			p.DeliveredAt = &now
		}
		row, err := store.New(s.Pool).RecordWebhookAttempt(ctx, p)
		if err != nil {
			return d, fmt.Errorf("record webhook attempt: %w", err)
		}
		if err := store.New(s.Pool).PruneWebhookDeliveries(ctx, store.PruneWebhookDeliveriesParams{IntegrationID: d.IntegrationID, Keep: webhookKeep}); err != nil && s.Log != nil {
			s.Log.Warn("webhook: podar histórico", "integration_id", d.IntegrationID, "err", err)
		}
		return row, nil
	}
	giveUp := func(msg string, res webhook.Result) (store.WebhookDelivery, WebhookOutcome, error) {
		row, err := record(domain.DeliveryFailed, res, msg, nil)
		return row, WebhookGiveUp, err
	}

	if !integ.Enabled && !d.Test {
		return giveUp("integração desligada", webhook.Result{})
	}
	cfg := parseIntegrationConfig(integ.Config)
	// A política pode ter mudado desde que o webhook foi salvo: confere de novo antes de qualquer conexão.
	if _, err := netguard.ValidateURL(cfg.URL, false, s.Integrations.policy()); err != nil {
		return giveUp("destino não permitido: "+err.Error(), webhook.Result{})
	}
	sec, err := s.open(integ.ID, integ.SecretEnc)
	if err != nil || sec.Secret == "" {
		return giveUp("segredo do webhook indisponível", webhook.Result{})
	}
	cctx, cancel := context.WithTimeout(ctx, webhookTimeout+2*time.Second)
	defer cancel()
	res := webhook.Send(cctx, s.Integrations.client(), cfg.URL, sec.Secret, d.ID.String(), d.Event, []byte(d.Payload), s.Now().UTC())
	if res.OK() {
		row, err := record(domain.DeliverySuccess, res, "", nil)
		return row, WebhookDone, err
	}
	msg := redactSecrets(res.Err, sec)
	if strings.Contains(msg, netguard.ErrBlocked.Error()) {
		return giveUp("destino não permitido: "+netguard.ErrBlocked.Error(), res)
	}
	if attempt >= maxAttempts {
		return giveUp(msg, res)
	}
	next := s.Now().UTC().Add(WebhookBackoff(attempt))
	row, err := record(domain.DeliveryRetrying, res, msg, &next)
	return row, WebhookRetry, err
}

// ListWebhookDeliveries devolve as últimas entregas de um webhook.
func (s *Service) ListWebhookDeliveries(ctx context.Context, companyID, integrationID domain.ID, limit int) ([]domain.WebhookDelivery, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: integrationID}); err != nil {
		return nil, mapErr(err, "get integration")
	}
	rows, err := q.ListWebhookDeliveries(ctx, store.ListWebhookDeliveriesParams{CompanyID: companyID, IntegrationID: integrationID, Lim: i32(clamp(limit, 30, 100))})
	if err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	out := make([]domain.WebhookDelivery, len(rows))
	for i, r := range rows {
		out[i] = deliveryFromRow(r)
	}
	return out, nil
}

// IntegrationTest é o resultado do botão "Testar".
type IntegrationTest struct {
	OK        bool                    `json:"ok"`
	Status    int                     `json:"status,omitempty"`
	LatencyMs int                     `json:"latency_ms,omitempty"`
	Error     string                  `json:"error,omitempty"`
	Note      string                  `json:"note,omitempty"`
	Delivery  *domain.WebhookDelivery `json:"delivery,omitempty"`
}

// TestIntegration testa uma integração (admin). Webhook: manda um evento `ping` assinado, registrado no
// histórico, sem retry. MCP http: faz o `initialize` do protocolo no endereço, com os headers guardados.
// MCP stdio: só confere que o comando existe (o teste nunca executa o comando). Os dois passam pela
// proteção SSRF.
func (s *Service) TestIntegration(ctx context.Context, _ domain.Actor, companyID, id domain.ID) (IntegrationTest, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return IntegrationTest{}, err
	}
	integ, err := s.read().GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: id})
	if err != nil {
		return IntegrationTest{}, mapErr(err, "get integration")
	}
	cfg := parseIntegrationConfig(integ.Config)
	switch domain.IntegrationKind(integ.Kind) {
	case domain.IntegrationWebhook:
		return s.testWebhook(ctx, companyID, integ)
	default:
		if cfg.Transport == domain.TransportStdio {
			if !s.Integrations.AllowStdio {
				return IntegrationTest{Error: "stdio está desligado neste servidor (HANKO_INTEGRATIONS_ALLOW_STDIO)"}, nil
			}
			if _, err := exec.LookPath(cfg.Command); err != nil {
				return IntegrationTest{Error: "comando não encontrado no servidor"}, nil
			}
			return IntegrationTest{OK: true, Note: "o comando existe; o teste não o executa"}, nil
		}
		return s.testMCPHTTP(ctx, integ, cfg), nil
	}
}

func (s *Service) testWebhook(ctx context.Context, companyID domain.ID, integ store.Integration) (IntegrationTest, error) {
	co, err := s.read().GetCompanyByID(ctx, companyID)
	if err != nil {
		return IntegrationTest{}, mapErr(err, "get company")
	}
	if integ.SecretEnc == "" {
		return IntegrationTest{Error: "defina o segredo do webhook antes de testar"}, nil
	}
	did := domain.NewID()
	now := s.Now().UTC()
	body, err := json.Marshal(WebhookEnvelope{ID: did.String(), Event: domain.EventPing, OccurredAt: now,
		Company: EnvelopeCompany{ID: companyID.String(), Slug: co.Slug},
		Subject: EnvelopeSubject{Type: "integration", ID: integ.ID.String()},
		Data:    json.RawMessage(`{"message":"entrega de teste do Hanko"}`)})
	if err != nil {
		return IntegrationTest{}, fmt.Errorf("marshal ping: %w", err)
	}
	row, err := store.New(s.Pool).CreateWebhookDelivery(ctx, store.CreateWebhookDeliveryParams{ID: did, CompanyID: companyID,
		IntegrationID: integ.ID, Event: domain.EventPing, Test: true, Payload: string(body), Now: now})
	if err != nil {
		return IntegrationTest{}, fmt.Errorf("create test delivery: %w", err)
	}
	rec, _, err := s.attemptWebhook(ctx, row, integ, 1, 1)
	if err != nil {
		return IntegrationTest{}, err
	}
	d := deliveryFromRow(rec)
	out := IntegrationTest{OK: rec.Status == domain.DeliverySuccess, Error: rec.Error, Delivery: &d}
	if d.HTTPStatus != nil {
		out.Status = *d.HTTPStatus
	}
	if d.LatencyMs != nil {
		out.LatencyMs = *d.LatencyMs
	}
	return out, nil
}

func (s *Service) testMCPHTTP(ctx context.Context, integ store.Integration, cfg domain.IntegrationConfig) IntegrationTest {
	u, err := netguard.ValidateURL(cfg.URL, true, s.Integrations.policy())
	if err != nil {
		return IntegrationTest{Error: "destino não permitido: " + err.Error()}
	}
	sec, err := s.open(integ.ID, integ.SecretEnc)
	if err != nil {
		return IntegrationTest{Error: "segredos indisponíveis: " + err.Error()}
	}
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"hanko","version":"1"}}}`
	cctx, cancel := context.WithTimeout(ctx, webhookTimeout+2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, u.String(), bytes.NewReader([]byte(initialize)))
	if err != nil {
		return IntegrationTest{Error: "requisição inválida"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", webhook.UserAgent)
	for k, v := range sec.Headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := s.Integrations.client().Do(req)
	lat := int(time.Since(start) / time.Millisecond)
	if err != nil {
		msg := "sem resposta"
		if strings.Contains(err.Error(), netguard.ErrBlocked.Error()) {
			msg = "destino não permitido: " + netguard.ErrBlocked.Error()
		} else if errors.Is(err, context.DeadlineExceeded) {
			msg = "tempo esgotado"
		}
		return IntegrationTest{LatencyMs: lat, Error: msg}
	}
	defer resp.Body.Close()
	t := IntegrationTest{Status: resp.StatusCode, LatencyMs: lat}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return t
	}
	t.OK = true
	return t
}
