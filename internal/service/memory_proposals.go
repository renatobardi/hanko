package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/store"
)

// Propostas da curadoria de memória e o gate humano (ADR-0030).
//
// Uma chamada de tool da run de curadoria que remove, funde ou reescreve conteúdo não chega ao ai-memory: o
// proxy a entrega aqui (RecordMemoryProposal). A proposta guarda a chamada exata (tool + argumentos), o
// conteúdo proposto, o atual (quando o provedor deixa ler) e o diff, e abre um gate de tipo memory na Caixa de
// entrada. Aprovar enfileira a aplicação (job com retry, ApplyMemoryProposalTx); rejeitar descarta. O sha256
// da chamada aparece no gate: o que o humano aprovou é exatamente o que será repetido.

const (
	// memoryProposalMaxAttempts é quantas aplicações falhas o Hanko tenta sozinho; depois só o retry manual.
	memoryProposalMaxAttempts = 20
	maxProposalShown          = 20000 // por bloco de texto no corpo do gate
	maxProposalStored         = 200_000
)

// ProposalJobs é a capacidade opcional do Jobs de enfileirar a aplicação de uma proposta aprovada. Quando
// o Jobs não a tem (testes), a varredura periódica pega a proposta pelo next_attempt_at.
type ProposalJobs interface {
	ApplyMemoryProposalTx(ctx context.Context, tx pgx.Tx, companyID, proposalID domain.ID) error
}

// RecordProposalInput é a chamada retida pelo proxy numa run de curadoria.
type RecordProposalInput struct {
	RunID     domain.ID
	Tool      string
	Arguments map[string]any // escopo (workspace/project) já fixado pelo proxy
	Current   string         // conteúdo atual do alvo, se o proxy conseguiu ler (melhor esforço)
}

// MemoryProposalView é uma proposta com os nomes para exibir.
type MemoryProposalView struct {
	domain.MemoryProposal
	PersonaName string `json:"persona_name,omitempty"`
	RunName     string `json:"run_name,omitempty"`
}

func proposalFromRow(r store.MemoryProposal) domain.MemoryProposal {
	p := domain.MemoryProposal{ID: r.ID, CompanyID: r.CompanyID, WorkspaceID: r.WorkspaceID, WorkspaceSlug: r.WorkspaceSlug,
		RunID: r.RunID, GateID: r.GateID, Kind: domain.MemoryProposalKind(r.Kind), Status: domain.MemoryProposalStatus(r.Status),
		Tool: r.Tool, Targets: r.Targets, Proposed: r.ProposedContent, Current: r.CurrentContent, Diff: r.Diff, SHA256: r.Sha256,
		Attempts: int(r.Attempts), LastError: r.LastError, NextAttemptAt: r.NextAttemptAt, DecisionNote: r.DecisionNote,
		DecidedAt: r.DecidedAt, AppliedAt: r.AppliedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if p.Targets == nil {
		p.Targets = []string{}
	}
	if r.DecidedByType != nil {
		a := domain.Actor{Type: domain.ActorType(*r.DecidedByType), ID: r.DecidedByID}
		p.DecidedBy = &a
	}
	return p
}

var proposalKindLabel = map[domain.MemoryProposalKind]string{
	domain.ProposalMerge: "fusão", domain.ProposalDelete: "remoção", domain.ProposalRewrite: "reescrita",
}

// proposalSHA256 é o hash da chamada (tool + argumentos em JSON canônico: chaves ordenadas pelo encoder).
func proposalSHA256(tool string, args map[string]any) (string, []byte, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return "", nil, fmt.Errorf("marshal proposal arguments: %w", err)
	}
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil)), raw, nil
}

