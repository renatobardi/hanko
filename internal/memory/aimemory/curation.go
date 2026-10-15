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
// qualquer valor que os argumentos tragam: a aplicação de uma proposta nunca sai do workspace dela.
func (p *provider) CallTool(ctx context.Context, s memory.Scope, tool string, args map[string]any) (string, error) {
	merged := make(map[string]any, len(args)+2)
	for k, v := range args {
		merged[k] = v
	}
	for k, v := range scopeArgs(s) {
		merged[k] = v
	}
	return p.callTool(ctx, tool, merged)
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
