package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/store"
)

// Chat (ADR-0027): conversa direta de um humano com uma persona, fora de uma task. Cada mensagem humana
// dispara uma run da persona com o gatilho "chat", pelo mesmo caminho das demais (fila, adapter, custos,
// orçamento, trava semanal, alertas). Mensagens que chegam enquanto a run da conversa roda esperam e entram
// juntas na run seguinte.

const (
	// maxPendingMessages é quantas mensagens humanas podem esperar a run em andamento.
	maxPendingMessages = 20
	// chatContextMessages é quantas mensagens recentes a run lê para montar o histórico do prompt.
	chatContextMessages = 200
	// maxMessageListLimit é o teto de mensagens por consulta.
	maxMessageListLimit = 500
)

// ConversationView é uma conversa com os dados de exibição.
type ConversationView struct {
	domain.Conversation
	PersonaName string `json:"persona_name"`
	PersonaSlug string `json:"persona_slug"`
	// ActiveRunID é a run (na fila ou rodando) que está respondendo; nil quando a persona está ociosa.
	ActiveRunID *domain.ID `json:"active_run_id,omitempty"`
	// PendingMessages são mensagens humanas que ainda esperam uma run.
	PendingMessages int `json:"pending_messages"`
	// CostUSD soma o custo das runs da conversa.
	CostUSD float64 `json:"cost_usd"`
}

// ConversationFilter filtra a listagem de conversas.
type ConversationFilter struct {
	PersonaID *domain.ID
	// Status vazio = todas.
	Status domain.ConversationStatus
	Limit  int // default 50, máx 200
}

// CreateConversationInput abre uma conversa. Title vazio deriva da primeira mensagem.
type CreateConversationInput struct {
	PersonaID domain.ID
	Title     string
	Message   string
}

// ChatContext é o que a run de chat lê da conversa.
type ChatContext struct {
	Conversation domain.Conversation
	// History são as mensagens anteriores a esta run (humanas já respondidas, respostas da persona, avisos).
	History []domain.ConversationMessage
	// New são as mensagens humanas que esta run recolheu e deve responder.
	New []domain.ConversationMessage
	// ResumeSession é a sessão do harness da run anterior da conversa, quando há uma terminada com
	// sucesso no mesmo harness; o executor a usa só se o adapter souber retomar.
	ResumeSession string
}

func conversationFromRow(r store.Conversation) domain.Conversation {
	return domain.Conversation{ID: r.ID, CompanyID: r.CompanyID, PersonaID: r.PersonaID, Title: r.Title,
		Status: domain.ConversationStatus(r.Status), CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LastMessageAt: r.LastMessageAt}
}

func conversationView(c store.Conversation, personaName, personaSlug string, active *domain.ID, pending int64, cost float64) ConversationView {
	return ConversationView{Conversation: conversationFromRow(c), PersonaName: personaName, PersonaSlug: personaSlug,
		ActiveRunID: active, PendingMessages: int(pending), CostUSD: cost}
}

func messageFromRow(r store.ConversationMessage) domain.ConversationMessage {
	return domain.ConversationMessage{ID: r.ID, CompanyID: r.CompanyID, ConversationID: r.ConversationID,
		Author: actorOf(r.AuthorType, r.AuthorID), Kind: domain.MessageAuthor(r.AuthorType), Body: r.Body,
		RunID: r.RunID, CreatedAt: r.CreatedAt}
}

func loadConversationView(ctx context.Context, q *store.Queries, companyID, id domain.ID) (ConversationView, error) {
	r, err := q.GetConversationView(ctx, store.GetConversationViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return ConversationView{}, mapErr(err, "get conversation")
	}
	return conversationView(r.Conversation, r.PersonaName, r.PersonaSlug, r.ActiveRunID, r.PendingMessages, r.CostUsd), nil
}

// ---------- leitura ----------

