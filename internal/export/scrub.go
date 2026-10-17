package export

import (
	"regexp"
	"strings"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/secretref"
)

// isSecretRef: um valor que é só uma referência ${secret:NOME} é um nome, não um valor secreto; viaja e aparece em
// claro (ADR-0050). O valor do segredo nunca está no adapter_config.
func isSecretRef(v any) bool {
	s, ok := v.(string)
	return ok && secretref.IsRef(s)
}

// sensitiveKey reconhece nomes de chave que guardam credenciais.
var sensitiveKey = regexp.MustCompile(`(?i)(token|secret|passw(or)?d|passwd|pwd|api[-_]?key|apikey|credential|authorization|bearer|cookie|private[-_]?key|access[-_]?key|session[-_]?id|\bauth\b|^key$|[-_]key$)`)

// sensitiveFlag reconhece flags de linha de comando que carregam credencial (--api-key, --token...).
var sensitiveFlag = regexp.MustCompile(`(?i)^-{1,2}[a-z0-9_-]*(token|secret|passw(or)?d|passwd|api[-_]?key|apikey|credential|authorization|bearer|cookie|private[-_]?key|access[-_]?key)[a-z0-9_-]*(=.*)?$`)

// ScrubConfig devolve uma cópia do adapter_config sem chaves sensíveis nem valores que pareçam
// credenciais (chaves de API, JWT, Bearer, senha em URL). O que é removido some por inteiro: manter um
// "[REDACTED]" faria o time importado falhar mais tarde, longe da causa. A entrada não é alterada.
func ScrubConfig(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := scrubMap(in)
	if len(out) == 0 {
		return nil
	}
	return out
}

func scrubMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if sensitiveKey.MatchString(k) && !isSecretRef(v) {
			continue
		}
		if sv, ok := scrubValue(v); ok {
			out[k] = sv
		}
	}
	return out
}

func scrubValue(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		if adapter.Redact(x) != x {
			return nil, false
		}
		return x, true
	case map[string]any:
		return scrubMap(x), true
	case []any:
		out := make([]any, 0, len(x))
		for i := 0; i < len(x); i++ {
			s, isStr := x[i].(string)
			if isStr && sensitiveFlag.MatchString(s) {
				if !strings.Contains(s, "=") {
					i++ // "--token valor": o valor vem no elemento seguinte
				}
				continue
			}
			if sv, ok := scrubValue(x[i]); ok {
				out = append(out, sv)
			}
		}
		return out, true
	}
	return v, true
}

// MaskedValue substitui, na saída para humanos, o valor de um segredo do adapter_config.
const MaskedValue = "***"

// MaskConfig devolve uma cópia do adapter_config para exibição (API, CLI): os nomes ficam, os valores
// sensíveis viram MaskedValue. Todo valor de "env" é mascarado (variáveis de ambiente são, por natureza,
// o lugar das credenciais do agente); fora de "env" são mascarados os valores de chaves com cara de
// credencial, textos que a redação de segredos reconhece e o valor que segue uma flag como --api-key.
// A diferença para ScrubConfig é que o nome da variável continua visível. A entrada não é alterada.
func MaskConfig(in map[string]any) map[string]any {
	if len(in) == 0 {
		return in
	}
	return maskMap(in, false)
}

func maskMap(in map[string]any, allValues bool) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch {
		case isSecretRef(v):
			out[k] = v
		case allValues || sensitiveKey.MatchString(k):
			out[k] = MaskedValue
		case k == "env":
			if m, ok := v.(map[string]any); ok {
				out[k] = maskMap(m, true)
			} else {
				out[k] = MaskedValue
			}
		default:
			out[k] = maskValue(v)
		}
	}
	return out
}

func maskValue(v any) any {
	switch x := v.(type) {
	case string:
		if adapter.Redact(x) != x {
			return MaskedValue
		}
		return x
	case map[string]any:
		return maskMap(x, false)
	case []any:
		out := make([]any, len(x))
		for i := 0; i < len(x); i++ {
			s, isStr := x[i].(string)
			if isStr && sensitiveFlag.MatchString(s) {
				if before, _, ok := strings.Cut(s, "="); ok {
					out[i] = before + "=" + MaskedValue
					continue
				}
				out[i] = s
				if i+1 < len(x) {
					i++
					out[i] = MaskedValue
				}
				continue
			}
			out[i] = maskValue(x[i])
		}
		return out
	}
	return v
}
