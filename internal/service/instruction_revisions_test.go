package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Revisões append-only das instruções da persona (ADR-0052).

func hireWithInstructions(t *testing.T, e *env, text string) domain.Persona {
	t.Helper()
	return e.persona(t, "Ada", func(in *service.HirePersonaInput) { in.Instructions = text })
}

func TestInstructionRevisionsHireEditRestore(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := hireWithInstructions(t, e, "Você é a Ada.\nSeja breve.")
	if p.InstructionRevision != 1 {
		t.Fatalf("revisão ao contratar = %d, want 1", p.InstructionRevision)
	}
	revs, err := e.svc.ListInstructionRevisions(ctx, e.co.ID, p.ID)
	if err != nil || len(revs) != 1 || revs[0].Origin != domain.RevisionHire || revs[0].SHA256 != domain.RevisionSHA(p.Instructions) || revs[0].Body != "" {
		t.Fatalf("lista inicial: %+v %v", revs, err)
	}

	// mudar o texto cria a revisão 2, com o motivo; o mesmo texto não cria nada
	up, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{
		Instructions: ptr("Você é a Ada.\nSeja detalhista."), InstructionsReason: "ficou curto demais"})
	if err != nil || up.InstructionRevision != 2 || up.Instructions != "Você é a Ada.\nSeja detalhista." {
		t.Fatalf("edição: %+v %v", up, err)
	}
	same, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Instructions: ptr(up.Instructions)})
	if err != nil || same.InstructionRevision != 2 {
		t.Fatalf("mesmo texto: %+v %v", same, err)
	}
	// mudar outro campo não mexe nas revisões
	other, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Title: ptr("CTO")})
	if err != nil || other.InstructionRevision != 2 {
		t.Fatalf("outro campo: %+v %v", other, err)
	}

	r2, err := e.svc.GetInstructionRevision(ctx, e.co.ID, p.ID, 2)
	if err != nil || r2.Reason != "ficou curto demais" || r2.Origin != domain.RevisionEdit || r2.Author.Type != domain.ActorHuman || r2.Body != up.Instructions {
		t.Fatalf("revisão 2: %+v %v", r2, err)
	}

	// restaurar a 1 cria a 3 com o texto antigo; restaurar o que já vale não faz nada
	rest, err := e.svc.RestoreInstructionRevision(ctx, domain.HumanActor, e.co.ID, p.ID, 1, "voltei")
	if err != nil || rest.InstructionRevision != 3 || rest.Instructions != "Você é a Ada.\nSeja breve." {
		t.Fatalf("restaurar: %+v %v", rest, err)
	}
	r3, _ := e.svc.GetInstructionRevision(ctx, e.co.ID, p.ID, 3)
	if r3.Origin != domain.RevisionRestore || r3.RestoredFrom == nil || *r3.RestoredFrom != 1 || r3.Reason != "voltei" {
		t.Fatalf("revisão 3: %+v", r3)
	}
	again, err := e.svc.RestoreInstructionRevision(ctx, domain.HumanActor, e.co.ID, p.ID, 3, "")
	if err != nil || again.InstructionRevision != 3 {
		t.Fatalf("restaurar a vigente: %+v %v", again, err)
	}
	_, err = e.svc.RestoreInstructionRevision(ctx, domain.HumanActor, e.co.ID, p.ID, 99, "")
	wantErr(t, err, domain.ErrNotFound)

	// lista (mais nova primeiro) e diff
	revs, _ = e.svc.ListInstructionRevisions(ctx, e.co.ID, p.ID)
	if len(revs) != 3 || revs[0].Revision != 3 || revs[2].Revision != 1 {
		t.Fatalf("lista: %+v", revs)
	}
	d, err := e.svc.DiffInstructionRevisions(ctx, e.co.ID, p.ID, 1, 2)
	if err != nil || d.Added != 1 || d.Removed != 1 {
		t.Fatalf("diff: %+v %v", d, err)
	}

	// o motivo longo demais é recusado
	_, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{
		Instructions: ptr("x"), InstructionsReason: strings.Repeat("a", 501)})
	wantErr(t, err, domain.ErrInvalid)

	// cada revisão é um fato da atividade
	acts, err := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "persona", SubjectID: &p.ID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range acts {
		if a.Verb == "persona.instructions_changed" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("fatos persona.instructions_changed = %d, want 3", n)
	}
}

