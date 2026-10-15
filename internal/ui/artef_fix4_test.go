package ui

import (
	"net/http"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Correção da revisão do ADR-0028: o markdown de um artefato não carrega imagem externa (pixel de rastreio).
func TestArtifactMarkdownDoesNotLoadRemoteImages(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	id := artifactIDOf(t, e.f, "slide-01-tese.md", 2)
	aid, err := domain.ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	e.f.ArtifactData[aid] = []byte("# Relatório\n\n![pixel](https://track.example/p.gif?u=42)\n\nTexto **normal**.\n")
	rec := e.get(t, "/c/hanko/artefatos/"+id)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `href="https://track.example/p.gif?u=42"`, `rel="noopener noreferrer nofollow"`, ">pixel</a>", "<strong>normal</strong>")
	wantNotContains(t, rec, `src="https://track.example`)
}
