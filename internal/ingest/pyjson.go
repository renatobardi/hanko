// Package ingest decodifica o OTLP que os agentes externos mandam (ADR-0076) nas linhas das tabelas de telemetria bruta.
//
// As regras são as do agent-studio (`agent_studio/otlp.py@845764f` do oute-agent), portadas como estão: a chave de
// dedupe de cada linha se calcula sobre o registro decodificado, e a do histórico migrado foi calculada por aquele
// código. Por isso este pacote reproduz o JSON canônico do Python (`json.dumps` com chaves ordenadas, sem espaços e sem
// escapar o que não é ASCII) byte a byte: um replay de uma janela já migrada não pode gerar uma chave diferente.
package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrBadPayload é um corpo que não é OTLP válido: erro permanente (400), o collector não reenvia.
var ErrBadPayload = errors.New("corpo OTLP inválido")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadPayload, fmt.Sprintf(format, a...))
}

// Os valores genéricos são os do `json.loads` do Python: nil, bool, string, *big.Int (int), float64, []any e
// map[string]any. Um número com ponto ou expoente é float; os outros são int, de qualquer tamanho.

// DecodeJSON lê um valor JSON no modelo acima.
func DecodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, bad("%v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, bad("dados depois do valor JSON")
	}
	return normalize(v)
}

func normalize(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		s := x.String()
		if strings.ContainsAny(s, ".eE") {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil && !errors.Is(err, strconv.ErrRange) {
				return nil, bad("número inválido %q", s)
			}
			return f, nil
		}
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, bad("número inválido %q", s)
		}
		return n, nil
	case []any:
		for i := range x {
			n, err := normalize(x[i])
			if err != nil {
				return nil, err
			}
			x[i] = n
		}
	case map[string]any:
		for k := range x {
			n, err := normalize(x[k])
			if err != nil {
				return nil, err
			}
			x[k] = n
		}
	}
	return v, nil
}

// Canon é o `json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)` do Python.
func Canon(v any) string {
	var b strings.Builder
	writeCanon(&b, v)
	return b.String()
}

func writeCanon(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		writeString(b, x)
	case *big.Int:
		b.WriteString(x.String())
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case float64:
		b.WriteString(pyFloat(x))
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanon(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, k)
			b.WriteByte(':')
			writeCanon(b, x[k])
		}
		b.WriteByte('}')
	default:
		writeString(b, fmt.Sprint(x)) // o `default=str` do Python
	}
}

func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		case r == utf8.RuneError:
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// pyFloat é o `repr` do float do Python: o menor número de dígitos que volta ao mesmo valor, em notação decimal quando o
// ponto cai entre 1e-4 e 1e16 e em notação científica (`1e+16`, `1e-05`) fora disso.
func pyFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	mant, expo, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	exp10, _ := strconv.Atoi(expo)
	decpt := exp10 + 1
	if decpt > 16 || decpt < -3 {
		out := digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		es := strconv.Itoa(exp10)
		if exp10 >= 0 {
			es = "+" + es
		}
		if len(strings.TrimLeft(es, "+-")) < 2 {
			es = es[:1] + "0" + es[1:]
		}
		return sign + out + "e" + es
	}
	switch {
	case decpt <= 0:
		return sign + "0." + strings.Repeat("0", -decpt) + digits
	case decpt >= len(digits):
		return sign + digits + strings.Repeat("0", decpt-len(digits)) + ".0"
	}
	return sign + digits[:decpt] + "." + digits[decpt:]
}

// contentHash é o `content_hash` de otlp.py: o sha256 do JSON canônico.
func contentHash(v any) string {
	sum := sha256.Sum256([]byte(Canon(v)))
	return hex.EncodeToString(sum[:])
}
