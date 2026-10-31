package domain

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed" // as listas de palavras vêm do arquivo de dados do contrato
	"encoding/binary"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Gerador único de nome (ADR-0084): `Adjetivo_Substantivo` em inglês, 256 × 256 = 65.536 combinações, para a conversa
// externa, a rodada externa e, depois, os runs. As listas são dado do contrato, em `names/names.txt`, e as duas regras
// moram aqui: a derivada do id (conversa, run) e o sorteio entre os livres (rodada).

//go:embed names/names.txt
var externalNamesFile string

var (
	externalNamesOnce sync.Once
	externalAdjs      []string
	externalNouns     []string
)

// externalNameLists lê as duas listas do arquivo embutido. O formato é conferido por teste; arquivo fora do formato
// deixa as listas vazias e o gerador devolve "".
func externalNameLists() (adjectives, nouns []string) {
	externalNamesOnce.Do(func() {
		var section *[]string
		for _, line := range strings.Split(externalNamesFile, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "# adjectives":
				section = &externalAdjs
			case line == "# nouns":
				section = &externalNouns
			case line == "" || strings.HasPrefix(line, "#"):
			case section != nil:
				*section = append(*section, line)
			}
		}
	})
	return externalAdjs, externalNouns
}

// ExternalNameAttempts é quantas variações do hash o service tenta antes de recorrer ao sufixo numérico (ADR-0084 item 2).
const ExternalNameAttempts = 16

// ExternalNameSpace é quantos nomes distintos o par de listas produz.
func ExternalNameSpace() int {
	a, n := externalNameLists()
	return len(a) * len(n)
}

func externalNameHash(id ID, attempt int) uint64 {
	h := sha256.New()
	h.Write(id[:])
	h.Write([]byte(strconv.Itoa(attempt))) // decimal, como no RunName
	return binary.BigEndian.Uint64(h.Sum(nil)[:8])
}

// ExternalName deriva o nome do id: o sha256 do id e da tentativa escolhe o adjetivo e o substantivo. A tentativa 0 é o
// nome padrão; as seguintes só valem quando o nome já existe na empresa. O mesmo id e a mesma tentativa dão sempre o
// mesmo nome. Devolve "" se as listas estão vazias.
func ExternalName(id ID, attempt int) string {
	adjs, nouns := externalNameLists()
	if len(adjs) == 0 || len(nouns) == 0 {
		return ""
	}
	h := externalNameHash(id, attempt)
	return adjs[h%uint64(len(adjs))] + "_" + nouns[(h/uint64(len(adjs)))%uint64(len(nouns))]
}

// ExternalNameFallback é o último recurso quando as ExternalNameAttempts tentativas colidem: o nome da tentativa 0 com um
// número de 3 dígitos, de 100 a 999, derivado do id e de n (Brave_Otter_417).
func ExternalNameFallback(id ID, n int) string {
	base := ExternalName(id, 0)
	if base == "" {
		return ""
	}
	h := externalNameHash(id, 1000+n)
	return base + "_" + string(rune('0'+h%9+1)) + string(rune('0'+(h/9)%10)) + string(rune('0'+(h/90)%10))
}

// externalFallbackTries limita as tentativas do sufixo numérico: são 900 sufixos por nome, e a empresa que esgota todos
// já passou de qualquer uso real.
const externalFallbackTries = 900

// ChooseExternalName acha o nome do id que a empresa ainda não usa. `taken` diz se um nome já existe nela. Tenta as
// ExternalNameAttempts variações do hash e depois o sufixo numérico. Devolve "" se as listas estão vazias ou se nenhuma
// variação serve.
func ChooseExternalName(id ID, taken func(name string) bool) string {
	for attempt := 0; attempt < ExternalNameAttempts; attempt++ {
		if name := ExternalName(id, attempt); name == "" || !taken(name) {
			return name
		}
	}
	for n := 0; n < externalFallbackTries; n++ {
		if name := ExternalNameFallback(id, n); name == "" || !taken(name) {
			return name
		}
	}
	return ""
}

// DrawExternalName sorteia, entre todos os nomes que não estão em `taken`, um nome para a rodada (ADR-0084 item 2). `pick`
// devolve um inteiro em [0, n); em produção, RandomIndex. ok é falso quando não sobrou nenhum nome livre.
func DrawExternalName(taken []string, pick func(n int) int) (name string, ok bool) {
	adjs, nouns := externalNameLists()
	space := len(adjs) * len(nouns)
	if space == 0 {
		return "", false
	}
	adjIdx := make(map[string]int, len(adjs))
	for i, a := range adjs {
		adjIdx[a] = i
	}
	nounIdx := make(map[string]int, len(nouns))
	for i, n := range nouns {
		nounIdx[n] = i
	}
	used := map[int]bool{}
	for _, t := range taken {
		a, n, found := strings.Cut(t, "_")
		ai, aok := adjIdx[a]
		ni, nok := nounIdx[n]
		if found && aok && nok {
			used[ai*len(nouns)+ni] = true
		}
	}
	free := space - len(used)
	if free <= 0 {
		return "", false
	}
	// o r-ésimo nome livre: anda pelos índices ocupados em ordem e soma um a r a cada ocupado que fica antes dele
	usedSorted := make([]int, 0, len(used))
	for i := range used {
		usedSorted = append(usedSorted, i)
	}
	sort.Ints(usedSorted)
	r := pick(free)
	for _, u := range usedSorted {
		if u <= r {
			r++
		}
	}
	return adjs[r/len(nouns)] + "_" + nouns[r%len(nouns)], true
}

// RandomIndex é um inteiro aleatório em [0, n), do gerador criptográfico.
func RandomIndex(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0 // o rand do sistema não falha; sem ele, o primeiro livre ainda é um nome válido
	}
	return int(v.Int64())
}
