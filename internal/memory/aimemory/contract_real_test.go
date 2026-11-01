package aimemory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/memory"
)

// TestContractRealServer exercita o provider contra um ai-memory real (ADR-0015). Só roda com
// HANKO_AI_MEMORY_CONTRACT_URL (e o token em HANKO_AI_MEMORY_CONTRACT_TOKEN). Grava e apaga páginas no workspace
// "contrato": aponte para um servidor de teste, nunca para o de produção.
func TestContractRealServer(t *testing.T) {
	base := os.Getenv("HANKO_AI_MEMORY_CONTRACT_URL")
	if base == "" {
		t.Skip("HANKO_AI_MEMORY_CONTRACT_URL vazio")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := New(Options{URL: base, Token: os.Getenv("HANKO_AI_MEMORY_CONTRACT_TOKEN"), ReadPageTool: "memory_read_page", Timeout: 20 * time.Second})
	s := memory.Scope{Workspace: "contrato", Project: "default", Cwd: "/tmp"}
	if err := p.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if sc, ok := p.(memory.SearchCapable); !ok || !sc.SearchSupported(ctx) {
		t.Fatal("memory_query ausente")
	}
	tag := fmt.Sprintf("contratohanko%d", time.Now().UnixNano())
	path := "hanko/runs/" + tag + ".md"
	if err := p.Record(ctx, s, memory.Fact{Path: path, Title: "Contrato " + tag, Body: "corpo " + tag, Tags: []string{"contrato"}}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := p.Record(ctx, s, memory.Fact{Path: path, Title: "Contrato " + tag, Body: "corpo " + tag}); err != nil {
		t.Fatalf("record idempotente: %v", err)
	}
	b, err := p.Briefing(ctx, s)
	t.Logf("briefing (%d bytes): %v", len(b), err)
	if err != nil {
		t.Fatalf("briefing: %v", err)
	}
	if _, err := p.AcceptHandoff(ctx, s); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	hits, err := p.Search(ctx, []memory.Scope{s}, tag, 5)
	t.Logf("hits um escopo: %+v", hits)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search: %v (%d hits)", err, len(hits))
	}
	hits, err = p.Search(ctx, []memory.Scope{s, {Workspace: "contrato", Project: "outro"}}, tag, 5)
	t.Logf("hits dois escopos: %+v", hits)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search multi: %v (%d hits)", err, len(hits))
	}
	longBody := tag + strings.Repeat("\nlinha do corpo longo", 1000)
	if err := p.Record(ctx, s, memory.Fact{Path: path, Title: "Contrato " + tag, Body: longBody}); err != nil {
		t.Fatalf("record longo: %v", err)
	}
	txt, err := p.(memory.PageReader).ReadPage(ctx, s, path)
	if err != nil || strings.TrimSpace(txt) != longBody {
		t.Fatalf("read page: %v (%d bytes de %d)", err, len(txt), len(longBody))
	}
	if err := p.FinalizeSession(ctx, s, "claude-code", "11111111-1111-4111-8111-111111111111"); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := p.(memory.ToolCaller).CallTool(ctx, s, "memory_delete_page", map[string]any{"path": path}); err != nil {
		t.Fatalf("delete page: %v", err)
	}
}
