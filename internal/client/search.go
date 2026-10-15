package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/api"
)

// SearchQuery são os parâmetros da busca única da empresa.
type SearchQuery struct {
	Q     string
	Types []string // vazio = todos
	Limit int      // por tipo; 0 = padrão do servidor
}

// Search busca q na empresa (GET /companies/{c}/search).
func (c *Client) Search(ctx context.Context, company string, q SearchQuery) (api.SearchResult, error) {
	v := url.Values{"q": {q.Q}}
	if len(q.Types) > 0 {
		v.Set("types", strings.Join(q.Types, ","))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	var out api.SearchResult
	err := c.do(ctx, http.MethodGet, cp(company)+"/search", v, nil, &out)
	return out, err
}
