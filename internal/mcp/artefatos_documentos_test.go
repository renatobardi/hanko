package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Leitura, alteração e restauração de documentos de artefato pela persona (ADR-0064).

type restoreCall struct {
	version   domain.ID
	task, run *domain.ID
	actor     domain.Actor
}

var restores []restoreCall

func (f *fake) GetArtifactView(_ context.Context, _, id domain.ID) (service.ArtifactView, error) {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range st.attach {
		if a.ID == id {
			return a, nil
		}
	}
	return service.ArtifactView{}, domain.ErrNotFound
}

func (f *fake) ArtifactVersions(_ context.Context, _, id domain.ID) ([]domain.Artifact, error) {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	var doc domain.ID
	for _, a := range st.attach {
		if a.ID == id {
			doc = a.DocumentID
		}
	}
	var out []domain.Artifact
	for i := len(st.attach) - 1; i >= 0; i-- {
		if st.attach[i].DocumentID == doc {
			out = append(out, st.attach[i].Artifact)
		}
	}
	return out, nil
}

func (f *fake) ArtifactVersionID(_ context.Context, _, doc domain.ID, version string) (domain.ID, error) {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(st.attach) - 1; i >= 0; i-- {
		a := st.attach[i]
		if a.DocumentID == doc && (version == "" || a.SemVer == version) {
			return a.ID, nil
		}
	}
	return domain.ID{}, domain.ErrNotFound
}

func (f *fake) RestoreArtifact(_ context.Context, a domain.Actor, _, versionID domain.ID, task, run *domain.ID) (service.ArtifactView, error) {
	restores = append(restores, restoreCall{version: versionID, task: task, run: run, actor: a})
	v := service.ArtifactView{Artifact: domain.Artifact{ID: domain.NewID(), DocumentID: versionID, Major: 3, Change: domain.ChangeMajor}, SemVer: "3.0"}
	return v, nil
}

func (e *env) seedDocVersion(f *fake, doc domain.ID, name, semver, content string) service.ArtifactView {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	sv, _ := domain.ParseSemVer(semver)
	v := service.ArtifactView{Artifact: domain.Artifact{ID: domain.NewID(), CompanyID: f.company.ID, DocumentID: doc, Name: name,
		Mime: "text/markdown", SizeBytes: int64(len(content)), Class: domain.ClassArtifact, Author: domain.HumanActor,
		Major: sv.Major, Minor: sv.Minor, Change: domain.ChangeMinor, ChangeReason: "ajuste", CreatedAt: time.Now()},
		SemVer: semver, Tags: []string{"arquitetura"}}
	st.attach = append(st.attach, v)
	st.content[v.ID] = []byte(content)
	return v
}

func TestArtifactDocumentTools(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	doc := domain.NewID()
	e.seedDocVersion(e.f, doc, "plano.md", "1.0", "# v1\n")
	e.seedDocVersion(e.f, doc, "plano.md", "1.1", "# v1.1\n")

	res := call(t, cs, "hanko_artifact_list", map[string]any{})
	if res.IsError || !strings.Contains(text(res), doc.String()) || !strings.Contains(text(res), "1.1") {
		t.Fatalf("lista: %s", text(res))
	}

	var got artifactGetOut
	res = call(t, cs, "hanko_artifact_get", map[string]any{"artifact": doc.String(), "history": true})
	if res.IsError {
		t.Fatal(text(res))
	}
	structured(t, res, &got)
	if got.Content != "# v1.1\n" || got.Artifact.Version != "1.1" || got.Artifact.Document != doc.String() || len(got.History) != 2 {
		t.Fatalf("a mais nova com histórico: %+v", got)
	}
	got = artifactGetOut{}
	res = call(t, cs, "hanko_artifact_get", map[string]any{"artifact": doc.String(), "version": "1.0"})
	structured(t, res, &got)
	if got.Content != "# v1\n" {
		t.Fatalf("versão anterior: %+v", got)
	}
	if r := call(t, cs, "hanko_artifact_get", map[string]any{"artifact": "não-é-id"}); !r.IsError {
		t.Error("id inválido deveria falhar")
	}

	// alterar: o id do documento e a classificação chegam ao serviço
	res = call(t, cs, "hanko_artifact_put", map[string]any{"name": "plano-novo.md", "content": "# v2\n", "artifact": doc.String(),
		"change": "MAJOR", "change_reason": "nova premissa", "tags": []string{"arquitetura"}})
	if res.IsError {
		t.Fatal(text(res))
	}
	in := e.f.puts[len(e.f.puts)-1]
	if in.DocumentID == nil || *in.DocumentID != doc || in.Change == nil || *in.Change != domain.ChangeMajor || in.ChangeReason != "nova premissa" || len(in.Tags) != 1 {
		t.Fatalf("put com artifact: %+v", in)
	}
	if r := call(t, cs, "hanko_artifact_put", map[string]any{"name": "a.md", "content": "x", "change": "patch"}); !r.IsError {
		t.Error("change inválido deveria falhar")
	}

	// restaurar: com token de run, a versão nova fica com a task e a execução do token
	restores = nil
	res = call(t, cs, "hanko_artifact_restore", map[string]any{"artifact": doc.String(), "version": "1.0"})
	if res.IsError || len(restores) != 1 {
		t.Fatalf("restaurar: %s", text(res))
	}
	if r := restores[0]; r.task == nil || *r.task != e.f.tasks[0].ID || r.run == nil || *r.run != e.runID || r.actor.Type != domain.ActorPersona {
		t.Fatalf("escopo da restauração: %+v", r)
	}
	if r := call(t, cs, "hanko_artifact_restore", map[string]any{"artifact": doc.String()}); !r.IsError {
		t.Error("restaurar sem versão deveria falhar")
	}
}
