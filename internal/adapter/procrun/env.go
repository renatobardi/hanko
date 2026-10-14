package procrun

import (
	"sort"
	"strings"
)

// baseEnvNames são as variáveis do processo servidor repassadas ao harness.
var baseEnvNames = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LANG": true, "TERM": true, "TMPDIR": true, "TZ": true,
	"SHELL": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"HTTPS_PROXY": true, "HTTP_PROXY": true, "NO_PROXY": true,
	"https_proxy": true, "http_proxy": true, "no_proxy": true,
}

// baseEnvPrefixes são prefixos repassados (LC_* inclusive).
var baseEnvPrefixes = []string{"LC_", "ANTHROPIC_", "CLAUDE_", "OPENAI_", "CODEX_", "OTEL_", "NODE_", "XDG_", "GIT_", "GH_"}

// HarnessEnv monta o ambiente do harness a partir de uma allowlist do ambiente do
// servidor (environ no formato KEY=VALUE), mais extraAllow (nomes exatos ou prefixos
// terminados em "*", vindos de HANKO_AGENT_ENV_ALLOW) e por fim extra, que sempre vence.
// Variáveis HANKO_* do servidor nunca passam; só as que chegam em extra (spec.Env).
func HarnessEnv(environ []string, extraAllow []string, extra map[string]string) []string {
	allowed := func(k string) bool {
		if strings.HasPrefix(k, "HANKO_") {
			return false
		}
		if baseEnvNames[k] {
			return true
		}
		for _, p := range baseEnvPrefixes {
			if strings.HasPrefix(k, p) {
				return true
			}
		}
		for _, a := range extraAllow {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			if p, ok := strings.CutSuffix(a, "*"); ok {
				if p != "" && strings.HasPrefix(k, p) {
					return true
				}
			} else if k == a {
				return true
			}
		}
		return false
	}
	m := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" && allowed(k) {
			m[k] = v
		}
	}
	for k, v := range extra {
		m[k] = v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}
