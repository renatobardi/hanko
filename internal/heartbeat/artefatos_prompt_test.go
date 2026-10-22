package heartbeat

import (
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestArtifactsSection(t *testing.T) {
	if got := artifactsSection(service.RunContext{}); got != "" {
		t.Fatalf("sem documentos deve ser vazio: %q", got)
	}
	doc := domain.NewID()
	at := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	rc := service.RunContext{Artifacts: []service.ArtifactBrief{
		{DocumentID: doc, Name: "plano.md", SemVer: "2.1", Mime: "text/markdown", TaskKey: "HNK-7", Tags: []string{"arquitetura"}, UpdatedAt: at, InTask: true},
	}}
	got := artifactsSection(rc)
	for _, want := range []string{"## Artefatos", "`plano.md` 2.1", "id " + doc.String(), "desta task", "#arquitetura", "artifact=<id>"} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
	// a seção e a regra de alterar sem criar documento novo chegam aos três prompts
	system, prompt := BuildPrompt(rc, "", "")
	if !strings.Contains(prompt, "## Artefatos") || !strings.Contains(system, "artifact=<id do documento>") || !strings.Contains(system, "Nunca crie outro documento") {
		t.Error("BuildPrompt sem a seção ou a regra de artefatos")
	}
}