// RecordMemoryProposal registra a chamada retida de uma run de curadoria e abre o gate. A mesma chamada na
// mesma run devolve a proposta existente (created=false). Quem chama é o proxy de memória, autenticado pelo
// run token; só vale para run de curadoria ainda ativa.
func (s *Service) RecordMemoryProposal(ctx context.Context, companyID domain.ID, in RecordProposalInput) (MemoryProposalView, bool, error) {
	if strings.TrimSpace(in.Tool) == "" {
		return MemoryProposalView{}, false, domain.Invalid("tool", "required")
	}
	sha, rawArgs, err := proposalSHA256(in.Tool, in.Arguments)
	if err != nil {
		return MemoryProposalView{}, false, err
	}
	var out MemoryProposalView
	created := false
	run := func() error {
		return s.withTx(ctx, func(t *txc) error {
			r, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: in.RunID})
			if err != nil {
				return mapErr(err, "get run")
			}
			if domain.RunTrigger(r.Trigger) != domain.TriggerMemory {
				return fmt.Errorf("memory proposal: only a consolidation run proposes: %w", domain.ErrNotAllowed)
			}
			if !domain.RunStatus(r.Status).Active() {
				return domain.Invalid("run", fmt.Sprintf("run already %s", r.Status))
			}
			if ex, err := t.q.GetMemoryProposalByRunSHA(ctx, store.GetMemoryProposalByRunSHAParams{RunID: &in.RunID, Sha256: sha}); err == nil {
				out, err = s.proposalView(ctx, t.q, proposalFromRow(ex))
				return err
			} else if !isNoRows(err) {
				return fmt.Errorf("lookup proposal: %w", err)
			}
			task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: r.TaskID})
			if err != nil {
				return mapErr(err, "get task")
			}
			slug := "default"
			if task.WorkspaceID != nil {
				ws, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *task.WorkspaceID})
				if err != nil && !isNoRows(err) {
					return fmt.Errorf("get workspace: %w", err)
				}
				if err == nil {
					slug = ws.Slug
				}
			}
			p, err := s.insertProposal(t, companyID, r, task.WorkspaceID, slug, in, sha, rawArgs)
			if err != nil {
				return err
			}
			out, err = s.proposalView(ctx, t.q, p)
			created = true
			return err
		})
	}
	err = run()
	if err != nil && isUnique(err) {
		created = false
		err = run() // perdeu a corrida na mesma chamada: a segunda passada devolve a vencedora
	}
	if err != nil {
		return MemoryProposalView{}, false, err
	}
	return out, created, nil
}

func (s *Service) insertProposal(t *txc, companyID domain.ID, run store.Run, wsID *domain.ID, slug string,
	in RecordProposalInput, sha string, rawArgs []byte) (domain.MemoryProposal, error) {
	kind := domain.MemoryProposalKind(memory.InferKind(in.Tool))
	targets := memory.TargetsOf(in.Arguments)
	proposed := clipText(memory.ProposedOf(in.Arguments), maxProposalStored)
	current := clipText(in.Current, maxProposalStored)
	diff := ""
	switch {
	case current != "" && proposed != "":
		diff = memory.UnifiedDiff(current, proposed)
	case current != "" && kind == domain.ProposalDelete:
		diff = memory.UnifiedDiff(current, "")
	}
	row, err := t.q.InsertMemoryProposal(t.ctx, store.InsertMemoryProposalParams{ID: domain.NewID(), CompanyID: companyID,
		WorkspaceID: wsID, WorkspaceSlug: slug, RunID: &run.ID, Kind: string(kind), Tool: in.Tool, Arguments: rawArgs,
		Targets: targets, ProposedContent: proposed, CurrentContent: current, Diff: diff, Sha256: sha, Now: t.now})
	if err != nil {
		return domain.MemoryProposal{}, mapErr(err, "insert memory proposal")
	}
	p := proposalFromRow(row)
	personaName := ""
	if per, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: run.PersonaID}); err == nil {
		personaName = per.Name
	}
	title := fmt.Sprintf("Memória %s: %s", slug, proposalKindLabel[kind])
	if len(targets) > 0 {
		title += " de " + targets[0]
		if len(targets) > 1 {
			title += fmt.Sprintf(" e mais %d", len(targets)-1)
		}
	}
	g, err := t.createGate(domain.PersonaActor(run.PersonaID), companyID, RequestGateInput{Kind: domain.GateMemory,
		Title: truncateRunes(title, 300), Body: proposalGateBody(p, personaName, run.Name), RunID: &run.ID,
		EventID: "memory.proposal:" + p.ID.String()}, nil)
	if err != nil {
		return domain.MemoryProposal{}, err
	}
	if err := t.q.SetMemoryProposalGate(t.ctx, store.SetMemoryProposalGateParams{ID: p.ID, GateID: &g.ID, Now: t.now}); err != nil {
		return domain.MemoryProposal{}, fmt.Errorf("set proposal gate: %w", err)
	}
	p.GateID = &g.ID
	return p, t.activity(domain.PersonaActor(run.PersonaID), companyID, "memory.proposal_created", "gate", g.ID, map[string]any{
		"proposal_id": p.ID, "kind": kind, "tool": in.Tool, "targets": targets, "workspace": slug, "run_id": run.ID, "sha256": sha}, "")
}

