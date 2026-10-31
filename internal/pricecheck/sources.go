// Package pricecheck confere os preços de modelo contra duas fontes públicas, models.dev e OpenRouter (ADR-0082 item 7).
//
// É a porta de `price_sources.py` e `prices.py@845764f` do oute-agent. A busca é a única saída à internet do módulo: duas URLs
// fixas, só https, GET sem credencial, sem seguir redirecionamento, com tempo e tamanho máximos. O que volta é dado de
// terceiro: vira JSON validado, e só os números que passam na conferência (finitos, >= 0, abaixo de um teto) chegam ao resto
// do código. O texto da fonte nunca vai a log, a alerta nem a resposta; sai só o código da falha.
package pricecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// As duas fontes.
const (
	ModelsDev  = "models.dev"
	OpenRouter = "openrouter"
)

// Sources são as fontes, na ordem em que se leem.
var Sources = []string{ModelsDev, OpenRouter}

// DefaultURLs são as URLs fixas das fontes.
var DefaultURLs = map[string]string{ModelsDev: "https://models.dev/api.json", OpenRouter: "https://openrouter.ai/api/v1/models"}

// Limites da busca (`price_sources.py:25-36`).
const (
	MaxBytes       = 32 << 20 // o models.dev tem ~5 MB hoje; acima disto a resposta é recusada
	OperationLimit = 20 * time.Second
	Deadline       = 60 * time.Second
	MaxUSDPerMTok  = 100_000
)

// Códigos de falha: o único texto que sai de uma falha, nunca o corpo nem a mensagem da fonte.
const (
	ErrURL      = "url"
	ErrNet      = "rede"
	ErrTimeout  = "tempo"
	ErrHTTP     = "http"
	ErrRedirect = "redirecionamento"
	ErrSize     = "tamanho"
	ErrJSON     = "json"
	ErrFormat   = "formato"
	ErrInternal = "interno"
)

// SourceError é uma falha ao buscar ou ler uma fonte; Code é um dos códigos acima.
type SourceError struct{ Code string }

func (e *SourceError) Error() string { return "fonte de preço: " + e.Code }

func fail(code string) error { return &SourceError{Code: code} }

// CodeOf devolve o código de uma falha de fonte; qualquer outro erro é "interno".
func CodeOf(err error) string {
	var se *SourceError
	if errors.As(err, &se) {
		return se.Code
	}
	return ErrInternal
}

// Fields são os quatro eixos de preço, em USD por 1M tokens.
var Fields = []string{"input", "output", "cache_read", "cache_creation"}

// Partial é uma leitura parcial de uma fonte: os eixos presentes e válidos. Sempre tem `input` e `output`.
type Partial map[string]float64

// family é uma família de modelo: o prefixo do nosso id, o provedor no models.dev, o prefixo no OpenRouter e se o provedor é
// obrigatório no models.dev (ausente = o formato mudou). A `zai` é opcional: sem o provedor, o `glm-*` fica sem fonte.
type family struct {
	prefix, provider, router string
	required                 bool
}

var families = []family{{"claude-", "anthropic", "anthropic", true}, {"gpt-", "openai", "openai", true}, {"glm-", "zai", "z-ai", false}}

var (
	dateSuffix  = regexp.MustCompile(`-\d{8}$`)
	minorSuffix = regexp.MustCompile(`^(claude-.+?-\d)-(\d)$`)
)

// SourceID é o id de um modelo numa fonte. No models.dev são o provedor e o id; no OpenRouter, só o id completo.
type SourceID struct{ Provider, ID string }

// SourceIDs devolve o id do modelo em cada fonte, ou nada quando o modelo não tem regra (nunca se adivinha por semelhança).
func SourceIDs(model string) map[string]SourceID {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	for _, f := range families {
		if !strings.HasPrefix(m, f.prefix) {
			continue
		}
		base := dateSuffix.ReplaceAllString(m, "")
		if strings.HasPrefix(m, "claude-") {
			base = minorSuffix.ReplaceAllString(base, "$1.$2")
		}
		return map[string]SourceID{ModelsDev: {Provider: f.provider, ID: m}, OpenRouter: {ID: f.router + "/" + base}}
	}
	return nil
}

// ---------- busca ----------

// HTTPClient é o que a busca precisa; em produção, um cliente do netguard (ADR-0029).
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// NoRedirect devolve um cliente que não segue redirecionamento: seguir deixaria a fonte mandar o Hanko a outro host.
func NoRedirect(c *http.Client) *http.Client {
	cp := *c
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cp
}

// HTTPSOnly confere que a URL é https com host e sem credencial.
func HTTPSOnly(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fail(ErrURL)
	}
	return nil
}

