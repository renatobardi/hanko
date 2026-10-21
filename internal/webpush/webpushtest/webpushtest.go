// Package webpushtest é o serviço de push falso dos testes (ADR-0042): um httptest.Server que faz o papel do serviço
// de push do navegador e do próprio navegador — confere o cabeçalho VAPID, decifra a mensagem aes128gcm com a chave do
// "aparelho" e guarda o texto recebido. Nenhuma rede real.
package webpushtest

import (
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
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/webpush"
)

var b64 = base64.RawURLEncoding

// Device é um navegador com push: tem o par de chaves e o segredo de autenticação de uma assinatura.
type Device struct {
	priv *ecdh.PrivateKey
	auth []byte
	Sub  webpush.Subscription
}

// NewDevice cria um aparelho com assinatura para endpoint.
func NewDevice(endpoint string) *Device {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		panic(err) // só em teste
	}
	auth := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, auth); err != nil {
		panic(err)
	}
	return &Device{priv: k, auth: auth, Sub: webpush.Subscription{Endpoint: endpoint, P256dh: b64.EncodeToString(k.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}}
}

// Decrypt faz o que o navegador faz (RFC 8291 §4 + RFC 8188): lê o cabeçalho, deriva as chaves e abre o registro.
func (d *Device) Decrypt(body []byte) ([]byte, error) {
	if len(body) < 16+4+1 {
		return nil, errors.New("corpo curto")
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	if len(body) < 21+idlen || rs < 18 {
		return nil, errors.New("cabeçalho inválido")
	}
	asPub := body[21 : 21+idlen]
	ct := body[21+idlen:]
	if len(ct) > int(rs) {
		return nil, errors.New("registro maior que rs")
	}
	asKey, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		return nil, fmt.Errorf("chave do servidor: %w", err)
	}
	secret, err := d.priv.ECDH(asKey)
	if err != nil {
		return nil, err
	}
	uaPub := d.priv.PublicKey().Bytes()
	prkKey, err := hkdf.Extract(sha256.New, secret, d.auth)
	if err != nil {
		return nil, err
	}
	info := append([]byte("WebPush: info\x00"), uaPub...)
	info = append(info, asPub...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("autenticação da mensagem: %w", err)
	}
	// remove o preenchimento: zeros e o delimitador 0x02 do último registro
	i := len(pt) - 1
	for i >= 0 && pt[i] == 0 {
		i--
	}
	if i < 0 || pt[i] != 0x02 {
		return nil, errors.New("delimitador do último registro ausente")
	}
	return pt[:i], nil
}

// VerifyVAPID confere o cabeçalho `vapid t=<jwt>, k=<chave>`: assinatura ES256, audience, validade. Devolve a chave
// pública (base64url) que assinou.
func VerifyVAPID(header, wantAudience string, now time.Time) (string, error) {
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		return "", errors.New("esquema não é vapid")
	}
	var jwt, k string
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "t="); ok {
			jwt = v
		}
		if v, ok := strings.CutPrefix(part, "k="); ok {
			k = v
		}
	}
	segs := strings.Split(jwt, ".")
	if len(segs) != 3 || k == "" {
		return "", errors.New("t ou k ausente")
	}
	pub, err := b64.DecodeString(k)
	if err != nil || len(pub) != 65 {
		return "", errors.New("k inválida")
	}
	sig, err := b64.DecodeString(segs[2])
	if err != nil || len(sig) != 64 {
		return "", errors.New("assinatura inválida")
	}
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	if err != nil {
		return "", errors.New("k inválida")
	}
	der, err := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])})
	if err != nil {
		return "", errors.New("assinatura inválida")
	}
	sum := sha256.Sum256([]byte(segs[0] + "." + segs[1]))
	if !ecdsa.VerifyASN1(key, sum[:], der) {
		return "", errors.New("assinatura não confere")
	}
	raw, err := b64.DecodeString(segs[1])
	if err != nil {
		return "", errors.New("claims inválidas")
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return "", errors.New("claims inválidas")
	}
	if claims.Aud != wantAudience {
		return "", fmt.Errorf("aud = %q, quer %q", claims.Aud, wantAudience)
	}
	if claims.Exp <= now.Unix() || claims.Exp > now.Add(24*time.Hour).Unix() {
		return "", errors.New("exp fora de (agora, agora+24h]")
	}
	if claims.Sub == "" {
		return "", errors.New("sub ausente")
	}
	return k, nil
}

// Message é o que o serviço falso recebeu.
type Message struct {
	Path          string
	Payload       []byte // já decifrado
	Authorization string
	TTL           string
	// Err é o problema encontrado ao conferir a mensagem (VAPID, decifragem); vazio = tudo certo.
	Err string
	// VAPIDKey é a chave pública que assinou.
	VAPIDKey string
}

// Service é o serviço de push falso.
type Service struct {
	*httptest.Server
	mu      sync.Mutex
	devices map[string]*Device
	status  map[string]int
	msgs    []Message
}

// NewService sobe o serviço; fecha ao fim do teste.
func NewService(t testing.TB) *Service {
	t.Helper()
	s := &Service{devices: map[string]*Device{}, status: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// Device cria um aparelho cuja assinatura aponta para path neste serviço.
func (s *Service) Device(path string) *Device {
	d := NewDevice(s.URL + path)
	s.mu.Lock()
	s.devices[path] = d
	s.mu.Unlock()
	return d
}

// SetStatus faz o serviço responder code para path (404/410 = assinatura expirada; 500 = falha).
func (s *Service) SetStatus(path string, code int) {
	s.mu.Lock()
	s.status[path] = code
	s.mu.Unlock()
}

// Messages devolve uma cópia do que chegou, na ordem.
func (s *Service) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.msgs...)
}

// Reset esquece as mensagens.
func (s *Service) Reset() {
	s.mu.Lock()
	s.msgs = nil
	s.mu.Unlock()
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	s.mu.Lock()
	dev, code := s.devices[r.URL.Path], s.status[r.URL.Path]
	s.mu.Unlock()
	m := Message{Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), TTL: r.Header.Get("TTL")}
	switch {
	case r.Method != http.MethodPost:
		m.Err = "método " + r.Method
	case r.Header.Get("Content-Encoding") != "aes128gcm":
		m.Err = "Content-Encoding = " + r.Header.Get("Content-Encoding")
	case dev == nil:
		m.Err = "assinatura desconhecida"
	default:
		if k, err := VerifyVAPID(m.Authorization, "http://"+r.Host, time.Now()); err != nil {
			m.Err = "vapid: " + err.Error()
		} else {
			m.VAPIDKey = k
		}
		if pt, err := dev.Decrypt(body); err != nil {
			m.Err += " decifrar: " + err.Error()
		} else {
			m.Payload = pt
		}
	}
	s.mu.Lock()
	s.msgs = append(s.msgs, m)
	s.mu.Unlock()
	if code == 0 {
		code = http.StatusCreated
	}
	w.WriteHeader(code)
}
