package domain

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"math"
	"math/big"
	"slices"
	"strings"
)

// passphraseWordsFile é a lista embutida da frase-senha (ADR-0068 §7.1): uma palavra minúscula por linha.
//
//go:embed passphrase_words.txt
var passphraseWordsFile string

var passphraseWords = strings.Fields(passphraseWordsFile)

// GenClass é uma classe de caractere do gerador.
type GenClass string

const (
	GenLower   GenClass = "lower"
	GenUpper   GenClass = "upper"
	GenDigits  GenClass = "digits"
	GenSymbols GenClass = "symbols"
)

// genAlphabets são os caracteres de cada classe. Os símbolos evitam aspas, barra e espaço, que quebram comandos e URLs.
var genAlphabets = map[GenClass]string{
	GenLower:   "abcdefghijklmnopqrstuvwxyz",
	GenUpper:   "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	GenDigits:  "0123456789",
	GenSymbols: "!#$%&*+-=?@^_~",
}

const (
	GenMinLength     = 8
	GenMaxLength     = 128
	GenDefaultLength = 32
	GenMinWords      = 3
	GenMaxWords      = 12
	GenDefaultBytes  = 32
	WeakEntropyBits  = 60
	passphraseSep    = "-"
)

// GenOptions descreve o valor pedido ao gerador. Zero em tudo = 32 bytes aleatórios em base64url (ADR-0050 §6).
type GenOptions struct {
	Length          int
	Classes         []GenClass
	PassphraseWords int
}

// Generated é um valor do gerador e a entropia estimada em bits. Não é gravado.
type Generated struct {
	Value       string
	EntropyBits int
}

// IsZero diz se nenhuma opção foi pedida (o padrão de sempre).
func (o GenOptions) IsZero() bool {
	return o.Length == 0 && len(o.Classes) == 0 && o.PassphraseWords == 0
}

// Valid confere os limites e a combinação. Frase-senha não aceita tamanho nem classe: são modos diferentes.
func (o GenOptions) Valid() error {
	if o.PassphraseWords != 0 {
		if o.Length != 0 || len(o.Classes) != 0 {
			return Invalid("generator", "frase-senha não combina com tamanho nem classes")
		}
		if o.PassphraseWords < GenMinWords || o.PassphraseWords > GenMaxWords {
			return Invalid("passphrase_words", "frase-senha de 3 a 12 palavras")
		}
		return nil
	}
	if o.Length != 0 && (o.Length < GenMinLength || o.Length > GenMaxLength) {
		return Invalid("length", "tamanho de 8 a 128 caracteres")
	}
	for _, c := range o.Classes {
		if _, ok := genAlphabets[c]; !ok {
			return Invalid("classes", "classes válidas: lower, upper, digits, symbols")
		}
	}
	return nil
}

// Generate devolve um valor conforme o. Usa crypto/rand; com classes, cada classe escolhida aparece ao menos uma vez.
func Generate(o GenOptions) (Generated, error) {
	if err := o.Valid(); err != nil {
		return Generated{}, err
	}
	switch {
	case o.PassphraseWords != 0:
		return generatePassphrase(o.PassphraseWords)
	case o.Length == 0 && len(o.Classes) == 0:
		b := make([]byte, GenDefaultBytes)
		if _, err := rand.Read(b); err != nil {
			return Generated{}, err
		}
		return Generated{Value: base64.RawURLEncoding.EncodeToString(b), EntropyBits: GenDefaultBytes * 8}, nil
	default:
		return generateChars(o)
	}
}

func generatePassphrase(n int) (Generated, error) {
	// o limite confere antes da alocação: n vem do usuário (go/uncontrolled-allocation-size)
	if n < GenMinWords || n > GenMaxWords {
		return Generated{}, Invalid("passphrase_words", "frase-senha de 3 a 12 palavras")
	}
	words := make([]string, n)
	for i := range words {
		k, err := pickIndex(len(passphraseWords))
		if err != nil {
			return Generated{}, err
		}
		words[i] = passphraseWords[k]
	}
	bits := float64(n) * math.Log2(float64(len(passphraseWords)))
	return Generated{Value: strings.Join(words, passphraseSep), EntropyBits: int(bits)}, nil
}

func generateChars(o GenOptions) (Generated, error) {
	classes := o.Classes
	if len(classes) == 0 {
		classes = []GenClass{GenLower, GenUpper, GenDigits}
	}
	classes = uniqueClasses(classes)
	length := o.Length
	if length == 0 {
		length = GenDefaultLength
	}
	// o limite confere antes da alocação: length vem do usuário (go/uncontrolled-allocation-size)
	if length < GenMinLength || length > GenMaxLength {
		return Generated{}, Invalid("length", "tamanho de 8 a 128 caracteres")
	}
	var pool string
	for _, c := range classes {
		pool += genAlphabets[c]
	}
	out := make([]byte, 0, length)
	// uma de cada classe primeiro: a senha sempre tem o que foi pedido
	for _, c := range classes {
		ch, err := pickChar(genAlphabets[c])
		if err != nil {
			return Generated{}, err
		}
		out = append(out, ch)
	}
	for len(out) < length {
		ch, err := pickChar(pool)
		if err != nil {
			return Generated{}, err
		}
		out = append(out, ch)
	}
	if err := shuffle(out); err != nil {
		return Generated{}, err
	}
	bits := float64(length) * math.Log2(float64(len(pool)))
	return Generated{Value: string(out), EntropyBits: int(bits)}, nil
}

// EstimateEntropy estima a entropia de um valor pelas classes de caractere que ele usa (tamanho × log2 do conjunto).
// É uma estimativa para o relatório de saúde, não uma medida do segredo.
func EstimateEntropy(value string) int {
	var lower, upper, digit, other bool
	n := 0
	for _, r := range value {
		n++
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			other = true
		}
	}
	pool := 0
	for _, on := range []struct {
		ok   bool
		size int
	}{{lower, 26}, {upper, 26}, {digit, 10}, {other, 33}} {
		if on.ok {
			pool += on.size
		}
	}
	if n == 0 || pool == 0 {
		return 0
	}
	return int(float64(n) * math.Log2(float64(pool)))
}

// IsPasswordField diz se o campo é de senha pelo nome (`password` ou `senha`). Só esses campos entram no achado "fraco".
func IsPasswordField(name string) bool {
	n := strings.ToLower(name)
	return n == "password" || n == "senha"
}

func uniqueClasses(in []GenClass) []GenClass {
	var out []GenClass
	for _, c := range in {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// pickIndex sorteia um índice em [0, n) com crypto/rand, sem viés de módulo.
func pickIndex(n int) (int, error) {
	i, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(i.Int64()), nil
}

func pickChar(alphabet string) (byte, error) {
	k, err := pickIndex(len(alphabet))
	if err != nil {
		return 0, err
	}
	return alphabet[k], nil
}

// shuffle embaralha b (Fisher-Yates com crypto/rand), para que as classes obrigatórias não fiquem sempre no início.
func shuffle(b []byte) error {
	for i := len(b) - 1; i > 0; i-- {
		j, err := pickIndex(i + 1)
		if err != nil {
			return err
		}
		b[i], b[j] = b[j], b[i]
	}
	return nil
}
