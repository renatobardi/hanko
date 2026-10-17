package ui

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Instruções da persona: edição com motivo, histórico de revisões, diff e restauração (ADR-0052).

type instructionsBackend interface {
	ListInstructionRevisions(ctx context.Context, companyID, personaID domain.ID) ([]domain.InstructionRevision, error)
	GetInstructionRevision(ctx context.Context, companyID, personaID domain.ID, revision int) (domain.InstructionRevision, error)
	DiffInstructionRevisions(ctx context.Context, companyID, personaID domain.ID, from, to int) (textdiff.Result, error)
	RestoreInstructionRevision(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, revision int, reason string) (domain.Persona, error)
}

func (s *server) instructionsBE() (instructionsBackend, bool) {
	b, ok := s.be.(instructionsBackend)
	return b, ok
}

func (s *server) instructionsRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	mux.HandleFunc("GET /c/{company}/personas/{persona}/instrucoes", co(s.handleInstructions))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/instrucoes", post(co(s.handleInstructionsSave)))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/instrucoes/restaurar", post(co(s.handleInstructionsRestore)))
}

var instructionNotices = map[string]string{
	"salva":      i18n.N("Instruções salvas: uma revisão nova foi criada."),
	"restaurada": i18n.N("Revisão restaurada: uma revisão nova foi criada com o texto dela."),
	"igual":      i18n.N("O texto não mudou: nenhuma revisão nova."),
}

func instructionOrigin(ctx context.Context, o domain.RevisionOrigin) string {
	switch o {
	case domain.RevisionBackfill:
		return i18n.T(ctx, "texto anterior às revisões")
	case domain.RevisionHire:
		return i18n.T(ctx, "contratação")
	case domain.RevisionEdit:
		return i18n.T(ctx, "edição")
	case domain.RevisionRestore:
		return i18n.T(ctx, "restauração")
	case domain.RevisionProposal:
		return i18n.T(ctx, "proposta da persona aprovada")
	case domain.RevisionImport:
		return i18n.T(ctx, "importação")
	}
	return string(o)
}

func (s *server) handleInstructions(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderInstructions(w, r, co, http.StatusOK, "", "", "")
}

// renderInstructions desenha a página; errMsg/typed/reason carregam um envio que falhou.
func (s *server) renderInstructions(w http.ResponseWriter, r *http.Request, co domain.Company, status int, errMsg, typed, reason string) {
	ib, ok := s.instructionsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	self := base + "/personas/" + p.Slug + "/instrucoes"
	revs, err := ib.ListInstructionRevisions(ctx, co.ID, p.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc := loadLoc(co.Timezone)
	vm := views.InstructionsVM{Name: p.Name, Slug: p.Slug, Back: base + "/personas/" + p.Slug, Revision: p.InstructionRevision,
		Current: p.Instructions, Error: errMsg, Reason: reason, Notice: i18n.T(ctx, instructionNotices[r.URL.Query().Get("ok")])}
	if typed != "" {
		vm.Current = typed
	}
	if views.RoleFrom(ctx).AtLeast(domain.MemberAdmin) {
		vm.EditURL = self
	}
	for i, v := range revs {
		row := views.InstructionRow{Revision: v.Revision, Origin: instructionOrigin(ctx, v.Origin), Author: actorLabel(ctx, v.Author),
			Actor: actorLabel(ctx, v.Actor), Reason: v.Reason, Size: fmt.Sprintf("%.1f KiB", float64(v.BodyBytes)/1024), SHA: v.SHA256[:12],
			When: agoCtx(ctx, v.CreatedAt, s.now(), loc), ViewHref: self + "?ver=" + strconv.Itoa(v.Revision) + "#revisao",
			Current: v.Revision == p.InstructionRevision}
		if vm.EditURL != "" {
			row.RestoreAction = self + "/restaurar"
		}
		if i+1 < len(revs) {
			row.DiffHref = self + "?de=" + strconv.Itoa(revs[i+1].Revision) + "&para=" + strconv.Itoa(v.Revision) + "#diff"
		}
		vm.Rows = append(vm.Rows, row)
	}
	// propostas da própria persona esperando decisão
	if gs, err := s.be.ListGates(ctx, co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending},
		Kinds: []domain.GateKind{domain.GateInstruction}, PersonaID: &p.ID, Limit: 10}); err == nil {
		for _, g := range gs {
			vm.Proposals = append(vm.Proposals, views.InstructionProposal{Title: g.Title, Href: base + "/gates/" + g.ID.String(),
				When: agoCtx(ctx, g.CreatedAt, s.now(), loc)})
		}
	}
	q := r.URL.Query()
	if rev, err := strconv.Atoi(q.Get("ver")); err == nil && rev > 0 {
		v, err := ib.GetInstructionRevision(ctx, co.ID, p.ID, rev)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		vm.View = &views.InstructionView{Revision: v.Revision, Body: v.Body, CloseHref: self}
	}
	from, ferr := strconv.Atoi(q.Get("de"))
	to, terr := strconv.Atoi(q.Get("para"))
	if ferr == nil && terr == nil && from > 0 && to > 0 {
		d := &views.DiffVM{FromVersion: from, ToVersion: to, CloseHref: self}
		res, err := ib.DiffInstructionRevisions(ctx, co.ID, p.ID, from, to)
		if err != nil {
			_, _, d.Error = friendly(ctx, err)
		} else {
			d.Added, d.Removed, d.Coarse, d.Same = res.Added, res.Removed, res.Coarse, !res.Changed()
			d.Lines, d.Cut = diffLines(res)
		}
		vm.Diff = d
	}
	sh := s.shell(r, co, "personas", p.Name, views.Crumb{Label: i18n.T(ctx, "Personas"), Href: base + "/personas"},
		views.Crumb{Label: p.Name, Href: base + "/personas/" + p.Slug}, views.Crumb{Label: i18n.T(ctx, "Instruções")})
	s.render(w, r, status, views.InstructionsPage(sh, vm))
}

func (s *server) handleInstructionsSave(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	text := r.PostFormValue("instructions")
	reason := trimForm(r, "reason")
	saved, err := s.be.UpdatePersona(ctx, actorFrom(ctx), co.ID, p.ID, service.UpdatePersonaInput{Instructions: &text, InstructionsReason: reason})
	if err != nil {
		if st, title, msg := friendly(ctx, err); st < 500 && st != http.StatusForbidden && st != http.StatusNotFound {
			s.renderInstructions(w, r, co, st, title+": "+msg, text, reason)
			return
		}
		s.fail(w, r, &co, err)
		return
	}
	ok := "salva"
	if saved.InstructionRevision == p.InstructionRevision {
		ok = "igual"
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"/instrucoes?ok="+ok)
}

func (s *server) handleInstructionsRestore(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.instructionsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	rev, err := strconv.Atoi(trimForm(r, "revision"))
	if err != nil || rev < 1 {
		s.fail(w, r, &co, domain.Invalid("revision", i18n.T(ctx, "revisão inválida")))
		return
	}
	if _, err := ib.RestoreInstructionRevision(ctx, actorFrom(ctx), co.ID, p.ID, rev, ""); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"/instrucoes?ok=restaurada")
}