// ListConversations lists the company's conversations, most recently active first. Every member sees every
// conversation of the company (ADR-0027).
func (s *Service) ListConversations(ctx context.Context, companyID domain.ID, f ConversationFilter) ([]ConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListConversationViewsParams{CompanyID: companyID, PersonaID: f.PersonaID, Lim: i32(clamp(f.Limit, 50, 200))}
	if f.Status != "" {
		if !f.Status.Valid() {
			return nil, domain.Invalid("status", fmt.Sprintf("unknown conversation status %q", f.Status))
		}
		st := string(f.Status)
		p.Status = &st
	}
	rows, err := s.read().ListConversationViews(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	out := make([]ConversationView, len(rows))
	for i, r := range rows {
		out[i] = conversationView(r.Conversation, r.PersonaName, r.PersonaSlug, r.ActiveRunID, r.PendingMessages, r.CostUsd)
	}
	return out, nil
}

// GetConversation returns one conversation.
func (s *Service) GetConversation(ctx context.Context, companyID, id domain.ID) (ConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ConversationView{}, err
	}
	return loadConversationView(ctx, s.read(), companyID, id)
}

// ListConversationMessages returns the latest limit messages of a conversation in chronological order.
func (s *Service) ListConversationMessages(ctx context.Context, companyID, id domain.ID, limit int) ([]domain.ConversationMessage, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetConversation(ctx, store.GetConversationParams{CompanyID: companyID, ID: id}); err != nil {
		return nil, mapErr(err, "get conversation")
	}
	rows, err := q.ListConversationMessages(ctx, store.ListConversationMessagesParams{CompanyID: companyID, ConversationID: id,
		Lim: i32(clamp(limit, 200, maxMessageListLimit))})
	if err != nil {
		return nil, fmt.Errorf("list conversation messages: %w", err)
	}
	out := make([]domain.ConversationMessage, len(rows))
	for i, r := range rows {
		out[i] = messageFromRow(r)
	}
	return out, nil
}

// ---------- escrita ----------

func requireHuman(actor domain.Actor) error {
	if actor.Type != domain.ActorHuman {
		return fmt.Errorf("chat: only a human writes in a conversation: %w", domain.ErrNotAllowed)
	}
	return nil
}

func cleanMessage(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", domain.Invalid("message", "escreva algo antes de enviar")
	}
	if len(body) > domain.MaxMessageBytes {
		return "", domain.Invalid("message", fmt.Sprintf("a mensagem passa de %d bytes", domain.MaxMessageBytes))
	}
	return strings.ToValidUTF8(body, ""), nil
}

// CreateConversation opens a conversation with a persona. With a message it also sends it, in the same
// transaction; when the persona cannot answer now (paused, over budget, weekly guard) nothing is created and
// the reason is the error.
func (s *Service) CreateConversation(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateConversationInput) (ConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ConversationView{}, err
	}
	if err := requireHuman(actor); err != nil {
		return ConversationView{}, err
	}
	var msg string
	if strings.TrimSpace(in.Message) != "" {
		var err error
		if msg, err = cleanMessage(in.Message); err != nil {
			return ConversationView{}, err
		}
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = domain.TitleFromMessage(msg)
	}
	if title == "" {
		return ConversationView{}, domain.Invalid("title", "informe um título ou a primeira mensagem")
	}
	if n := len([]rune(title)); n > domain.MaxConversationTitle {
		return ConversationView{}, domain.Invalid("title", fmt.Sprintf("o título passa de %d caracteres", domain.MaxConversationTitle))
	}
	var (
		out     ConversationView
		blocked error
	)
	err := s.withTx(ctx, func(t *txc) error {
		p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: in.PersonaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		conv, err := t.q.CreateConversation(ctx, store.CreateConversationParams{ID: domain.NewID(), CompanyID: companyID,
			PersonaID: p.ID, Title: title, CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create conversation")
		}
		if err := t.activity(actor, companyID, "conversation.created", "conversation", conv.ID,
			map[string]any{"persona_id": p.ID, "title": title}, ""); err != nil {
			return err
		}
		if msg != "" {
			_, berr, err := t.sendChatMessage(conv, actor, msg)
			if err != nil {
				return err
			}
			if berr != nil {
				// The persona cannot answer: do not leave an empty conversation behind. The side effects of the
				// refusal (pause, alerts) stay.
				blocked = berr
				return t.q.DeleteConversation(ctx, store.DeleteConversationParams{CompanyID: companyID, ID: conv.ID})
			}
		}
		out, err = loadConversationView(ctx, t.q, companyID, conv.ID)
		return err
	})
	if err != nil {
		return ConversationView{}, err
	}
	if blocked != nil {
		return ConversationView{}, blocked
	}
	return out, nil
}