func TestInstructionRevisionsAreAppendOnly(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := hireWithInstructions(t, e, "texto")
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE persona_instruction_revisions SET body = 'adulterado' WHERE persona_id = $1`, p.ID); err == nil {
		t.Fatal("UPDATE numa revisão deveria ser recusado pelo banco")
	}
	// apagar a persona leva as revisões junto (cascata)
	if _, err := e.svc.Pool.Exec(ctx, `DELETE FROM personas WHERE id = $1`, p.ID); err != nil {
		t.Fatalf("apagar a persona: %v", err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM persona_instruction_revisions WHERE persona_id = $1`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("revisões após apagar a persona: %d %v", n, err)
	}
}

func TestRunRecordsInstructionRevision(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := hireWithInstructions(t, e, "v1")
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Instructions: ptr("v2")}); err != nil {
		t.Fatal(err)
	}
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil || rc.Persona.InstructionRevision != 2 || rc.Persona.Instructions != "v2" {
		t.Fatalf("contexto: %+v %v", rc.Persona, err)
	}
	if err := e.svc.RecordRunInstructions(ctx, e.co.ID, run.ID, rc.Persona.InstructionRevision); err != nil {
		t.Fatal(err)
	}
	// uma mudança depois não reescreve o que a run usou; a primeira gravação vale
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Instructions: ptr("v3")}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RecordRunInstructions(ctx, e.co.ID, run.ID, 3); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.GetRun(ctx, e.co.ID, run.ID)
	if err != nil || got.InstructionRevision == nil || *got.InstructionRevision != 2 || got.InstructionSHA256 != domain.RevisionSHA("v2") {
		t.Fatalf("run: rev=%v sha=%s %v", got.InstructionRevision, got.InstructionSHA256, err)
	}
	// revisão 0 ou inexistente não grava nada nem falha
	if err := e.svc.RecordRunInstructions(ctx, e.co.ID, run.ID, 0); err != nil {
		t.Fatal(err)
	}
}

