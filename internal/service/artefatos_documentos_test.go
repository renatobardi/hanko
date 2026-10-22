package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Documento de artefato com identidade estável e versão MAJOR.MINOR (ADR-0064).

func lines(n int, word string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(word)
		b.WriteString(" linha ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestArtifactDocumentAcrossTasksAndNames(t *testing.T) {
	e := newEnv(t)
	t1 := e.task(t, service.CreateTaskInput{Title: "escrever"})
	t2 := e.task(t, service.CreateTaskInput{Title: "revisar"})
	base := lines(50, "plano")
	v1 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte(base), TaskID: &t1.ID, Tags: []string{"Arquitetura"}})
	if v1.SemVer != "1.0" || v1.Change != domain.ChangeMajor || len(v1.Tags) != 1 || v1.Tags[0] != "arquitetura" {
		t.Fatalf("primeira versão: %+v", v1)
	}
	// outra task e outro nome, mas o mesmo documento: versão nova, não documento novo
	doc := v1.DocumentID
	small := strings.Replace(base, "plano linha \n", "plano linha ajustada\n", 1)
	v2 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano-revisado.md", Content: []byte(small), TaskID: &t2.ID, DocumentID: &doc})
	if v2.DocumentID != doc || v2.SemVer != "1.1" || v2.Change != domain.ChangeMinor || v2.ChangeDeclared != nil || v2.ChangeRatio == nil || v2.Versions != 2 {
		t.Fatalf("ajuste pequeno sem declaração vira minor pela medida: %+v", v2)
	}
	// reescrita sem declaração: major pela medida
	v3 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano-revisado.md", Content: []byte(lines(50, "outro")), DocumentID: &doc})
	if v3.SemVer != "2.0" || v3.Change != domain.ChangeMajor {
		t.Fatalf("reescrita: %+v", v3)
	}
	// a listagem mostra o documento uma vez, achável pela task de qualquer versão
	for _, tk := range []domain.ID{t1.ID, t2.ID} {
		list, err := e.svc.ListArtifacts(context.Background(), e.co.ID, service.ArtifactFilter{TaskID: &tk})
		if err != nil || len(list) != 1 || list[0].ID != v3.ID || list[0].Name != "plano-revisado.md" {
			t.Fatalf("lista por task %s: %+v %v", tk, list, err)
		}
	}
	list, err := e.svc.ListArtifacts(context.Background(), e.co.ID, service.ArtifactFilter{Tag: "arquitetura"})
	if err != nil || len(list) != 1 {
		t.Fatalf("filtro por tag: %+v %v", list, err)
	}
	vs, err := e.svc.ArtifactVersions(context.Background(), e.co.ID, v1.ID)
	if err != nil || len(vs) != 3 || vs[0].SemVer().String() != "2.0" || vs[2].SemVer().String() != "1.0" {
		t.Fatalf("histórico: %+v %v", vs, err)
	}
	if _, err := e.svc.ArtifactDiff(context.Background(), e.co.ID, v1.ID, v3.ID); err != nil {
		t.Fatalf("diff entre versões com nomes diferentes do mesmo documento: %v", err)
	}
}

func TestArtifactCompatSameNameSameTask(t *testing.T) {
	e := newEnv(t)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	a := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "r.md", Content: []byte("a\n"), TaskID: &tk.ID})
	b := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "r.md", Content: []byte("b\n"), TaskID: &tk.ID})
	if a.DocumentID != b.DocumentID || b.Version != 2 {
		t.Fatalf("sem id, mesmo nome na mesma task continua versionando: %+v %+v", a, b)
	}
	same := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "r.md", Content: []byte("b\n"), TaskID: &tk.ID})
	if same.ID != b.ID {
		t.Fatalf("mesmo conteúdo não cria versão: %+v", same)
	}
	c := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "r.md", Content: []byte("c\n")})
	if c.DocumentID == a.DocumentID {
		t.Fatal("sem task é outro escopo: documento novo")
	}
}

