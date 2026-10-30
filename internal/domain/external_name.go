package domain

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// Gerador único de nome (ADR-0084): `Adjetivo_Substantivo` em inglês, derivado do texto que identifica a coisa (o
// session.id de uma conversa externa). As listas são dado do contrato, em names/*.txt: mudar uma palavra muda o nome
// de conversas novas, nunca o de conversas já gravadas, porque o nome fica salvo na linha. Hoje são as 64 + 64 do
// agent-studio; as 192 + 192 palavras novas do ADR-0084 entram num PR próprio, depois da revisão do Bardi.

//go:embed names/adjectives.txt
var externalAdjectivesData string

//go:embed names/nouns.txt
var externalNounsData string

var (
	externalAdjectives = wordList(externalAdjectivesData)
	externalNouns      = wordList(externalNounsData)
)

// externalNameSuffixes é quantos sufixos numéricos o gerador tenta depois das tentativas por hash.
const externalNameSuffixes = 8

// ErrExternalNameExhausted diz que nenhum nome do texto ficou livre na empresa, nem com sufixo. Não acontece na prática
// (o par de listas dá ExternalNameSpace() nomes); se acontecer, a ingestão tenta de novo depois.
var ErrExternalNameExhausted = fmt.Errorf("external name: no free name: %w", ErrConflict)

func wordList(data string) []string {
	var out []string
	for _, w := range strings.Split(data, "\n") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// ExternalNameSpace é quantos nomes distintos o par de listas produz.
func ExternalNameSpace() int { return len(externalAdjectives) * len(externalNouns) }

// ExternalNameWords devolve as duas listas, adjetivos e substantivos, como estão no arquivo de dados (para os testes e
// para a cópia do oute-agent).
func ExternalNameWords() (adjectives, nouns []string) {
	return append([]string(nil), externalAdjectives...), append([]string(nil), externalNouns...)
}

func externalNameHash(seed string, attempt int) uint64 {
	h := sha256.New()
	h.Write([]byte(seed))
	h.Write([]byte{0}) // separa o texto da tentativa: "ab"+"1" não pode virar "a"+"b1"
	h.Write([]byte(strconv.Itoa(attempt)))
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

// ExternalName deriva o nome da tentativa attempt do texto seed. A tentativa 0 é o nome padrão; as seguintes só são
// usadas quando o padrão já existe na empresa (ChooseExternalName).
func ExternalName(seed string, attempt int) string {
	h := externalNameHash(seed, attempt)
	adj := externalAdjectives[h%uint64(len(externalAdjectives))]
	noun := externalNouns[(h/uint64(len(externalAdjectives)))%uint64(len(externalNouns))]
	return adj + "_" + noun
}

// ExternalNameFallback é o último recurso: o nome da tentativa 0 com um sufixo de 3 dígitos (100 a 999), que o hash do
// texto escolhe. `n` varia o sufixo quando o primeiro já existe.
func ExternalNameFallback(seed string, n int) string {
	h := externalNameHash(seed, 1000+n)
	return ExternalName(seed, 0) + "_" + strconv.Itoa(100+int(h%900))
}

// ChooseExternalName devolve o primeiro nome que `taken` diz livre: as tentativas por hash, depois os sufixos
// numéricos. `taken` consulta a empresa; um erro dela interrompe a escolha.
func ChooseExternalName(seed string, taken func(name string) (bool, error)) (string, error) {
	for a := 0; a < RunNameAttempts; a++ {
		name := ExternalName(seed, a)
		used, err := taken(name)
		if err != nil {
			return "", err
		}
		if !used {
			return name, nil
		}
	}
	for n := 0; n < externalNameSuffixes; n++ {
		name := ExternalNameFallback(seed, n)
		used, err := taken(name)
		if err != nil {
			return "", err
		}
		if !used {
			return name, nil
		}
	}
	return "", ErrExternalNameExhausted
}
