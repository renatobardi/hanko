package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// Gates estendidos (ADR-0041): pergunta, plano, reenvio com revisões, audiência e contratação com ficha.

// maxPendingHireGates é quantos pedidos de contratação pendentes uma persona pode ter ao mesmo tempo.
const maxPendingHireGates = 5

// ---------- preparo do pedido ----------

// prepareGateExtras valida o que é específico do tipo (formulário, ficha, plano) e a audiência, e monta o payload.
// Roda antes da transação do RequestGate (lê o artefato do plano e confere membros).
func (s *Service) prepareGateExtras(ctx context.Context, actor domain.Actor, companyID domain.ID, in *RequestGateInput) error {
	if err := in.Audience.Validate(); err != nil {
		return err
	}
	for _, u := range in.Audience.Users {
		if _, err := s.read().GetMemberRole(ctx, store.GetMemberRoleParams{CompanyID: companyID, UserID: u}); err != nil {
			if isNoRows(err) {
				return domain.Invalid("audience.users", fmt.Sprintf("%s não é membro da empresa", u))
			}
			return fmt.Errorf("audience member: %w", err)
		}
	}
	switch in.Kind {
	case domain.GateQuestion:
		if in.Form == nil {
			return domain.Invalid("form", "obrigatório num gate question")
		}
		if in.Hire != nil || in.Artifact != "" {
			return domain.Invalid("kind", "ficha de contratação e plano não valem numa pergunta")
		}
		if err := in.Form.Validate(); err != nil {
			return err
		}
		raw, err := json.Marshal(in.Form)
		if err != nil {
			return fmt.Errorf("marshal form: %w", err)
		}
		in.payload = raw
	case domain.GateHire:
		if in.Form != nil || in.Artifact != "" {
			return domain.Invalid("kind", "formulário e plano não valem numa contratação")
		}
		if in.Hire != nil {
			raw, err := s.hirePayload(ctx, actor, companyID, *in.Hire)
			if err != nil {
				return err
			}
			in.payload = raw
		}
	case domain.GatePlan:
		if in.Form != nil || in.Hire != nil {
			return domain.Invalid("kind", "formulário e ficha de contratação não valem num plano")
		}
		if in.TaskID == nil {
			return domain.Invalid("task", "um plano é sempre sobre uma task")
		}
		body, payload, err := s.resolvePlan(ctx, companyID, *in.TaskID, in.Artifact, in.Body)
		if err != nil {
			return err
		}
		in.Body, in.payload = body, payload
	default:
		if in.Form != nil || in.Hire != nil || in.Artifact != "" {
			return domain.Invalid("kind", fmt.Sprintf("formulário, ficha e plano não valem num gate %s", in.Kind))
		}
	}
	return nil
}

// hirePayload valida a ficha (sem banco) e a serializa como payload de um gate hire. Humano precisa ser admin.
func (s *Service) hirePayload(ctx context.Context, actor domain.Actor, companyID domain.ID, spec domain.HireSpec) (json.RawMessage, error) {
	if actor.Type == domain.ActorHuman {
		if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
			return nil, err
		}
	}
	if err := spec.Check(); err != nil {
		return nil, err
	}
	if _, err := s.prepareHire(hireInputOf(spec)); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		return nil, fmt.Errorf("marshal hire spec: %w", err)
	}
	return raw, nil
}

// hireInputOf converte a ficha na entrada de contratação.
func hireInputOf(s domain.HireSpec) HirePersonaInput {
	return HirePersonaInput{Name: s.Name, Slug: s.Slug, Title: s.Title, Icon: s.Icon, Instructions: s.Instructions,
		Agent: s.Agent, Model: s.Model, AdapterConfig: s.AdapterConfig, ReportsTo: s.ReportsTo, WorkspaceID: s.WorkspaceID,
		HeartbeatIntervalSec: s.HeartbeatIntervalSec, BudgetMonthlyCents: s.BudgetMonthlyCents, SubscriptionID: s.SubscriptionID,
		RequireReview: s.RequireReview, ReviewerPersona: s.ReviewerPersona, ReviewerAgent: s.ReviewerAgent, ReviewerModel: s.ReviewerModel}
}