func TestPersonaProposesInstructionsThroughGate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := hireWithInstructions(t, e, "base\nlinha 2")
	self := domain.PersonaActor(p.ID)

	// só a própria persona propõe
	_, err := e.svc.ProposePersonaInstructions(ctx, domain.HumanActor, e.co.ID, p.ID, service.ProposeInstructionsInput{Body: "novo"})
	wantErr(t, err, domain.ErrNotAllowed)
	other := e.persona(t, "Bia", nil)
	_, err = e.svc.ProposePersonaInstructions(ctx, domain.PersonaActor(other.ID), e.co.ID, p.ID, service.ProposeInstructionsInput{Body: "novo"})
	wantErr(t, err, domain.ErrNotAllowed)
	// o gate instruction não se abre pelo caminho genérico
	_, err = e.svc.RequestGate(ctx, self, e.co.ID, service.RequestGateInput{Kind: domain.GateInstruction, Title: "x"})
	wantErr(t, err, domain.ErrInvalid)
	// texto igual ao vigente e vazio são recusados
	_, err = e.svc.ProposePersonaInstructions(ctx, self, e.co.ID, p.ID, service.ProposeInstructionsInput{Body: "base\nlinha 2"})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.ProposePersonaInstructions(ctx, self, e.co.ID, p.ID, service.ProposeInstructionsInput{Body: "  "})
	wantErr(t, err, domain.ErrInvalid)

	g, err := e.svc.ProposePersonaInstructions(ctx, self, e.co.ID, p.ID, service.ProposeInstructionsInput{
		Body: "base\nlinha 2 revisada\nlinha 3", Reason: "aprendi isto", EventID: "prop-1"})
	if err != nil || g.Kind != domain.GateInstruction || g.Status != domain.GatePending || g.PersonaID == nil {
		t.Fatalf("proposta: %+v %v", g.Gate, err)
	}
	if !strings.Contains(g.Body, "aprendi isto") || !strings.Contains(g.Body, "- linha 2") || !strings.Contains(g.Body, "+ linha 2 revisada") {
		t.Fatalf("corpo do gate sem o diff:\n%s", g.Body)
	}
	// idempotente por event_id
	g2, err := e.svc.ProposePersonaInstructions(ctx, self, e.co.ID, p.ID, service.ProposeInstructionsInput{Body: "outro", EventID: "prop-1"})
	if err != nil || g2.ID != g.ID {
		t.Fatalf("event_id: %+v %v", g2.Gate, err)
	}
	// a persona não fica bloqueada nem as instruções mudam antes da decisão
	cur, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if cur.Status == domain.PersonaAwaitingGate || cur.Instructions != "base\nlinha 2" || cur.InstructionRevision != 1 {
		t.Fatalf("persona após propor: %+v", cur)
	}

	// aprovar aplica a revisão: autor = a persona, ator = o humano
	dec, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved})
	if err != nil || dec.Status != domain.GateApproved {
		t.Fatalf("aprovar: %+v %v", dec.Gate, err)
	}
	cur, _ = e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if cur.InstructionRevision != 2 || cur.Instructions != "base\nlinha 2 revisada\nlinha 3" {
		t.Fatalf("persona após aprovar: %+v", cur)
	}
	r2, _ := e.svc.GetInstructionRevision(ctx, e.co.ID, p.ID, 2)
	if r2.Origin != domain.RevisionProposal || r2.Author.Type != domain.ActorPersona || r2.Actor.Type != domain.ActorHuman || r2.GateID == nil || *r2.GateID != g.ID || r2.Reason != "aprendi isto" {
		t.Fatalf("revisão aprovada: %+v", r2)
	}
}

func TestInstructionProposalRejectedOrStale(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := hireWithInstructions(t, e, "a")
	self := domain.PersonaActor(p.ID)

	// rejeitar não toca na persona
	g, err := e.svc.ProposePersonaInstructions(ctx, self, e.co.ID, p.ID, service.ProposeInstructionsInput{Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateRejected}); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if cur.Instructions != "a" || cur.InstructionRevision != 1 {
		t.Fatalf("após rejeitar: %+v", cur)
	}

	// um humano edita no meio: aprovar a proposta antiga falha e o gate segue pendente
	g, err = e.svc.ProposePersonaInstructions(ctx, self, e.co.ID, p.ID, service.ProposeInstructionsInput{Body: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Instructions: ptr("editado")}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved})
	wantErr(t, err, domain.ErrConflict)
	got, _ := e.svc.GetGate(ctx, e.co.ID, g.ID)
	if got.Status != domain.GatePending {
		t.Fatalf("gate após conflito: %s", got.Status)
	}
	cur, _ = e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if cur.Instructions != "editado" || cur.InstructionRevision != 2 {
		t.Fatalf("a edição humana deve permanecer: %+v", cur)
	}
	// rejeitar a proposta velha continua possível
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateRejected}); err != nil {
		t.Fatal(err)
	}
}

func TestImportedPersonaGetsRevision(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := hireWithInstructions(t, e, "Texto exportado.")
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "clone"})
	if err != nil {
		t.Fatal(err)
	}
	pp, err := e.svc.GetPersona(ctx, res.Company.ID, p.Slug)
	if err != nil || pp.InstructionRevision != 1 {
		t.Fatalf("persona importada: %+v %v", pp, err)
	}
	revs, _ := e.svc.ListInstructionRevisions(ctx, res.Company.ID, pp.ID)
	if len(revs) != 1 || revs[0].Origin != domain.RevisionImport {
		t.Fatalf("revisões da importada: %+v", revs)
	}
}
