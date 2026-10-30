// Package secretscan acha segredo colado num texto antes de ele sair do Hanko (ADR-0079 item 6). A lista de padrões é a
// de SECRET_PATTERNS em jev.py@845764f do oute-agent, na mesma ordem, com os ids em inglês que o ADR fixa. Só classes
// simples e repetições limitadas, para a busca não ficar lenta com texto grande.
package secretscan

import "regexp"

type pattern struct {
	kind string
	re   *regexp.Regexp
}

var patterns = []pattern{
	{"private_key", regexp.MustCompile(`-----BEGIN [A-Z ]{0,24}PRIVATE KEY-----`)},
	{"api_key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{"github_token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})`)},
	{"aws_key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"slack_token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"google_key", regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{"bearer", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{20,}`)},
	{"url_with_password", regexp.MustCompile(`\b[a-z][a-z0-9+.-]{1,15}://[^\s/:@]{1,64}:[^\s/@]{1,64}@`)},
	{"assignment", regexp.MustCompile(`(?i)\b[\w.-]{0,32}(?:api[_-]?key|secret|token|passw(?:or)?d|senha)[\w.-]{0,16}\s{0,3}[:=]\s{0,3}["']?[^\s"']{8,}`)},
}

// Kind devolve o tipo do primeiro padrão que o texto contém (`api_key`, `bearer`…), ou "" se nenhum. Nunca devolve o
// trecho: o tipo é o único dado que pode ir ao ledger e ao evento.
func Kind(text string) string {
	for _, p := range patterns {
		if p.re.MatchString(text) {
			return p.kind
		}
	}
	return ""
}
