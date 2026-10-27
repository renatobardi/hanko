package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Alertas de agente externo e ajustes por empresa (ADR-0083). Os 17 tipos são os do agent-studio
// (`alerts.py`, `price_alerts.py` e `cost_alerts.py@845764f`), com `price_fixed_differs` renomeado para
// `price_locked_differs` (ADR-0083 item 1). `AlertKinds` continua sendo a lista dos tipos que o Hanko mede em
// /metrics; os tipos externos têm a lista própria `ExternalAlertKinds`.

const (
	AlertQueue                    AlertKind = "queue"
	AlertDestinationRefusing      AlertKind = "destination_refusing"
	AlertHostNoData               AlertKind = "host_no_data"
	AlertSpool                    AlertKind = "spool"
	AlertQuota                    AlertKind = "quota"
	AlertRoundStalled             AlertKind = "round_stalled"
	AlertRoundOld                 AlertKind = "round_old"
	AlertLLMProxyDown             AlertKind = "llm_proxy_down"
	AlertMemoryCapture            AlertKind = "memory_capture"
	AlertMemoryHooksUntrusted     AlertKind = "memory_hooks_untrusted"
	AlertPriceChanged             AlertKind = "price_changed"
	AlertPriceSourcesDiverge      AlertKind = "price_sources_diverge"
	AlertPriceSourceDown          AlertKind = "price_source_down"
	AlertPriceModelUnpriced       AlertKind = "price_model_unpriced"
	AlertPriceLockedDiffers       AlertKind = "price_locked_differs"
	AlertCostClaudeDiff           AlertKind = "cost_claude_diff"
	AlertCostSubscriptionUnpriced AlertKind = "cost_subscription_unpriced"
)

// ExternalAlertKinds lista os 17 tipos de agente externo.
var ExternalAlertKinds = []AlertKind{
	AlertQueue, AlertDestinationRefusing, AlertHostNoData, AlertSpool, AlertQuota, AlertRoundStalled, AlertRoundOld,
	AlertLLMProxyDown, AlertMemoryCapture, AlertMemoryHooksUntrusted,
	AlertPriceChanged, AlertPriceSourcesDiverge, AlertPriceSourceDown, AlertPriceModelUnpriced, AlertPriceLockedDiffers,
	AlertCostClaudeDiff, AlertCostSubscriptionUnpriced,
}

// IsExternal informa se o tipo é um dos 17 de agente externo.
func (k AlertKind) IsExternal() bool {
	for _, v := range ExternalAlertKinds {
		if k == v {
			return true
		}
	}
	return false
}

// ExternalAlertSeverity é a severidade de um tipo externo (ADR-0083 item 5): `critical` para o que perde dado ou trava
// trabalho (`host_no_data`, `destination_refusing`, `spool` e `quota`), `warning` para os outros 13.
func ExternalAlertSeverity(k AlertKind) AlertSeverity {
	switch k {
	case AlertHostNoData, AlertDestinationRefusing, AlertSpool, AlertQuota:
		return AlertCritical
	}
	return AlertWarning
}

// Sujeitos novos de alerta (ADR-0083 item 1), além de persona, run, company e secret.
const (
	AlertSubjectExternalHost  = "external_host"
	AlertSubjectExternalAgent = "external_agent"
	AlertSubjectExternalRound = "external_round"
	AlertSubjectModel         = "model"
	AlertSubjectPriceSource   = "price_source"
)

// alertSubjectNamespace é o espaço de nomes dos ids derivados de sujeito (UUID v5).
var alertSubjectNamespace = uuid.MustParse("6b6f6d68-6561-4c74-8000-000000000083")

// AlertSubjectID deriva o id de um sujeito que não tem id (modelo, fonte de preço) ou cuja identidade tem várias
// dimensões (ADR-0083 item 1): um UUID v5 da empresa, do tipo e das dimensões, na ordem dada. O nome legível vai em
// `data`. A mesma entrada dá sempre o mesmo id; uma dimensão diferente dá outro.
func AlertSubjectID(company ID, kind AlertKind, dims ...string) ID {
	return uuid.NewSHA1(alertSubjectNamespace, []byte(company.String()+"\x00"+string(kind)+"\x00"+strings.Join(dims, "\x00")))
}

// ---------- ajustes por empresa ----------