// resolvePlan devolve o corpo e o payload de um gate plan: o texto do artefato (id ou nome na task) copiado na hora
// do pedido, ou o texto inline quando não há artefato.
func (s *Service) resolvePlan(ctx context.Context, companyID, taskID domain.ID, ref, body string) (string, json.RawMessage, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		if strings.TrimSpace(body) == "" {
			return "", nil, domain.Invalid("body", "o plano vai no corpo ou num artefato da task (artifact)")
		}
		return body, json.RawMessage(`{}`), nil
	}
	var art domain.Artifact
	if id, err := domain.ParseID(ref); err == nil {
		a, _, err := s.ArtifactContent(ctx, companyID, id)
		if err != nil {
			return "", nil, domain.Invalid("artifact", "artefato não encontrado na empresa")
		}
		art = a
	} else {
		list, err := s.ListArtifacts(ctx, companyID, ArtifactFilter{TaskID: &taskID, Limit: 500})
		if err != nil {
			return "", nil, err
		}
		for _, a := range list {
			if a.Name == ref {
				art = a.Artifact
				break
			}
		}
		if art.ID == (domain.ID{}) {
			return "", nil, domain.Invalid("artifact", fmt.Sprintf("a task não tem o artefato %q: publique o plano com hanko_artifact_put antes", ref))
		}
	}
	if art.TaskID == nil || *art.TaskID != taskID {
		return "", nil, domain.Invalid("artifact", "o artefato do plano precisa ser da mesma task do gate")
	}
	if !strings.HasPrefix(art.Mime, "text/") {
		return "", nil, domain.Invalid("artifact", "o plano precisa ser um artefato de texto (Markdown)")
	}
	_, content, err := s.ArtifactContent(ctx, companyID, art.ID)
	if err != nil {
		return "", nil, err
	}
	if !utf8.Valid(content) || utf8.RuneCount(content) > maxGateBody {
		return "", nil, domain.Invalid("artifact", "o plano é grande demais ou não é texto UTF-8")
	}
	raw, err := json.Marshal(map[string]any{"artifact_id": art.ID, "artifact_name": art.Name, "artifact_version": art.Version, "sha256": art.SHA256})
	if err != nil {
		return "", nil, fmt.Errorf("marshal plan payload: %w", err)
	}
	return string(content), raw, nil
}

// checkHireRequest confere, dentro da transação, o que só o banco sabe: slug livre, referências existentes e o teto de
// pedidos pendentes da persona que pede.
func (t *txc) checkHireRequest(companyID domain.ID, requester *domain.ID, spec domain.HireSpec) error {
	in := hireInputOf(spec)
	prep, err := t.s.prepareHire(in)
	if err != nil {
		return err
	}
	if _, err := t.q.GetPersonaBySlug(t.ctx, store.GetPersonaBySlugParams{CompanyID: companyID, Slug: prep.slug}); err == nil {
		return fmt.Errorf("já existe uma persona com o slug %q: %w", prep.slug, domain.ErrConflict)
	} else if !isNoRows(err) {
		return fmt.Errorf("lookup slug: %w", err)
	}
	for field, id := range map[string]*domain.ID{"reports_to": spec.ReportsTo, "reviewer_persona": spec.ReviewerPersona} {
		if id == nil {
			continue
		}
		if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *id}); err != nil {
			return domain.Invalid("spec."+field, "persona not found in company")
		}
	}
	if spec.WorkspaceID != nil {
		if _, err := t.q.GetWorkspaceByID(t.ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *spec.WorkspaceID}); err != nil {
			return domain.Invalid("spec.workspace_id", "workspace not found in company")
		}
	}
	if spec.SubscriptionID != nil {
		if _, err := t.q.GetSubscriptionByID(t.ctx, store.GetSubscriptionByIDParams{CompanyID: companyID, ID: *spec.SubscriptionID}); err != nil {
			return domain.Invalid("spec.subscription_id", "subscription not found in company")
		}
	}
	if requester != nil {
		n, err := t.q.CountPendingHireGatesForPersona(t.ctx, requester)
		if err != nil {
			return fmt.Errorf("count pending hires: %w", err)
		}
		if n >= maxPendingHireGates {
			return fmt.Errorf("a persona já tem %d pedidos de contratação pendentes: espere uma decisão: %w", n, domain.ErrConflict)
		}
	}
	return nil
}

