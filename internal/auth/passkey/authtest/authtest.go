// Package authtest é um autenticador WebAuthn de software para os testes do Hanko (ADR-0070): gera a
// chave no teste, monta as respostas JSON de navigator.credentials.create/get e assina com ES256.
// Só é importado por testes; CBOR mínimo embutido (mapas e bytes fixos), sem dependências.
package authtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

// Authenticator é uma credencial de software. O contador de assinatura sobe a cada prova, como o
// de um autenticador de verdade.
type Authenticator struct {
	Key       *ecdsa.PrivateKey
	AAGUID    [16]byte
	CredID    []byte
	SignCount uint32
	// Origin é a origem declarada no clientDataJSON (a página que chamou o navegador). Vazio deriva
	// do rpId como https://rpId; os testes que precisam de outra origem a informam.
	Origin string
}

// New cria um autenticador novo (chave ES256 P-256 e id de credencial aleatórios).
func New() *Authenticator {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err) // só em teste: GenerateKey P-256 com crypto/rand não falha na prática
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		panic(err)
	}
	var aaguid [16]byte
	copy(aaguid[:], "hanko-authtest-") // AAGUID identificável; byte 16 é o terminador implícito
	return &Authenticator{Key: k, AAGUID: aaguid, CredID: id}
}

var b64 = base64.RawURLEncoding

// creationOptions são os campos que o autenticador lê de PublicKeyCredentialCreationOptions.
// creationOptions é PublicKeyCredentialCreationOptions (o valor de publicKey).
type creationOptions struct {
	Challenge string `json:"challenge"`
	RP        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"rp"`
}

// requestOptions são os campos que o autenticador lê de PublicKeyCredentialRequestOptions.
// requestOptions é PublicKeyCredentialRequestOptions (o valor de publicKey).
type requestOptions struct {
	Challenge string `json:"challenge"`
	RPID      string `json:"rpId"`
}

// Create responde a navigator.credentials.create com as opções do servidor (PublicKeyCredentialCreationOptions
// em JSON) e devolve a resposta que vai no corpo de finish: id, rawId, type e response.
func (a *Authenticator) Create(options json.RawMessage) (json.RawMessage, error) {
	var top struct {
		PublicKey json.RawMessage `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &top); err != nil || len(top.PublicKey) == 0 {
		return nil, fmt.Errorf("authtest: opções de cadastro sem publicKey")
	}
	var o creationOptions
	if err := json.Unmarshal(top.PublicKey, &o); err != nil {
		return nil, fmt.Errorf("authtest: opções de cadastro: %w", err)
	}
	challenge, err := b64.DecodeString(o.Challenge)
	if err != nil {
		return nil, fmt.Errorf("authtest: desafio de cadastro: %w", err)
	}
	if len(challenge) < 16 {
		return nil, fmt.Errorf("authtest: desafio de cadastro curto demais (%d bytes)", len(challenge))
	}
	rpIDHash := sha256.Sum256([]byte(o.RP.ID))

	cose, err := coseKey(&a.Key.PublicKey)
	if err != nil {
		return nil, err
	}
	authData := make([]byte, 0, 37+2+len(a.CredID)+len(cose))
	authData = append(authData, rpIDHash[:]...)
	authData = append(authData, 0x5D) // UP | UV | BE | BS | AT
	authData = binary.BigEndian.AppendUint32(authData, 0)
	authData = append(authData, a.AAGUID[:]...)
	credLen := len(a.CredID)
	if credLen > math.MaxUint16 {
		return nil, fmt.Errorf("authtest: credencial grande demais (%d bytes)", credLen)
	}
	authData = binary.BigEndian.AppendUint16(authData, uint16(credLen))
	authData = append(authData, a.CredID...)
	authData = append(authData, cose...)

	clientData := map[string]any{"type": "webauthn.create", "challenge": o.Challenge, "origin": a.origin(o.RP.ID),
		"crossOrigin": false}
	clientJSON, err := json.Marshal(clientData)
	if err != nil {
		return nil, err
	}
	attObj := cborAttestation(authData)
	resp := map[string]any{
		"id": b64.EncodeToString(a.CredID), "rawId": b64.EncodeToString(a.CredID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientJSON),
			"attestationObject": b64.EncodeToString(attObj),
			"transports":        []string{"internal"},
		},
	}
	return json.Marshal(resp)
}

