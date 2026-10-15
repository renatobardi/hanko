package service_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Artefatos (ADR-0028).

func (e *env) put(t *testing.T, actor domain.Actor, in service.PutArtifactInput) service.ArtifactView {
	t.Helper()
	v, err := e.svc.PutArtifact(context.Background(), actor, e.co.ID, in)
	if err != nil {
		t.Fatalf("put %s: %v", in.Name, err)
	}
	return v
}

func TestArtifactVersions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tk := e.task(t, service.CreateTaskInput{Title: "relatório"})
	other := e.task(t, service.CreateTaskInput{Title: "outra"})

	v1 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("# v1\n"), TaskID: &tk.ID})
	if v1.Version != 1 || v1.Mime != "text/markdown" || v1.SizeBytes != 5 || len(v1.SHA256) != 64 || v1.TaskKey != tk.Key || v1.Versions != 1 {
		t.Fatalf("v1: %+v", v1)
	}
	v2 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("# v2\n"), TaskID: &tk.ID})
	if v2.Version != 2 || v2.ID == v1.ID || v2.Versions != 2 {
		t.Fatalf("v2: %+v", v2)
	}
	// o mesmo conteúdo não cria versão nova
	again := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("# v2\n"), TaskID: &tk.ID})
	if again.ID != v2.ID || again.Version != 2 {
		t.Fatalf("repetição criou versão: %+v", again)
	}
	// outra task, ou nenhuma, é outra cadeia
	if o := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("# v2\n"), TaskID: &other.ID}); o.Version != 1 {
		t.Errorf("outra task: %+v", o)
	}
	if o := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("# v2\n")}); o.Version != 1 || o.TaskKey != "" {
		t.Errorf("sem task: %+v", o)
	}
	if o := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "plano.md", Content: []byte("# v3\n")}); o.Version != 2 {
		t.Errorf("sem task, versão 2: %+v", o)
	}

	vs, err := e.svc.ArtifactVersions(ctx, e.co.ID, v1.ID)
	if err != nil || len(vs) != 2 || vs[0].Version != 2 || vs[1].Version != 1 {
		t.Fatalf("versões: %+v %v", vs, err)
	}
	a, b, err := e.svc.ArtifactContent(ctx, e.co.ID, v1.ID)
	if err != nil || a.Version != 1 || string(b) != "# v1\n" {
		t.Fatalf("conteúdo da v1: %+v %q %v", a, b, err)
	}

	// só a mais nova de cada cadeia na listagem; todas com AllVersions
	list, err := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{TaskID: &tk.ID})
	if err != nil || len(list) != 1 || list[0].Version != 2 || list[0].Versions != 2 {
		t.Fatalf("lista da task: %+v %v", list, err)
	}
	if all, _ := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{TaskID: &tk.ID, AllVersions: true}); len(all) != 2 {
		t.Errorf("todas as versões: %d", len(all))
	}
}

func TestArtifactDiff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tk := e.task(t, service.CreateTaskInput{})
	v1 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "n.txt", Content: []byte("a\nb\nc\n"), TaskID: &tk.ID})
	v2 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "n.txt", Content: []byte("a\nX\nc\n"), TaskID: &tk.ID})
	r, err := e.svc.ArtifactDiff(ctx, e.co.ID, v1.ID, v2.ID)
	if err != nil || r.Added != 1 || r.Removed != 1 {
		t.Fatalf("diff: %+v %v", r, err)
	}
	// cadeias diferentes
	o := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "n.txt", Content: []byte("zzz")})
	_, err = e.svc.ArtifactDiff(ctx, e.co.ID, v1.ID, o.ID)
	wantErr(t, err, domain.ErrInvalid)
	// binário não tem diff
	p1 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "i.png", Content: pngBytes()})
	p2 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "i.png", Content: append(pngBytes(), 0)})
	_, err = e.svc.ArtifactDiff(ctx, e.co.ID, p1.ID, p2.ID)
	wantErr(t, err, domain.ErrInvalid)
}

func pngBytes() []byte {
	return []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
}

func TestArtifactValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.ArtifactMaxBytes = 100
	e.svc.ArtifactQuotaBytes = 150
	put := func(name, mime string, n int) error {
		_, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: name, Mime: mime, Content: bytes.Repeat([]byte("x"), n)})
		return err
	}
	for name, c := range map[string]struct {
		name, mime string
		n          int
	}{
		"nome vazio":           {"  ", "", 5},
		"nome com barra":       {"a/b.txt", "", 5},
		"nome com contrabarra": {`a\b.txt`, "", 5},
		"nome com controle":    {"a\x00b", "", 5},
		"nome ..":              {"..", "", 5},
		"nome longo":           {strings.Repeat("a", 201), "", 5},
		"conteúdo vazio":       {"a.txt", "", 0},
		"grande demais":        {"a.txt", "", 101},
		"mime inválido":        {"a.txt", "isso não é mime", 5},
	} {
		t.Run(name, func(t *testing.T) { wantErr(t, put(c.name, c.mime, c.n), domain.ErrInvalid) })
	}
	// dentro do limite
	if err := put("a.txt", "", 100); err != nil {
		t.Fatal(err)
	}
	// a cota (150) conta todas as versões: 100 usados + 60 estoura, 100 + 50 cabe
	if _, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "b.txt", Content: bytes.Repeat([]byte("y"), 60)}); err == nil ||
		!strings.Contains(err.Error(), "quota") {
		t.Fatalf("cota: %v", err)
	}
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "b.txt", Content: bytes.Repeat([]byte("y"), 50)})
	u, err := e.svc.GetArtifactUsage(ctx, e.co.ID)
	if err != nil || u.UsedBytes != 150 || u.Versions != 2 || u.QuotaBytes != 150 || u.MaxFileBytes != 100 {
		t.Fatalf("uso: %+v %v", u, err)
	}
	// apagar libera a cota
	list, _ := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{})
	if err := e.svc.DeleteArtifact(ctx, domain.HumanActor, e.co.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if u, _ = e.svc.GetArtifactUsage(ctx, e.co.ID); u.UsedBytes >= 150 || u.Versions != 1 {
		t.Errorf("uso depois de apagar: %+v", u)
	}
}

func TestArtifactMime(t *testing.T) {
	e := newEnv(t)
	cases := []struct{ name, declared, want string }{
		{"a.md", "", "text/markdown"},
		{"a.md", "application/octet-stream", "text/markdown"},
		{"a.json", "", "application/json"},
		{"a.go", "", "text/plain"},
		{"x.bin", "text/x-markdown; charset=utf-8", "text/markdown"},
		{"x.bin", "Image/PNG", "image/png"},
		{"x.svg", "", "image/svg+xml"},
		{"noext", "", "text/plain"},
	}
	for _, c := range cases {
		w := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: c.name, Mime: c.declared, Content: []byte("texto " + c.declared)})
		if w.Mime != c.want {
			t.Errorf("%q declarado %q: mime %q, quer %q", c.name, c.declared, w.Mime, c.want)
		}
	}
	// deduz pelo conteúdo quando o nome não ajuda
	if v := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "figura", Content: pngBytes()}); v.Mime != "image/png" {
		t.Errorf("sniff: %q", v.Mime)
	}
}

func TestArtifactLinksAndAuthorship(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	pr := e.project(t, "Lançamento", nil)
	p := e.persona(t, "Ana", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID, ProjectID: &pr.ID})
	other := e.task(t, service.CreateTaskInput{Title: "o"})
	run, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: tk.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}

	// persona autora: task e run do token; o projeto vem da task
	v := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "saida.md", Content: []byte("oi"), TaskID: &tk.ID, RunID: &run.ID})
	if v.PersonaID == nil || *v.PersonaID != p.ID || v.PersonaSlug != "ana" || v.Author.Type != domain.ActorPersona ||
		v.ProjectSlug != "lancamento" || v.RunID == nil || v.RunName == "" || v.TaskKey != tk.Key {
		t.Fatalf("artefato da persona: %+v", v)
	}
	// só run: a task vem da run
	w := e.put(t, domain.PersonaActor(p.ID), service.PutArtifactInput{Name: "so-run.md", Content: []byte("oi"), RunID: &run.ID})
	if w.TaskID == nil || *w.TaskID != tk.ID {
		t.Errorf("task da run: %+v", w)
	}
	// run de outra task
	_, err = e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "x.md", Content: []byte("x"), TaskID: &other.ID, RunID: &run.ID})
	wantErr(t, err, domain.ErrInvalid)
	// referências inexistentes
	ghost := domain.NewID()
	for name, in := range map[string]service.PutArtifactInput{
		"task":    {Name: "x", Content: []byte("x"), TaskID: &ghost},
		"run":     {Name: "x", Content: []byte("x"), RunID: &ghost},
		"projeto": {Name: "x", Content: []byte("x"), ProjectID: &ghost},
	} {
		_, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, in)
		if err == nil {
			t.Errorf("%s inexistente deveria falhar", name)
		}
		wantErr(t, err, domain.ErrInvalid)
	}

	// humano: sem persona
	h := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "h.md", Content: []byte("h"), ProjectID: &pr.ID})
	if h.PersonaID != nil || h.Author.Type != domain.ActorHuman || h.ProjectSlug != "lancamento" {
		t.Errorf("humano: %+v", h)
	}

	// filtros
	pid := p.ID
	for name, c := range map[string]struct {
		f    service.ArtifactFilter
		want int
	}{
		"persona":      {service.ArtifactFilter{PersonaID: &pid}, 2},
		"projeto":      {service.ArtifactFilter{ProjectID: &pr.ID}, 3},
		"run":          {service.ArtifactFilter{RunID: &run.ID}, 2},
		"task":         {service.ArtifactFilter{TaskID: &tk.ID}, 2},
		"tipo md":      {service.ArtifactFilter{Kind: domain.KindMarkdown}, 3},
		"tipo imagem":  {service.ArtifactFilter{Kind: domain.KindImage}, 0},
		"tipo outro":   {service.ArtifactFilter{Kind: domain.KindOther}, 0},
		"busca":        {service.ArtifactFilter{Query: "SAIDA"}, 1},
		"busca com %":  {service.ArtifactFilter{Query: "%"}, 0},
		"sem filtro":   {service.ArtifactFilter{}, 3},
		"tipo texto":   {service.ArtifactFilter{Kind: domain.KindText}, 0},
		"tudo, limite": {service.ArtifactFilter{Limit: 2}, 2},
	} {
		got, err := e.svc.ListArtifacts(ctx, e.co.ID, c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: %d artefatos (%v), quer %d", name, len(got), err, c.want)
		}
	}
	if _, err := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{Kind: "nope"}); err == nil {
		t.Error("tipo desconhecido deveria falhar")
	}
}

