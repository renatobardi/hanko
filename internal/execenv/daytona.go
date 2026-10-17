package execenv

import (
	"bytes"
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

// Daytona é o CloudDriver do provider Daytona (ADR-0055): API REST com chave Bearer para criar, consultar e apagar
// sandboxes e emitir o token do gateway SSH. Sem SDK: só net/http. As formas das chamadas seguem a documentação
// pública da API; o cliente é testado contra um servidor httptest e o contrato real deve ser conferido com uma conta
// antes do uso em produção (docs/validacao/m11.md).
type Daytona struct {
	apiURL     string
	sshHost    string
	sshPort    int
	knownHosts string
	http       *http.Client
	pollEvery  time.Duration
	readyWait  time.Duration
}

// DaytonaOptions configura o driver (tudo vem do operador, nunca da empresa).
type DaytonaOptions struct {
	// APIURL é a base da API (padrão https://app.daytona.io/api).
	APIURL string
	// SSHHost e SSHPort são o gateway SSH (padrão ssh.app.daytona.io:22).
	SSHHost string
	SSHPort int
	// KnownHosts são as linhas de known_hosts do gateway (obrigatório).
	KnownHosts string
	// HTTP é o cliente HTTP (padrão: timeout de 30 s por requisição).
	HTTP *http.Client
	// PollEvery e ReadyWait controlam a espera pelo estado "started" (padrões 2 s e 2 min).
	PollEvery, ReadyWait time.Duration
}

// Padrões do Daytona hospedado.
const (
	DaytonaDefaultAPI     = "https://app.daytona.io/api"
	DaytonaDefaultSSHHost = "ssh.app.daytona.io"
)

// NewDaytona valida as opções e devolve o driver.
func NewDaytona(o DaytonaOptions) (*Daytona, error) {
	if o.APIURL == "" {
		o.APIURL = DaytonaDefaultAPI
	}
	u, err := url.Parse(strings.TrimRight(o.APIURL, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("HANKO_ENV_CLOUD_API_URL inválida: %q", o.APIURL)
	}
	if o.SSHHost == "" {
		o.SSHHost = DaytonaDefaultSSHHost
	}
	if !ValidHostOrIP(o.SSHHost) {
		return nil, fmt.Errorf("HANKO_ENV_CLOUD_SSH_HOST inválido: %q", o.SSHHost)
	}
	if o.SSHPort == 0 {
		o.SSHPort = 22
	}
	if strings.TrimSpace(o.KnownHosts) == "" {
		return nil, errors.New("HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS é obrigatório: cole a saída de ssh-keyscan do gateway (o ssh nunca aceita uma chave desconhecida)")
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if o.PollEvery <= 0 {
		o.PollEvery = 2 * time.Second
	}
	if o.ReadyWait <= 0 {
		o.ReadyWait = 2 * time.Minute
	}
	return &Daytona{apiURL: strings.TrimRight(o.APIURL, "/"), sshHost: o.SSHHost, sshPort: o.SSHPort,
		knownHosts: strings.TrimSpace(o.KnownHosts), http: o.HTTP, pollEvery: o.PollEvery, readyWait: o.ReadyWait}, nil
}

func (d *Daytona) Name() string       { return "daytona" }
func (d *Daytona) KnownHosts() string { return d.knownHosts }

// New devolve o cliente com a chave de API da empresa.
func (d *Daytona) New(apiKey string) CloudProvider { return &daytonaClient{d: d, key: apiKey} }

type daytonaClient struct {
	d   *Daytona
	key string
}

// daytonaError é um erro de API sem o corpo inteiro (que pode ecoar dados) nem a chave.
type daytonaError struct {
	status int
	msg    string
}

func (e *daytonaError) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("daytona respondeu %d: %s", e.status, e.msg)
	}
	return fmt.Sprintf("daytona respondeu %d", e.status)
}

// do faz uma requisição JSON autenticada; out (se não nil) recebe o corpo decodificado.
func (c *daytonaClient) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("daytona: montar o pedido: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.d.apiURL+path, body)
	if err != nil {
		return fmt.Errorf("daytona: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.d.http.Do(req)
	if err != nil {
		return fmt.Errorf("daytona: %s", strings.ReplaceAll(err.Error(), c.key, "***"))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Message any `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := ""
		switch m := e.Message.(type) {
		case string:
			msg = m
		case []any:
			parts := make([]string, 0, len(m))
			for _, p := range m {
				parts = append(parts, fmt.Sprint(p))
			}
			msg = strings.Join(parts, "; ")
		}
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return &daytonaError{status: resp.StatusCode, msg: strings.ReplaceAll(msg, c.key, "***")}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("daytona: resposta inválida: %w", err)
		}
	}
	return nil
}

func (c *daytonaClient) Ping(ctx context.Context) error {
	var out json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/sandbox?limit=1", nil, &out); err != nil {
		var de *daytonaError
		if errors.As(err, &de) && (de.status == http.StatusUnauthorized || de.status == http.StatusForbidden) {
			return fmt.Errorf("chave de API recusada pelo provider (%d)", de.status)
		}
		return err
	}
	return nil
}

type daytonaSandbox struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Error string `json:"errorReason"`
}

func (c *daytonaClient) Create(ctx context.Context, spec CloudSpec) (CloudSandbox, error) {
	body := map[string]any{
		"name":              spec.Name,
		"labels":            spec.Labels,
		"cpu":               spec.CPUs,
		"memory":            spec.MemoryGiB,
		"disk":              spec.DiskGiB,
		"networkBlockAll":   spec.BlockNetwork,
		"public":            false,
		"autoStopInterval":  int(spec.IdleStop / time.Minute),
		"autoDeleteInterval": int(spec.Reap / time.Minute),
	}
	if spec.Snapshot != "" {
		body["snapshot"] = spec.Snapshot
	}
	var sb daytonaSandbox
	if err := c.do(ctx, http.MethodPost, "/sandbox", body, &sb); err != nil {
		return CloudSandbox{}, err
	}
	if sb.ID == "" {
		return CloudSandbox{}, errors.New("daytona não devolveu o id do sandbox")
	}
	deadline := time.Now().Add(c.d.readyWait)
	for {
		switch strings.ToLower(sb.State) {
		case "started":
			return CloudSandbox{ID: sb.ID}, nil
		case "error", "build_failed", "destroyed", "destroying", "archived":
			c.cleanup(sb.ID)
			return CloudSandbox{}, fmt.Errorf("o sandbox %s não ficou pronto (estado %s %s)", sb.ID, sb.State, sb.Error)
		}
		if time.Now().After(deadline) {
			c.cleanup(sb.ID)
			return CloudSandbox{}, fmt.Errorf("o sandbox %s não ficou pronto em %s (estado %q)", sb.ID, c.d.readyWait, sb.State)
		}
		select {
		case <-ctx.Done():
			c.cleanup(sb.ID)
			return CloudSandbox{}, ctx.Err()
		case <-time.After(c.d.pollEvery):
		}
		if err := c.do(ctx, http.MethodGet, "/sandbox/"+url.PathEscape(sb.ID), nil, &sb); err != nil {
			c.cleanup(sb.ID)
			return CloudSandbox{}, err
		}
	}
}

// cleanup apaga um sandbox que falhou ao ficar pronto, sem depender do contexto da criação.
func (c *daytonaClient) cleanup(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = c.Delete(ctx, id)
}

func (c *daytonaClient) SSH(ctx context.Context, id string, ttl time.Duration) (CloudSSH, error) {
	var out struct {
		Token string `json:"token"`
	}
	mins := int(ttl / time.Minute)
	if mins < 1 {
		mins = 1
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/sandbox/%s/ssh-access?expiresInMinutes=%d", url.PathEscape(id), mins), nil, &out); err != nil {
		return CloudSSH{}, err
	}
	if out.Token == "" {
		return CloudSSH{}, errors.New("daytona não devolveu o token de acesso SSH")
	}
	return CloudSSH{Host: c.d.sshHost, Port: c.d.sshPort, User: out.Token}, nil
}

func (c *daytonaClient) Delete(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/sandbox/"+url.PathEscape(id), nil, nil)
	var de *daytonaError
	if errors.As(err, &de) && de.status == http.StatusNotFound {
		return nil
	}
	return err
}
