package aimemory

import (
	"context"
	"strings"

	"github.com/renatobardi/hanko/internal/memory"
)

// Capacidades da consolidação governada (ADR-0030): repetir uma chamada aprovada e ler uma página.

var (
	_ memory.ToolCaller = (*provider)(nil)
	_ memory.PageReader = (*provider)(nil)
)

// CallTool repete uma tool no ai-memory com as credenciais do Hanko. O escopo (workspace/projeto) sobrepõe
// o valor que os argumentos trazem: a aplicação de uma proposta nunca sai do workspace dela. Só as chaves que a
// chamada original já tinha são sobrepostas (como o proxy faz); uma tool que não as declara não ganha
// argumentos que ela não conhece, e o escopo segue nos cabeçalhos X-Memory-* da requisição.
func (p *provider) CallTool(ctx context.Context, s memory.Scope, tool string, args map[string]any) (string, error) {
	merged := make(map[string]any, len(args))
	for k, v := range args {
		merged[k] = v
	}
	for k, v := range scopeArgs(s) {
		if _, has := merged[k]; has {
			merged[k] = v
		}
	}
	proj := s.Project
	if proj == "" {
		proj = defaultProject
	}
	return p.callTool(withScopeHeaders(ctx, s.Workspace, proj), tool, merged)
}

type scopeHeadersKey struct{}

type scopeHeaders struct{ workspace, project string }

func withScopeHeaders(ctx context.Context, workspace, project string) context.Context {
	return context.WithValue(ctx, scopeHeadersKey{}, scopeHeaders{workspace: workspace, project: project})
}

// ReadPage lê o conteúdo de uma página pela tool de leitura configurada (Options.ReadPageTool). Sem ela,
// devolve memory.ErrUnsupported: o diff da proposta mostra só o conteúdo proposto.
func (p *provider) ReadPage(ctx context.Context, s memory.Scope, path string) (string, error) {
	if strings.TrimSpace(p.readTool) == "" {
		return "", memory.ErrUnsupported
	}
	args := scopeArgs(s)
	args["path"] = path
	return p.callTool(ctx, p.readTool, args)
}
