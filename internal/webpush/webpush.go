// Package webpush envia mensagens de web push (ADR-0042) sem dependência externa: VAPID (RFC 8292, JWT ES256) e a
// criptografia de mensagem aes128gcm (RFC 8188 + RFC 8291), tudo com a stdlib. Quem fala com o serviço de push do
// navegador é o Sender, que recebe o cliente HTTP (o app passa o cliente com proteção SSRF; os testes, um httptest).
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Tetos do protocolo e do envio.
const (
	// MaxPayload é o maior texto que cabe numa mensagem (um registro de 4096 bytes menos o cabeçalho e o delimitador).
	MaxPayload = 3993
	recordSize = 4096
	// DefaultTTL é por quanto tempo o serviço de push guarda a mensagem se o aparelho estiver desligado.
	DefaultTTL  = 24 * time.Hour
	jwtLifetime = 12 * time.Hour // menos de 24 h (RFC 8292)
	maxResponse = 4 << 10
)

// Erros do envio.
var (
	// ErrGone: a assinatura não existe mais (404 ou 410 do serviço de push); quem enviou deve apagá-la.
	ErrGone = errors.New("webpush: assinatura expirada ou cancelada")
	// ErrPayloadTooLarge: a mensagem não cabe num registro.
	ErrPayloadTooLarge = errors.New("webpush: mensagem grande demais")
	// ErrBadSubscription: p256dh ou auth da assinatura não são válidos.
	ErrBadSubscription = errors.New("webpush: assinatura inválida")
)

var b64 = base64.RawURLEncoding

// Subscription é o que o navegador entrega em pushManager.subscribe().toJSON(): endpoint e as duas chaves em base64url.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// VAPID é o par de chaves que identifica o servidor perante o serviço de push.
type VAPID struct {
	priv *ecdsa.PrivateKey
}

// GenerateVAPID cria um par novo (P-256).
func GenerateVAPID() (VAPID, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return VAPID{}, fmt.Errorf("webpush: gerar chave: %w", err)
	}
	return VAPID{priv: k}, nil
}

// Valid diz se há chave.
func (v VAPID) Valid() bool { return v.priv != nil }

// PublicKey é a chave pública não comprimida (65 bytes) em base64url: o applicationServerKey do navegador.
func (v VAPID) PublicKey() string {
	if v.priv == nil {
		return ""
	}
	ek, err := v.priv.PublicKey.ECDH()
	if err != nil {
		return ""
	}
	return b64.EncodeToString(ek.Bytes())
}

// Marshal serializa a chave privada (32 bytes em base64url) para guardar cifrada.
func (v VAPID) Marshal() ([]byte, error) {
	if v.priv == nil {
		return nil, errors.New("webpush: sem chave")
	}
	ek, err := v.priv.ECDH()
	if err != nil {
		return nil, fmt.Errorf("webpush: %w", err)
	}
	return []byte(b64.EncodeToString(ek.Bytes())), nil
}

// ParseVAPID lê o que Marshal escreveu.
func ParseVAPID(b []byte) (VAPID, error) {
	raw, err := b64.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != 32 {
		return VAPID{}, errors.New("webpush: chave privada inválida")
	}
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return VAPID{}, errors.New("webpush: chave privada inválida")
	}
	return VAPID{priv: k}, nil
}