// SendMessage adds a human message to an open conversation and has the persona answer it. When a run of the
// conversation is already going, the message waits and the next run answers it together with any others that
// arrived meanwhile. When the persona cannot work now (paused, in error, over budget, weekly guard) the message
// is refused and the error carries the reason.
func (s *Service) SendMessage(ctx context.Context, actor domain.Actor, companyID, conversationID domain.ID, body string) (domain.ConversationMessage, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.ConversationMessage{}, err
	}
	if err := requireHuman(actor); err != nil {
		return domain.ConversationMessage{}, err
	}
	body, err := cleanMessage(body)
	if err != nil {
		return domain.ConversationMessage{}, err
	}
	var (
		out     domain.ConversationMessage
		blocked error
	)
	err = s.withTx(ctx, func(t *txc) error {
		conv, err := t.q.GetConversationForUpdate(ctx, store.GetConversationForUpdateParams{CompanyID: companyID, ID: conversationID})
		if err != nil {
			return mapErr(err, "get conversation")
		}
		if domain.ConversationStatus(conv.Status) != domain.ConversationOpen {
			return domain.Invalid("conversation", "a conversa está arquivada; reabra para continuar")
		}
		m, berr, err := t.sendChatMessage(conv, actor, body)
		if err != nil {
			return err
		}
		blocked = berr
		if berr == nil {
			out = messageFromRow(m)
		}
		return nil
	})
	if err != nil {
		return domain.ConversationMessage{}, err
	}
	if blocked != nil {
		return domain.ConversationMessage{}, blocked
	}
	return out, nil
}

// SetConversationStatus archives or reopens a conversation. Archiving does not cancel a run in progress; its
// reply still lands in the conversation.
func (s *Service) SetConversationStatus(ctx context.Context, actor domain.Actor, companyID, id domain.ID, status domain.ConversationStatus) (ConversationView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ConversationView{}, err
	}
	if !status.Valid() {
		return ConversationView{}, domain.Invalid("status", fmt.Sprintf("unknown conversation status %q", status))
	}
	var out ConversationView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetConversationForUpdate(ctx, store.GetConversationForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get conversation")
		}
		if domain.ConversationStatus(cur.Status) != status {
			if _, err := t.q.SetConversationStatus(ctx, store.SetConversationStatusParams{CompanyID: companyID, ID: id, Status: string(status), Now: t.now}); err != nil {
				return mapErr(err, "set conversation status")
			}
			verb := "conversation.archived"
			if status == domain.ConversationOpen {
				verb = "conversation.reopened"
			}
			if err := t.activity(actor, companyID, verb, "conversation", id, map[string]any{"persona_id": cur.PersonaID}, ""); err != nil {
				return err
			}
		}
		out, err = loadConversationView(ctx, t.q, companyID, id)
		return err
	})
	return out, err
}

// ---------- núcleo transacional ----------

