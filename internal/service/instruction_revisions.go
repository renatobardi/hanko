package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// Revisões das instruções da persona (ADR-0052). Toda escrita de personas.instructions grava uma revisão na mesma
// transação; restaurar cria uma revisão nova; a persona propõe pelo gate kind=instruction e um humano aprova.

const (
	// maxInstructionRunes é o maior texto de instruções (e de proposta) aceito.
	maxInstructionRunes = 100000
	// maxInstructionDiffBytes é o maior texto comparado no diff entre revisões.
	maxInstructionDiffBytes = 512 << 10
	// maxGateDiffBytes é quanto do diff vai no corpo do gate da proposta.
	maxGateDiffBytes = 30000
)

// instructionRecord descreve uma revisão a gravar.
type instructionRecord struct {
	companyID, personaID domain.ID
	current              int // revisão vigente antes desta (a nova é current+1)
	body                 string
	author, actor        domain.Actor
	reason               string
	origin               domain.RevisionOrigin
	restoredFrom         *int
	gateID               *domain.ID
}

func cleanReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if !domain.ValidRevisionReason(reason) {
		return "", domain.Invalid("reason", fmt.Sprintf("too long (max %d characters)", domain.MaxRevisionReason))
	}
	return reason, nil
}

// recordInstructionRevision grava a revisão seguinte e o fato persona.instructions_changed. NÃO altera
// personas.instructions: quem chama já escreveu o texto. Devolve o número da revisão criada.
func (t *txc) recordInstructionRevision(r instructionRecord) (int, error) {
	rev := r.current + 1
	row, err := t.q.InsertPersonaInstructionRevision(t.ctx, store.InsertPersonaInstructionRevisionParams{ID: domain.NewID(),
		CompanyID: r.companyID, PersonaID: r.personaID, Revision: i32(rev), Body: r.body, Sha256: domain.RevisionSHA(r.body),
		AuthorType: string(r.author.Type), AuthorID: r.author.ID, ActorType: string(r.actor.Type), ActorID: r.actor.ID,
		Reason: r.reason, Origin: string(r.origin), RestoredFrom: intToPtr32(r.restoredFrom), GateID: r.gateID, Now: t.now})
	if err != nil {
		return 0, mapErr(err, "insert instruction revision")
	}
	if err := t.q.SetPersonaInstructionRevision(t.ctx, store.SetPersonaInstructionRevisionParams{CompanyID: r.companyID,
		ID: r.personaID, Revision: i32(rev)}); err != nil {
		return 0, fmt.Errorf("set instruction revision: %w", err)
	}
	data := map[string]any{"revision": rev, "sha256": row.Sha256, "origin": r.origin, "bytes": len(r.body)}
	if r.reason != "" {
		data["reason"] = r.reason
	}
	if r.restoredFrom != nil {
		data["restored_from"] = *r.restoredFrom
	}
	if r.gateID != nil {
		data["gate_id"] = *r.gateID
	}
	if r.author != r.actor {
		data["author"] = r.author
	}
	return rev, t.activity(r.actor, r.companyID, "persona.instructions_changed", "persona", r.personaID, data, "")
}

func intToPtr32(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := i32(*v)
	return &n
}

func instructionRevisionFromRow(r store.PersonaInstructionRevision) domain.InstructionRevision {
	return domain.InstructionRevision{ID: r.ID, CompanyID: r.CompanyID, PersonaID: r.PersonaID,
		RevisionMeta: domain.RevisionMeta{Revision: int(r.Revision), SHA256: r.Sha256, Author: actorOf(r.AuthorType, r.AuthorID),
			Actor: actorOf(r.ActorType, r.ActorID), Reason: r.Reason, CreatedAt: r.CreatedAt},
		Origin: domain.RevisionOrigin(r.Origin), RestoredFrom: intPtr(r.RestoredFrom), GateID: r.GateID,
		BodyBytes: len(r.Body), Body: r.Body}
}