// fence devolve uma cerca de código mais longa que qualquer sequência de crases em text.
func fence(text string) string {
	longest, cur := 0, 0
	for _, r := range text {
		if r == '`' {
			cur++
			longest = max(longest, cur)
		} else {
			cur = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func codeBlock(lang, text string) string {
	f := fence(text)
	return f + lang + "\n" + strings.TrimRight(text, "\n") + "\n" + f + "\n"
}

// proposalGateBody é o corpo markdown do gate: o que a curadoria quer fazer, o diff e o hash.
func proposalGateBody(p domain.MemoryProposal, persona, runName string) string {
	var b strings.Builder
	who := "A curadoria"
	if persona != "" {
		who = "A persona **" + persona + "**"
	}
	fmt.Fprintf(&b, "%s propõe uma %s na memória do workspace **%s**", who, proposalKindLabel[p.Kind], p.WorkspaceSlug)
	if runName != "" {
		fmt.Fprintf(&b, " (execução %s)", runName)
	}
	b.WriteString(".\n\n")
	fmt.Fprintf(&b, "- Operação: %s (`%s`)\n", proposalKindLabel[p.Kind], p.Tool)
	if len(p.Targets) > 0 {
		b.WriteString("- Páginas afetadas: ")
		for i, tg := range p.Targets {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "`%s`", tg)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- sha256 da proposta: `%s`\n\n", p.SHA256)
	b.WriteString("Nada muda na memória antes da sua decisão. Aprovar aplica esta chamada no ai-memory (com nova tentativa automática se ele estiver fora do ar); rejeitar descarta a proposta.\n")
	switch {
	case p.Diff != "":
		b.WriteString("\n## Diferença\n\n")
		b.WriteString(codeBlock("diff", clipText(p.Diff, maxProposalShown)))
	case p.Proposed != "":
		b.WriteString("\n## Conteúdo proposto\n\n")
		b.WriteString(codeBlock("", clipText(p.Proposed, maxProposalShown)))
		b.WriteString("\n_O conteúdo atual não está disponível para comparar._\n")
	case p.Current != "":
		b.WriteString("\n## Conteúdo atual\n\n")
		b.WriteString(codeBlock("", clipText(p.Current, maxProposalShown)))
	}
	return b.String()
}

func (s *Service) proposalView(ctx context.Context, q *store.Queries, p domain.MemoryProposal) (MemoryProposalView, error) {
	v := MemoryProposalView{MemoryProposal: p}
	if p.RunID != nil {
		if r, err := q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: p.CompanyID, ID: *p.RunID}); err == nil {
			v.RunName = r.Name
			if per, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: p.CompanyID, ID: r.PersonaID}); err == nil {
				v.PersonaName = per.Name
			}
		} else if !isNoRows(err) {
			return MemoryProposalView{}, fmt.Errorf("get proposal run: %w", err)
		}
	}
	return v, nil
}

