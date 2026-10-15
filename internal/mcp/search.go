package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/service"
)

// marked devolve os segmentos como texto, com as palavras que casaram entre « e ».
func marked(segs []search.Segment) string {
	var b strings.Builder
	for _, s := range segs {
		if s.Match {
			b.WriteString("«" + s.Text + "»")
		} else {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

// search é hanko_search. O escopo é sempre a empresa do token (scope): a persona não busca em outra empresa.
func (t *tools) search(ctx context.Context, _ *sdk.CallToolRequest, in searchIn) (*sdk.CallToolResult, searchOut, error) {
	_, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, searchOut{}, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, searchOut{}, errors.New("query é obrigatório")
	}
	types, err := service.ParseSearchTypes(in.Types)
	if err != nil {
		return nil, searchOut{}, opErr("buscar", err)
	}
	r, err := t.b.Search(ctx, company, in.Query, service.SearchFilter{Types: types, Limit: in.Limit})
	if err != nil {
		return nil, searchOut{}, opErr("buscar", err)
	}
	out := searchOut{Query: r.Query, Total: r.Total, Groups: make([]searchGroupOut, 0, len(r.Groups))}
	var sb strings.Builder
	if len(r.Groups) == 0 {
		sb.WriteString("Nada encontrado.")
	}
	for _, g := range r.Groups {
		og := searchGroupOut{Type: string(g.Type), More: g.More, Unavailable: g.Unavailable, Hits: make([]searchHitOut, 0, len(g.Hits))}
		fmt.Fprintf(&sb, "\n## %s (%d)", g.Type, len(g.Hits))
		if g.Unavailable {
			sb.WriteString(" — indisponível agora")
		}
		for _, h := range g.Hits {
			oh := searchHitOut{Type: string(h.Type), Ref: h.Ref, Title: marked(h.Title), Snippet: marked(h.Snippet), TaskKey: h.TaskKey,
				TaskTitle: h.TaskTitle, Status: h.Status, Author: h.Author, Exact: h.Exact}
			og.Hits = append(og.Hits, oh)
			fmt.Fprintf(&sb, "\n- %s %s", h.Ref, oh.Title)
			if oh.Snippet != "" {
				sb.WriteString("\n  " + strings.ReplaceAll(oh.Snippet, "\n", " "))
			}
		}
		if g.More {
			sb.WriteString("\n  (há mais; refine a busca)")
		}
		out.Groups = append(out.Groups, og)
	}
	return textResult("%s", strings.TrimSpace(sb.String())), out, nil
}
