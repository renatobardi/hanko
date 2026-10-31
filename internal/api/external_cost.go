package api

import (
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Contrato do custo elevado (ADR-0082) e dos alertas de agente externo (ADR-0083), grupo 2 da v0.8 (#124). Cada operação
// responde 501 até a fatia 2 (#126) ligar o backend. O alerta em si usa a API de alertas que já existe: os 17 tipos novos
// aparecem nela quando a fatia 2 os abrir.

const (
	costContract = "Contrato do custo elevado e dos alertas externos (ADR-0082 e 0083)"
	sliceCost    = "fatia 2 (#126)"
)

// SubscriptionMonthlyValue é uma linha do histórico de mensalidade de uma assinatura (ADR-0082 item 6). O histórico só
// recebe linhas novas: o valor de um dia é o da linha com o maior início menor ou igual a ele, e a registrada por último
// desempata.
type SubscriptionMonthlyValue struct {
	ID         string    `json:"id" format:"uuid"`
	MonthlyUSD float64   `json:"monthly_usd" minimum:"0"`
	StartsOn   string    `json:"starts_on" format:"date" doc:"Dia em que o valor passa a valer."`
	RecordedAt time.Time `json:"recorded_at"`
	Origin     string    `json:"origin" enum:"created,manual,migrated"`
	Actor      ActorRef  `json:"actor"`
}

type addMonthlyValueIn struct {
	SubscriptionPath
	Body struct {
		MonthlyUSD float64 `json:"monthly_usd" minimum:"0" doc:"O valor mensal novo."`
		StartsOn   string  `json:"starts_on" format:"date" doc:"Dia em que passa a valer. No passado, muda o pago desses dias (owner ou admin, com reautenticação recente; ADR-0070)."`
	}
}

// PriceCheckRun é uma conferência de preços (ADR-0082 item 7).
type PriceCheckRun struct {
	ID         string             `json:"id" format:"uuid"`
	Reason     string             `json:"reason" enum:"boot,daily,manual,migrated"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt *time.Time         `json:"finished_at,omitempty"`
	Sources    []PriceCheckSource `json:"sources"`
}

// PriceCheckSource é o resultado de uma fonte numa conferência; a falha guarda só o código, nunca o corpo.
type PriceCheckSource struct {
	Source    string `json:"source"`
	Status    string `json:"status" enum:"ok,down"`
	ErrorCode string `json:"error_code,omitempty"`
}

// PriceCheckResult é o resultado de um modelo numa fonte.
type PriceCheckResult struct {
	Model                string   `json:"model"`
	Source               string   `json:"source"`
	Status               string   `json:"status" enum:"equal,changed,new,diverges,locked_differs,no_source,source_down"`
	InputPerMTok         *float64 `json:"input_per_mtok,omitempty"`
	OutputPerMTok        *float64 `json:"output_per_mtok,omitempty"`
	CacheReadPerMTok     *float64 `json:"cache_read_per_mtok,omitempty"`
	CacheCreationPerMTok *float64 `json:"cache_creation_per_mtok,omitempty"`
	PriceID              *string  `json:"price_id,omitempty" format:"uuid"`
}

type PriceCheckPath struct {
	CompanyPath
	Run string `path:"run" format:"uuid" doc:"Id da conferência."`
}

// PriceCheckDetail é uma conferência com o resultado de cada modelo.
type PriceCheckDetail struct {
	PriceCheckRun
	Results []PriceCheckResult `json:"results"`
}

// AlertSetting é um limite ou um prazo de alerta da empresa, com o valor em vigor (ADR-0083 itens 3 e 6). Só o que a
// empresa mudou fica guardado; o resto vale o padrão.
type AlertSetting struct {
	Key        string     `json:"key" example:"limit.host_no_data_minutes" doc:"limit.<nome> ou due.<tipo>."`
	Value      float64    `json:"value" doc:"O valor em vigor."`
	Default    float64    `json:"default" doc:"O padrão (os valores da Oute)."`
	Min        float64    `json:"min"`
	Max        float64    `json:"max"`
	Unit       string     `json:"unit" example:"min"`
	Overridden bool       `json:"overridden" doc:"A empresa mudou o valor."`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type AlertSettingPath struct {
	CompanyPath
	Key string `path:"key" example:"due.queue" doc:"Chave do ajuste."`
}

type putAlertSettingIn struct {
	AlertSettingPath
	Body struct {
		Value float64 `json:"value" minimum:"0" doc:"Prazo em horas (0 = sem prazo, até 720) ou o valor do limite, na unidade dele."`
	}
}

func (s *server) registerExternalCost(api huma.API) {
	const tag = "external-cost"
	b := "/companies/{company}"

	pendingContractOp[listExternalNamedIn, ListOut[PriceCheckRun]](api, op("list-price-checks", http.MethodGet, b+"/price-checks", tag,
		"Lista as conferências de preços, da mais recente para a mais antiga"), costContract, sliceCost)
	pendingContractOp[PriceCheckPath, ItemOut[PriceCheckDetail]](api, op("get-price-check", http.MethodGet, b+"/price-checks/{run}", tag,
		"Mostra uma conferência de preços com o resultado de cada modelo"), costContract, sliceCost)

}