// ListMemoryProposals lista as propostas da empresa (pendentes primeiro).
func (s *Service) ListMemoryProposals(ctx context.Context, companyID domain.ID, statuses []domain.MemoryProposalStatus, limit int) ([]MemoryProposalView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListMemoryProposalsParams{CompanyID: companyID, Lim: i32(clamp(limit, 50, 200))}
	for _, st := range statuses {
		if !st.Valid() {
			return nil, domain.Invalid("status", fmt.Sprintf("unknown proposal status %q", st))
		}
		p.Statuses = append(p.Statuses, string(st))
	}
	q := s.read()
	rows, err := q.ListMemoryProposals(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list memory proposals: %w", err)
	}
	out := make([]MemoryProposalView, len(rows))
	for i, r := range rows {
		if out[i], err = s.proposalView(ctx, q, proposalFromRow(r)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetMemoryProposal devolve uma proposta (id completo).
func (s *Service) GetMemoryProposal(ctx context.Context, companyID, id domain.ID) (MemoryProposalView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return MemoryProposalView{}, err
	}
	q := s.read()
	r, err := q.GetMemoryProposal(ctx, store.GetMemoryProposalParams{CompanyID: companyID, ID: id})
	if err != nil {
		return MemoryProposalView{}, mapErr(err, "get memory proposal")
	}
	return s.proposalView(ctx, q, proposalFromRow(r))
}

// DecideMemoryProposal é a decisão humana sobre a proposta, pelo gate dela (mesma trilha de auditoria,
// mesmo papel mínimo). approved aplica; rejected e changes_requested descartam.
func (s *Service) DecideMemoryProposal(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in DecideGateInput) (MemoryProposalView, error) {
	p, err := s.GetMemoryProposal(ctx, companyID, id)
	if err != nil {
		return MemoryProposalView{}, err
	}
	if p.GateID == nil {
		return MemoryProposalView{}, fmt.Errorf("proposal has no gate: %w", domain.ErrConflict)
	}
	if _, err := s.DecideGate(ctx, actor, companyID, *p.GateID, in); err != nil {
		return MemoryProposalView{}, err
	}
	return s.GetMemoryProposal(ctx, companyID, id)
}

// memoryProposalDecided propaga a decisão do gate memory para a proposta, na mesma transação do gate. Devolve
// o workspace da proposta (a decisão vira memória dele).
func (t *txc) memoryProposalDecided(actor domain.Actor, g domain.Gate, decision domain.GateStatus, note string) (*domain.ID, error) {
	gid := g.ID
	p, err := t.q.GetMemoryProposalByGateForUpdate(t.ctx, store.GetMemoryProposalByGateForUpdateParams{CompanyID: g.CompanyID, GateID: &gid})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get proposal by gate: %w", err)
	}
	status, next := domain.ProposalRejected, (*time.Time)(nil)
	if decision == domain.GateApproved {
		status, next = domain.ProposalApproved, ptr(t.now)
	}
	row, err := t.q.DecideMemoryProposal(t.ctx, store.DecideMemoryProposalParams{CompanyID: g.CompanyID, ID: p.ID, Status: string(status),
		DecidedByType: ptr(string(actor.Type)), DecidedByID: actor.ID, DecisionNote: note, Now: &t.now, NextAttemptAt: next})
	if isNoRows(err) {
		return p.WorkspaceID, nil // já decidida por outro caminho
	}
	if err != nil {
		return nil, mapErr(err, "decide memory proposal")
	}
	if err := t.activity(actor, g.CompanyID, "memory.proposal_decided", "gate", g.ID, map[string]any{
		"proposal_id": p.ID, "decision": decision, "status": status, "kind": p.Kind, "workspace": p.WorkspaceSlug, "sha256": p.Sha256}, ""); err != nil {
		return nil, err
	}
	if status == domain.ProposalApproved {
		if err := t.enqueueApply(g.CompanyID, row.ID); err != nil {
			return nil, err
		}
	}
	return row.WorkspaceID, nil
}

func (t *txc) enqueueApply(companyID, proposalID domain.ID) error {
	if pj, ok := t.s.Jobs.(ProposalJobs); ok {
		if err := pj.ApplyMemoryProposalTx(t.ctx, t.tx, companyID, proposalID); err != nil {
			return fmt.Errorf("enqueue apply_memory_proposal: %w", err)
		}
	}
	return nil
}

// RetryMemoryProposal recoloca na fila a aplicação de uma proposta que falhou (admin).
func (s *Service) RetryMemoryProposal(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (MemoryProposalView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return MemoryProposalView{}, err
	}
	err := s.withTx(ctx, func(t *txc) error {
		row, err := t.q.RetryMemoryProposal(ctx, store.RetryMemoryProposalParams{CompanyID: companyID, ID: id, Now: &t.now})
		if isNoRows(err) {
			if _, gerr := t.q.GetMemoryProposal(ctx, store.GetMemoryProposalParams{CompanyID: companyID, ID: id}); gerr != nil {
				return mapErr(gerr, "get memory proposal")
			}
			return fmt.Errorf("only a failed proposal is retried: %w", domain.ErrConflict)
		}
		if err != nil {
			return mapErr(err, "retry memory proposal")
		}
		if err := t.activity(actor, companyID, "memory.proposal_retry", "gate", derefID(row.GateID, row.ID), map[string]any{"proposal_id": row.ID}, ""); err != nil {
			return err
		}
		return t.enqueueApply(companyID, row.ID)
	})
	if err != nil {
		return MemoryProposalView{}, err
	}
	return s.GetMemoryProposal(ctx, companyID, id)
}

func derefID(p *domain.ID, fallback domain.ID) domain.ID {
	if p != nil {
		return *p
	}
	return fallback
}

// ---- aplicação (job apply_memory_proposal) ----

// MemoryApplyItem é o que o job precisa para repetir a chamada aprovada no provedor de memória.
type MemoryApplyItem struct {
	Proposal    domain.MemoryProposal
	CompanySlug string
	Tool        string
	Arguments   map[string]any
}

// LoadMemoryProposalForApply carrega uma proposta aprovada (ou falha, em nova tentativa). Qualquer outro
// estado é ErrConflict: o job vira no-op.
func (s *Service) LoadMemoryProposalForApply(ctx context.Context, companyID, id domain.ID) (MemoryApplyItem, error) {
	q := s.read()
	r, err := q.GetMemoryProposal(ctx, store.GetMemoryProposalParams{CompanyID: companyID, ID: id})
	if err != nil {
		return MemoryApplyItem{}, mapErr(err, "get memory proposal")
	}
	if st := domain.MemoryProposalStatus(r.Status); st != domain.ProposalApproved && st != domain.ProposalFailed {
		return MemoryApplyItem{}, fmt.Errorf("proposal is %s: %w", r.Status, domain.ErrConflict)
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return MemoryApplyItem{}, mapErr(err, "company")
	}
	var args map[string]any
	if err := json.Unmarshal(r.Arguments, &args); err != nil {
		return MemoryApplyItem{}, fmt.Errorf("decode proposal arguments: %w", err)
	}
	return MemoryApplyItem{Proposal: proposalFromRow(r), CompanySlug: co.Slug, Tool: r.Tool, Arguments: args}, nil
}

// MarkMemoryProposalApplied registra a aplicação e deixa um relato na própria memória (página em
// hanko/curadoria/, aditiva) pelo outbox.
func (s *Service) MarkMemoryProposalApplied(ctx context.Context, companyID, id domain.ID) error {
	return s.withTx(ctx, func(t *txc) error {
		row, err := t.q.MarkMemoryProposalApplied(ctx, store.MarkMemoryProposalAppliedParams{ID: id, Now: &t.now})
		if isNoRows(err) {
			return nil // já aplicada ou fora do ciclo
		}
		if err != nil {
			return mapErr(err, "mark proposal applied")
		}
		p := proposalFromRow(row)
		if err := t.activity(domain.SystemActor, companyID, "memory.proposal_applied", "gate", derefID(row.GateID, row.ID), map[string]any{
			"proposal_id": p.ID, "kind": p.Kind, "workspace": p.WorkspaceSlug, "attempts": p.Attempts, "sha256": p.SHA256}, ""); err != nil {
			return err
		}
		return t.memoryFact(companyID, p.WorkspaceID, appliedFact(p, t.now), "memory.proposal_applied:"+p.ID.String())
	})
}

func appliedFact(p domain.MemoryProposal, now time.Time) memory.Fact {
	var b strings.Builder
	fmt.Fprintf(&b, "# Curadoria aplicada: %s\n\n", proposalKindLabel[p.Kind])
	fmt.Fprintf(&b, "- Operação: %s (`%s`)\n- Workspace: %s\n- Proposta: %s\n- sha256: `%s`\n", proposalKindLabel[p.Kind], p.Tool, p.WorkspaceSlug, p.ID, p.SHA256)
	if len(p.Targets) > 0 {
		fmt.Fprintf(&b, "- Páginas: %s\n", strings.Join(p.Targets, ", "))
	}
	if p.DecidedBy != nil {
		fmt.Fprintf(&b, "- Aprovada por: %s\n", actorLabel(*p.DecidedBy))
	}
	if p.DecisionNote != "" {
		fmt.Fprintf(&b, "\n## Nota da decisão\n\n%s\n", p.DecisionNote)
	}
	if p.Diff != "" {
		b.WriteString("\n## Diferença\n\n" + codeBlock("diff", clipText(p.Diff, maxProposalShown)))
	}
	return memory.Fact{Kind: "memory.proposal_applied",
		Path:  fmt.Sprintf("%s%s-%s.md", memory.CuratorReportPrefix, now.Format("2006-01-02"), p.ID.String()[:8]),
		Title: fmt.Sprintf("Curadoria aplicada (%s)", proposalKindLabel[p.Kind]), Body: b.String(), Tags: []string{"hanko", "curadoria"}}
}

// MarkMemoryProposalFailed registra uma aplicação que falhou e agenda a próxima tentativa com backoff (nada
// se perde: a proposta fica falha, com o erro, até dar certo). Passadas memoryProposalMaxAttempts, só retry manual.
func (s *Service) MarkMemoryProposalFailed(ctx context.Context, companyID, id domain.ID, errMsg string) (time.Duration, error) {
	var wait time.Duration
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetMemoryProposalForUpdate(ctx, store.GetMemoryProposalForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get memory proposal")
		}
		wait = MemoryBackoff(int(cur.Attempts))
		var next *time.Time
		if int(cur.Attempts)+1 < memoryProposalMaxAttempts {
			n := t.now.Add(wait)
			next = &n
		}
		row, err := t.q.MarkMemoryProposalFailed(ctx, store.MarkMemoryProposalFailedParams{ID: id, LastError: clipText(errMsg, 1000),
			NextAttemptAt: next, Now: t.now})
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return mapErr(err, "mark proposal failed")
		}
		return t.activity(domain.SystemActor, companyID, "memory.proposal_failed", "gate", derefID(row.GateID, row.ID), map[string]any{
			"proposal_id": row.ID, "attempts": row.Attempts, "error": row.LastError, "will_retry": next != nil}, "")
	})
	return wait, err
}

