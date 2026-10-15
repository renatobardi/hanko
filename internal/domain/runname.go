package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"
)

// Nome amigável de execução: Adjetivo_Substantivo em pt-BR (ex.: Veloz_Raposa), derivado do id da
// run. Os adjetivos são invariáveis em gênero (veloz, gentil, radiante...), então concordam com
// qualquer substantivo. As listas são parte do contrato: mudar uma palavra muda o nome de runs
// novas, nunca o de runs já gravadas (o nome fica salvo na run).
var runAdjectives = [...]string{
	"Veloz", "Ágil", "Feroz", "Gentil", "Audaz", "Sagaz", "Leal", "Fiel", "Forte", "Brilhante",
	"Elegante", "Valente", "Paciente", "Radiante", "Constante", "Suave", "Nobre", "Simples", "Cortês", "Doce",
	"Verde", "Azul", "Alegre", "Livre", "Celeste", "Estelar", "Lunar", "Solar", "Polar", "Ardente",
	"Fulgente", "Silente", "Prudente", "Inteligente", "Eficiente", "Persistente", "Resiliente", "Vigilante", "Errante", "Itinerante",
	"Vibrante", "Reluzente", "Cintilante", "Fervente", "Capaz", "Tenaz", "Eficaz", "Fugaz", "Perspicaz", "Voraz",
	"Sutil", "Hábil", "Útil", "Imortal", "Real", "Frugal", "Natural", "Genial", "Cordial", "Glacial",
	"Boreal", "Austral", "Sideral", "Tropical",
}

var runNouns = [...]string{
	"Raposa", "Falcão", "Coruja", "Lobo", "Onça", "Tucano", "Arara", "Jaguar", "Capivara", "Tartaruga",
	"Golfinho", "Baleia", "Garça", "Condor", "Águia", "Gavião", "Pantera", "Lince", "Tamanduá", "Jacaré",
	"Cervo", "Zebra", "Girafa", "Pinguim", "Foca", "Orca", "Polvo", "Cometa", "Nebulosa", "Aurora",
	"Galáxia", "Vulcão", "Cascata", "Corrente", "Montanha", "Floresta", "Pradaria", "Savana", "Geleira", "Penhasco",
	"Cânion", "Farol", "Bússola", "Âncora", "Navio", "Caravela", "Moinho", "Ponte", "Torre", "Lanterna",
	"Prisma", "Cristal", "Rubi", "Safira", "Esmeralda", "Ônix", "Jade", "Opala", "Coral", "Relâmpago",
	"Ventania", "Maré", "Horizonte", "Trilha",
}

// RunNameAttempts é quantas variações do hash o service tenta antes de recorrer ao sufixo numérico.
const RunNameAttempts = 16

// RunNameSpace é quantos nomes distintos o par de listas produz.
func RunNameSpace() int { return len(runAdjectives) * len(runNouns) }

func runNameHash(id ID, attempt int) uint64 {
	h := sha256.New()
	h.Write(id[:])
	h.Write([]byte(strconv.Itoa(attempt))) // decimal, sem conversão de sinal
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

// RunName deriva o nome amigável do id da run. attempt 0 é o nome padrão; as tentativas seguintes
// (até RunNameAttempts-1) só são usadas pelo service quando o nome já existe na empresa. Sem
// colisão prática: o par de listas dá RunNameSpace() nomes e a empresa só usa a tentativa seguinte
// quando o par sorteado já está tomado. Depois da última tentativa, RunNameFallback acrescenta um número.
func RunName(id ID, attempt int) string {
	h := runNameHash(id, attempt)
	adj := runAdjectives[h%uint64(len(runAdjectives))]
	noun := runNouns[(h/uint64(len(runAdjectives)))%uint64(len(runNouns))]
	return adj + "_" + noun
}

// RunNameFallback é o último recurso quando todas as tentativas colidem: o nome da tentativa 0
// com um número de 3 dígitos derivado do id (Veloz_Raposa_417).
func RunNameFallback(id ID, n int) string {
	h := runNameHash(id, 1000+n)
	return RunName(id, 0) + "_" + string(rune('0'+h%9+1)) + string(rune('0'+(h/9)%10)) + string(rune('0'+(h/90)%10))
}

// foldAccents tira os acentos do português, para o nome poder ser digitado em qualquer teclado.
var foldAccents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "é", "e", "ê", "e", "í", "i", "ó", "o", "ô", "o", "õ", "o", "ú", "u", "ü", "u", "ç", "c")

func foldWord(s string) string { return foldAccents.Replace(strings.ToLower(s)) }

var (
	adjByFold  = fold(runAdjectives[:])
	nounByFold = fold(runNouns[:])
)

func fold(words []string) map[string]string {
	m := make(map[string]string, len(words))
	for _, w := range words {
		m[foldWord(w)] = w
	}
	return m
}

// CanonicalRunName reconhece um nome de execução digitado sem acento ou com outra caixa
// (agil_falcao, VELOZ_RAPOSA) e devolve a forma gravada (Ágil_Falcão). Também aceita o sufixo numérico
// do RunNameFallback. ok é falso quando o texto não tem a forma de um nome de execução.
func CanonicalRunName(s string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(s), "_")
	if len(parts) != 2 && len(parts) != 3 {
		return "", false
	}
	adj, ok1 := adjByFold[foldWord(parts[0])]
	noun, ok2 := nounByFold[foldWord(parts[1])]
	if !ok1 || !ok2 {
		return "", false
	}
	name := adj + "_" + noun
	if len(parts) == 3 {
		if len(parts[2]) != 3 || strings.Trim(parts[2], "0123456789") != "" {
			return "", false
		}
		name += "_" + parts[2]
	}
	return name, true
}