// hireGateDecided cria a persona quando um gate hire com ficha é aprovado, na mesma transação da decisão.
func (t *txc) hireGateDecided(actor domain.Actor, g domain.Gate, decision domain.GateStatus) error {
	if decision != domain.GateApproved || len(g.Payload) == 0 {
		return nil
	}
	var p struct {
		Spec *domain.HireSpec `json:"spec"`
	}
	if err := json.Unmarshal(g.Payload, &p); err != nil || p.Spec == nil {
		return nil // pedido livre (sem ficha): só a decisão
	}
	spec := *p.Spec
	if spec.ReportsTo == nil && g.PersonaID != nil { // quem contrata é o chefe, salvo a ficha dizer outro
		spec.ReportsTo = g.PersonaID
	}
	prep, err := t.s.prepareHire(hireInputOf(spec))
	if err != nil {
		return err
	}
	_, err = t.createPersona(actor, g.CompanyID, prep, &g.ID)
	return err
}

// ---------- audiência ----------

// callerUser devolve o usuário do contexto e o papel dele na empresa; restricted é falso sem usuário (modo trusted).
func (s *Service) callerUser(ctx context.Context, companyID domain.ID) (user domain.User, role domain.MemberRole, restricted bool, err error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return domain.User{}, domain.MemberOwner, false, nil
	}
	role, err = roleOf(ctx, s.read(), id.User, companyID)
	return id.User, role, true, err
}

// requireAudience recusa (ErrForbidden) quem não está na audiência gravada no gate.
func (s *Service) requireAudience(ctx context.Context, companyID domain.ID, raw []byte) error {
	aud := audienceOf(raw)
	if aud.Empty() {
		return nil
	}
	u, role, restricted, err := s.callerUser(ctx, companyID)
	if err != nil {
		return err
	}
	if restricted && !aud.Allows(u.ID, role) {
		return fmt.Errorf("este gate é restrito a uma audiência da qual você não faz parte: %w", domain.ErrForbidden)
	}
	return nil
}

func audienceOf(raw []byte) domain.GateAudience {
	var a domain.GateAudience
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &a)
	}
	return a
}

// ---------- pergunta: responder, dispensar, reabrir ----------

// gateForm lê o formulário de um gate question.
func gateForm(g domain.Gate) (domain.QuestionForm, error) {
	var f domain.QuestionForm
	if err := json.Unmarshal(g.Payload, &f); err != nil || len(f.Fields) == 0 {
		return domain.QuestionForm{}, fmt.Errorf("o gate %s não tem formulário: %w", g.ID, domain.ErrInvalid)
	}
	return f, nil
}

// gateAnswerMarkdown é a resposta de uma question em markdown ("" quando não há).
func gateAnswerMarkdown(g domain.Gate) string {
	if g.Kind != domain.GateQuestion || len(g.Answer) == 0 {
		return ""
	}
	f, err := gateForm(g)
	if err != nil {
		return ""
	}
	var a domain.QuestionAnswer
	if err := json.Unmarshal(g.Answer, &a); err != nil {
		return ""
	}
	return "**Respostas**\n\n" + f.Render(a)
}

// AnswerGateInput é a resposta do humano a um gate question.
type AnswerGateInput struct {
	Answers map[string][]string // chave do campo → valores
	Note    string
}

// AnswerGate responde um gate question pendente (humano, member ou mais, dentro da audiência). A resposta é validada
// contra o formulário e gravada; o gate vira approved; a resposta vai para a thread da task e a persona é acordada.
func (s *Service) AnswerGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in AnswerGateInput) (GateView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return GateView{}, err
	}
	if actor.Type != domain.ActorHuman {
		return GateView{}, fmt.Errorf("answer gate: only a human answers a question: %w", domain.ErrNotAllowed)
	}
	note := strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(note) > maxGateBody {
		return GateView{}, domain.Invalid("note", "too long")
	}
	var out GateView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetGateByIDForUpdate(t.ctx, store.GetGateByIDForUpdateParams{CompanyID: companyID, ID: gateID})
		if err != nil {
			return mapErr(err, "get gate")
		}
		g0 := gateFromRow(cur)
		if g0.Kind != domain.GateQuestion {
			return domain.Invalid("kind", "só um gate question tem resposta; use decide nos demais")
		}
		if g0.Status != domain.GatePending {
			return fmt.Errorf("gate already %s: %w", g0.Status, domain.ErrConflict)
		}
		if err := s.requireAudience(t.ctx, companyID, cur.Audience); err != nil {
			return err
		}
		form, err := gateForm(g0)
		if err != nil {
			return err
		}
		ans, err := form.CheckAnswer(in.Answers)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(ans)
		if err != nil {
			return fmt.Errorf("marshal answer: %w", err)
		}
		row, err := t.q.AnswerGate(t.ctx, store.AnswerGateParams{CompanyID: companyID, ID: gateID, Answer: raw,
			DecidedByType: ptr(string(actor.Type)), DecidedByID: actor.ID, DecisionNote: note, Now: &t.now})
		if err != nil {
			return mapErr(err, "answer gate")
		}
		g := gateFromRow(row)
		if err := t.activity(actor, companyID, "gate.answered", "gate", g.ID, map[string]any{
			"kind": g.Kind, "title": g.Title, "task_id": g.TaskID, "persona_id": g.PersonaID, "answers": ans.Values, "note": note}, ""); err != nil {
			return err
		}
		out, err = t.afterGateDecided(actor, companyID, g, domain.GateApproved, nil)
		return err
	})
	if err != nil {
		return GateView{}, err
	}
	return out, nil
}