// MemoryProposalRef identifica uma proposta devida para aplicação.
type MemoryProposalRef struct{ CompanyID, ID domain.ID }

// DueMemoryProposals lista as propostas aprovadas (ou falhas) cuja aplicação está devida (varredura).
func (s *Service) DueMemoryProposals(ctx context.Context, limit int) ([]MemoryProposalRef, error) {
	now := s.Now().UTC()
	rows, err := s.read().DueMemoryProposals(ctx, store.DueMemoryProposalsParams{Now: &now, Lim: i32(clamp(limit, 100, 1000))})
	if err != nil {
		return nil, fmt.Errorf("due memory proposals: %w", err)
	}
	out := make([]MemoryProposalRef, len(rows))
	for i, r := range rows {
		out[i] = MemoryProposalRef{CompanyID: r.CompanyID, ID: r.ID}
	}
	return out, nil
}

// ---- permissões de memória por workspace ----

// GrantMemoryAccess libera uma persona a ler (ou ler e escrever) a memória de outro workspace (admin). A
// persona já alcança o workspace da própria run (o da task, senão o dela); a concessão vale para os demais.
func (s *Service) GrantMemoryAccess(ctx context.Context, actor domain.Actor, companyID, personaID, workspaceID domain.ID, access domain.MemoryAccess) (domain.MemoryGrant, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.MemoryGrant{}, err
	}
	if !access.Valid() {
		return domain.MemoryGrant{}, domain.Invalid("access", "want read or write")
	}
	var out domain.MemoryGrant
	err := s.withTx(ctx, func(t *txc) error {
		per, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return domain.Invalid("persona", "persona not found in company")
		}
		ws, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: workspaceID})
		if err != nil {
			return domain.Invalid("workspace", "workspace not found in company")
		}
		row, err := t.q.UpsertMemoryGrant(ctx, store.UpsertMemoryGrantParams{CompanyID: companyID, PersonaID: personaID,
			WorkspaceID: workspaceID, Access: string(access), Now: t.now})
		if err != nil {
			return mapErr(err, "grant memory access")
		}
		out = domain.MemoryGrant{CompanyID: companyID, PersonaID: personaID, WorkspaceID: workspaceID, Access: domain.MemoryAccess(row.Access),
			PersonaName: per.Name, WorkspaceSlug: ws.Slug, CreatedAt: row.CreatedAt}
		return t.activity(actor, companyID, "memory.access_granted", "persona", personaID, map[string]any{
			"workspace": ws.Slug, "workspace_id": workspaceID, "access": access}, "")
	})
	return out, err
}

