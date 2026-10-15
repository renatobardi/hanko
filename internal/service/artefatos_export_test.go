package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// Export e importação de artefatos (ADR-0028).

// artifactFixture monta uma empresa com projeto, persona, task, uma run encerrada e três versões de artefatos.
func artifactFixture(t *testing.T) (*env, domain.Run) {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	pr := e.project(t, "Lançamento", nil)
	p := e.persona(t, "Ana", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "relatório", PersonaID: &p.ID, ProjectID: &pr.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tk, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	done, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{}})
	if err != nil {
		t.Fatal(err)
	}
	e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "plano.md", Content: []byte("# v1\n"), TaskID: &tk.ID, RunID: &done.ID})
	e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "plano.md", Content: []byte("# v2\n"), TaskID: &tk.ID, RunID: &done.ID})
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "logo.png", Content: pngBytes(), ProjectID: &pr.ID})
	return e, done
}

func TestExportImportArtifacts(t *testing.T) {
	e, run := artifactFixture(t)
	ctx := context.Background()

	// sem a opção, o arquivo não traz o campo
	plain, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil || len(plain.Artifacts) != 0 {
		t.Fatalf("sem artefatos: %d %v", len(plain.Artifacts), err)
	}
	if b, _ := json.Marshal(plain); bytes.Contains(b, []byte(`"artifacts"`)) {
		t.Error("o arquivo sem --with-artifacts não deveria ter o campo")
	}

	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{WithRuns: true, WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != export.Format || len(doc.Artifacts) != 3 {
		t.Fatalf("exportou %d artefatos (formato %s)", len(doc.Artifacts), doc.Format)
	}
	a := doc.Artifacts[0]
	if a.Name != "plano.md" || a.Version != 1 || string(a.Content) != "# v1\n" || a.Task != 1 || a.Project != "lancamento" ||
		a.Persona != "ana" || a.Run == "" || a.Author.Type != "persona" || a.Author.Persona != "ana" || len(a.SHA256) != 64 {
		t.Fatalf("primeiro artefato: %+v", a)
	}

	// a escrita em fluxo é idêntica ao json.Marshal do documento completo
	st, err := e.svc.OpenExport(ctx, e.co.ID, service.ExportOptions{WithRuns: true, WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := st.WriteTo(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	st.Close()
	want, _ := json.Marshal(doc)
	var gotDoc, wantDoc export.Document
	if err := json.Unmarshal(buf.Bytes(), &gotDoc); err != nil {
		t.Fatalf("o fluxo não é JSON válido: %v", err)
	}
	_ = json.Unmarshal(want, &wantDoc)
	gotDoc.ExportedAt, wantDoc.ExportedAt = time.Time{}, time.Time{}
	gb, _ := json.Marshal(gotDoc)
	wb, _ := json.Marshal(wantDoc)
	if !bytes.Equal(gb, wb) {
		t.Errorf("fluxo diferente do documento em memória")
	}

	// importar recria as versões, o conteúdo e as ligações
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "copia"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts["artifacts"] != 3 {
		t.Errorf("contagens: %v", res.Counts)
	}
	list, err := e.svc.ListArtifacts(ctx, res.Company.ID, service.ArtifactFilter{AllVersions: true})
	if err != nil || len(list) != 3 {
		t.Fatalf("importados: %d %v", len(list), err)
	}
	byKey := map[string]service.ArtifactView{}
	for _, v := range list {
		byKey[v.Name+":"+string(rune('0'+v.Version))] = v
	}
	v2 := byKey["plano.md:2"]
	if v2.TaskKey != "HNK-1" || v2.ProjectSlug != "lancamento" || v2.PersonaSlug != "ana" || v2.RunName != run.Name || v2.Versions != 2 || v2.Author.Type != domain.ActorPersona {
		t.Errorf("v2 importada: %+v", v2)
	}
	if _, b, err := e.svc.ArtifactContent(ctx, res.Company.ID, v2.ID); err != nil || string(b) != "# v2\n" {
		t.Errorf("conteúdo: %q %v", b, err)
	}
	logo := byKey["logo.png:1"]
	if logo.TaskID != nil || logo.ProjectSlug != "lancamento" || logo.Mime != "image/png" {
		t.Errorf("logo: %+v", logo)
	}
	// a empresa original não mudou, e a cota da nova conta o que veio
	if u, _ := e.svc.GetArtifactUsage(ctx, res.Company.ID); u.Versions != 3 {
		t.Errorf("uso da nova: %+v", u)
	}
	// a cadeia importada aceita a versão seguinte
	again, err := e.svc.PutArtifact(ctx, domain.HumanActor, res.Company.ID, service.PutArtifactInput{Name: "plano.md", Content: []byte("# v3\n"), TaskID: v2.TaskID})
	if err != nil || again.Version != 3 {
		t.Errorf("versão seguinte na cadeia importada: %+v %v", again, err)
	}
}

func TestExportArtifactsWithoutRunsDropsRunLink(t *testing.T) {
	e, _ := artifactFixture(t)
	ctx := context.Background()
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range doc.Artifacts {
		if a.Run != "" {
			t.Fatalf("sem with_runs não há run para referenciar: %+v", a)
		}
	}
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "sem-runs"})
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := e.svc.ListArtifacts(ctx, res.Company.ID, service.ArtifactFilter{AllVersions: true}); len(l) != 3 {
		t.Errorf("importados: %d", len(l))
	}
}

func TestExportArtifactsTooBig(t *testing.T) {
	e := newEnv(t)
	e.svc.ArtifactMaxBytes = service.MaxArtifactMaxBytes
	e.svc.ArtifactQuotaBytes = 1 << 30
	for i := 0; i < 2; i++ {
		e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "f" + string(rune('a'+i)) + ".bin", Mime: "application/octet-stream",
			Content: bytes.Repeat([]byte{byte(i + 1)}, 11<<20)})
	}
	_, err := e.svc.ExportCompany(context.Background(), e.co.ID, service.ExportOptions{WithArtifacts: true})
	wantErr(t, err, domain.ErrInvalid)
	if _, err := e.svc.ExportCompany(context.Background(), e.co.ID, service.ExportOptions{}); err != nil {
		t.Errorf("sem artefatos exporta: %v", err)
	}
}