// DismissGate dispensa uma pergunta pendente sem respondê-la; a persona é acordada com a nota e segue sem a resposta.
func (s *Service) DismissGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, note string) (GateView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return GateView{}, err
	}
	if actor.Type != domain.ActorHuman {
		return GateView{}, fmt.Errorf("dismiss gate: only a human dismisses a question: %w", domain.ErrNotAllowed)
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxGateBody {
		return GateView{}, domain.Invalid("note", "too long")
	}
	var out GateView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetGateByIDForUpdate(t.ctx, store.GetGateByIDForUpdateParams{CompanyID: companyID, ID: gateID})
		if err != nil {
			return mapErr(err, "get gate")
		}
		if domain.GateKind(cur.Kind) != domain.GateQuestion {
			return domain.Invalid("kind", "só uma pergunta se dispensa; use decide nos demais")
		}
		if domain.GateStatus(cur.Status) != domain.GatePending {
			return fmt.Errorf("gate already %s: %w", cur.Status, domain.ErrConflict)
		}
		if err := s.requireAudience(t.ctx, companyID, cur.Audience); err != nil {
			return err
		}
		row, err := t.q.DismissGate(t.ctx, store.DismissGateParams{CompanyID: companyID, ID: gateID,
			DecidedByType: ptr(string(actor.Type)), DecidedByID: actor.ID, DecisionNote: note, Now: &t.now})
		if err != nil {
			return mapErr(err, "dismiss gate")
		}
		g := gateFromRow(row)
		if err := t.activity(actor, companyID, "gate.dismissed", "gate", g.ID, map[string]any{
			"kind": g.Kind, "title": g.Title, "task_id": g.TaskID, "persona_id": g.PersonaID, "note": note}, ""); err != nil {
			return err
		}
		out, err = t.afterGateDecided(actor, companyID, g, domain.GateApproved, nil)
		return err
	})
	if err != nil {
		return GateView{}, err
	}
	return out, nil
}

// ReopenGate reabre uma pergunta dispensada: volta a pending e a persona espera de novo.
func (s *Service) ReopenGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID) (GateView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return GateView{}, err
	}
	if actor.Type != domain.ActorHuman {
		return GateView{}, fmt.Errorf("reopen gate: only a human reopens a question: %w", domain.ErrNotAllowed)
	}
	var out GateView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetGateByIDForUpdate(t.ctx, store.GetGateByIDForUpdateParams{CompanyID: companyID, ID: gateID})
		if err != nil {
			return mapErr(err, "get gate")
		}
		if domain.GateStatus(cur.Status) != domain.GateDismissed {
			return fmt.Errorf("só uma pergunta dispensada se reabre (o gate está %s): %w", cur.Status, domain.ErrConflict)
		}
		if err := s.requireAudience(t.ctx, companyID, cur.Audience); err != nil {
			return err
		}
		row, err := t.q.ReopenGate(t.ctx, store.ReopenGateParams{CompanyID: companyID, ID: gateID})
		if err != nil {
			return mapErr(err, "reopen gate")
		}
		g := gateFromRow(row)
		if err := t.activity(actor, companyID, "gate.reopened", "gate", g.ID, map[string]any{
			"kind": g.Kind, "title": g.Title, "task_id": g.TaskID, "persona_id": g.PersonaID}, ""); err != nil {
			return err
		}
		var task *store.Task
		if g.TaskID != nil {
			tk, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: *g.TaskID})
			if err != nil && !isNoRows(err) {
				return fmt.Errorf("get gate task: %w", err)
			}
			if err == nil {
				task = &tk
			}
		}
		if g.PersonaID != nil {
			if err := t.parkForGate(actor, companyID, g, task); err != nil {
				return err
			}
		}
		if out, err = loadGateView(t.ctx, t.q, companyID, g.ID); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "gate.updated", g)
		return nil
	})
	if err != nil {
		return GateView{}, err
	}
	return out, nil
}