// Get responde a navigator.credentials.get com as opções do servidor (PublicKeyCredentialRequestOptions em
// JSON) e devolve a resposta que vai no corpo da prova: id, rawId, type e response assinados.
func (a *Authenticator) Get(options json.RawMessage) (json.RawMessage, error) {
	var top struct {
		PublicKey json.RawMessage `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &top); err != nil || len(top.PublicKey) == 0 {
		return nil, fmt.Errorf("authtest: opções de prova sem publicKey")
	}
	var o requestOptions
	if err := json.Unmarshal(top.PublicKey, &o); err != nil {
		return nil, fmt.Errorf("authtest: opções de prova: %w", err)
	}
	if _, err := b64.DecodeString(o.Challenge); err != nil {
		return nil, fmt.Errorf("authtest: desafio de prova: %w", err)
	}
	rpIDHash := sha256.Sum256([]byte(o.RPID))

	a.SignCount++
	authData := make([]byte, 0, 37)
	authData = append(authData, rpIDHash[:]...)
	authData = append(authData, 0x1D) // UP | UV | BE | BS
	authData = binary.BigEndian.AppendUint32(authData, a.SignCount)

	clientData := map[string]any{"type": "webauthn.get", "challenge": o.Challenge, "origin": a.origin(o.RPID),
		"crossOrigin": false}
	clientJSON, err := json.Marshal(clientData)
	if err != nil {
		return nil, err
	}
	sig, err := signAssertion(a.Key, authData, clientJSON)
	if err != nil {
		return nil, err
	}
	resp := map[string]any{
		"id": b64.EncodeToString(a.CredID), "rawId": b64.EncodeToString(a.CredID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientJSON),
			"authenticatorData": b64.EncodeToString(authData),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        "",
		},
	}
	return json.Marshal(resp)
}

func signAssertion(k *ecdsa.PrivateKey, authData, clientJSON []byte) ([]byte, error) {
	ch := sha256.Sum256(clientJSON)
	msg := append(append([]byte{}, authData...), ch[:]...)
	digest := sha256.Sum256(msg)
	return ecdsa.SignASN1(rand.Reader, k, digest[:])
}

func (a *Authenticator) origin(rpID string) string {
	if a.Origin != "" {
		return a.Origin
	}
	return "https://" + rpID
}

// coseKey monta a chave pública ES256 em COSE (mapa CBOR: kty 2, alg -7, crv 1, x, y).
func coseKey(pub *ecdsa.PublicKey) ([]byte, error) {
	raw, err := pub.Bytes() // ponto SEC 1 sem compressão: 0x04 || X || Y, com 32 bytes por coordenada em P-256
	if err != nil {
		return nil, fmt.Errorf("authtest: chave pública: %w", err)
	}
	x, y := raw[1:33], raw[33:65]
	out := cborHead(5, 5)             // map(5)
	out = append(out, cborUint(1)...) // kty
	out = append(out, cborUint(2)...)
	out = append(out, cborUint(3)...) // alg
	out = append(out, cborNeg(7)...)  // ES256
	out = append(out, cborNeg(1)...)  // crv
	out = append(out, cborUint(1)...)
	out = append(out, cborNeg(2)...) // x
	out = append(out, cborBytes(x)...)
	out = append(out, cborNeg(3)...) // y
	out = append(out, cborBytes(y)...)
	return out, nil
}

// cborAttestation monta o attestationObject com fmt "none" (atestação none, ADR-0070).
func cborAttestation(authData []byte) []byte {
	out := cborHead(5, 3) // map(3)
	out = append(out, cborText("fmt")...)
	out = append(out, cborText("none")...)
	out = append(out, cborText("attStmt")...)
	out = append(out, cborHead(5, 0)...) // map(0)
	out = append(out, cborText("authData")...)
	out = append(out, cborBytes(authData)...)
	return out
}

// cborHead devolve o cabeçalho do major type (mapa = 5) com o tamanho n.
func cborHead(major byte, n uint64) []byte { return cborLen(major<<5, n) }

func cborLen(prefix byte, n uint64) []byte {
	b := binary.BigEndian.AppendUint64(nil, n) // os bytes de n; o cabeçalho usa só a cauda que cabe na faixa
	switch {
	case n < 24:
		return []byte{prefix | b[7]}
	case n <= 0xff:
		return []byte{prefix | 24, b[7]}
	case n <= 0xffff:
		return append([]byte{prefix | 25}, b[6:]...)
	case n <= 0xffffffff:
		return append([]byte{prefix | 26}, b[4:]...)
	default:
		return append([]byte{prefix | 27}, b...)
	}
}

func cborUint(n uint64) []byte { return cborLen(0x00, n) }

// cborNeg codifica o inteiro negativo -n (CBOR guarda -1-x no major type 1).
func cborNeg(n uint64) []byte { return cborLen(0x20, n-1) }

func cborBytes(b []byte) []byte { return append(cborLen(0x40, uint64(len(b))), b...) }
func cborText(s string) []byte  { return append(cborLen(0x60, uint64(len(s))), s...) }