func TestImportArtifactsInvalid(t *testing.T) {
	e, _ := artifactFixture(t)
	ctx := context.Background()
	base, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{WithRuns: true, WithArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	clone := func(mod func(*export.Document)) export.Document {
		b, _ := json.Marshal(base)
		var d export.Document
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		mod(&d)
		return d
	}
	for name, mod := range map[string]func(*export.Document){
		"versão fora de ordem":   func(d *export.Document) { d.Artifacts[0].Version = 2 },
		"versão repetida":        func(d *export.Document) { d.Artifacts[1].Version = 1 },
		"sha256 errado":          func(d *export.Document) { d.Artifacts[0].SHA256 = strings.Repeat("0", 64) },
		"conteúdo vazio":         func(d *export.Document) { d.Artifacts[0].Content = nil },
		"task inexistente":       func(d *export.Document) { d.Artifacts[0].Task = 99 },
		"projeto inexistente":    func(d *export.Document) { d.Artifacts[2].Project = "nao-existe" },
		"persona inexistente":    func(d *export.Document) { d.Artifacts[0].Persona = "fantasma" },
		"run inexistente":        func(d *export.Document) { d.Artifacts[0].Run = "r99" },
		"nome com barra":         func(d *export.Document) { d.Artifacts[0].Name = "../etc/passwd" },
		"mime inválido":          func(d *export.Document) { d.Artifacts[2].Mime = "isso não é mime" },
		"autor persona ausente":  func(d *export.Document) { d.Artifacts[0].Author.Persona = "fantasma" },
		"tipo de autor inválido": func(d *export.Document) { d.Artifacts[0].Author.Type = "robô" },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.ImportCompany(ctx, domain.HumanActor, clone(mod), service.ImportOptions{Slug: "ruim"})
			wantErr(t, err, domain.ErrInvalid)
			if _, err := e.svc.GetCompany(ctx, "ruim"); err == nil {
				t.Error("importação inválida deixou empresa")
			}
		})
	}
	// arquivo maior que o limite por arquivo desta instalação
	e.svc.ArtifactMaxBytes = 3
	_, err = e.svc.ImportCompany(ctx, domain.HumanActor, clone(func(*export.Document) {}), service.ImportOptions{Slug: "ruim"})
	wantErr(t, err, domain.ErrInvalid)
}