// ListInstructionRevisions devolve as revisões das instruções da persona, da mais nova para a mais antiga, sem o corpo.
func (s *Service) ListInstructionRevisions(ctx context.Context, companyID, personaID domain.ID) ([]domain.InstructionRevision, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID}); err != nil {
		return nil, mapErr(err, "get persona")
	}
	rows, err := q.ListPersonaInstructionRevisions(ctx, store.ListPersonaInstructionRevisionsParams{CompanyID: companyID, PersonaID: personaID, Lim: 500})
	if err != nil {
		return nil, fmt.Errorf("list instruction revisions: %w", err)
	}
	out := make([]domain.InstructionRevision, len(rows))
	for i, r := range rows {
		out[i] = domain.InstructionRevision{ID: r.ID, CompanyID: r.CompanyID, PersonaID: r.PersonaID,
			RevisionMeta: domain.RevisionMeta{Revision: int(r.Revision), SHA256: r.Sha256, Author: actorOf(r.AuthorType, r.AuthorID),
				Actor: actorOf(r.ActorType, r.ActorID), Reason: r.Reason, CreatedAt: r.CreatedAt},
			Origin: domain.RevisionOrigin(r.Origin), RestoredFrom: intPtr(r.RestoredFrom), GateID: r.GateID, BodyBytes: int(r.BodyBytes)}
	}
	return out, nil
}

// GetInstructionRevision devolve uma revisão com o corpo.
func (s *Service) GetInstructionRevision(ctx context.Context, companyID, personaID domain.ID, revision int) (domain.InstructionRevision, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.InstructionRevision{}, err
	}
	r, err := s.read().GetPersonaInstructionRevision(ctx, store.GetPersonaInstructionRevisionParams{CompanyID: companyID,
		PersonaID: personaID, Revision: i32(revision)})
	if err != nil {
		return domain.InstructionRevision{}, mapErr(err, "get instruction revision")
	}
	return instructionRevisionFromRow(r), nil
}

// DiffInstructionRevisions compara duas revisões (de from para to).
func (s *Service) DiffInstructionRevisions(ctx context.Context, companyID, personaID domain.ID, from, to int) (textdiff.Result, error) {
	a, err := s.GetInstructionRevision(ctx, companyID, personaID, from)
	if err != nil {
		return textdiff.Result{}, err
	}
	b, err := s.GetInstructionRevision(ctx, companyID, personaID, to)
	if err != nil {
		return textdiff.Result{}, err
	}
	if len(a.Body) > maxInstructionDiffBytes || len(b.Body) > maxInstructionDiffBytes {
		return textdiff.Result{}, domain.Invalid("revision", fmt.Sprintf("too large to compare (max %d bytes)", maxInstructionDiffBytes))
	}
	return textdiff.Lines(a.Body, b.Body), nil
}

// RestoreInstructionRevision volta as instruções ao texto de uma revisão antiga criando uma revisão NOVA (admin).
// Restaurar o texto que já vale não muda nada.
func (s *Service) RestoreInstructionRevision(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, revision int, reason string) (domain.Persona, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Persona{}, err
	}
	reason, err := cleanReason(reason)
	if err != nil {
		return domain.Persona{}, err
	}
	var out domain.Persona
	err = s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetPersonaByIDForUpdate(t.ctx, store.GetPersonaByIDForUpdateParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		old, err := t.q.GetPersonaInstructionRevision(t.ctx, store.GetPersonaInstructionRevisionParams{CompanyID: companyID,
			PersonaID: personaID, Revision: i32(revision)})
		if err != nil {
			return mapErr(err, "get instruction revision")
		}
		if old.Body == cur.Instructions {
			out = personaFromRow(cur)
			return nil
		}
		from := revision
		row, err := t.applyInstructions(cur, old.Body, actorOf(old.AuthorType, old.AuthorID), actor, reason, domain.RevisionRestore, &from, nil)
		if err != nil {
			return err
		}
		out = personaFromRow(row)
		return nil
	})
	return out, err
}