func TestArtifactDeclaredChangeAndDispute(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	base := lines(40, "a")
	v1 := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "x.md", Content: []byte(base), TaskID: &tk.ID})
	doc := v1.DocumentID
	minor, major := domain.ChangeMinor, domain.ChangeMajor
	// persona que declara precisa dizer o motivo
	_, err := e.svc.PutArtifact(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PutArtifactInput{Name: "x.md", Content: []byte(lines(40, "b")),
		DocumentID: &doc, TaskID: &tk.ID, Change: &minor})
	wantErr(t, err, domain.ErrInvalid)
	// declarou minor numa reescrita: vale o declarado, marcado como questionado
	v2 := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "x.md", Content: []byte(lines(40, "b")),
		DocumentID: &doc, TaskID: &tk.ID, Change: &minor, ChangeReason: "ajuste de texto"})
	if v2.SemVer != "1.1" || v2.Change != domain.ChangeMinor || !v2.ChangeDisputed || v2.ChangeReason != "ajuste de texto" {
		t.Fatalf("declaração contrariada: %+v", v2)
	}
	// declarou major com uma linha mudada: vale, questionado
	almost := strings.Replace(lines(40, "b"), "b linha \n", "b linha!\n", 1)
	v3 := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "x.md", Content: []byte(almost),
		DocumentID: &doc, TaskID: &tk.ID, Change: &major, ChangeReason: "conclusão invertida"})
	if v3.SemVer != "2.0" || !v3.ChangeDisputed {
		t.Fatalf("major em poucas linhas: %+v", v3)
	}
	// reclassificar: só a mais nova, renumerada a partir da anterior
	if _, err := e.svc.ReclassifyArtifact(ctx, domain.HumanActor, e.co.ID, v2.ID, domain.ChangeMajor); err == nil {
		t.Fatal("reclassificar versão antiga deveria falhar")
	}
	r, err := e.svc.ReclassifyArtifact(ctx, domain.HumanActor, e.co.ID, v3.ID, domain.ChangeMinor)
	if err != nil || r.SemVer != "1.2" || r.ChangeDisputed {
		t.Fatalf("reclassificada: %+v %v", r, err)
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE verb = 'artifact.reclassified'`); n != 1 {
		t.Fatalf("fato artifact.reclassified: %d", n)
	}
}

func TestArtifactRestore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v1 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "r.md", Content: []byte(lines(20, "um"))})
	doc := v1.DocumentID
	v2 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "r.md", Content: []byte(lines(20, "dois")), DocumentID: &doc})
	if _, err := e.svc.RestoreArtifact(ctx, domain.HumanActor, e.co.ID, v2.ID, nil, nil); err == nil {
		t.Fatal("restaurar a mais nova deveria falhar")
	}
	r, err := e.svc.RestoreArtifact(ctx, domain.HumanActor, e.co.ID, v1.ID, nil, nil)
	if err != nil || r.SemVer != "3.0" || r.RestoredFrom == nil || *r.RestoredFrom != v1.ID || r.SHA256 != v1.SHA256 || r.ChangeReason != "restaurada de 1.0" {
		t.Fatalf("restaurada: %+v %v", r, err)
	}
	id, err := e.svc.ArtifactVersionID(ctx, e.co.ID, doc, "2.0")
	if err != nil || id != v2.ID {
		t.Fatalf("versão por M.m: %v %v", id, err)
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE verb = 'artifact.restored'`); n != 1 {
		t.Fatalf("fato artifact.restored: %d", n)
	}
}

func TestArtifactPersonaProjectScope(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	pa, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Alfa"})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Beta"})
	if err != nil {
		t.Fatal(err)
	}
	ta := e.task(t, service.CreateTaskInput{Title: "a", ProjectID: &pa.ID})
	tb := e.task(t, service.CreateTaskInput{Title: "b", ProjectID: &pb.ID})
	v := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "doc.md", Content: []byte("x\n"), TaskID: &ta.ID})
	if v.DocumentProjectID == nil || *v.DocumentProjectID != pa.ID {
		t.Fatalf("o documento herda o projeto da task: %+v", v)
	}
	doc := v.DocumentID
	_, err = e.svc.PutArtifact(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PutArtifactInput{Name: "doc.md", Content: []byte("y\n"), TaskID: &tb.ID, DocumentID: &doc})
	wantErr(t, err, domain.ErrNotAllowed)
	tc := e.task(t, service.CreateTaskInput{Title: "c", ProjectID: &pa.ID})
	if _, err := e.svc.PutArtifact(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PutArtifactInput{Name: "doc.md", Content: []byte("y\n"), TaskID: &tc.ID, DocumentID: &doc}); err != nil {
		t.Fatalf("mesmo projeto: %v", err)
	}
	// projeto, workspace e tags não são identidade: mudar não cria versão
	d, err := e.svc.UpdateArtifactDocument(ctx, domain.HumanActor, e.co.ID, doc, service.ArtifactDocumentPatch{ProjectID: &pb.ID, Tags: []string{"cliente-x"}})
	if err != nil || d.ProjectID == nil || *d.ProjectID != pb.ID || len(d.Tags) != 1 {
		t.Fatalf("editar documento: %+v %v", d, err)
	}
	vs, _ := e.svc.ArtifactVersions(ctx, e.co.ID, v.ID)
	if len(vs) != 2 {
		t.Fatalf("editar o documento não cria versão: %d", len(vs))
	}
}

