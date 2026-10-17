package mcp

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Revisões das instruções, anexos, projeto e meta pelo MCP (ADR-0052).

// m8State guarda o que as tools do M8 gravam no fake.
type m8State struct {
	proposals  []service.ProposeInstructionsInput
	proposedBy domain.ID
	attach     []service.ArtifactView
	content    map[domain.ID][]byte
	projects   []service.CreateProjectInput
	goals      []service.CreateGoalInput
}

var m8 = map[*fake]*m8State{}

func (f *fake) m8() *m8State {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m8[f] == nil {
		m8[f] = &m8State{content: map[domain.ID][]byte{}}
	}
	return m8[f]
}

func (f *fake) ProposePersonaInstructions(_ context.Context, a domain.Actor, _, personaID domain.ID, in service.ProposeInstructionsInput) (service.GateView, error) {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor = a
	st.proposals = append(st.proposals, in)
	st.proposedBy = personaID
	return service.GateView{Gate: domain.Gate{ID: domain.NewID(), CompanyID: f.company.ID, Kind: domain.GateInstruction, Status: domain.GatePending}}, nil
}

func (f *fake) ListArtifacts(_ context.Context, _ domain.ID, fl service.ArtifactFilter) ([]service.ArtifactView, error) {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []service.ArtifactView
	for i := len(st.attach) - 1; i >= 0; i-- { // do mais novo ao mais antigo, como o service
		a := st.attach[i]
		if fl.TaskID != nil && (a.TaskID == nil || *a.TaskID != *fl.TaskID) {
			continue
		}
		if fl.ConversationID != nil && (a.ConversationID == nil || *a.ConversationID != *fl.ConversationID) {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (f *fake) ArtifactContent(_ context.Context, _, id domain.ID) (domain.Artifact, []byte, error) {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range st.attach {
		if a.ID == id {
			return a.Artifact, st.content[id], nil
		}
	}
	return domain.Artifact{}, nil, domain.ErrNotFound
}

func (e *env) seedAttachment(f *fake, name, mime string, content []byte, task *domain.ID) service.ArtifactView {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	v := service.ArtifactView{Artifact: domain.Artifact{ID: domain.NewID(), CompanyID: f.company.ID, TaskID: task, Name: name, Mime: mime,
		SizeBytes: int64(len(content)), Class: domain.ClassAttachment, Author: domain.HumanActor, Version: 1, CreatedAt: time.Now()}}
	st.attach = append(st.attach, v)
	st.content[v.ID] = content
	return v
}

func TestInstructionPropose(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	res := call(t, cs, "hanko_persona_instruction_propose", map[string]any{
		"instructions": "Novo texto completo.", "reason": "aprendi", "event_id": "ev-1"})
	if res.IsError {
		t.Fatal(text(res))
	}
	var out instructionProposeOut
	structured(t, res, &out)
	st := e.f.m8()
	if out.GateID == "" || out.Status != "pending" || len(st.proposals) != 1 {
		t.Fatalf("saída: %+v propostas=%d", out, len(st.proposals))
	}
	p := st.proposals[0]
	if p.Body != "Novo texto completo." || p.Reason != "aprendi" || p.EventID != "ev-1" || p.RunID == nil || *p.RunID != e.runID {
		t.Errorf("entrada do service: %+v", p)
	}
	// a persona do token é quem propõe, e só para si
	if st.proposedBy != e.f.personas[1].ID || e.f.lastActor.Type != domain.ActorPersona {
		t.Errorf("proponente: %v %+v", st.proposedBy, e.f.lastActor)
	}
}

func TestInstructionProposeNeedsPersonaToken(t *testing.T) {
	e := setup(t, true) // modo trusted, sem token: ator humano
	cs, _ := e.connect(t, "")
	defer cs.Close()
	res := call(t, cs, "hanko_persona_instruction_propose", map[string]any{"company": "acme", "instructions": "x"})
	if !res.IsError || !strings.Contains(text(res), "persona") {
		t.Fatalf("humano propondo: %q", text(res))
	}
}

func TestAttachmentTools(t *testing.T) {
	e := setup(t, false)
	task := e.f.tasks[0].ID
	other := e.f.tasks[1].ID
	spec := e.seedAttachment(e.f, "spec.md", "text/markdown", []byte("# Spec\n\ndetalhe"), &task)
	png := e.seedAttachment(e.f, "foto.png", "image/png", []byte("\x89PNG\r\n\x1a\n"), &task)
	big := e.seedAttachment(e.f, "enorme.txt", "text/plain", nil, &task)
	e.f.m8().attach[2].SizeBytes = maxAttachmentTextBytes + 1
	hidden := e.seedAttachment(e.f, "segredo.txt", "text/plain", []byte("de outra task"), &other)

	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	// lista só os anexos da task do token
	var list attachmentListOut
	res := call(t, cs, "hanko_attachment_list", map[string]any{})
	if res.IsError {
		t.Fatal(text(res))
	}
	structured(t, res, &list)
	if len(list.Attachments) != 3 || list.Attachments[0].Name != "enorme.txt" {
		t.Fatalf("lista: %+v", list.Attachments)
	}
	for _, a := range list.Attachments {
		if a.ID == hidden.ID.String() {
			t.Fatal("anexo de outra task vazou na lista")
		}
	}

	// texto por id
	var got attachmentGetOut
	res = call(t, cs, "hanko_attachment_get", map[string]any{"id": spec.ID.String()})
	if res.IsError {
		t.Fatal(text(res))
	}
	structured(t, res, &got)
	if got.Content != "# Spec\n\ndetalhe" || got.Base64 != "" || got.Truncated {
		t.Fatalf("texto: %+v", got)
	}
	// binário por nome, em base64
	res = call(t, cs, "hanko_attachment_get", map[string]any{"name": "foto.png"})
	got = attachmentGetOut{}
	structured(t, res, &got)
	if raw, err := base64.StdEncoding.DecodeString(got.Base64); err != nil || string(raw) != "\x89PNG\r\n\x1a\n" || got.Content != "" {
		t.Fatalf("binário: %+v %v", got, err)
	}
	_ = png
	// grande demais: só os metadados
	res = call(t, cs, "hanko_attachment_get", map[string]any{"id": big.ID.String()})
	got = attachmentGetOut{}
	structured(t, res, &got)
	if !got.Truncated || got.Content != "" || got.Attachment.Name != "enorme.txt" {
		t.Fatalf("grande: %+v", got)
	}
	// o anexo de outra task não se lê, nem pelo id
	if r := call(t, cs, "hanko_attachment_get", map[string]any{"id": hidden.ID.String()}); !r.IsError || strings.Contains(text(r), "de outra task") {
		t.Fatalf("anexo de outra task: %q", text(r))
	}
	// id e name juntos, ou nenhum
	for _, args := range []map[string]any{{}, {"id": spec.ID.String(), "name": "spec.md"}} {
		if r := call(t, cs, "hanko_attachment_get", args); !r.IsError {
			t.Errorf("args %v deveriam falhar", args)
		}
	}
	// com token de run, informar outra task é recusado
	if r := call(t, cs, "hanko_attachment_list", map[string]any{"task": e.f.tasks[1].Key}); !r.IsError {
		t.Error("task de outra execução deveria ser recusada")
	}
}

func TestAttachmentPut(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	res := call(t, cs, "hanko_attachment_put", map[string]any{"name": "apoio.txt", "content": "material"})
	if res.IsError {
		t.Fatal(text(res))
	}
	in := e.f.puts[len(e.f.puts)-1]
	if in.Class != domain.ClassAttachment || in.TaskID == nil || *in.TaskID != e.f.tasks[0].ID || in.RunID == nil || *in.RunID != e.runID || string(in.Content) != "material" {
		t.Fatalf("entrada do service: %+v", in)
	}
	if e.f.lastActor.Type != domain.ActorPersona {
		t.Errorf("autor: %+v", e.f.lastActor)
	}
	if r := call(t, cs, "hanko_attachment_put", map[string]any{"name": "x", "content": "a", "content_base64": "YQ=="}); !r.IsError {
		t.Error("content e content_base64 juntos deveriam falhar")
	}
	if r := call(t, cs, "hanko_attachment_put", map[string]any{"name": "x", "content_base64": "!!!"}); !r.IsError {
		t.Error("base64 inválido deveria falhar")
	}
	if r := call(t, cs, "hanko_attachment_put", map[string]any{"name": "", "content": "a"}); !r.IsError {
		t.Error("sem nome deveria falhar")
	}
}

// ---- projeto e meta (hanko_project_create / hanko_goal_create) ----

func (f *fake) GetWorkspace(_ context.Context, _ domain.ID, ref string) (domain.Workspace, error) {
	if ref == "site" {
		return domain.Workspace{ID: domain.NewID(), Slug: "site", Name: "Site"}, nil
	}
	return domain.Workspace{}, domain.ErrNotFound
}

func (f *fake) CreateProject(_ context.Context, a domain.Actor, _ domain.ID, in service.CreateProjectInput) (domain.Project, error) {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor = a
	st.projects = append(st.projects, in)
	p := domain.Project{ID: domain.NewID(), CompanyID: f.company.ID, Slug: in.Slug, Name: in.Name, Description: in.Description, Status: domain.ProjectActive}
	if p.Slug == "" {
		p.Slug = domain.Slugify(in.Name)
	}
	f.projects = append(f.projects, service.ProjectView{Project: p})
	return p, nil
}

func (f *fake) CreateGoal(_ context.Context, a domain.Actor, _ domain.ID, in service.CreateGoalInput) (domain.Goal, error) {
	st := f.m8()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor = a
	st.goals = append(st.goals, in)
	g := domain.Goal{ID: domain.NewID(), CompanyID: f.company.ID, Slug: in.Slug, Title: in.Title, Metric: in.Metric, Unit: in.Unit,
		StartValue: in.Start, TargetValue: in.Target, CurrentValue: in.Start, DueDate: in.DueDate, Status: domain.GoalActive}
	if g.Slug == "" {
		g.Slug = domain.Slugify(in.Title)
	}
	f.goals = append(f.goals, service.GoalView{Goal: g})
	return g, nil
}

func TestProjectCreate(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	budget := int64(5000)
	res := call(t, cs, "hanko_project_create", map[string]any{"name": "Site novo", "description": "Refazer o site", "workspace": "site", "budget_monthly_cents": budget})
	if res.IsError {
		t.Fatal(text(res))
	}
	var out projectCreateOut
	structured(t, res, &out)
	if !out.Created || out.Project.Slug != "site-novo" || out.Project.Name != "Site novo" {
		t.Fatalf("saída: %+v", out)
	}
	in := e.f.m8().projects[0]
	if in.Name != "Site novo" || in.WorkspaceID == nil || in.BudgetMonthlyCents == nil || *in.BudgetMonthlyCents != 5000 || e.f.lastActor.Type != domain.ActorPersona {
		t.Fatalf("entrada do service: %+v ator %+v", in, e.f.lastActor)
	}
	// repetir o slug devolve o projeto existente, sem criar outro
	res = call(t, cs, "hanko_project_create", map[string]any{"name": "Site novo"})
	out = projectCreateOut{}
	structured(t, res, &out)
	if res.IsError || out.Created || out.Project.Slug != "site-novo" || len(e.f.m8().projects) != 1 {
		t.Fatalf("repetição: %+v %d", out, len(e.f.m8().projects))
	}
	for name, args := range map[string]map[string]any{"sem nome": {"name": " "}, "workspace inexistente": {"name": "Outro", "workspace": "nao-existe"}} {
		if r := call(t, cs, "hanko_project_create", args); !r.IsError {
			t.Errorf("%s deveria falhar", name)
		}
	}
}

func TestGoalCreate(t *testing.T) {
	e := setup(t, false)
	e.f.projects = append(e.f.projects, service.ProjectView{Project: domain.Project{ID: domain.NewID(), Slug: "site", Name: "Site"}})
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	res := call(t, cs, "hanko_goal_create", map[string]any{"title": "Baixar o p95", "metric": "p95", "unit": "ms", "start_value": 900, "target_value": 400,
		"due_date": "2026-12-31", "project": "site", "owner": "me"})
	if res.IsError {
		t.Fatal(text(res))
	}
	var out goalCreateOut
	structured(t, res, &out)
	if !out.Created || out.Goal.Slug != "baixar-o-p95" || out.Goal.Start != 900 || out.Goal.Target != 400 || out.Goal.DueDate != "2026-12-31" || out.Goal.Progress != 0 {
		t.Fatalf("saída: %+v", out)
	}
	in := e.f.m8().goals[0]
	if in.ProjectID == nil || in.OwnerPersonaID == nil || *in.OwnerPersonaID != e.f.personas[1].ID || e.f.lastActor.Type != domain.ActorPersona {
		t.Fatalf("entrada do service: %+v", in)
	}
	// o mesmo slug devolve a existente
	res = call(t, cs, "hanko_goal_create", map[string]any{"title": "Baixar o p95", "start_value": 1, "target_value": 2})
	out = goalCreateOut{}
	structured(t, res, &out)
	if res.IsError || out.Created || len(e.f.m8().goals) != 1 {
		t.Fatalf("repetição: %+v", out)
	}
	bad := map[string]map[string]any{
		"sem título":    {"title": "", "start_value": 1, "target_value": 2},
		"data ruim":     {"title": "X", "start_value": 1, "target_value": 2, "due_date": "31/12/2026"},
		"projeto":       {"title": "Y", "start_value": 1, "target_value": 2, "project": "nao-existe"},
		"dona inválida": {"title": "Z", "start_value": 1, "target_value": 2, "owner": "fantasma"},
	}
	for name, args := range bad {
		if r := call(t, cs, "hanko_goal_create", args); !r.IsError {
			t.Errorf("%s deveria falhar", name)
		}
	}
}

func TestOwnerMeNeedsToken(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "")
	defer cs.Close()
	if r := call(t, cs, "hanko_goal_create", map[string]any{"company": "acme", "title": "G", "start_value": 1, "target_value": 2, "owner": "me"}); !r.IsError {
		t.Fatal("owner=me sem token deveria falhar")
	}
}