// applyInstructions escreve o texto na persona (já travada, cur) e grava a revisão. Devolve a linha nova.
func (t *txc) applyInstructions(cur store.Persona, body string, author, actor domain.Actor, reason string,
	origin domain.RevisionOrigin, restoredFrom *int, gateID *domain.ID) (store.Persona, error) {
	rev, err := t.recordInstructionRevision(instructionRecord{companyID: cur.CompanyID, personaID: cur.ID, current: int(cur.InstructionRevision),
		body: body, author: author, actor: actor, reason: reason, origin: origin, restoredFrom: restoredFrom, gateID: gateID})
	if err != nil {
		return store.Persona{}, err
	}
	row, err := t.q.SetPersonaInstructions(t.ctx, store.SetPersonaInstructionsParams{CompanyID: cur.CompanyID, ID: cur.ID,
		Instructions: body, Revision: i32(rev), Now: t.now})
	if err != nil {
		return store.Persona{}, mapErr(err, "set persona instructions")
	}
	return row, nil
}

// ProposeInstructionsInput é a mudança que a persona sugere para as próprias instruções.
type ProposeInstructionsInput struct {
	Body    string
	Reason  string
	RunID   *domain.ID
	EventID string // idempotência: repetir devolve o gate existente
}

// ProposePersonaInstructions abre o gate kind=instruction com a mudança sugerida (só a própria persona, para si). O
// gate NÃO bloqueia a persona nem a task; mostra o diff contra a revisão-base. Aprovar aplica (ver
// instructionGateDecided).
func (s *Service) ProposePersonaInstructions(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, in ProposeInstructionsInput) (GateView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return GateView{}, err
	}
	if actor.Type != domain.ActorPersona || actor.ID == nil || *actor.ID != personaID {
		return GateView{}, fmt.Errorf("propose instructions: only the persona itself proposes changes to its instructions (humans edit directly): %w", domain.ErrNotAllowed)
	}
	reason, err := cleanReason(in.Reason)
	if err != nil {
		return GateView{}, err
	}
	switch n := utf8.RuneCountInString(in.Body); {
	case strings.TrimSpace(in.Body) == "":
		return GateView{}, domain.Invalid("instructions", "required")
	case n > maxInstructionRunes:
		return GateView{}, domain.Invalid("instructions", fmt.Sprintf("too long (max %d characters)", maxInstructionRunes))
	case !utf8.ValidString(in.Body):
		return GateView{}, domain.Invalid("instructions", "must be valid UTF-8")
	}
	var out GateView
	run := func() error {
		return s.withTx(ctx, func(t *txc) error {
			if in.EventID != "" {
				ex, err := t.q.GetGateByEventID(ctx, store.GetGateByEventIDParams{CompanyID: companyID, EventID: &in.EventID})
				if err == nil {
					out, err = loadGateView(ctx, t.q, companyID, ex.ID)
					return err
				}
				if !isNoRows(err) {
					return fmt.Errorf("lookup event: %w", err)
				}
			}
			p, err := t.q.GetPersonaByIDForUpdate(ctx, store.GetPersonaByIDForUpdateParams{CompanyID: companyID, ID: personaID})
			if err != nil {
				return mapErr(err, "get persona")
			}
			if in.Body == p.Instructions {
				return domain.Invalid("instructions", "same as the current instructions")
			}
			if in.RunID != nil {
				r, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: *in.RunID})
				if err != nil || r.PersonaID != personaID {
					return domain.Invalid("run_id", "run not found for this persona")
				}
			}
			title := "Mudar as instruções de " + p.Name
			if reason != "" {
				title += ": " + clipRunes(reason, 120)
			}
			g, err := t.createGate(actor, companyID, RequestGateInput{Kind: domain.GateInstruction, Title: title,
				Body: instructionProposalBody(p, in.Body, reason), RunID: in.RunID, EventID: in.EventID}, &personaID)
			if err != nil {
				return err
			}
			if _, err := t.q.CreatePersonaInstructionProposal(ctx, store.CreatePersonaInstructionProposalParams{ID: domain.NewID(),
				CompanyID: companyID, PersonaID: personaID, GateID: g.ID, BaseRevision: p.InstructionRevision, Body: in.Body,
				Reason: reason, RunID: in.RunID, Now: t.now}); err != nil {
				return mapErr(err, "create instruction proposal")
			}
			out, err = loadGateView(ctx, t.q, companyID, g.ID)
			return err
		})
	}
	err = run()
	if err != nil && in.EventID != "" && isUniqueOn(err, "gates_event_uq") {
		err = run()
	}
	if err != nil {
		return GateView{}, err
	}
	return out, nil
}

