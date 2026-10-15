package mcp

import (
	"bytes"
	"encoding/base64"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestRequestBodyLimit(t *testing.T) {
	const def = 4 << 20
	if got := requestBodyLimit(0); got != def {
		t.Errorf("sem limite de artefato: %d, quer o padrão %d", got, int64(def))
	}
	if got := requestBodyLimit(1 << 20); got != def {
		t.Errorf("artefato pequeno não reduz o padrão: %d", got)
	}
	// 16 MiB em base64 (x4/3) cabem com folga
	if got, need := requestBodyLimit(16<<20), int64(16<<20)*4/3; got <= need {
		t.Errorf("limite %d não comporta %d de base64", got, need)
	}
}

// Um artefato do tamanho configurado (maior que os ~3 MiB que o padrão do SDK deixava passar) chega à tool.
func TestArtifactPutLargeBody(t *testing.T) {
	f := newFake()
	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	e := &env{f: f, tokens: tokens, runID: domain.NewID()}
	tok, err := tokens.Issue(f.company.ID, f.personas[1].ID, e.runID, f.tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	e.tok = tok
	started := time.Now()
	f.runs[e.runID] = service.RunView{Run: domain.Run{ID: e.runID, Status: domain.RunRunning, Trigger: domain.TriggerAssign, Agent: "fake", CreatedAt: started, StartedAt: &started}}
	e.srv = httptest.NewServer(newHandler(f, Deps{Tokens: tokens, Version: "test", ArtifactMaxBytes: 8 << 20}))
	t.Cleanup(e.srv.Close)
	cs, err := e.connect(t, tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	big := bytes.Repeat([]byte{0x89}, 6<<20) // 8 MiB em base64: acima dos 4 MiB do padrão do SDK
	res := call(t, cs, "hanko_artifact_put", map[string]any{"name": "grande.bin", "content_base64": base64.StdEncoding.EncodeToString(big)})
	if res.IsError {
		t.Fatal(text(res))
	}
	if len(f.puts) != 1 || len(f.puts[0].Content) != len(big) {
		t.Fatalf("o conteúdo não chegou inteiro: %d puts", len(f.puts))
	}
}