// sendChatMessage stores the human message and makes sure a run will answer it. The conversation row must be
// locked by the caller (or be new). When the persona cannot take a run now it returns blocked and stores
// nothing; the transaction must still commit, because the refusal may have paused the persona and opened alerts.
func (t *txc) sendChatMessage(conv store.Conversation, actor domain.Actor, body string) (msg store.ConversationMessage, blocked, err error) {
	var run *store.Run
	_, aerr := t.q.ActiveRunForConversation(t.ctx, &conv.ID)
	switch {
	case aerr == nil:
		pending, err := t.q.ListPendingConversationMessages(t.ctx, conv.ID)
		if err != nil {
			return msg, nil, fmt.Errorf("list pending messages: %w", err)
		}
		if len(pending) >= maxPendingMessages {
			return msg, nil, domain.Invalid("message", fmt.Sprintf("já há %d mensagens esperando a resposta; aguarde a persona", len(pending)))
		}
	case isNoRows(aerr):
		r, berr, err := t.openChatRun(conv)
		if err != nil || berr != nil {
			return msg, berr, err
		}
		run = &r
	default:
		return msg, nil, fmt.Errorf("active run for conversation: %w", aerr)
	}
	msg, err = t.q.InsertConversationMessage(t.ctx, store.InsertConversationMessageParams{ID: domain.NewID(), CompanyID: conv.CompanyID,
		ConversationID: conv.ID, AuthorType: string(domain.MessageHuman), AuthorID: actor.ID, Body: body, Now: t.now})
	if err != nil {
		return msg, nil, fmt.Errorf("insert message: %w", err)
	}
	if err := t.q.TouchConversation(t.ctx, store.TouchConversationParams{ID: conv.ID, Now: t.now}); err != nil {
		return msg, nil, fmt.Errorf("touch conversation: %w", err)
	}
	if err := t.activity(actor, conv.CompanyID, "conversation.message", "conversation", conv.ID,
		map[string]any{"message_id": msg.ID, "persona_id": conv.PersonaID, "queued": run == nil}, ""); err != nil {
		return msg, nil, err
	}
	if run != nil {
		if err := t.launchChatRun(conv, *run); err != nil {
			return msg, nil, err
		}
	}
	return msg, nil, nil
}

// personaBlockedError is the refusal of a persona that cannot take a chat run; Error() is the reason.
type personaBlockedError struct{ reason string }

func (e *personaBlockedError) Error() string { return e.reason }
func (e *personaBlockedError) Unwrap() error { return domain.ErrPersonaUnavailable }

// chatBlockReason explains why a persona in this state cannot take a chat run ("" = it can).
func chatBlockReason(p store.Persona) string {
	st := domain.PersonaStatus(p.Status)
	if st.CanWork() || st == domain.PersonaAwaitingGate {
		return ""
	}
	why := ""
	switch st {
	case domain.PersonaPaused:
		why = "pausada"
		if p.PauseReason != nil && strings.TrimSpace(*p.PauseReason) != "" {
			why += " (" + strings.TrimSpace(*p.PauseReason) + ")"
		}
	case domain.PersonaError:
		why = "em erro"
		if p.LastError != nil && strings.TrimSpace(*p.LastError) != "" {
			why += " (" + clipText(strings.TrimSpace(*p.LastError), 200) + ")"
		}
	case domain.PersonaOff:
		why = "desligada"
	default:
		why = string(st)
	}
	return fmt.Sprintf("a persona %s está %s e não pode responder agora", p.Name, why)
}

// openChatRun checks that the persona can take a run (status, monthly budgets, weekly guard) and creates the
// queued run of the conversation. It claims no messages and enqueues nothing (launchChatRun). A refusal comes
// back as blocked (the transaction still commits its side effects); err is an unexpected failure.
func (t *txc) openChatRun(conv store.Conversation) (run store.Run, blocked, err error) {
	p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: conv.CompanyID, ID: conv.PersonaID})
	if err != nil {
		return run, nil, mapErr(err, "get persona")
	}
	if reason := chatBlockReason(p); reason != "" {
		return run, &personaBlockedError{reason: reason}, nil
	}
	co, err := t.q.GetCompanyByID(t.ctx, conv.CompanyID)
	if err != nil {
		return run, nil, mapErr(err, "get company")
	}
	if berr := t.checkBudgets(co, p, nil, true); berr != nil {
		return run, berr, nil
	}
	model, modelSource := domain.ResolveModel("", p.Model)
	sub, err := t.subscriptionFor(p, model)
	if err != nil {
		return run, nil, err
	}
	var subID *domain.ID
	if sub != nil {
		subID = &sub.ID
		if gerr := t.checkWeeklyGuard(co, *sub); gerr != nil {
			if !errors.Is(gerr, domain.ErrSubscriptionGuard) {
				return run, nil, gerr
			}
			return run, gerr, nil
		}
	}
	// A chat has no task or project: it works in the persona's workspace (or a temporary directory).
	ws, wsSource := pickWorkspace(nil, nil, p)
	run, err = t.insertRun(conv.CompanyID, nil, &conv.ID, p.ID, p.Agent, model, modelSource, subID, ws, wsSource, domain.TriggerChat)
	if err != nil {
		return run, nil, err
	}
	if err := t.activity(domain.SystemActor, conv.CompanyID, "run.queued", "run", run.ID,
		map[string]any{"conversation_id": conv.ID, "persona_id": p.ID, "trigger": domain.TriggerChat}, ""); err != nil {
		return run, nil, err
	}
	return run, nil, nil
}