func TestArtifactAttachmentDocumentGoesAway(t *testing.T) {
	e := newEnv(t)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	a := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "foto.png", Content: []byte("\x89PNG\r\n\x1a\n...."), TaskID: &tk.ID, Class: domain.ClassAttachment})
	if a.SemVer != "1.0" {
		t.Fatalf("anexo é 1.0: %+v", a)
	}
	if _, err := e.svc.Pool.Exec(context.Background(), "DELETE FROM tasks WHERE id = $1", tk.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM artifact_documents WHERE id = $1`, a.DocumentID); n != 0 {
		t.Fatalf("documento do anexo apagado em cascata deveria sumir: %d", n)
	}
}

// O briefing da run lista os documentos da task, do projeto e os que a persona publicou em outro lugar (por exemplo
// no chat), uma vez cada, para ela alterar em vez de recriar (ADR-0064).
func TestRunContextListsArtifactDocuments(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Alfa"})
	if err != nil {
		t.Fatal(err)
	}
	tk := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID, ProjectID: &pr.ID})
	other := e.task(t, service.CreateTaskInput{Title: "outra", ProjectID: &pr.ID})
	inTask := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("a\n"), TaskID: &tk.ID})
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("b\n"), TaskID: &tk.ID})
	inProject := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "contexto.md", Content: []byte("c\n"), TaskID: &other.ID})
	mine := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "rascunho.md", Content: []byte("d\n")})
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "alheio.md", Content: []byte("e\n")})

	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tk, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[domain.ID]service.ArtifactBrief{}
	for _, a := range rc.Artifacts {
		got[a.DocumentID] = a
	}
	if len(rc.Artifacts) != 3 || !got[inTask.DocumentID].InTask || got[inTask.DocumentID].SemVer != "2.0" ||
		got[inProject.DocumentID].DocumentID == (domain.ID{}) || got[mine.DocumentID].DocumentID == (domain.ID{}) {
		t.Fatalf("documentos no briefing: %+v", rc.Artifacts)
	}
}

// Alterar por id sem nome mantém o nome do documento (a taxonomia não muda por descuido).
func TestArtifactPutByIDKeepsName(t *testing.T) {
	e := newEnv(t)
	v1 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("a\n")})
	doc := v1.DocumentID
	v2 := e.put(t, domain.HumanActor, service.PutArtifactInput{Content: []byte("b\n"), DocumentID: &doc})
	if v2.Name != "plano.md" || v2.DocumentID != doc || v2.Version != 2 {
		t.Fatalf("sem nome: %+v", v2)
	}
	missing := domain.NewID()
	_, err := e.svc.PutArtifact(context.Background(), domain.HumanActor, e.co.ID, service.PutArtifactInput{Content: []byte("c\n"), DocumentID: &missing})
	wantErr(t, err, domain.ErrInvalid)
}

// Export e importação levam o documento, MAJOR.MINOR, a classificação e as tags; um documento que atravessa tasks e
// muda de nome volta como um documento só (ADR-0064).
func TestExportImportArtifactDocuments(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	t1 := e.task(t, service.CreateTaskInput{Title: "a"})
	t2 := e.task(t, service.CreateTaskInput{Title: "b"})
	v1 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte(lines(30, "a")), TaskID: &t1.ID, Tags: []string{"arquitetura"}})
	doc := v1.DocumentID
	minor := domain.ChangeMinor
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano-final.md", Content: []byte(lines(30, "a") + "fim\n"), TaskID: &t2.ID,
		DocumentID: &doc, Change: &minor, ChangeReason: "fecho"})

	out, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{WithArtifacts: true})
	if err != nil || len(out.Artifacts) != 2 {
		t.Fatalf("export: %d %v", len(out.Artifacts), err)
	}
	if a := out.Artifacts[1]; a.Document != doc.String() || a.Major != 1 || a.Minor != 1 || a.Change != "minor" || a.ChangeReason != "fecho" || len(a.Tags) != 1 {
		t.Fatalf("exportada: %+v", a)
	}
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, out, service.ImportOptions{Slug: "copia"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.ListArtifacts(ctx, res.Company.ID, service.ArtifactFilter{})
	if err != nil || len(list) != 1 || list[0].SemVer != "1.1" || list[0].Versions != 2 || list[0].Name != "plano-final.md" ||
		len(list[0].Tags) != 1 || list[0].Tags[0] != "arquitetura" || list[0].DocumentID == doc {
		t.Fatalf("importado: %+v %v", list, err)
	}

	// arquivo antigo (sem documento nem MAJOR.MINOR): agrupa por (task, nome) e numera vN como N.0
	for i := range out.Artifacts {
		a := &out.Artifacts[i]
		a.Document, a.Major, a.Minor, a.Change, a.Tags = "", 0, 0, "", nil
		a.Name = "plano.md"
		a.Task = 0
	}
	res, err = e.svc.ImportCompany(ctx, domain.HumanActor, out, service.ImportOptions{Slug: "antigo"})
	if err != nil {
		t.Fatal(err)
	}
	list, err = e.svc.ListArtifacts(ctx, res.Company.ID, service.ArtifactFilter{})
	if err != nil || len(list) != 1 || list[0].SemVer != "2.0" {
		t.Fatalf("arquivo antigo: %+v %v", list, err)
	}
}

// Correções da revisão do ADR-0064.
func TestArtifactReviewFixes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	pa, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Alfa"})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Beta"})
	if err != nil {
		t.Fatal(err)
	}
	ta := e.task(t, service.CreateTaskInput{Title: "a", ProjectID: &pa.ID})
	tb := e.task(t, service.CreateTaskInput{Title: "b", ProjectID: &pb.ID})
	inB := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "b.md", Content: []byte("b\n"), TaskID: &tb.ID})
	docB := inB.DocumentID

	// a persona não escolhe o projeto que vale para a regra: informar project=B numa task de A não libera o documento de B
	_, err = e.svc.PutArtifact(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PutArtifactInput{Content: []byte("x\n"), TaskID: &ta.ID,
		ProjectID: &pb.ID, DocumentID: &docB})
	wantErr(t, err, domain.ErrNotAllowed)

	// pelo nome, um documento que foi movido para outro projeto deixa de ser da persona: ela cria outro em vez de falhar
	mine := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "r.md", Content: []byte("1\n"), TaskID: &ta.ID})
	if _, err := e.svc.UpdateArtifactDocument(ctx, domain.HumanActor, e.co.ID, mine.DocumentID, service.ArtifactDocumentPatch{ProjectID: &pb.ID}); err != nil {
		t.Fatal(err)
	}
	again := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "r.md", Content: []byte("2\n"), TaskID: &ta.ID})
	if again.DocumentID == mine.DocumentID || again.SemVer != "1.0" {
		t.Fatalf("documento movido: %+v", again)
	}

	// a persona define tags só num documento novo
	_, err = e.svc.PutArtifact(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PutArtifactInput{Content: []byte("3\n"), TaskID: &ta.ID,
		DocumentID: &again.DocumentID, Tags: []string{"x"}})
	wantErr(t, err, domain.ErrInvalid)
	if v := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "novo.md", Content: []byte("n\n"), TaskID: &ta.ID, Tags: []string{"x"}}); len(v.Tags) != 1 {
		t.Fatalf("tags num documento novo: %+v", v)
	}

	// mesmo conteúdo com tags: não cria versão, mas grava as tags
	same := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "b.md", Content: []byte("b\n"), TaskID: &tb.ID, Tags: []string{"cliente-x"}})
	if same.ID != inB.ID || len(same.Tags) != 1 || same.Tags[0] != "cliente-x" {
		t.Fatalf("tags no mesmo conteúdo: %+v", same)
	}

	// apagar a task: as versões ficam, mas o documento órfão não captura uma publicação nova sem task de mesmo nome
	orphan := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "solto.md", Content: []byte("o\n"), TaskID: &ta.ID})
	if _, err := e.svc.Pool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", ta.ID); err != nil {
		t.Fatal(err)
	}
	fresh := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "solto.md", Content: []byte("f\n")})
	if fresh.DocumentID == orphan.DocumentID || fresh.SemVer != "1.0" {
		t.Fatalf("documento órfão capturou a publicação: %+v", fresh)
	}
	vs, _ := e.svc.ArtifactVersions(ctx, e.co.ID, orphan.ID)
	if len(vs) != 1 {
		t.Fatalf("histórico do órfão: %d", len(vs))
	}

	// restauração é sempre major: não vira minor
	v2 := e.put(t, domain.HumanActor, service.PutArtifactInput{Content: []byte("b2\n"), DocumentID: &docB})
	r, err := e.svc.RestoreArtifact(ctx, domain.HumanActor, e.co.ID, inB.ID, nil, nil)
	if err != nil || v2.ID == r.ID {
		t.Fatal(err)
	}
	_, err = e.svc.ReclassifyArtifact(ctx, domain.HumanActor, e.co.ID, r.ID, domain.ChangeMinor)
	wantErr(t, err, domain.ErrInvalid)
}
