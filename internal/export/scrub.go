package export

import (
	"regexp"
	"strings"

	"github.com/renatobardi/hanko/internal/adapter"
)

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
		if sensitiveKey.MatchString(k) {
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