func TestArtifactRolesAndIsolation(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin} {
		u := mkUser(t, e, string(r)+"@x.com", domain.RoleMember)
		join(t, e, u, r)
		users[r] = u
	}
	in := service.PutArtifactInput{Name: "a.md", Content: []byte("x")}
	if _, err := e.svc.PutArtifact(as(users[domain.MemberViewer]), domain.HumanActor, e.co.ID, in); err == nil {
		t.Fatal("viewer não publica")
	} else {
		wantErr(t, err, domain.ErrForbidden)
	}
	v, err := e.svc.PutArtifact(as(users[domain.MemberMember]), users[domain.MemberMember].Actor(), e.co.ID, in)
	if err != nil {
		t.Fatalf("member publica: %v", err)
	}
	if _, err := e.svc.ListArtifacts(as(users[domain.MemberViewer]), e.co.ID, service.ArtifactFilter{}); err != nil {
		t.Errorf("viewer lista: %v", err)
	}
	if _, _, err := e.svc.ArtifactContent(as(users[domain.MemberViewer]), e.co.ID, v.ID); err != nil {
		t.Errorf("viewer lê: %v", err)
	}
	wantErr(t, e.svc.DeleteArtifact(as(users[domain.MemberMember]), domain.HumanActor, e.co.ID, v.ID), domain.ErrForbidden)
	if err := e.svc.DeleteArtifact(as(users[domain.MemberAdmin]), users[domain.MemberAdmin].Actor(), e.co.ID, v.ID); err != nil {
		t.Errorf("admin apaga: %v", err)
	}
	_, _, err = e.svc.ArtifactContent(bg, e.co.ID, v.ID)
	wantErr(t, err, domain.ErrNotFound)

	// outra empresa não vê
	other, err := e.svc.CreateCompany(bg, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	w := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "b.md", Content: []byte("y")})
	_, _, err = e.svc.ArtifactContent(bg, other.ID, w.ID)
	wantErr(t, err, domain.ErrNotFound)
	if l, _ := e.svc.ListArtifacts(bg, other.ID, service.ArtifactFilter{}); len(l) != 0 {
		t.Errorf("a outra empresa viu %d artefatos", len(l))
	}
}

func TestArtifactConcurrentVersions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tk := e.task(t, service.CreateTaskInput{})
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{
				Name: "corrida.txt", Content: []byte(fmt.Sprintf("versão %d", i)), TaskID: &tk.ID})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("put concorrente: %v", err)
		}
	}
	all, err := e.svc.ListArtifacts(ctx, e.co.ID, service.ArtifactFilter{TaskID: &tk.ID, AllVersions: true})
	if err != nil || len(all) != n {
		t.Fatalf("%d versões (%v), quer %d", len(all), err, n)
	}
	seen := map[int]bool{}
	for _, a := range all {
		seen[a.Version] = true
	}
	for v := 1; v <= n; v++ {
		if !seen[v] {
			t.Errorf("falta a versão %d: %v", v, seen)
		}
	}
}

func TestArtifactActivity(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "a.md", Content: []byte("x")})
	if err := e.svc.DeleteArtifact(ctx, domain.HumanActor, e.co.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	acts, err := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "artifact"})
	if err != nil || len(acts) != 2 {
		t.Fatalf("atividade: %+v %v", acts, err)
	}
	verbs := map[string]bool{}
	for _, a := range acts {
		verbs[a.Verb] = true
	}
	if !verbs["artifact.created"] || !verbs["artifact.deleted"] {
		t.Errorf("verbos: %v", verbs)
	}
}
