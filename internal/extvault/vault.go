// Package extvault é o provider externo de segredos (ADR-0050 §B; o nome evita confusão com o cofre do Hanko,
// ADR-0068 §9): resolve valores guardados num cofre externo no momento do despacho. A interface é pequena de
// propósito: o Hanko só lê. O 1º provider fala a API KV v2 do Vault (também servida pelo OpenBao), com token estático
// vindo do ambiente do servidor.
package extvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider lê um segredo do cofre. ref é a referência guardada no Hanko (o formato é do provider).
type Provider interface {
	// Name identifica o provider em telas e métricas.
	Name() string
	// Get devolve o valor. O erro nunca carrega o valor e só tem texto seguro para exibir.
	Get(ctx context.Context, ref string) (string, error)
}

// ErrNotFound: o cofre respondeu que o caminho ou o campo não existe.
var ErrNotFound = errors.New("vault: segredo não encontrado no cofre")

const (
	// KV2Name é o nome do provider da API KV v2.
	KV2Name = "vault-kv2"

	defaultMount   = "secret"
	defaultField   = "value"
	requestTimeout = 10 * time.Second
	maxResponse    = 1 << 20
)

// KV2Options configura o provider KV v2.
type KV2Options struct {
	Addr      string // https://vault.exemplo:8200
	Token     string // token estático (HANKO_VAULT_TOKEN)
	Mount     string // motor KV v2; padrão "secret"
	Namespace string // opcional (X-Vault-Namespace)
	// HTTP substitui o cliente (testes). Nil usa um cliente com timeout e sem seguir redirecionamentos.
	HTTP *http.Client
}

// KV2 é o provider da API KV v2.
type KV2 struct {
	base      *url.URL
	token     string
	mount     string
	namespace string
	hc        *http.Client
}

// NewKV2 valida a configuração e monta o provider.
func NewKV2(o KV2Options) (*KV2, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(o.Addr), "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("vault: HANKO_VAULT_ADDR inválido: use http(s)://host[:porta]")
	}
	if strings.TrimSpace(o.Token) == "" {
		return nil, errors.New("vault: HANKO_VAULT_TOKEN é obrigatório com HANKO_VAULT_ADDR")
	}
	mount := strings.Trim(strings.TrimSpace(o.Mount), "/")
	if mount == "" {
		mount = defaultMount
	}
	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &KV2{base: u, token: strings.TrimSpace(o.Token), mount: mount, namespace: strings.TrimSpace(o.Namespace), hc: hc}, nil
}

// Name implementa Provider.
func (k *KV2) Name() string { return KV2Name }

// SplitRef separa `caminho#campo`; sem campo vale "value".
func SplitRef(ref string) (path, field string, err error) {
	ref = strings.TrimSpace(ref)
	path, field, _ = strings.Cut(ref, "#")
	path = strings.Trim(path, "/")
	if field == "" {
		field = defaultField
	}
	if path == "" || len(ref) > 500 || strings.ContainsAny(ref, "\x00\r\n") || strings.Contains(path, "..") {
		return "", "", errors.New("referência de cofre inválida: use caminho#campo (ex.: apps/hanko#token)")
	}
	return path, field, nil
}

// Get implementa Provider.
func (k *KV2) Get(ctx context.Context, ref string) (string, error) {
	path, field, err := SplitRef(ref)
	if err != nil {
		return "", err
	}
	u := *k.base
	u.Path = strings.TrimRight(k.base.Path, "/") + "/v1/" + k.mount + "/data/" + path
	u.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return "", fmt.Errorf("vault: %w", err)
	}
	req.Header.Set("X-Vault-Token", k.token)
	if k.namespace != "" {
		req.Header.Set("X-Vault-Namespace", k.namespace)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := k.hc.Do(req)
	if err != nil {
		return "", errors.New("vault: cofre inacessível") // o erro de rede pode citar a URL; não repassa
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return "", errors.New("vault: resposta inválida do cofre")
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", ErrNotFound
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return "", errors.New("vault: o token do Hanko não tem permissão de leitura neste caminho")
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("vault: o cofre respondeu HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", errors.New("vault: resposta inválida do cofre")
	}
	raw, ok := doc.Data.Data[field]
	if !ok {
		return "", ErrNotFound
	}
	s, ok := raw.(string)
	if !ok || s == "" {
		return "", errors.New("vault: o campo do segredo não é um texto não vazio")
	}
	return s, nil
}
