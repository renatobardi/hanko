// Package totp gera o código de uso único (RFC 6238) de uma semente guardada no cofre (ADR-0068 §4.3). É puro: a
// semente entra, o código sai. Quem chama nunca recebe a semente de volta.
package totp

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238: o algoritmo padrão do TOTP é HMAC-SHA1
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Padrões da RFC 6238 e do formato otpauth:// (o que vale quando a URI não diz outra coisa).
const (
	DefaultDigits = 6
	DefaultPeriod = 30
	minDigits     = 6
	maxDigits     = 8
	maxPeriod     = 300
)

// ErrSeed é a semente recusada: nem base32 nem URI otpauth:// de TOTP válida.
var ErrSeed = errors.New("semente TOTP inválida")

// Seed é uma semente já lida, com os parâmetros que a URI otpauth:// trouxe (ou os padrões).
type Seed struct {
	secret    []byte
	digits    int
	period    int
	algorithm string
}

// Parse lê uma semente em base32 ou uma URI otpauth://totp/. A semente em base32 aceita espaços e minúsculas, como
// os apps de autenticação a mostram.
func Parse(v string) (Seed, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "otpauth://") {
		return parseURI(v)
	}
	secret, err := decodeBase32(v)
	if err != nil {
		return Seed{}, err
	}
	return Seed{secret: secret, digits: DefaultDigits, period: DefaultPeriod, algorithm: "SHA1"}, nil
}

func parseURI(v string) (Seed, error) {
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "otpauth" || u.Host != "totp" {
		return Seed{}, fmt.Errorf("%w: a URI precisa ser otpauth://totp/", ErrSeed)
	}
	q := u.Query()
	secret, err := decodeBase32(q.Get("secret"))
	if err != nil {
		return Seed{}, err
	}
	s := Seed{secret: secret, digits: DefaultDigits, period: DefaultPeriod, algorithm: "SHA1"}
	if d := q.Get("digits"); d != "" {
		if s.digits, err = strconv.Atoi(d); err != nil || s.digits < minDigits || s.digits > maxDigits {
			return Seed{}, fmt.Errorf("%w: digits entre %d e %d", ErrSeed, minDigits, maxDigits)
		}
	}
	if p := q.Get("period"); p != "" {
		if s.period, err = strconv.Atoi(p); err != nil || s.period < 1 || s.period > maxPeriod {
			return Seed{}, fmt.Errorf("%w: period entre 1 e %d segundos", ErrSeed, maxPeriod)
		}
	}
	if a := q.Get("algorithm"); a != "" {
		s.algorithm = strings.ToUpper(a)
		if _, err := newHash(s.algorithm); err != nil {
			return Seed{}, err
		}
	}
	return s, nil
}

// decodeBase32 aceita a semente como os apps a mostram: maiúsculas ou minúsculas, com espaços, com ou sem `=`.
func decodeBase32(v string) ([]byte, error) {
	clean := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(v))
	clean = strings.TrimRight(clean, "=")
	if clean == "" {
		return nil, fmt.Errorf("%w: a semente está vazia", ErrSeed)
	}
	if pad := len(clean) % 8; pad != 0 {
		clean += strings.Repeat("=", 8-pad)
	}
	out, err := base32.StdEncoding.DecodeString(clean)
	if err != nil || len(out) == 0 {
		return nil, fmt.Errorf("%w: a semente não é base32", ErrSeed)
	}
	return out, nil
}

func newHash(alg string) (func() hash.Hash, error) {
	switch alg {
	case "SHA1":
		return sha1.New, nil
	case "SHA256":
		return sha256.New, nil
	case "SHA512":
		return sha512.New, nil
	}
	return nil, fmt.Errorf("%w: algoritmo %q não suportado (SHA1, SHA256 ou SHA512)", ErrSeed, alg)
}

// At devolve o código válido no instante now e os segundos que faltam para ele vencer.
func (s Seed) At(now time.Time) (code string, remaining int) {
	mk, err := newHash(s.algorithm)
	if err != nil { // a URI já foi conferida em Parse; o padrão é SHA1
		mk = sha1.New
	}
	unix := now.Unix()
	counter := uint64(unix) / uint64(s.period) //nolint:gosec // instante positivo: o relógio não vai antes de 1970
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(mk, s.secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range s.digits {
		mod *= 10
	}
	code = fmt.Sprintf("%0*d", s.digits, bin%mod)
	remaining = s.period - int(unix%int64(s.period))
	return code, remaining
}
