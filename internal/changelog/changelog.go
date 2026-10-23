// Package changelog lê o CHANGELOG.md (formato Keep a Changelog) embutido no binário e escolhe as novidades da versão
// instalada. Não faz rede: o texto é o da versão que está rodando (ADR-0061).
package changelog

import (
	"regexp"
	"strings"
)

// UnreleasedVersion é a versão do bloco "[Unreleased]".
const UnreleasedVersion = "Unreleased"

// Release é um bloco "## [versão] — data" do CHANGELOG.
type Release struct {
	// Version é a versão sem o "v" (ou "Unreleased").
	Version string `json:"version"`
	// Date é a data do título ("" quando não há).
	Date string `json:"date,omitempty"`
	// Note é o resto do título depois da data (ex.: "marco interno, sem tag").
	Note string `json:"note,omitempty"`
	// Body é o markdown do bloco, sem o título.
	Body string `json:"body"`
}

var headRe = regexp.MustCompile(`^##\s+\[([^\]]+)\]\s*(?:[—–-]\s*(\d{4}-\d{2}-\d{2})\s*(?:\((.*)\))?)?\s*$`)

// Parse separa o CHANGELOG em versões, da mais nova (a primeira do arquivo) para a mais antiga.
func Parse(md string) []Release {
	var out []Release
	var cur *Release
	var body []string
	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
			out = append(out, *cur)
		}
		cur, body = nil, nil
	}
	for _, ln := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(ln, "## ") {
			flush()
			if m := headRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
				cur = &Release{Version: strings.TrimPrefix(m[1], "v"), Date: m[2], Note: m[3]}
			}
			continue
		}
		if cur != nil {
			body = append(body, ln)
		}
	}
	flush()
	return out
}

// normalize reduz o que o binário diz de si ("v0.2.0", "0.2.0-5-gabc123", "dev") à versão que o CHANGELOG pode ter.
func normalize(v string) []string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	cands := []string{v}
	if i := strings.IndexByte(v, '-'); i > 0 {
		cands = append(cands, v[:i])
	}
	return cands
}

// For escolhe o bloco da versão instalada. Versão desconhecida, de desenvolvimento ("dev") ou ainda sem bloco próprio
// (compilada depois do último release) usa o [Unreleased]; recém-cortada a versão ele fica vazio e então vale o bloco
// mais novo com conteúdo. ok é false só quando o arquivo não tem nada utilizável.
func For(rels []Release, installed string) (Release, bool) {
	for _, c := range normalize(installed) {
		for _, r := range rels {
			if strings.EqualFold(r.Version, c) && c != "" {
				return r, true
			}
		}
	}
	for _, r := range rels {
		if r.Version == UnreleasedVersion && strings.TrimSpace(r.Body) != "" {
			return r, true
		}
	}
	for _, r := range rels {
		if r.Version != UnreleasedVersion {
			return r, true
		}
	}
	if len(rels) > 0 {
		return rels[0], true
	}
	return Release{}, false
}

// Key é o que a pessoa dispensa: a versão instalada normalizada (o que muda quando ela atualiza).
func Key(installed string) string {
	c := normalize(installed)
	if len(c) == 0 || c[0] == "" {
		return "dev"
	}
	return c[0]
}
