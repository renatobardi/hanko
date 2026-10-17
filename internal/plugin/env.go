package plugin

import (
	"path/filepath"
	"sort"
	"strings"
)

// envNames é a allowlist mínima do ambiente de um plugin: nada de HANKO_*, de credenciais de provedores de
// modelo nem de variáveis de nuvem. HOME e TMPDIR apontam para o diretório de trabalho do plugin.
var envNames = []string{"PATH", "LANG", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR"}

// proxyNames só passam para plugins com a capacidade http.outbound (a rede é declarada ao operador).
var proxyNames = []string{"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy"}

// Env monta o ambiente do processo de um plugin a partir do ambiente do servidor (KEY=VALUE).
func Env(environ []string, workDir string, outbound bool) []string {
	allow := map[string]bool{}
	for _, n := range envNames {
		allow[n] = true
	}
	if outbound {
		for _, n := range proxyNames {
			allow[n] = true
		}
	}
	m := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || strings.HasPrefix(k, "HANKO_") {
			continue
		}
		if allow[k] || strings.HasPrefix(k, "LC_") {
			m[k] = v
		}
	}
	if _, ok := m["PATH"]; !ok {
		m["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	if workDir != "" {
		m["HOME"] = workDir
		m["TMPDIR"] = filepath.Join(workDir, "tmp")
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
