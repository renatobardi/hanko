package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Limites de senha. O máximo evita que uma senha gigante vire vetor de CPU.
const (
	MinPasswordLen = 10
	MaxPasswordLen = 256
)

// PasswordParams são os parâmetros do argon2id. Os defaults seguem a faixa "forte" do
// OWASP (m ≥ 19 MiB, t ≥ 2) com folga: 64 MiB, 3 passadas, 2 lanes.
type PasswordParams struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultPasswordParams é o custo usado em produção.
var DefaultPasswordParams = PasswordParams{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32, SaltLen: 16}

// ErrBadPassword: senha fora da política (tamanho).
var ErrBadPassword = fmt.Errorf("senha deve ter entre %d e %d caracteres", MinPasswordLen, MaxPasswordLen)

// ValidatePassword aplica a política de senha para novas senhas.
func ValidatePassword(pw string) error {
	n := len([]rune(pw))
	if n < MinPasswordLen || n > MaxPasswordLen {
		return ErrBadPassword
	}
	return nil
}

// HashPassword devolve o hash no formato PHC ($argon2id$v=19$m=…,t=…,p=…$salt$hash).
func HashPassword(pw string) (string, error) { return DefaultPasswordParams.Hash(pw) }

// Hash calcula o hash com estes parâmetros.
func (p PasswordParams) Hash(pw string) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("gerar salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

var errBadHash = errors.New("hash de senha inválido")

// VerifyPassword compara pw com o hash PHC em tempo constante. Um hash malformado é erro;
// senha errada é (false, nil).
func VerifyPassword(pw, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var p PasswordParams
	var threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &threads); err != nil {
		return false, errBadHash
	}
	// Teto defensivo: um hash adulterado no banco não pode pedir GBs ou horas de CPU.
	if p.Memory < 8 || p.Memory > 512*1024 || p.Time < 1 || p.Time > 16 || threads < 1 || threads > 32 {
		return false, errBadHash
	}
	p.Threads = uint8(threads)
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false, errBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// BurnPassword gasta o mesmo custo de uma verificação quando o usuário não existe, para
// que o tempo de resposta não revele se o e-mail está cadastrado.
func BurnPassword(pw string) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword("hanko-dummy-password") })
	_, _ = VerifyPassword(pw, dummyHash)
}