// ---------- reenvio com revisões ----------

// ResubmitGateInput é o texto novo de um gate devolvido com mudanças pedidas.
type ResubmitGateInput struct {
	Title    string // vazio = mantém
	Body     string // texto novo (obrigatório, salvo o plano ler o artefato)
	Artifact string // gate plan: artefato (id ou nome) com a versão nova do plano
	Hire     *domain.HireSpec
	Note     string // o que mudou, para quem decide
}

var errResubmitKind = errors.New("só approval, plan e hire podem ser reenviados")

// ResubmitGate reenvia o pedido no MESMO gate depois de changes_requested: a versão anterior fica em gate_revisions, o
// gate recebe o texto novo (revisão +1) e volta a pending; a persona e a task voltam a esperar a decisão.
// Quem reenvia: a persona que pediu ou um humano member+.
func (s *Service) ResubmitGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in ResubmitGateInput) (GateView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return GateView{}, err
	}
	if actor.Type == domain.ActorSystem {
		return GateView{}, fmt.Errorf("resubmit gate: %w", domain.ErrNotAllowed)
	}
	if utf8.RuneCountInString(in.Body) > maxGateBody || utf8.RuneCountInString(in.Note) > maxGateBody {
		return GateView{}, domain.Invalid("body", "too long")
	}
	title := strings.TrimSpace(in.Title)
	if title != "" && utf8.RuneCountInString(title) > 300 {
		return GateView{}, domain.Invalid("title", "must have 1-300 characters")
	}
	// o preparo (artefato do plano, ficha) usa o tipo do gate: lê o gate antes da transação
	pre, err := s.GetGate(ctx, companyID, gateID)
	if err != nil {
		return GateView{}, err
	}
	if k := pre.Kind; k != domain.GateApproval && k != domain.GatePlan && k != domain.GateHire {
		return GateView{}, domain.Invalid("kind", errResubmitKind.Error())
	}
	x := RequestGateInput{Kind: pre.Kind, Body: in.Body, TaskID: pre.TaskID, Artifact: in.Artifact, Hire: in.Hire}
	if pre.Kind == domain.GateApproval && strings.TrimSpace(in.Body) == "" {
		return GateView{}, domain.Invalid("body", "o texto novo do pedido é obrigatório")
	}
	if pre.Kind == domain.GateHire && in.Hire == nil && strings.TrimSpace(in.Body) == "" {
		return GateView{}, domain.Invalid("body", "informe o texto novo ou a ficha ajustada")
	}
	if err := s.prepareGateExtras(ctx, actor, companyID, &x); err != nil {
		return GateView{}, err
	}
	var out GateView
	err = s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetGateByIDForUpdate(t.ctx, store.GetGateByIDForUpdateParams{CompanyID: companyID, ID: gateID})
		if err != nil {
			return mapErr(err, "get gate")
		}
		if domain.GateStatus(cur.Status) != domain.GateChangesRequested {
			return fmt.Errorf("só um gate com mudanças pedidas se reenvia (o gate está %s): %w", cur.Status, domain.ErrConflict)
		}
		if actor.Type == domain.ActorPersona && (cur.PersonaID == nil || actor.ID == nil || *cur.PersonaID != *actor.ID) {
			return fmt.Errorf("resubmit gate: só a persona que pediu reenvia: %w", domain.ErrNotAllowed)
		}
		if err := s.requireAudience(t.ctx, companyID, cur.Audience); err != nil {
			return err
		}
		if domain.GateKind(cur.Kind) == domain.GateHire && actor.Type == domain.ActorHuman { // contratar é decisão de admin (ADR-0021)
			if err := s.require(t.ctx, companyID, domain.MemberAdmin); err != nil {
				return err
			}
		}
		newTitle := cur.Title
		if title != "" {
			newTitle = title
		}
		newBody := x.Body
		if strings.TrimSpace(newBody) == "" {
			newBody = cur.Body
		}
		newPayload := []byte(`{}`)
		switch {
		case len(x.payload) > 0:
			newPayload = x.payload
		case len(cur.Payload) > 0:
			newPayload = cur.Payload
		}
		sum := sha256.Sum256([]byte(cur.Body))
		if _, err := t.q.InsertGateRevision(t.ctx, store.InsertGateRevisionParams{ID: domain.NewID(), CompanyID: companyID, GateID: gateID,
			Revision: cur.Revision, Title: cur.Title, Body: cur.Body, Payload: cur.Payload, DecisionNote: cur.DecisionNote,
			Sha256: hex.EncodeToString(sum[:]), Now: t.now}); err != nil {
			return mapErr(err, "insert gate revision")
		}
		row, err := t.q.ResubmitGate(t.ctx, store.ResubmitGateParams{CompanyID: companyID, ID: gateID, Title: newTitle, Body: newBody, Payload: newPayload})
		if err != nil {
			return mapErr(err, "resubmit gate")
		}
		g := gateFromRow(row)
		if err := t.activity(actor, companyID, "gate.resubmitted", "gate", g.ID, map[string]any{
			"kind": g.Kind, "title": g.Title, "task_id": g.TaskID, "persona_id": g.PersonaID,
			"revision": g.Revision, "note": strings.TrimSpace(in.Note)}, ""); err != nil {
			return err
		}
		var task *store.Task
		if g.TaskID != nil {
			tk, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: *g.TaskID})
			if err != nil && !isNoRows(err) {
				return fmt.Errorf("get gate task: %w", err)
			}
			if err == nil {
				task = &tk
			}
		}
		if g.PersonaID != nil {
			if err := t.parkForGate(actor, companyID, g, task); err != nil {
				return err
			}
		}
		if task != nil && strings.TrimSpace(in.Note) != "" {
			if err := t.resubmitComment(companyID, *task, g, strings.TrimSpace(in.Note)); err != nil {
				return err
			}
		}
		if out, err = loadGateView(t.ctx, t.q, companyID, g.ID); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "gate.updated", g)
		if task != nil {
			if tv, err := loadTaskView(t.ctx, t.q, companyID, task.ID); err == nil {
				t.publish(companyTopic(companyID), "task.updated", tv)
			}
		}
		return nil
	})
	if err != nil {
		return GateView{}, err
	}
	return out, nil
}

