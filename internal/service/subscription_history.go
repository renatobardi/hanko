package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Histórico de mensalidade (ADR-0082 item 6). Cada assinatura tem um histórico que só recebe linhas novas: o valor de um
// dia é o da linha com o maior início menor ou igual a ele, e a registrada por último desempata. `subscriptions.monthly_usd`
// continua como o valor vigente hoje, atualizado na mesma transação em que se acrescenta uma linha.

// SubscriptionMonthlyValueView é uma linha do histórico de mensalidade de uma assinatura.
type SubscriptionMonthlyValueView struct {
	ID         domain.ID
	MonthlyUSD float64
	StartsOn   time.Time
	RecordedAt time.Time
	Origin     string
	ActorType  string
	ActorID    *domain.ID
}

func dayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// addInitialMonthlyValue grava a linha de nascimento da assinatura: o valor dela, com início no dia da criação.
func addInitialMonthlyValue(t *txc, companyID, subID domain.ID, monthly float64) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO subscription_monthly_history (company_id, subscription_id, monthly_usd, starts_on, recorded_at, origin, actor_type)
		VALUES ($1, $2, $3, $4, $5, 'created', 'system')`, companyID, subID, monthly, dayOf(t.now), t.now)
	if err != nil {
		return fmt.Errorf("monthly history: %w", err)
	}
	return nil
}

// addMonthlyValueRow acrescenta uma linha e põe em `monthly_usd` o valor que vale hoje.
func addMonthlyValueRow(t *txc, actor domain.Actor, companyID, subID domain.ID, monthly float64, startsOn time.Time, origin string) error {
	actorType := "system"
	var actorID *domain.ID
	if actor.Type == domain.ActorHuman {
		actorType, actorID = "human", actor.ID
	}
	if _, err := t.tx.Exec(t.ctx, `INSERT INTO subscription_monthly_history (company_id, subscription_id, monthly_usd, starts_on, recorded_at, origin, actor_type, actor_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, companyID, subID, monthly, dayOf(startsOn), t.now, origin, actorType, actorID); err != nil {
		return fmt.Errorf("monthly history: %w", err)
	}
	if _, err := t.tx.Exec(t.ctx, `UPDATE subscriptions SET updated_at = $3, monthly_usd = (
		SELECT h.monthly_usd FROM subscription_monthly_history h WHERE h.subscription_id = $2 AND h.starts_on <= $4::date
		ORDER BY h.starts_on DESC, h.recorded_at DESC, h.id DESC LIMIT 1) WHERE company_id = $1 AND id = $2`,
		companyID, subID, t.now, dayOf(t.now)); err != nil {
		return fmt.Errorf("monthly history: %w", err)
	}
	return t.activity(actor, companyID, "subscription.monthly_value_added", "subscription", subID,
		map[string]any{"monthly_usd": monthly, "starts_on": dayOf(startsOn).Format("2006-01-02"), "origin": origin}, "")
}

// ListSubscriptionMonthlyHistory lista o histórico de mensalidade de uma assinatura, do início mais novo para o mais antigo.
func (s *Service) ListSubscriptionMonthlyHistory(ctx context.Context, companyID domain.ID, ref string) ([]SubscriptionMonthlyValueView, error) {
	st, err := s.GetSubscription(ctx, companyID, ref)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, monthly_usd::float8, starts_on, recorded_at, origin, actor_type, actor_id FROM subscription_monthly_history
		WHERE company_id = $1 AND subscription_id = $2 ORDER BY starts_on DESC, recorded_at DESC, id DESC`, companyID, st.ID)
	if err != nil {
		return nil, fmt.Errorf("monthly history: %w", err)
	}
	defer rows.Close()
	out := []SubscriptionMonthlyValueView{}
	for rows.Next() {
		var v SubscriptionMonthlyValueView
		if err := rows.Scan(&v.ID, &v.MonthlyUSD, &v.StartsOn, &v.RecordedAt, &v.Origin, &v.ActorType, &v.ActorID); err != nil {
			return nil, fmt.Errorf("monthly history: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AddSubscriptionMonthlyValue acrescenta uma linha ao histórico (owner ou admin, com reautenticação recente; ADR-0070). Um
// início no passado muda o "pago" desses dias, e o `activity_event` registra a troca. Início no futuro é recusado: o valor
// vigente (`monthly_usd`) só é atualizado na hora da troca.
func (s *Service) AddSubscriptionMonthlyValue(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, monthly float64, startsOn time.Time) (SubscriptionMonthlyValueView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return SubscriptionMonthlyValueView{}, err
	}
	if math.IsNaN(monthly) || math.IsInf(monthly, 0) || monthly < 0 {
		return SubscriptionMonthlyValueView{}, domain.Invalid("monthly_usd", "must be >= 0")
	}
	identity, _ := auth.IdentityFrom(ctx)
	if _, err := s.RequireReauth(identity); err != nil {
		return SubscriptionMonthlyValueView{}, err
	}
	st, err := s.GetSubscription(ctx, companyID, ref)
	if err != nil {
		return SubscriptionMonthlyValueView{}, err
	}
	if dayOf(startsOn).After(dayOf(s.Now())) {
		return SubscriptionMonthlyValueView{}, domain.Invalid("starts_on", "must not be in the future")
	}
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetSubscriptionByIDForUpdate(ctx, store.GetSubscriptionByIDForUpdateParams{CompanyID: companyID, ID: st.ID}); err != nil {
			return mapErr(err, "get subscription")
		}
		return addMonthlyValueRow(t, actor, companyID, st.ID, monthly, startsOn, "manual")
	})
	if err != nil {
		return SubscriptionMonthlyValueView{}, err
	}
	list, err := s.ListSubscriptionMonthlyHistory(ctx, companyID, st.ID.String())
	if err != nil {
		return SubscriptionMonthlyValueView{}, err
	}
	for _, v := range list {
		if v.MonthlyUSD == monthly && dayOf(v.StartsOn).Equal(dayOf(startsOn)) && v.Origin == "manual" {
			return v, nil // a mais nova com esse valor e início (a lista vem da mais nova para a mais antiga)
		}
	}
	return SubscriptionMonthlyValueView{}, errors.New("monthly history: linha recém-gravada não encontrada")
}

// externalKeyChange diz se a chave externa muda e para quê (nil = tirar).
func externalKeyChange(cur *string, in UpdateSubscriptionInput) (bool, *string, error) {
	if in.ClearExternalKey && in.ExternalKey != nil {
		return false, nil, domain.Invalid("external_key", "cannot set and clear at once")
	}
	if in.ClearExternalKey {
		return cur != nil, nil, nil
	}
	if in.ExternalKey == nil {
		return false, cur, nil
	}
	if err := domain.ValidateSubscriptionExternalKey(*in.ExternalKey); err != nil {
		return false, nil, err
	}
	return cur == nil || *cur != *in.ExternalKey, in.ExternalKey, nil
}

// setExternalKey grava a chave externa da assinatura (nil tira); a chave é única por empresa.
func setExternalKey(t *txc, companyID, subID domain.ID, key *string) error {
	if _, err := t.tx.Exec(t.ctx, `UPDATE subscriptions SET external_key = $3, updated_at = $4 WHERE company_id = $1 AND id = $2`, companyID, subID, key, t.now); err != nil {
		return mapErr(err, "subscription external key")
	}
	return nil
}