// RevokeMemoryAccess retira a concessão (admin). Sem concessão é ErrNotFound.
func (s *Service) RevokeMemoryAccess(ctx context.Context, actor domain.Actor, companyID, personaID, workspaceID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		n, err := t.q.DeleteMemoryGrant(ctx, store.DeleteMemoryGrantParams{CompanyID: companyID, PersonaID: personaID, WorkspaceID: workspaceID})
		if err != nil {
			return fmt.Errorf("revoke memory access: %w", err)
		}
		if n == 0 {
			return notFound("memory grant")
		}
		return t.activity(actor, companyID, "memory.access_revoked", "persona", personaID, map[string]any{"workspace_id": workspaceID}, "")
	})
}

// ListMemoryGrants lista as concessões da empresa.
func (s *Service) ListMemoryGrants(ctx context.Context, companyID domain.ID) ([]domain.MemoryGrant, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListMemoryGrants(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list memory grants: %w", err)
	}
	out := make([]domain.MemoryGrant, len(rows))
	for i, r := range rows {
		g := r.MemoryGrant
		out[i] = domain.MemoryGrant{CompanyID: g.CompanyID, PersonaID: g.PersonaID, WorkspaceID: g.WorkspaceID,
			Access: domain.MemoryAccess(g.Access), PersonaName: r.PersonaName, WorkspaceSlug: r.WorkspaceSlug, CreatedAt: g.CreatedAt}
	}
	return out, nil
}

// MemoryAccessTo é o nível de acesso extra de uma persona à memória do workspace de slug dado (vazio = sem
// concessão). É a consulta do proxy /memory-mcp; roda sem identidade de usuário (a persona é limitada à
// empresa do token) e nunca atravessa empresas.
func (s *Service) MemoryAccessTo(ctx context.Context, companyID, personaID domain.ID, workspaceSlug string) (domain.MemoryAccess, error) {
	q := s.read()
	ws, err := q.GetWorkspaceBySlug(ctx, store.GetWorkspaceBySlugParams{CompanyID: companyID, Slug: workspaceSlug})
	if isNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get workspace: %w", err)
	}
	acc, err := q.GetMemoryGrantAccess(ctx, store.GetMemoryGrantAccessParams{CompanyID: companyID, PersonaID: personaID, WorkspaceID: ws.ID})
	if isNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get memory grant: %w", err)
	}
	return domain.MemoryAccess(acc), nil
}
