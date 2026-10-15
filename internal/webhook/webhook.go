// Package webhook assina e envia os webhooks de saída (ADR-0029). Não conhece banco nem fila: recebe o
// corpo pronto, o segredo e um cliente HTTP (com a proteção SSRF de netguard) e devolve o resultado da entrega.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cabeçalhos enviados em toda entrega.
const (
	HeaderSignature = "X-Hanko-Signature"
	HeaderEvent     = "X-Hanko-Event"
	HeaderDelivery  = "X-Hanko-Delivery"
	UserAgent       = "Hanko-Webhook/1"

	// SignatureVersion identifica o esquema: HMAC-SHA256 sobre "<t>.<corpo>".
	SignatureVersion = "v1"

	// MaxResponseRead é quanto da resposta é lido (e descartado) para liberar a conexão.
	MaxResponseRead = 4 << 10
)

// Sign devolve o valor do cabeçalho X-Hanko-Signature: "t=<unix>,v1=<hex>", onde hex é o HMAC-SHA256, com o
// segredo da integração, de "<t>.<corpo>". O timestamp entra na assinatura, então o receptor pode recusar
// entregas antigas (Verify faz isso) e uma entrega capturada não vale para sempre.
func Sign(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return "t=" + ts + "," + SignatureVersion + "=" + mac(secret, ts, body)
}

func mac(secret, ts string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(ts))
	h.Write([]byte{'.'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Verify é o lado do receptor (e dos testes): confere a assinatura em tempo constante e recusa timestamps
// fora da tolerância (padrão 5 min quando tol <= 0).
func Verify(secret, header string, body []byte, now time.Time, tol time.Duration) error {
	if tol <= 0 {
		tol = 5 * time.Minute
	}
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case SignatureVersion:
			sig = v
		}
	}
	if ts == "" || sig == "" {
		return errors.New("assinatura malformada")
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("timestamp inválido")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > tol || d < -tol {
		return errors.New("timestamp fora da tolerância")
	}
	want := mac(secret, ts, body)
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(sig))) {
		return errors.New("assinatura inválida")
	}
	return nil
}

// Result é o desfecho de uma tentativa de entrega.
type Result struct {
	Status  int           // código HTTP; 0 quando não houve resposta
	Latency time.Duration // do envio até a resposta (ou o erro)
	Err     string        // vazio em caso de sucesso (2xx)
}

// OK informa se a entrega foi aceita (2xx).
func (r Result) OK() bool { return r.Err == "" && r.Status >= 200 && r.Status < 300 }

// Send faz um POST application/json assinado. O corpo é enviado exatamente como recebido (a assinatura vale
// para esses bytes). Nunca segue redirecionamentos (o cliente de netguard devolve o 3xx, que conta como falha).
func Send(ctx context.Context, c *http.Client, url, secret, deliveryID, event string, body []byte, now time.Time) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{Err: "requisição inválida"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set(HeaderEvent, event)
	req.Header.Set(HeaderDelivery, deliveryID)
	req.Header.Set(HeaderSignature, Sign(secret, now, body))
	start := time.Now()
	resp, err := c.Do(req)
	lat := time.Since(start)
	if err != nil {
		return Result{Latency: lat, Err: transportErr(err)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseRead))
	r := Result{Status: resp.StatusCode, Latency: lat}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		r.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return r
}

// transportErr resume um erro de transporte sem ecoar a URL (que pode carregar um token na query).
func transportErr(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "tempo esgotado"
	case errors.Is(err, context.Canceled):
		return "cancelado"
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return "tempo esgotado"
	}
	msg := err.Error()
	// *url.Error repete a URL; guardamos só a causa.
	if i := strings.LastIndex(msg, ": "); i >= 0 && strings.Contains(msg[:i], "://") {
		msg = msg[i+2:]
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
