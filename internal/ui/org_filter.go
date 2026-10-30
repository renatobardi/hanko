package ui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Filtro do organograma (ADR-0087 itens 7 e 8). A ilha aplica o recorte sobre a árvore que a página já traz e guarda a
// escolha na URL e no cookie hanko_org; o host só lê os dois para abrir o mesmo recorte. Os parâmetros são os mesmos na
// URL e no cookie: tag (ids separados por vírgula), todas=1 (E), semtag=1, arquivadas=1, nivel (1 = topo), prof
// (profundidade máxima) e modo (destacar | podar).

// orgFilterCookie guarda a última escolha do filtro. A ilha o grava (por isso não é HttpOnly); só tem a escolha do
// filtro, nada sensível.
const orgFilterCookie = "hanko_org"

var orgFilterKeys = []string{"tag", "todas", "semtag", "arquivadas", "nivel", "prof", "modo"}

// orgFilter é o filtro no formato que a ilha recebe em data-filter.
type orgFilter struct {
	Tags     []string `json:"tags"`
	All      bool     `json:"all"`
	Untagged bool     `json:"untagged"`
	Archived bool     `json:"archived"`
	Levels   []int    `json:"levels"`
	MaxDepth int      `json:"maxDepth"`
	Mode     string   `json:"mode"` // highlight | prune
}

// orgFilterFrom lê o filtro da URL; sem nenhum parâmetro de filtro, do cookie. Valor malformado é ignorado.
func orgFilterFrom(r *http.Request) orgFilter {
	src := r.URL.Query()
	explicit := false
	for _, k := range orgFilterKeys {
		explicit = explicit || src.Has(k)
	}
	if !explicit {
		src = orgFilterCookieValues(r)
	}
	return parseOrgFilter(src)
}

// orgFilterCookieValues lê o cookie hanko_org. A ilha grava a query com encodeURIComponent; o valor volta decodificado.
func orgFilterCookieValues(r *http.Request) url.Values {
	c, err := r.Cookie(orgFilterCookie)
	if err != nil {
		return url.Values{}
	}
	raw, err := url.QueryUnescape(c.Value)
	if err != nil {
		return url.Values{}
	}
	v, err := url.ParseQuery(raw)
	if err != nil {
		return url.Values{}
	}
	return v
}

func parseOrgFilter(v url.Values) orgFilter {
	f := orgFilter{Tags: []string{}, Levels: []int{}, Mode: string(domain.OrgHighlight), All: v.Get("todas") == "1",
		Untagged: v.Get("semtag") == "1", Archived: v.Get("arquivadas") == "1"}
	for _, part := range strings.Split(v.Get("tag"), ",") {
		if id, err := domain.ParseID(strings.TrimSpace(part)); err == nil {
			f.Tags = append(f.Tags, id.String())
		}
	}
	for _, part := range strings.Split(v.Get("nivel"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n >= 1 && n <= 64 {
			f.Levels = append(f.Levels, n)
		}
	}
	if n, err := strconv.Atoi(v.Get("prof")); err == nil && n >= 0 && n <= 64 {
		f.MaxDepth = n
	}
	if v.Get("modo") == "podar" {
		f.Mode = string(domain.OrgPrune)
	}
	return f
}

func (f orgFilter) json() string {
	b, err := json.Marshal(f)
	if err != nil { // struct de tipos simples: não falha
		return "{}"
	}
	return string(b)
}

// orgTagsJSON é o cadastro para a ilha: todas as tags, com as arquivadas (o filtro as mostra sob "mostrar arquivadas").
func orgTagsJSON(tags []service.PersonaTagView) string {
	type tag struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
		Archived    bool   `json:"archived"`
	}
	out := make([]tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, tag{ID: t.ID.String(), Name: t.Name, Color: string(t.Color), Description: t.Description, Archived: t.Archived()})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func tagIDsOf(tags []domain.PersonaTag) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.ID.String()
	}
	return out
}