// launchChatRun hands every waiting human message to the run and enqueues its execution.
func (t *txc) launchChatRun(conv store.Conversation, run store.Run) error {
	if _, err := t.q.ClaimPendingConversationMessages(t.ctx, store.ClaimPendingConversationMessagesParams{ConversationID: conv.ID, RunID: &run.ID}); err != nil {
		return fmt.Errorf("claim messages: %w", err)
	}
	if t.s.Jobs != nil {
		if err := t.s.Jobs.ExecuteRunTx(t.ctx, t.tx, conv.CompanyID, run.ID); err != nil {
			return fmt.Errorf("enqueue execute_run: %w", err)
		}
	}
	t.publishRunStatus(runFromRow(run))
	return nil
}

// chatNotice is the system message that tells the conversation why a run ended without a reply.
func chatNotice(status domain.RunStatus, summary, errMsg string) string {
	switch status {
	case domain.RunDone:
		if strings.TrimSpace(summary) == "" {
			return "A persona terminou a execução sem escrever uma resposta."
		}
	case domain.RunFailed:
		if strings.TrimSpace(errMsg) == "" {
			errMsg = "o agente reportou erro"
		}
		return "A execução falhou: " + clipText(strings.TrimSpace(errMsg), 500)
	case domain.RunCancelled:
		return "A resposta foi cancelada."
	case domain.RunInterrupted:
		return "A resposta foi interrompida porque o servidor reiniciou. Envie a mensagem de novo se ainda precisar dela."
	case domain.RunExpired:
		return "A execução passou do tempo limite sem responder."
	}
	return "A execução terminou sem resposta."
}

