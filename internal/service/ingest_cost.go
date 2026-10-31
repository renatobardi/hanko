package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Custo de uma chamada ao modelo na ingestão (ADR-0082 itens 1 a 3; `cost.py@845764f` do oute-agent). O custo real é o do
// span ou, no span `claude_code.llm_request`, o do log `api_request` de mesmo `request_id`. O de lista é a tabela de
// preços aplicada aos tokens, com o preço da hora do fato; é calculado uma vez e gravado. Modelo sem preço é `unpriced`,
// nunca zero.

const (
	claudeCallSpan  = "claude_code.llm_request"
	zaiSubscription = "zai"
	// logCostMargin é a folga com que se procura o log da chamada: o log sai no fim e o span leva a hora do início.
	logCostMargin = time.Hour
)

var apiRequestEvents = []string{"api_request", "claude_code.api_request"}

// priceRow é um preço de modelo, em USD por 1M tokens, vigente desde `from` (ns).
type priceRow struct {
	id                                      domain.ID
	from                                    int64
	input, output, cacheRead, cacheCreation float64
}

// priceBook é a tabela de preços na memória durante um lote.
type priceBook struct {
	by   map[string][]priceRow // modelo (minúsculas) -> linhas por início de vigência
	keys []string              // os modelos, do mais longo para o mais curto: a busca pelo prefixo mais longo
}

func loadPriceBook(ctx context.Context, t *txc) (*priceBook, error) {
	rows, err := t.tx.Query(ctx, `SELECT id, lower(model), input_per_mtok::float8, output_per_mtok::float8, cache_read_per_mtok::float8,
		cache_creation_per_mtok::float8, (extract(epoch FROM effective_at) * 1000000000)::bigint FROM model_prices ORDER BY effective_at, id`)
	if err != nil {
		return nil, fmt.Errorf("ingest: ler a tabela de preços: %w", err)
	}
	defer rows.Close()
	b := &priceBook{by: map[string][]priceRow{}}
	for rows.Next() {
		var model string
		var p priceRow
		if err := rows.Scan(&p.id, &model, &p.input, &p.output, &p.cacheRead, &p.cacheCreation, &p.from); err != nil {
			return nil, fmt.Errorf("ingest: ler a tabela de preços: %w", err)
		}
		b.by[model] = append(b.by[model], p)
	}
	for k := range b.by {
		b.keys = append(b.keys, k)
	}
	sort.Slice(b.keys, func(i, j int) bool {
		return len(b.keys[i]) > len(b.keys[j]) || len(b.keys[i]) == len(b.keys[j]) && b.keys[i] < b.keys[j]
	})
	return b, rows.Err()
}

// lookup acha o preço do modelo na hora do fato (ADR-0082 item 3): tira o prefixo do provedor (`openai/gpt-5` vira `gpt-5`) e
// busca o prefixo mais longo da tabela, o que inclui o nome exato. Antes da primeira linha do modelo vale a primeira.
func (b *priceBook) lookup(model string, atNs int64) (priceRow, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if m == "" {
		return priceRow{}, false
	}
	for _, k := range b.keys {
		if strings.HasPrefix(m, k) {
			hist := b.by[k]
			cur := hist[0]
			for _, p := range hist {
				if p.from > atNs {
					break
				}
				cur = p
			}
			return cur, true
		}
	}
	return priceRow{}, false
}

func (p priceRow) estimate(input, output, cacheRead, cacheCreation int64) float64 {
	pos := func(n int64) float64 {
		if n < 0 {
			return 0
		}
		return float64(n)
	}
	return (pos(input)*p.input + pos(output)*p.output + pos(cacheRead)*p.cacheRead + pos(cacheCreation)*p.cacheCreation) / 1e6
}

// isSubscriptionCall diz se a chamada é de assinatura (`claude`, `codex` ou `zai`): pela chave `oute.subscription` ou, sem
// ela (histórico), pelo agente `claude` ou `codex` (`cost.py:subscription_of`).
func isSubscriptionCall(subscription, agent string) bool {
	switch subscription {
	case "claude", "codex", zaiSubscription:
		return true
	case "":
		return agent == "claude" || agent == "codex"
	}
	return false
}

// classifiedCost é o custo de uma chamada, já na forma que a tabela guarda.
type classifiedCost struct {
	class   domain.CallCostClass
	real    *float64
	list    *float64
	priceID *domain.ID
}

// classifyCost aplica a regra de `cost.call_cost`: real quando há custo efetivo; senão a tabela pelos tokens, `listed` para
// chamada de assinatura e `estimated` para as outras; sem preço, `unpriced`. O custo de lista também é gravado quando a
// chamada tem custo real (a conferência do Claude compara os dois). A `zai` descarta o custo informado.
func classifyCost(reported *float64, subscription, agent string, price priceRow, hasPrice bool, in, out, cr, cc int64) classifiedCost {
	var c classifiedCost
	if hasPrice {
		est := price.estimate(in, out, cr, cc)
		c.list, c.priceID = &est, &price.id
	}
	if reported != nil && subscription != zaiSubscription {
		c.class, c.real = domain.CallCostReal, reported
		return c
	}
	switch {
	case !hasPrice:
		c.class = domain.CallCostUnpriced
	case isSubscriptionCall(subscription, agent):
		c.class = domain.CallCostListed
	default:
		c.class = domain.CallCostEstimated
	}
	return c
}

// logCosts devolve, por `request_id`, o custo do log `api_request` das chamadas pedidas: vários logs do mesmo request_id dão
// um custo só (o maior). Só os logs na janela da hora do fato, com a folga de uma hora de cada lado.
func logCosts(ctx context.Context, t *txc, companyID domain.ID, requestIDs []string, minNs, maxNs int64) (map[string]float64, error) {
	out := map[string]float64{}
	if len(requestIDs) == 0 {
		return out, nil
	}
	rows, err := t.tx.Query(ctx, `SELECT attributes->>'request_id', attributes->>'cost_usd' FROM telemetry_logs
		WHERE company_id = $1 AND event_name = ANY($2) AND attributes->>'request_id' = ANY($3)
		  AND time_unix_nano >= $4 AND time_unix_nano < $5`,
		companyID, apiRequestEvents, requestIDs, minNs-logCostMargin.Nanoseconds(), maxNs+logCostMargin.Nanoseconds())
	if err != nil {
		return nil, fmt.Errorf("ingest: custo no log api_request: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rid string
		var raw *string
		if err := rows.Scan(&rid, &raw); err != nil {
			return nil, fmt.Errorf("ingest: custo no log api_request: %w", err)
		}
		if raw == nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(*raw), 64)
		if err != nil || v < 0 {
			continue
		}
		if cur, ok := out[rid]; !ok || v > cur {
			out[rid] = v
		}
	}
	return out, rows.Err()
}