// instructionProposalBody é o corpo markdown do gate: persona, revisão-base, motivo e o diff.
func instructionProposalBody(p store.Persona, body, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** propõe mudar as próprias instruções (a partir da revisão %d).\n\n", p.Name, p.InstructionRevision)
	if reason != "" {
		fmt.Fprintf(&b, "**Motivo:** %s\n\n", reason)
	}
	b.WriteString("### Mudanças\n\n```diff\n")
	b.WriteString(textdiff.Unified(textdiff.Lines(p.Instructions, body), 3, maxGateDiffBytes))
	b.WriteString("```\n")
	return b.String()
}

// instructionGateDecided roda dentro do DecideGate de um gate instruction: aprovado, aplica o texto proposto como
// revisão nova (autor = a persona que propôs, ator = o humano que aprovou) — desde que a revisão-base ainda seja a
// vigente; senão a decisão falha com conflito (alguém editou no meio: rejeite e peça de novo). Rejeitar e pedir
// mudanças não tocam na persona.
func (t *txc) instructionGateDecided(actor domain.Actor, g domain.Gate, decision domain.GateStatus) error {
	if decision != domain.GateApproved {
		return nil
	}
	prop, err := t.q.GetPersonaInstructionProposalByGate(t.ctx, store.GetPersonaInstructionProposalByGateParams{CompanyID: g.CompanyID, GateID: g.ID})
	if err != nil {
		return mapErr(err, "get instruction proposal")
	}
	cur, err := t.q.GetPersonaByIDForUpdate(t.ctx, store.GetPersonaByIDForUpdateParams{CompanyID: g.CompanyID, ID: prop.PersonaID})
	if err != nil {
		return mapErr(err, "get persona")
	}
	if cur.InstructionRevision != prop.BaseRevision {
		return fmt.Errorf("the instructions changed since the proposal (revision %d, proposal made on %d): reject it and ask again: %w",
			cur.InstructionRevision, prop.BaseRevision, domain.ErrConflict)
	}
	if cur.Instructions == prop.Body {
		return nil
	}
	_, err = t.applyInstructions(cur, prop.Body, domain.PersonaActor(prop.PersonaID), actor, prop.Reason, domain.RevisionProposal, nil, &g.ID)
	return err
}

// RecordRunInstructions grava na run a revisão das instruções com que ela rodou (chamado pelo executor ao começar,
// com a revisão do mesmo RunContext de onde veio o texto do prompt). Revisão 0 ou inexistente não grava nada.
func (s *Service) RecordRunInstructions(ctx context.Context, companyID, runID domain.ID, revision int) error {
	if revision < 1 {
		return nil
	}
	if _, err := s.read().SetRunInstructionRevision(ctx, store.SetRunInstructionRevisionParams{CompanyID: companyID, RunID: runID,
		Revision: i32(revision)}); err != nil {
		return fmt.Errorf("record run instructions: %w", err)
	}
	return nil
}