// chatRunEnded closes a chat run in its conversation: the final summary becomes the persona's reply (or, when
// there is none, a notice says why), and messages that arrived during the run start the next one.
func (t *txc) chatRunEnded(run store.Run, status domain.RunStatus, summary, errMsg string) error {
	if run.ConversationID == nil {
		return nil
	}
	conv, err := t.q.GetConversation(t.ctx, store.GetConversationParams{CompanyID: run.CompanyID, ID: *run.ConversationID})
	if err != nil {
		return mapErr(err, "get conversation")
	}
	reply := status == domain.RunDone && strings.TrimSpace(summary) != ""
	body, kind, author := strings.TrimSpace(summary), domain.MessagePersona, &run.PersonaID
	if reply {
		body = clipText(body, 90000)
	} else {
		body, kind, author = chatNotice(status, summary, errMsg), domain.MessageSystem, nil
	}
	if err := t.addChatMessage(conv, kind, author, body, &run.ID); err != nil {
		return err
	}
	if reply {
		if err := t.activity(domain.PersonaActor(run.PersonaID), run.CompanyID, "conversation.replied", "conversation", conv.ID,
			map[string]any{"run_id": run.ID}, ""); err != nil {
			return err
		}
	}
	if status == domain.RunCancelled || status == domain.RunInterrupted ||
		domain.ConversationStatus(conv.Status) != domain.ConversationOpen {
		return nil
	}
	pending, err := t.q.ListPendingConversationMessages(t.ctx, conv.ID)
	if err != nil {
		return fmt.Errorf("list pending messages: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	next, berr, err := t.openChatRun(conv)
	if err != nil {
		return err
	}
	if berr != nil {
		return t.addChatMessage(conv, domain.MessageSystem, nil,
			fmt.Sprintf("Não consegui responder às %d mensagem(ns) em espera: %s. Elas seguem na fila e serão respondidas quando você enviar a próxima mensagem.", len(pending), berr.Error()), &run.ID)
	}
	return t.launchChatRun(conv, next)
}

// addChatMessage stores a persona or system message and moves the conversation to the top of the list.
func (t *txc) addChatMessage(conv store.Conversation, kind domain.MessageAuthor, author *domain.ID, body string, runID *domain.ID) error {
	if _, err := t.q.InsertConversationMessage(t.ctx, store.InsertConversationMessageParams{ID: domain.NewID(), CompanyID: conv.CompanyID,
		ConversationID: conv.ID, AuthorType: string(kind), AuthorID: author, Body: body, RunID: runID, Now: t.now}); err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	if err := t.q.TouchConversation(t.ctx, store.TouchConversationParams{ID: conv.ID, Now: t.now}); err != nil {
		return fmt.Errorf("touch conversation: %w", err)
	}
	return nil
}

// loadChatContext reads the conversation for a chat run: what was already said, what this run must answer and
// the harness session it may resume.
func loadChatContext(ctx context.Context, q *store.Queries, run domain.Run, persona domain.Persona) (*ChatContext, error) {
	if run.ConversationID == nil {
		return nil, nil
	}
	conv, err := q.GetConversation(ctx, store.GetConversationParams{CompanyID: run.CompanyID, ID: *run.ConversationID})
	if err != nil {
		return nil, mapErr(err, "get conversation")
	}
	rows, err := q.ListConversationMessages(ctx, store.ListConversationMessagesParams{CompanyID: run.CompanyID,
		ConversationID: conv.ID, Lim: chatContextMessages})
	if err != nil {
		return nil, fmt.Errorf("list conversation messages: %w", err)
	}
	cc := &ChatContext{Conversation: conversationFromRow(conv)}
	for _, r := range rows {
		m := messageFromRow(r)
		switch {
		case m.Kind == domain.MessageHuman && m.RunID != nil && *m.RunID == run.ID:
			cc.New = append(cc.New, m)
		case m.Kind == domain.MessageHuman && m.RunID == nil:
			// arrived after this run started: the next run answers it
		default:
			cc.History = append(cc.History, m)
		}
	}
	prev, err := q.LatestRunOfConversation(ctx, store.LatestRunOfConversationParams{ConversationID: run.ConversationID, ExceptID: run.ID})
	switch {
	case err == nil:
		if prev.Status == string(domain.RunDone) && prev.SessionRef != "" && prev.Agent == persona.Agent && prev.CreatedAt.Before(run.CreatedAt) {
			cc.ResumeSession = prev.SessionRef
		}
	case !isNoRows(err):
		return nil, fmt.Errorf("latest run of conversation: %w", err)
	}
	return cc, nil
}

// personaWorkspaceID is the workspace that scopes the memory of a persona's chat facts.
func (t *txc) personaWorkspaceID(companyID, personaID domain.ID) (*domain.ID, error) {
	p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
	if err != nil {
		return nil, mapErr(err, "get persona")
	}
	return p.WorkspaceID, nil
}

// chatRunFact is the memory fact of a finished chat run.
func chatRunFact(v RunView, status domain.RunStatus, res adapter.Result, now time.Time) memory.Fact {
	var b strings.Builder
	title := v.ConversationTitle
	if title == "" {
		title = "conversa"
	}
	fmt.Fprintf(&b, "# Chat — %s\n\n", title)
	fmt.Fprintf(&b, "- Persona: %s\n- Status: %s\n", v.PersonaName, status)
	if v.CostUSD != nil {
		est := ""
		if v.CostSource == domain.CostEstimated {
			est = " (estimado)"
		}
		fmt.Fprintf(&b, "- Custo: US$ %.4f%s\n", *v.CostUSD, est)
	} else {
		b.WriteString("- Custo: n/d\n")
	}
	fmt.Fprintf(&b, "- Run: %s\n", v.ID)
	if res.Error != "" {
		fmt.Fprintf(&b, "- Erro: %s\n", res.Error)
	}
	if strings.TrimSpace(res.Summary) != "" {
		fmt.Fprintf(&b, "\n## Resposta\n\n%s\n", res.Summary)
	}
	return memory.Fact{Kind: "run.finished",
		Path:  fmt.Sprintf("hanko/chats/%s-%s.md", now.Format("2006-01-02"), v.ID.String()[:8]),
		Title: fmt.Sprintf("Chat %s (%s)", title, status), Body: b.String(), Tags: []string{"hanko", "chat"}}
}
