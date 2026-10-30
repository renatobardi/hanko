package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/store"
)

// Ingestão da fonte externa (ADR-0075 a 0078; ADR-0076 para o listener). Um lote grava numa transação só: um span novo
// traz junto o host, o agente e a conversa que ele diz; um span repetido não muda nada (ADR-0076 item 6).

// TraceIngestResult conta o que um lote trouxe.
type TraceIngestResult struct {
	Accepted   int // spans novos gravados
	Duplicates int // spans que já estavam gravados (mesma chave de dedupe e mesma hora)
}

// ResolveIngestKey devolve a empresa e o id da chave de ingestão que autentica a requisição. Chave de outro escopo, ou
// de ingestão sem empresa, é domain.ErrNotAllowed; chave inválida ou revogada, domain.ErrUnauthenticated (ADR-0076 item 3).
func (s *Service) ResolveIngestKey(ctx context.Context, key string) (companyID, keyID domain.ID, err error) {
	_, info, err := s.ResolveAPIKeyInfo(ctx, key)
	if err != nil {
		return domain.ID{}, domain.ID{}, err
	}
	if info.Scope != domain.KeyScopeIngest || info.Company == (domain.ID{}) {
		return domain.ID{}, domain.ID{}, domain.ErrNotAllowed
	}
	return info.Company, info.ID, nil
}

// CreateIngestAPIKey cria a chave de ingestão de um host da empresa (ADR-0076 item 3): escopo ingest, presa à empresa.
// A chave em claro só sai aqui. Quem chama confere que o pedido é de owner ou admin.
func (s *Service) CreateIngestAPIKey(ctx context.Context, userID, companyID domain.ID, name, device string) (string, domain.APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return "", domain.APIKey{}, domain.Invalid("name", "de 1 a 80 caracteres")
	}
	device = strings.TrimSpace(device)
	key, prefix, hash, err := auth.NewAPIKey()
	if err != nil {
		return "", domain.APIKey{}, err
	}
	id := domain.NewID()
	err = s.withTx(ctx, func(t *txc) error {
		if err := store.InsertIngestAPIKey(ctx, t.tx, id, companyID, userID, name, prefix, hash, device, t.now); err != nil {
			return mapErr(err, "create ingest api key")
		}
		return t.activity(domain.Actor{Type: domain.ActorHuman, ID: &userID}, companyID, "api_key.ingest_created", "api_key", id,
			map[string]any{"name": name, "device": device}, "")
	})
	if err != nil {
		return "", domain.APIKey{}, err
	}
	return key, domain.APIKey{ID: id, UserID: userID, Name: name, Prefix: prefix, Scope: domain.KeyScopeIngest, Device: device,
		CreatedAt: s.Now().UTC()}, nil
}

// IngestTraces grava um lote de spans da empresa, numa transação. O 2xx do listener só sai depois do commit (ADR-0076
// item 7). Repetido não faz nada: não cria conversa, não move atributo nem horário, não soma contador.
func (s *Service) IngestTraces(ctx context.Context, companyID, keyID domain.ID, spans []ingest.Span) (TraceIngestResult, error) {
	var res TraceIngestResult
	err := s.withTx(ctx, func(t *txc) error {
		received := t.now.UnixNano()
		for i := range spans {
			fresh, err := t.ingestSpan(companyID, keyID, &spans[i], received)
			if err != nil {
				return err
			}
			if fresh {
				res.Accepted++
			} else {
				res.Duplicates++
			}
		}
		return nil
	})
	return res, err
}

// ingestSpan grava um span e, se ele é novo, o que ele traz: host, agente e conversa. ok é falso se o span já estava.
func (t *txc) ingestSpan(companyID, keyID domain.ID, sp *ingest.Span, received int64) (bool, error) {
	fresh, err := store.InsertTelemetrySpan(t.ctx, t.tx, spanRow(companyID, keyID, sp, received))
	if err != nil {
		return false, fmt.Errorf("grava span: %w", err)
	}
	if !fresh {
		return false, nil
	}
	hostID, err := t.touchHost(companyID, sp)
	if err != nil {
		return false, err
	}
	agentID, err := t.touchAgent(companyID, sp)
	if err != nil {
		return false, err
	}
	if sp.SessionID == "" {
		return true, nil // sem session.id não há conversa: o span fica só no bruto (ADR-0078)
	}
	return true, t.touchConversation(companyID, sp, hostID, agentID)
}

// touchHost cria o host externo do fato, se o nome é válido, e guarda a hora do último fato (ADR-0077 item 3).
func (t *txc) touchHost(companyID domain.ID, sp *ingest.Span) (*domain.ID, error) {
	if sp.HostName == "" || domain.ValidateExternalName(sp.HostName) != nil {
		return nil, nil
	}
	id, created, err := store.UpsertExternalHost(t.ctx, t.tx, companyID, domain.NewID(), sp.HostName, t.now)
	if err != nil {
		return nil, fmt.Errorf("host externo: %w", err)
	}
	if created {
		if err := t.activity(domain.SystemActor, companyID, "external_host.created", "external_host", id,
			map[string]any{"name": sp.HostName}, ""); err != nil {
			return nil, err
		}
	}
	if err := store.TouchExternalHost(t.ctx, t.tx, id, time.Unix(0, sp.TimeUnixNano).UTC()); err != nil {
		return nil, fmt.Errorf("hora do host externo: %w", err)
	}
	return &id, nil
}

