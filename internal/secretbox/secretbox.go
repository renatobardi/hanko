// Package secretbox cifra em repouso os segredos das integrações (ADR-0029): AES-256-GCM com a chave do
// servidor (HANKO_SECRET_KEY). O texto cifrado leva o id da chave que o gerou, então uma rotação
// (HANKO_SECRET_KEY nova + HANKO_SECRET_KEY_PREVIOUS antiga) continua decifrando o que já está no banco até
// que o servidor reescreva tudo com a chave nova (hanko integration rekey).
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const prefix = "v1"

// ErrNoKey: nenhuma HANKO_SECRET_KEY configurada. ErrUnknownKey: o texto foi cifrado com uma chave que o
// servidor não tem mais (nem como anterior). ErrCorrupt: formato inválido ou autenticação falhou.
var (
	ErrNoKey      = errors.New("secretbox: HANKO_SECRET_KEY não configurada")
	ErrUnknownKey = errors.New("secretbox: chave de cifragem desconhecida (rotação sem HANKO_SECRET_KEY_PREVIOUS?)")
	ErrCorrupt    = errors.New("secretbox: texto cifrado inválido")
)

type key struct {
	id   string
	aead cipher.AEAD
}

// Box cifra com a chave atual e decifra com a atual ou a anterior. O zero value não tem chave.
type Box struct {
	cur  *key
	prev *key
	// mac é a chave do HMAC de comparação (ADR-0068 §7.2), derivada da chave atual com rótulo próprio.
	mac []byte
}

func derive(secret string) (*key, error) {
	sum := sha256.Sum256([]byte("hanko/secretbox/v1\x00" + secret))
	blk, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	id := sha256.Sum256(sum[:])
	return &key{id: hex.EncodeToString(id[:4]), aead: g}, nil
}

// New monta o Box. current vazio = sem cifragem (Seal devolve ErrNoKey).
func New(current, previous string) (*Box, error) {
	b := &Box{}
	if current == "" {
		return b, nil
	}
	var err error
	if b.cur, err = derive(current); err != nil {
		return nil, err
	}
	// A chave da MAC sai da chave atual por HMAC-SHA256 com o segredo como chave (PRF), e não por SHA-256 puro
	// sobre o segredo: hash simples de segredo é o que o CodeQL marca (go/weak-sensitive-data-hashing). O rótulo
	// separa esta chave da chave de cifragem, que sai do mesmo segredo.
	kdf := hmac.New(sha256.New, []byte(current))
	kdf.Write([]byte("hanko/secretbox/mac/v1"))
	b.mac = kdf.Sum(nil)
	if previous != "" {
		if b.prev, err = derive(previous); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// Enabled informa se há chave para cifrar.
func (b *Box) Enabled() bool { return b != nil && b.cur != nil }

// KeyID é o id público (8 hex) da chave atual, para diagnóstico; vazio sem chave.
func (b *Box) KeyID() string {
	if !b.Enabled() {
		return ""
	}
	return b.cur.id
}

// MAC devolve o HMAC-SHA256 de plain em hex, com a chave atual. Serve para achar valores repetidos sem decifrar
// (ADR-0068 §7.2). Quem guarda o MAC precisa recalculá-lo depois de uma rotação de chave (`hanko secret rekey`).
func (b *Box) MAC(plain []byte) (string, error) {
	if !b.Enabled() {
		return "", ErrNoKey
	}
	h := hmac.New(sha256.New, b.mac)
	h.Write(plain)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Seal cifra plain. aad liga o texto cifrado ao seu dono (por exemplo o id da integração): copiar o valor
// para outra linha faz a decifragem falhar.
func (b *Box) Seal(plain []byte, aad string) (string, error) {
	if !b.Enabled() {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.cur.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	ct := b.cur.aead.Seal(nonce, nonce, plain, []byte(aad))
	return prefix + ":" + b.cur.id + ":" + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Open decifra um valor produzido por Seal.
func (b *Box) Open(sealed, aad string) ([]byte, error) {
	parts := strings.Split(sealed, ":")
	if len(parts) != 3 || parts[0] != prefix {
		return nil, ErrCorrupt
	}
	if !b.Enabled() {
		return nil, ErrNoKey
	}
	var k *key
	switch {
	case b.cur.id == parts[1]:
		k = b.cur
	case b.prev != nil && b.prev.id == parts[1]:
		k = b.prev
	default:
		return nil, ErrUnknownKey
	}
	raw, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(raw) < k.aead.NonceSize()+k.aead.Overhead() {
		return nil, ErrCorrupt
	}
	n := k.aead.NonceSize()
	pt, err := k.aead.Open(nil, raw[:n], raw[n:], []byte(aad))
	if err != nil {
		return nil, ErrCorrupt
	}
	return pt, nil
}

// NeedsReseal informa se sealed foi cifrado com outra chave que não a atual.
func (b *Box) NeedsReseal(sealed string) bool {
	parts := strings.Split(sealed, ":")
	return b.Enabled() && len(parts) == 3 && parts[1] != b.cur.id
}