// AlertSettingSpec descreve um ajuste de alerta de uma empresa (ADR-0083 itens 3 e 6): a chave, o padrão e a faixa.
type AlertSettingSpec struct {
	Key      string
	Default  float64
	Min, Max float64
	Unit     string
}

// AlertDueMaxHours é o maior prazo de "vence em": 30 dias (ADR-0083 item 6).
const AlertDueMaxHours = 720

// AlertLimitSpecs são os limites por empresa, com os valores da Oute como padrão (`config.toml:154-200@845764f`). A
// faixa de cada um é do autor: uma troca fora dela é recusada.
var AlertLimitSpecs = []AlertSettingSpec{
	{"limit.claude_cost_diff_pct", 10, 0, 1000, "%"},
	{"limit.host_no_data_minutes", 30, 1, 10080, "min"},
	{"limit.queue_max_ratio", 0.5, 0.01, 1, "ratio"},
	{"limit.refused_window_minutes", 15, 1, 10080, "min"},
	{"limit.spool_max_bytes", 41943040, 1, 1 << 40, "bytes"},
	{"limit.spool_dropped_window_minutes", 60, 1, 10080, "min"},
	{"limit.lookback_hours", 24, 1, 720, "h"},
	{"limit.llm_proxy_stale_minutes", 5, 1, 10080, "min"},
	{"limit.memory_capture_max_missing_pct", 20, 0, 100, "%"},
	{"limit.memory_capture_days", 7, 1, 30, "days"},
	{"limit.band_recent_hours", 2, 1, 720, "h"},
	{"limit.quota_max_pct", 98, 1, 100, "%"},
	{"limit.quota_reset_grace_minutes", 20, 0, 10080, "min"},
	{"limit.round_stalled_minutes", 30, 1, 10080, "min"},
}

// AlertDueRoundQuestion é a chave do prazo da pergunta da rodada, que também vence em 24 h (ADR-0083 item 6).
const AlertDueRoundQuestion = "due.round_question"

// AlertDueDefaultHours é o prazo padrão de "vence em" de um tipo: 24 h para os alertas de agente externo, e 0 ("sem
// prazo") para os que o Hanko já tinha, para que nada mude para quem já usa (ADR-0083 item 6).
func AlertDueDefaultHours(k AlertKind) float64 {
	if k.IsExternal() {
		return 24
	}
	return 0
}

// AlertDueSpecs são os prazos por tipo (horas; 0 = sem prazo) mais o da pergunta da rodada.
func AlertDueSpecs() []AlertSettingSpec {
	var out []AlertSettingSpec
	for _, k := range append(append([]AlertKind{}, AlertKinds...), ExternalAlertKinds...) {
		out = append(out, AlertSettingSpec{"due." + string(k), AlertDueDefaultHours(k), 0, AlertDueMaxHours, "h"})
	}
	return append(out, AlertSettingSpec{AlertDueRoundQuestion, 24, 0, AlertDueMaxHours, "h"})
}

// AlertSettingSpecOf acha o ajuste pela chave.
func AlertSettingSpecOf(key string) (AlertSettingSpec, bool) {
	for _, s := range AlertLimitSpecs {
		if s.Key == key {
			return s, true
		}
	}
	for _, s := range AlertDueSpecs() {
		if s.Key == key {
			return s, true
		}
	}
	return AlertSettingSpec{}, false
}

// ValidateAlertSetting confere a chave e o valor de um ajuste: a chave tem de existir e o valor cabe na faixa dela.
func ValidateAlertSetting(key string, value float64) error {
	s, ok := AlertSettingSpecOf(key)
	if !ok {
		return Invalid("key", "unknown alert setting")
	}
	if value < s.Min || value > s.Max {
		return Invalid("value", fmt.Sprintf("must be between %g and %g (%s)", s.Min, s.Max, s.Unit))
	}
	return nil
}

// ---------- classe de custo do run ----------

// RunCostClass dá a classe de custo de um run do Hanko a partir de `cost_source` e da assinatura (ADR-0082 item 2):
// `reported` é `real`, `none` é `unpriced`, e `estimated` é `listed` quando o run tem assinatura e `estimated` quando
// não tem. Valor desconhecido é `unpriced`: nunca vira zero.
func RunCostClass(source CostSource, hasSubscription bool) CallCostClass {
	switch source {
	case CostReported:
		return CallCostReal
	case CostEstimated:
		if hasSubscription {
			return CallCostListed
		}
		return CallCostEstimated
	}
	return CallCostUnpriced
}