// Fetch busca o corpo de `GET url`, no máximo MaxBytes, dentro do prazo. Falha = *SourceError.
func Fetch(ctx context.Context, c HTTPClient, rawURL string, allowHTTP bool) ([]byte, error) {
	if !allowHTTP {
		if err := HTTPSOnly(rawURL); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fail(ErrURL)
	}
	req.Header.Set("User-Agent", "hanko/price-check")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return nil, fail(ErrTimeout)
		}
		return nil, fail(ErrNet)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fail(ErrRedirect)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fail(ErrHTTP)
	}
	if resp.ContentLength > MaxBytes {
		return nil, fail(ErrSize)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return nil, fail(ErrTimeout)
		}
		return nil, fail(ErrNet)
	}
	if len(body) > MaxBytes {
		return nil, fail(ErrSize)
	}
	return body, nil
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// ---------- leitura (JSON de terceiro = dado) ----------

// LoadJSON lê o corpo como JSON; qualquer defeito (sintaxe, aninhamento fundo demais) é ErrJSON.
func LoadJSON(body []byte) (any, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fail(ErrJSON)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fail(ErrJSON)
	}
	return v, nil
}

// number converte um valor de preço para USD por 1M tokens, ou falha (false) quando não é um preço válido: booleano, texto
// fora do formato, negativo (o OpenRouter usa -1 para preço dinâmico), não finito ou acima do teto.
func number(v any, perToken bool) (float64, bool) {
	var r *big.Rat
	switch x := v.(type) {
	case json.Number:
		if perToken {
			return 0, false // o OpenRouter manda o preço por token como texto
		}
		r = new(big.Rat)
		if _, ok := r.SetString(x.String()); !ok {
			return 0, false
		}
	case string:
		if !perToken {
			return 0, false
		}
		r = new(big.Rat)
		if _, ok := r.SetString(strings.TrimSpace(x)); !ok {
			return 0, false
		}
		r.Mul(r, big.NewRat(1_000_000, 1))
	default:
		return 0, false
	}
	if r.Sign() < 0 || r.Cmp(big.NewRat(MaxUSDPerMTok, 1)) > 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(r.FloatString(9), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// partial lê os campos presentes e válidos; nil se `input` ou `output` falta ou é inválido (sem os dois não há preço). Cache
// ausente fica fora (a regra de concordância trata); cache inválido derruba o modelo.
func partial(raw any, names [4]string, perToken bool) Partial {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := Partial{}
	for i, f := range Fields {
		v, present := m[names[i]]
		if !present || v == nil {
			continue
		}
		n, ok := number(v, perToken)
		if !ok {
			return nil
		}
		out[f] = n
	}
	if _, ok := out["input"]; !ok {
		return nil
	}
	if _, ok := out["output"]; !ok {
		return nil
	}
	return out
}

// ParseModelsDev lê `{provedor: {models: {id: {cost: {...}}}}}`. Os provedores obrigatórios precisam existir com `models`,
// senão o formato mudou. Só o preço base; modelo sem preço válido não entra.
func ParseModelsDev(data any) (map[SourceID]Partial, error) {
	root, ok := data.(map[string]any)
	if !ok {
		return nil, fail(ErrFormat)
	}
	out := map[SourceID]Partial{}
	for _, f := range families {
		entry, present := root[f.provider]
		if !present && !f.required {
			continue
		}
		em, _ := entry.(map[string]any)
		models, ok := em["models"].(map[string]any)
		if !ok {
			return nil, fail(ErrFormat)
		}
		for id, e := range models {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if p := partial(em["cost"], [4]string{"input", "output", "cache_read", "cache_write"}, false); p != nil {
				out[SourceID{Provider: f.provider, ID: id}] = p
			}
		}
	}
	if len(out) == 0 {
		return nil, fail(ErrFormat)
	}
	return out, nil
}

// ParseOpenRouter lê `{data: [{id, pricing: {prompt, completion, …}}]}`, com o preço em USD por token, como texto.
func ParseOpenRouter(data any) (map[SourceID]Partial, error) {
	root, ok := data.(map[string]any)
	rows, isList := root["data"].([]any)
	if !ok || !isList {
		return nil, fail(ErrFormat)
	}
	out := map[SourceID]Partial{}
	for _, r := range rows {
		row, ok := r.(map[string]any)
		id, isStr := row["id"].(string)
		if !ok || !isStr {
			continue
		}
		if p := partial(row["pricing"], [4]string{"prompt", "completion", "input_cache_read", "input_cache_write"}, true); p != nil {
			out[SourceID{ID: id}] = p
		}
	}
	if len(out) == 0 {
		return nil, fail(ErrFormat)
	}
	return out, nil
}

// ReadSource busca e lê uma fonte: o preço parcial por id naquela fonte.
func ReadSource(ctx context.Context, c HTTPClient, source, rawURL string, allowHTTP bool) (map[SourceID]Partial, error) {
	body, err := Fetch(ctx, c, rawURL, allowHTTP)
	if err != nil {
		return nil, err
	}
	data, err := LoadJSON(body)
	if err != nil {
		return nil, err
	}
	switch source {
	case ModelsDev:
		return ParseModelsDev(data)
	case OpenRouter:
		return ParseOpenRouter(data)
	}
	return nil, fmt.Errorf("fonte desconhecida %q", source)
}
