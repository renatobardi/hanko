package adapter

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Mask substitui qualquer segredo detectado.
const Mask = "[REDACTED]"

// minSecretLen evita mascarar o texto inteiro por causa de um "segredo" trivial.
const minSecretLen = 4

type pattern struct {
	re   *regexp.Regexp
	repl string
}

var patterns = []pattern{
	{regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Mask},
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]+`), Mask},
	{regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}`), Mask},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), Mask},
	{regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}`), Mask},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), Mask},
	{regexp.MustCompile(`\bxox[bp]-[A-Za-z0-9-]{10,}`), Mask},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), Mask},
	{regexp.MustCompile(`(postgres(?:ql)?://[^:/@\s"'\\]+:)[^@\s"'\\]+@`), "${1}" + Mask + "@"},
}

// Redact mascara os valores exatos de secrets (ignorando vazios e curtos demais)
// e os padrões conhecidos de credenciais (Bearer, JWT, chaves de API, URL de banco).
func Redact(s string, secrets ...string) string {
	if s == "" {
		return s
	}
	for _, sec := range normalizeSecrets(secrets) {
		s = strings.ReplaceAll(s, sec, Mask)
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// normalizeSecrets remove vazios/curtos/duplicados e ordena do maior para o menor,
// para que um segredo contido em outro não deixe sobras.
func normalizeSecrets(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if len(s) < minSecretLen {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// RedactJSON aplica Redact ao JSON bruto mantendo-o válido.
func RedactJSON(raw json.RawMessage, secrets ...string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	out := Redact(string(raw), secrets...)
	if out == string(raw) {
		return raw
	}
	if json.Valid([]byte(out)) {
		return json.RawMessage(out)
	}
	b, _ := json.Marshal(map[string]any{"redacted": true, "raw": out})
	return b
}

// RedactEvent devolve o evento com Text e Payload mascarados.
func RedactEvent(e Event, secrets ...string) Event {
	e.Text = Redact(e.Text, secrets...)
	e.Payload = RedactJSON(e.Payload, secrets...)
	return e
}

// RedactResult mascara os textos livres do resultado (resumo e erro).
func RedactResult(r Result, secrets ...string) Result {
	r.Summary = Redact(r.Summary, secrets...)
	r.Error = Redact(r.Error, secrets...)
	return r
}

// RedactingSink mascara cada evento antes de repassá-lo ao sink.
func RedactingSink(next Sink, secrets ...string) Sink {
	secrets = normalizeSecrets(secrets)
	return SinkFunc(func(ctx context.Context, e Event) error {
		return next.Emit(ctx, RedactEvent(e, secrets...))
	})
}

var sensitiveEnv = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|CREDENTIAL)`)

// SpecSecrets lista os valores sensíveis de uma execução: variáveis de spec.Env
// com nome de credencial, HANKO_RUN_TOKEN e todos os headers das configurações MCP
// (inclusive o valor sem o prefixo "Bearer ").
func SpecSecrets(spec Spec) []string {
	out := append([]string(nil), spec.Secrets...)
	for k, v := range spec.Env {
		if k == "HANKO_RUN_TOKEN" || sensitiveEnv.MatchString(k) {
			out = append(out, v)
		}
	}
	if len(spec.MCPConfig) > 0 {
		var cfg struct {
			Servers map[string]struct {
				Headers map[string]string `json:"headers"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(spec.MCPConfig, &cfg) == nil {
			for _, s := range cfg.Servers {
				// servidores stdio da empresa (ADR-0029): todo valor de env é tratado como segredo
				for _, v := range s.Env {
					out = append(out, v)
				}
				for _, v := range s.Headers {
					out = append(out, v)
					if rest, ok := strings.CutPrefix(v, "Bearer "); ok {
						out = append(out, rest)
					}
				}
			}
		}
	}
	return out
}
