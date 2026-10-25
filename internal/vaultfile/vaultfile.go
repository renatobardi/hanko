// Package vaultfile é o formato .hkv do export cifrado do cofre (ADR-0068 §8.2): uma linha mágica, uma linha de
// cabeçalho em JSON (versão, KDF, iterações, sal, cifra e nonce) e o conteúdo cifrado com AES-256-GCM sob uma chave
// derivada da senha por PBKDF2-SHA256. O cabeçalho entra como dado autenticado: mexer nele faz a abertura falhar.
package vaultfile

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// Magic abre todo arquivo .hkv.
	Magic = "HKV1\n"
	// Iterations é o custo do PBKDF2 ao gravar (ADR-0068 §8.2). Ao abrir, aceita-se de Iterations a MaxIterations.
	Iterations    = 600000
	MaxIterations = 5000000
	// MinPasswordLen é o menor tamanho de senha aceito para gravar.
	MinPasswordLen = 12

	kdfName    = "pbkdf2-sha256"
	cipherName = "aes-256-gcm"
	saltLen    = 16
	keyLen     = 32
	maxHeader  = 1024
)

var (
	// ErrFormat diz que o arquivo não é um .hkv desta versão.
	ErrFormat = errors.New("arquivo de cofre inválido")
	// ErrPassword diz que a senha está errada ou o arquivo foi alterado (a cifra não distingue os dois).
	ErrPassword = errors.New("senha errada ou arquivo alterado")
)

type header struct {
	Version    int    `json:"v"`
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       []byte `json:"salt"`
	Cipher     string `json:"cipher"`
	Nonce      []byte `json:"nonce"`
}

func deriveGCM(password string, h header) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, h.Salt, h.Iterations, keyLen)
	if err != nil {
		return nil, fmt.Errorf("derivar chave: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	return cipher.NewGCM(block)
}

// Seal cifra plain com a senha e devolve o arquivo .hkv. Sal e nonce são novos a cada chamada.
func Seal(plain []byte, password string) ([]byte, error) {
	if len(password) < MinPasswordLen {
		return nil, fmt.Errorf("a senha do arquivo precisa de pelo menos %d caracteres", MinPasswordLen)
	}
	h := header{Version: 1, KDF: kdfName, Iterations: Iterations, Salt: make([]byte, saltLen), Cipher: cipherName}
	if _, err := rand.Read(h.Salt); err != nil {
		return nil, fmt.Errorf("sal: %w", err)
	}
	gcm, err := deriveGCM(password, h)
	if err != nil {
		return nil, err
	}
	h.Nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(h.Nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	hb, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("cabeçalho: %w", err)
	}
	prefix := append(append([]byte(Magic), hb...), '\n')
	return gcm.Seal(prefix, h.Nonce, plain, prefix), nil
}

// Open abre um arquivo .hkv. Formato que não bate dá ErrFormat; senha errada ou arquivo alterado dá ErrPassword.
func Open(file []byte, password string) ([]byte, error) {
	if !bytes.HasPrefix(file, []byte(Magic)) {
		return nil, ErrFormat
	}
	rest := file[len(Magic):]
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 || nl > maxHeader {
		return nil, ErrFormat
	}
	var h header
	if err := json.Unmarshal(rest[:nl], &h); err != nil {
		return nil, ErrFormat
	}
	if h.Version != 1 || h.KDF != kdfName || h.Cipher != cipherName || h.Iterations < Iterations || h.Iterations > MaxIterations ||
		len(h.Salt) < saltLen || len(h.Salt) > 64 {
		return nil, ErrFormat
	}
	gcm, err := deriveGCM(password, h)
	if err != nil {
		return nil, err
	}
	if len(h.Nonce) != gcm.NonceSize() {
		return nil, ErrFormat
	}
	prefixLen := len(Magic) + nl + 1
	plain, err := gcm.Open(nil, h.Nonce, file[prefixLen:], file[:prefixLen])
	if err != nil {
		return nil, ErrPassword
	}
	return plain, nil
}