// resubmitComment avisa na thread da task que o pedido foi reenviado, com a nota de quem reenviou.
func (t *txc) resubmitComment(companyID domain.ID, task store.Task, g domain.Gate, note string) error {
	body := fmt.Sprintf("**Gate reenviado (revisão %d)** — %s\n\n%s", g.Revision, g.Title, note)
	c, err := t.q.CreateComment(t.ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: companyID, TaskID: task.ID,
		AuthorType: string(domain.ActorSystem), Body: body, EventID: ptr(fmt.Sprintf("gate.resubmitted:%s:%d", g.ID, g.Revision)), Now: t.now})
	if err != nil {
		return mapErr(err, "post gate resubmit")
	}
	return t.activity(domain.SystemActor, companyID, "comment.added", "task", task.ID, map[string]any{"comment_id": c.ID, "gate_id": g.ID}, "")
}

// GateHistory são as versões de um gate com o diff entre a anterior e a vigente.
type GateHistory struct {
	Gate      GateView
	Revisions []domain.GateRevision // da mais nova para a mais antiga
	// Diff compara a revisão imediatamente anterior com o texto vigente; vazio quando o gate nunca foi reenviado.
	Diff textdiff.Result
}

// GateRevisions devolve o histórico de um gate.
func (s *Service) GateRevisions(ctx context.Context, companyID, gateID domain.ID) (GateHistory, error) {
	g, err := s.GetGate(ctx, companyID, gateID)
	if err != nil {
		return GateHistory{}, err
	}
	rows, err := s.read().ListGateRevisions(ctx, store.ListGateRevisionsParams{CompanyID: companyID, GateID: gateID})
	if err != nil {
		return GateHistory{}, fmt.Errorf("list gate revisions: %w", err)
	}
	h := GateHistory{Gate: g, Revisions: make([]domain.GateRevision, 0, len(rows))}
	for _, r := range rows {
		h.Revisions = append(h.Revisions, domain.GateRevision{ID: r.ID, GateID: r.GateID, Revision: int(r.Revision), Title: r.Title,
			Body: r.Body, Payload: r.Payload, DecisionNote: r.DecisionNote, SHA256: r.Sha256, CreatedAt: r.CreatedAt})
	}
	if len(h.Revisions) > 0 {
		h.Diff = textdiff.Lines(h.Revisions[0].Body, g.Body)
	}
	return h, nil
}
