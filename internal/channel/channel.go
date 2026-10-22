// Package channel são os drivers dos canais externos (ADR-0046): o protocolo do Telegram e do GitHub, sem banco e sem
// conhecer empresa, persona ou gate. O service orquestra (identidade, autorização, conversa, task, entrega); o driver só
// autentica o que chega, normaliza em Inbound e fala HTTP para enviar. Todo acesso à rede passa pelo cliente HTTP que o
// service entrega (com a proteção SSRF do netguard), então os testes apontam o driver para um httptest.
package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// InboundKind classifica o que chegou.
type InboundKind string

// Tipos de entrada.
const (
	InText    InboundKind = "text"    // mensagem comum
	InCommand InboundKind = "command" // /vincular, /pendentes… (Command e Arg)
	InAction  InboundKind = "action"  // clique num botão (Action)
	InIgnore  InboundKind = "ignore"  // o driver já sabe que não é para o Hanko (Reason)
)

// Attachment é um arquivo anexado à mensagem, ainda no provedor.
type Attachment struct {
	Ref  string `json:"ref"` // identificador no provedor (file_id)
	Name string `json:"name"`
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// Inbound é uma entrada normalizada.
type Inbound struct {
	ExternalID  string            `json:"external_id"` // update_id do Telegram, X-GitHub-Delivery
	Kind        InboundKind       `json:"kind"`
	Peer        string            `json:"peer"`   // chat do Telegram, dono/repo#N do GitHub
	Sender      string            `json:"sender"` // id do usuário no Telegram, login (minúsculas) no GitHub
	SenderLabel string            `json:"sender_label,omitempty"`
	Text        string            `json:"text,omitempty"`
	Command     string            `json:"command,omitempty"`
	Arg         string            `json:"arg,omitempty"`
	Action      string            `json:"action,omitempty"`      // dados do botão
	CallbackID  string            `json:"callback_id,omitempty"` // para responder ao clique
	Attachments []Attachment      `json:"attachments,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
	Reason      string            `json:"reason,omitempty"` // InIgnore
}

// Button é um botão de uma mensagem de saída: Data (callback) ou URL.
type Button struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Document é um arquivo enviado junto da mensagem.
type Document struct {
	Name    string
	Mime    string
	Content []byte
}

// Review é a review de um pull request.
type Review struct {
	Event domain.GitHubReviewEvent `json:"event"`
}

// Outbound é uma saída normalizada.
type Outbound struct {
	Peer      string
	Text      string
	Buttons   [][]Button
	Documents []Document
	Review    *Review
}

// Client carrega o que o driver precisa para uma chamada: o cliente HTTP, o token e a configuração do canal.
type Client struct {
	HTTP   *http.Client
	Token  string
	Config domain.ChannelConfig
}

// Driver é o protocolo de um canal.
type Driver interface {
	Kind() domain.ChannelKind
	// Verify autentica a requisição de entrada com o segredo do canal (tempo constante).
	Verify(h http.Header, body []byte, secret string) error
	// Parse normaliza a requisição (já autenticada). Pode devolver zero entradas.
	Parse(h http.Header, body []byte, cfg domain.ChannelConfig) ([]Inbound, error)
	// Send entrega uma saída e devolve o id da mensagem no provedor.
	Send(ctx context.Context, c Client, out Outbound) (string, error)
	// Fetch baixa um anexo (até max bytes).
	Fetch(ctx context.Context, c Client, ref string, max int64) ([]byte, error)
	// Register avisa o provedor para entregar os eventos em url, autenticados por secret.
	Register(ctx context.Context, c Client, url, secret string) error
	// Ack responde ao clique de um botão (some o relógio de espera no cliente).
	Ack(ctx context.Context, c Client, callbackID, text string) error
}

// Erros dos drivers.
var (
	// ErrUnauthorized: a requisição de entrada não se autenticou.
	ErrUnauthorized = errors.New("canal: requisição não autenticada")
	// ErrUnsupported: o driver não faz isso.
	ErrUnsupported = errors.New("canal: operação não suportada por este canal")
)

// PermanentError marca uma falha que repetir não resolve (credencial recusada, destino inexistente, conteúdo recusado).
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// IsPermanent diz se err é uma falha sem sentido em repetir.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// For devolve o driver do tipo.
func For(kind domain.ChannelKind) (Driver, bool) {
	switch kind {
	case domain.ChannelTelegram:
		return Telegram{}, true
	case domain.ChannelGitHub:
		return GitHub{}, true
	}
	return nil, false
}

// maxResponse é quanto da resposta do provedor se lê.
const maxResponse = 1 << 20

// defaultTimeout vale quando o cliente HTTP do service não tem prazo.
const defaultTimeout = 30 * time.Second

// call faz uma requisição JSON e devolve status e corpo. Os erros de rede nunca carregam a URL (o token do Telegram
// viaja nela): são trocados por uma mensagem fixa.
func call(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, &PermanentError{Err: fmt.Errorf("canal: codificar requisição: %w", err)}
		}
		rd = bytes.NewReader(b)
	}
	return send(ctx, hc, method, url, headers, rd, "application/json")
}

func send(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, body io.Reader, contentType string) (int, []byte, error) {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, nil, &PermanentError{Err: errors.New("canal: endereço inválido")}
	}
	if body != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, errors.New("canal: falha de rede ao chamar o provedor")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return resp.StatusCode, nil, errors.New("canal: falha ao ler a resposta do provedor")
	}
	return resp.StatusCode, b, nil
}

// classify transforma um status HTTP de falha em erro permanente ou transitório.
func classify(status int, what string) error {
	err := fmt.Errorf("canal: %s: o provedor respondeu %d", what, status)
	switch {
	case status == http.StatusTooManyRequests || status >= 500 || status == http.StatusRequestTimeout:
		return err
	case status >= 400:
		return &PermanentError{Err: err}
	}
	return err
}

// clip corta s em n runas.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// scrub tira o token de um texto de erro do provedor.
func scrub(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	return s
}