// touchAgent cria o agente externo do fato, se o nome é válido (ADR-0075 item 2).
func (t *txc) touchAgent(companyID domain.ID, sp *ingest.Span) (*domain.ID, error) {
	if sp.OuteAgent == "" || domain.ValidateExternalName(sp.OuteAgent) != nil {
		return nil, nil
	}
	id, created, err := store.UpsertExternalAgent(t.ctx, t.tx, companyID, domain.NewID(), sp.OuteAgent, t.now)
	if err != nil {
		return nil, fmt.Errorf("agente externo: %w", err)
	}
	if created {
		if err := t.activity(domain.SystemActor, companyID, "external_agent.created", "external_agent", id,
			map[string]any{"name": sp.OuteAgent}, ""); err != nil {
			return nil, err
		}
	}
	return &id, nil
}

// touchConversation cria a conversa do session.id, ou a atualiza com o fato novo (ADR-0078 item 1).
func (t *txc) touchConversation(companyID domain.ID, sp *ingest.Span, hostID, agentID *domain.ID) error {
	row, found, err := store.LockExternalConversation(t.ctx, t.tx, companyID, sp.SessionID)
	if err != nil {
		return fmt.Errorf("conversa externa: %w", err)
	}
	if !found {
		return t.createConversation(companyID, sp, hostID, agentID)
	}
	absorbSpan(&row, sp, hostID, agentID)
	if err := store.UpdateExternalConversation(t.ctx, t.tx, row, t.now); err != nil {
		return fmt.Errorf("atualiza conversa externa: %w", err)
	}
	return nil
}

func (t *txc) createConversation(companyID domain.ID, sp *ingest.Span, hostID, agentID *domain.ID) error {
	name, err := domain.ChooseExternalName(sp.SessionID, func(n string) (bool, error) {
		return store.ExternalConversationNameTaken(t.ctx, t.tx, companyID, n)
	})
	if err != nil {
		return fmt.Errorf("nome da conversa externa: %w", err)
	}
	row := store.ExternalConversationRow{ID: domain.NewID(), CompanyID: companyID, SessionID: sp.SessionID, Name: name,
		FirstAt: sp.TimeUnixNano, LastAt: sp.TimeUnixNano}
	absorbSpan(&row, sp, hostID, agentID)
	ok, err := store.InsertExternalConversation(t.ctx, t.tx, row, t.now)
	if err != nil {
		return fmt.Errorf("cria conversa externa: %w", err)
	}
	if !ok {
		// Outra transação criou a mesma conversa ou o mesmo nome antes: o lote tenta de novo (503 no listener).
		return fmt.Errorf("conversa externa %q: gravação concorrente: %w", sp.SessionID, domain.ErrConflict)
	}
	return t.activity(domain.SystemActor, companyID, "external_conversation.created", "external_conversation", row.ID,
		map[string]any{"name": name, "session_id": sp.SessionID}, "")
}

// absorbSpan aplica um fato novo à conversa. Cada atributo fica com o valor da hora mais antiga que o trouxe (ADR-0078
// item 1); a janela da conversa vai do primeiro ao último fato.
func absorbSpan(r *store.ExternalConversationRow, sp *ingest.Span, hostID, agentID *domain.ID) {
	at := sp.TimeUnixNano
	if hostID != nil && (r.HostAt == nil || at < *r.HostAt) {
		r.HostID, r.HostAt = hostID, &at
	}
	if agentID != nil && (r.AgentAt == nil || at < *r.AgentAt) {
		r.AgentID, r.AgentAt = agentID, &at
	}
	absorbText(&r.OuteInstance, &r.OuteInstanceAt, sp.OuteInstance, at)
	absorbText(&r.ServiceName, &r.ServiceNameAt, sp.ServiceName, at)
	absorbText(&r.SubscriptionKey, &r.SubscriptionKeyAt, sp.OuteSubscription, at)
	absorbText(&r.OuteRepo, &r.OuteRepoAt, sp.OuteRepo, at)
	r.FirstAt = min(r.FirstAt, at)
	r.LastAt = max(r.LastAt, at)
}

// absorbText guarda o texto se ele veio e se a hora é mais antiga que a do valor atual.
func absorbText(val *string, when **int64, v string, at int64) {
	if v == "" || (*when != nil && at >= **when) {
		return
	}
	*val, *when = v, &at
}

// spanRow monta a linha do span: os campos vazios viram NULL; o tempo e a chave de dedupe vêm do span.
func spanRow(companyID, keyID domain.ID, sp *ingest.Span, received int64) store.TelemetrySpanRow {
	return store.TelemetrySpanRow{
		CompanyID:          companyID,
		DedupeKey:          sp.DedupeKey,
		TimeUnixNano:       sp.TimeUnixNano,
		EndUnixNano:        optInt64(sp.EndUnixNano),
		DurationNs:         optInt64(sp.DurationNs),
		HostName:           optString(sp.HostName),
		OuteInstance:       optString(sp.OuteInstance),
		OuteAgent:          optString(sp.OuteAgent),
		ServiceName:        optString(sp.ServiceName),
		SessionID:          optString(sp.SessionID),
		OuteTaskID:         optString(sp.OuteTaskID),
		OuteSwarmRound:     optString(sp.OuteSwarmRound),
		OuteRepo:           optString(sp.OuteRepo),
		OuteSubscription:   optString(sp.OuteSubscription),
		TraceID:            sp.TraceID,
		SpanID:             sp.SpanID,
		ParentSpanID:       optString(sp.ParentSpanID),
		Name:               optString(sp.Name),
		Kind:               optInt32(sp.Kind),
		StatusCode:         optInt32(sp.StatusCode),
		StatusMessage:      optString(sp.StatusMessage),
		ScopeName:          optString(sp.ScopeName),
		ResourceAttributes: sp.ResourceAttributes,
		Attributes:         sp.Attributes,
		Events:             sp.Events,
		Links:              sp.Links,
		ReceivedUnixNano:   received,
		IngestedByKeyID:    keyID,
	}
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func optInt32(v int32) *int32 {
	if v == 0 {
		return nil
	}
	return &v
}