// Authorization monta o cabeçalho `vapid t=<jwt>, k=<chave pública>` para o endpoint (audience = origem dele).
func (v VAPID) Authorization(endpoint, subject string, now time.Time) (string, error) {
	if v.priv == nil {
		return "", errors.New("webpush: sem chave VAPID")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("webpush: endpoint inválido: %w", ErrBadSubscription)
	}
	claims, err := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(jwtLifetime).Unix(), "sub": subject})
	if err != nil {
		return "", fmt.Errorf("webpush: claims: %w", err)
	}
	head := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	signing := head + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	der, err := ecdsa.SignASN1(rand.Reader, v.priv, sum[:])
	if err != nil {
		return "", fmt.Errorf("webpush: assinar: %w", err)
	}
	var sig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		return "", fmt.Errorf("webpush: assinatura: %w", err)
	}
	raw := make([]byte, 64) // ES256: r || s, 32 bytes cada, big-endian
	sig.R.FillBytes(raw[:32])
	sig.S.FillBytes(raw[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(raw) + ", k=" + v.PublicKey(), nil
}

// Encrypt cifra payload para a assinatura no formato aes128gcm (RFC 8291) com um registro só. A chave efêmera e o
// sal são aleatórios; quem decifra (o navegador) só precisa da chave privada da assinatura e do segredo de autenticação.
func Encrypt(sub Subscription, payload []byte) ([]byte, error) {
	return encrypt(sub, payload, rand.Reader)
}

func encrypt(sub Subscription, payload []byte, rnd io.Reader) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, ErrPayloadTooLarge
	}
	uaPub, err := b64.DecodeString(strings.TrimRight(sub.P256dh, "="))
	if err != nil {
		return nil, fmt.Errorf("%w: p256dh", ErrBadSubscription)
	}
	authSecret, err := b64.DecodeString(strings.TrimRight(sub.Auth, "="))
	if err != nil || len(authSecret) == 0 {
		return nil, fmt.Errorf("%w: auth", ErrBadSubscription)
	}
	curve := ecdh.P256()
	uaKey, err := curve.NewPublicKey(uaPub)
	if err != nil {
		return nil, fmt.Errorf("%w: p256dh", ErrBadSubscription)
	}
	asKey, err := curve.GenerateKey(rnd)
	if err != nil {
		return nil, fmt.Errorf("webpush: chave efêmera: %w", err)
	}
	secret, err := asKey.ECDH(uaKey)
	if err != nil {
		return nil, fmt.Errorf("webpush: ecdh: %w", err)
	}
	asPub := asKey.PublicKey().Bytes()
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rnd, salt); err != nil {
		return nil, fmt.Errorf("webpush: sal: %w", err)
	}
	cek, nonce, err := deriveKeys(secret, authSecret, uaPub, asPub, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("webpush: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("webpush: gcm: %w", err)
	}
	plain := append(append([]byte{}, payload...), 0x02) // delimitador do último (e único) registro
	ct := gcm.Seal(nil, nonce, plain, nil)

	var body bytes.Buffer
	body.Write(salt)
	_ = binary.Write(&body, binary.BigEndian, uint32(recordSize))
	body.WriteByte(65) // tamanho da chave pública P-256 não comprimida
	body.Write(asPub)
	body.Write(ct)
	return body.Bytes(), nil
}

// deriveKeys implementa o HKDF do RFC 8291 §3.4 e do RFC 8188 §2.
func deriveKeys(secret, authSecret, uaPub, asPub, salt []byte) (cek, nonce []byte, err error) {
	prkKey, err := hkdf.Extract(sha256.New, secret, authSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: hkdf: %w", err)
	}
	keyInfo := append([]byte("WebPush: info\x00"), uaPub...)
	keyInfo = append(keyInfo, asPub...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: hkdf: %w", err)
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: hkdf: %w", err)
	}
	if cek, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16); err != nil {
		return nil, nil, fmt.Errorf("webpush: hkdf: %w", err)
	}
	if nonce, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12); err != nil {
		return nil, nil, fmt.Errorf("webpush: hkdf: %w", err)
	}
	return cek, nonce, nil
}

// Sender entrega mensagens ao serviço de push de cada navegador.
type Sender struct {
	// HTTP é o cliente de saída (o app passa o com proteção SSRF; os testes, o de um httptest.Server).
	HTTP  *http.Client
	VAPID VAPID
	// Subject identifica o operador perante o serviço de push: "mailto:..." ou uma URL https.
	Subject string
	// TTL padrão DefaultTTL.
	TTL time.Duration
	Now func() time.Time
}

// Send cifra payload e o entrega. Devolve ErrGone quando o serviço diz que a assinatura acabou (404/410); qualquer
// outro status fora de 2xx é um erro com o status, para o chamador contar a falha.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte) error {
	if s.HTTP == nil {
		return errors.New("webpush: sem cliente HTTP")
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	body, err := Encrypt(sub, payload)
	if err != nil {
		return err
	}
	auth, err := s.VAPID.Authorization(sub.Endpoint, s.Subject, now)
	if err != nil {
		return err
	}
	ttl := s.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webpush: pedido: %w", ErrBadSubscription)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(ttl.Seconds())))
	req.Header.Set("Urgency", "normal")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("webpush: enviar: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponse))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	}
	return fmt.Errorf("webpush: o serviço de push respondeu %d", resp.StatusCode)
}
