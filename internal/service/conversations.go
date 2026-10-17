package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

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
	// Room é a configuração e os participantes de uma sala (ADR-0033); nil no chat direto.
	Room *RoomView `json:"room,omitempty"`
}

// ConversationFilter filtra a listagem de conversas.
type ConversationFilter struct {
	PersonaID *domain.ID
	// Status vazio = todas.
	Status domain.ConversationStatus
	// Kind vazio = chat direto e salas; numa sala, PersonaID filtra por participante ativo (ADR-0033).
	Kind  domain.ConversationKind
	Limit int // default 50, máx 200
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
	// Room é o contexto de uma run de sala (participantes, modo, salto); nil no chat direto (ADR-0033).
	Room *RoomContext
}

func conversationFromRow(r store.Conversation) domain.Conversation {
	return domain.Conversation{ID: r.ID, CompanyID: r.CompanyID, PersonaID: idOrZero(r.PersonaID), Kind: domain.ConversationKind(r.Kind), Title: r.Title,
		Status: domain.ConversationStatus(r.Status), CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LastMessageAt: r.LastMessageAt}
}

func conversationView(c store.Conversation, personaName, personaSlug string, active *domain.ID, pending int64, cost float64) ConversationView {
	return ConversationView{Conversation: conversationFromRow(c), PersonaName: personaName, PersonaSlug: personaSlug,
		ActiveRunID: active, PendingMessages: int(pending), CostUSD: cost, Room: roomViewOf(c)}
}

func messageFromRow(r store.ConversationMessage) domain.ConversationMessage {
	return domain.ConversationMessage{ID: r.ID, CompanyID: r.CompanyID, ConversationID: r.ConversationID,
		Author: actorOf(r.AuthorType, r.AuthorID), Kind: domain.MessageAuthor(r.AuthorType), Body: r.Body,
		RunID: r.RunID, CreatedAt: r.CreatedAt, PersonaID: r.PersonaID, ReplyToID: r.ReplyToID, Mentions: r.Mentions,
		Marker: derefStr(r.Marker)}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ContextResetCommand é o comando que reinicia o contexto da conversa (ADR-0052).
const ContextResetCommand = "/new"

// parseContextReset diz se a mensagem começa com /new e devolve o texto que vem depois (que pode ser vazio). O
// comando é a mensagem inteira ("/new") ou a primeira palavra seguida de espaço ("/new e agora, qual o plano?").
func parseContextReset(body string) (reset bool, rest string) {
	b := strings.TrimSpace(body)
	if b == ContextResetCommand {
		return true, ""
	}
	if after, ok := strings.CutPrefix(b, ContextResetCommand); ok && after != "" && unicode.IsSpace([]rune(after)[0]) {
		return true, strings.TrimSpace(after)
	}
	return false, body
}

// insertContextMarker grava o marcador de reinício de contexto (mensagem de sistema) e o fato na atividade.
func (t *txc) insertContextMarker(conv store.Conversation, actor domain.Actor) (store.ConversationMessage, error) {
	m, err := t.q.InsertContextMarker(t.ctx, store.InsertContextMarkerParams{ID: domain.NewID(), CompanyID: conv.CompanyID,
		ConversationID: conv.ID, Body: "Contexto reiniciado (/new): a conversa recomeça daqui.", Now: t.now})
	if err != nil {
		return m, fmt.Errorf("insert context marker: %w", err)
	}
	if err := t.q.TouchConversation(t.ctx, store.TouchConversationParams{ID: conv.ID, Now: t.now}); err != nil {
		return m, fmt.Errorf("touch conversation: %w", err)
	}
	return m, t.activity(actor, conv.CompanyID, "conversation.context_reset", "conversation", conv.ID,
		map[string]any{"message_id": m.ID, "kind": conv.Kind}, "")
}

func loadConversationView(ctx context.Context, q *store.Queries, companyID, id domain.ID) (ConversationView, error) {
	r, err := q.GetConversationView(ctx, store.GetConversationViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return ConversationView{}, mapErr(err, "get conversation")
	}
	v := conversationView(r.Conversation, r.PersonaName, r.PersonaSlug, r.ActiveRunID, r.PendingMessages, r.CostUsd)
	if err := attachRooms(ctx, q, companyID, []*ConversationView{&v}); err != nil {
		return ConversationView{}, err
	}
	return v, nil
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
	if f.Kind != "" {
		if !f.Kind.Valid() {
			return nil, domain.Invalid("kind", fmt.Sprintf("unknown conversation kind %q", f.Kind))
		}
		k := string(f.Kind)
		p.Kind = &k
	}
	q := s.read()
	rows, err := q.ListConversationViews(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	out := make([]ConversationView, len(rows))
	ptrs := make([]*ConversationView, len(rows))
	for i, r := range rows {
		out[i] = conversationView(r.Conversation, r.PersonaName, r.PersonaSlug, r.ActiveRunID, r.PendingMessages, r.CostUsd)
		ptrs[i] = &out[i]
	}
	if err := attachRooms(ctx, q, companyID, ptrs); err != nil {
		return nil, err
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
	conv, err := q.GetConversation(ctx, store.GetConversationParams{CompanyID: companyID, ID: id})
	if err != nil {
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
	if domain.ConversationKind(conv.Kind) == domain.ConversationRoom {
		if err := attachRoomMessageInfo(ctx, q, companyID, conv.ID, out); err != nil {
			return nil, err
		}
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
			PersonaID: &p.ID, Title: title, CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create conversation")
		}
		// With a first message the persona must be able to take the run before anything is announced: a refused
		// conversation is deleted and leaves no activity (nor webhook) of a conversation that never existed. The
		// side effects of the refusal itself (pause, alerts) stay.
		var run *store.Run
		if msg != "" {
			r, berr, err := t.openChatRun(conv, nil)
			if err != nil {
				return err
			}
			if berr != nil {
				blocked = berr
				return t.q.DeleteConversation(ctx, store.DeleteConversationParams{CompanyID: companyID, ID: conv.ID})
			}
			run = &r
		}
		if err := t.activity(actor, companyID, "conversation.created", "conversation", conv.ID,
			map[string]any{"persona_id": p.ID, "title": title, "kind": domain.ConversationDirect}, ""); err != nil {
			return err
		}
		if run != nil {
			if _, err := t.storeChatMessage(conv, actor, msg, run); err != nil {
				return err
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
	return s.SendMessageWith(ctx, actor, companyID, conversationID, body, nil)
}

// SendMessageWith é SendMessage com anexos já enviados à conversa (PutArtifact com Class attachment e ConversationID,
// ainda sem mensagem): eles passam a pertencer à mensagem na mesma transação, então a run que a recolhe já os vê no
// briefing (ADR-0052). Num "/new" sozinho (só o marcador) os anexos ficam soltos na conversa.
func (s *Service) SendMessageWith(ctx context.Context, actor domain.Actor, companyID, conversationID domain.ID, body string, attachments []domain.ID) (domain.ConversationMessage, error) {
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
	reset, body := parseContextReset(body)
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
		if reset { // /new (ADR-0052): o marcador vale para a conversa direta e para a sala
			mk, err := t.insertContextMarker(conv, actor)
			if err != nil {
				return err
			}
			if body == "" {
				out = messageFromRow(mk)
				return t.clearDraft(companyID, domain.DraftConversation, conversationID)
			}
		}
		if domain.ConversationKind(conv.Kind) == domain.ConversationRoom {
			// sala (ADR-0033): várias personas, cada uma com a sua run e a sua fila
			m, berr, err := t.sendRoomMessage(conv, actor, body)
			if err != nil {
				return err
			}
			blocked, out = berr, m
			if berr == nil { // ADR-0052: enviar limpa o rascunho e liga os anexos, na mesma transação
				if err := t.linkMessageAttachments(companyID, conversationID, m.ID, attachments); err != nil {
					return err
				}
				return t.clearDraft(companyID, domain.DraftConversation, conversationID)
			}
			return nil
		}
		m, berr, err := t.sendChatMessage(conv, actor, body)
		if err != nil {
			return err
		}
		blocked = berr
		if berr == nil {
			out = messageFromRow(m)
			if err := t.linkMessageAttachments(companyID, conversationID, m.ID, attachments); err != nil {
				return err
			}
			return t.clearDraft(companyID, domain.DraftConversation, conversationID)
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
			if err := t.activity(actor, companyID, verb, "conversation", id, map[string]any{"persona_id": cur.PersonaID, "kind": cur.Kind}, ""); err != nil {
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
		r, berr, err := t.openChatRun(conv, nil)
		if err != nil || berr != nil {
			return msg, berr, err
		}
		run = &r
	default:
		return msg, nil, fmt.Errorf("active run for conversation: %w", aerr)
	}
	msg, err = t.storeChatMessage(conv, actor, body, run)
	return msg, nil, err
}

// storeChatMessage inserts the human message, records the activity and, when a run was opened for it, launches it.
func (t *txc) storeChatMessage(conv store.Conversation, actor domain.Actor, body string, run *store.Run) (msg store.ConversationMessage, err error) {
	msg, err = t.q.InsertConversationMessage(t.ctx, store.InsertConversationMessageParams{ID: domain.NewID(), CompanyID: conv.CompanyID,
		ConversationID: conv.ID, AuthorType: string(domain.MessageHuman), AuthorID: actor.ID, Body: body, Now: t.now})
	if err != nil {
		return msg, fmt.Errorf("insert message: %w", err)
	}
	if err := t.q.TouchConversation(t.ctx, store.TouchConversationParams{ID: conv.ID, Now: t.now}); err != nil {
		return msg, fmt.Errorf("touch conversation: %w", err)
	}
	if err := t.activity(actor, conv.CompanyID, "conversation.message", "conversation", conv.ID,
		map[string]any{"message_id": msg.ID, "persona_id": conv.PersonaID, "queued": run == nil, "kind": domain.ConversationDirect}, ""); err != nil {
		return msg, err
	}
	if run != nil {
		if err := t.launchChatRun(conv, *run); err != nil {
			return msg, err
		}
	}
	return msg, nil
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
func (t *txc) openChatRun(conv store.Conversation, cont *continuation) (run store.Run, blocked, err error) {
	return t.openPersonaRun(conv, idOrZero(conv.PersonaID), cont)
}

// openPersonaRun is openChatRun for a given persona of the conversation (the one of a direct chat, or a
// participant of a room, ADR-0033).
func (t *txc) openPersonaRun(conv store.Conversation, personaID domain.ID, cont *continuation) (run store.Run, blocked, err error) {
	p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: conv.CompanyID, ID: personaID})
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
	run, err = t.insertRun(conv.CompanyID, nil, &conv.ID, p.ID, p.Agent, model, modelSource, subID, ws, wsSource, domain.TriggerChat, cont)
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
	if domain.ConversationKind(conv.Kind) == domain.ConversationRoom {
		return t.roomRunEnded(conv, run, status, summary, errMsg)
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
			map[string]any{"run_id": run.ID, "kind": domain.ConversationDirect}, ""); err != nil {
			return err
		}
	}
	// Interrupted runs are handled by the continuation (chatInterrupted); cancelling one starts the next run with
	// whatever is pending (ADR-0039, reverts ADR-0027 §4 for cancellation).
	if status == domain.RunInterrupted || domain.ConversationStatus(conv.Status) != domain.ConversationOpen {
		return nil
	}
	pending, err := t.q.ListPendingConversationMessages(t.ctx, conv.ID)
	if err != nil {
		return fmt.Errorf("list pending messages: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	var cont *continuation
	if status == domain.RunCancelled && run.ResumePending && run.ResumeReason == string(domain.ContinueSteer) {
		cont = &continuation{of: run, reason: domain.ContinueSteer, attempt: int(run.ContinuationAttempt)} // steer by restart: keeps the session
	}
	next, berr, err := t.openChatRun(conv, cont)
	if err != nil {
		return err
	}
	if berr != nil {
		return t.addChatMessage(conv, domain.MessageSystem, nil,
			fmt.Sprintf("Não consegui responder às %d mensagem(ns) em espera: %s. Elas seguem na fila e serão respondidas quando você enviar a próxima mensagem.", len(pending), berr.Error()), &run.ID)
	}
	if cont != nil {
		if err := t.runOpenedAsContinuation(next, cont); err != nil {
			return err
		}
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
	if domain.ConversationKind(conv.Kind) == domain.ConversationRoom {
		return loadRoomContext(ctx, q, conv, run, persona)
	}
	rows, err := q.ListConversationMessages(ctx, store.ListConversationMessagesParams{CompanyID: run.CompanyID,
		ConversationID: conv.ID, Lim: chatContextMessages})
	if err != nil {
		return nil, fmt.Errorf("list conversation messages: %w", err)
	}
	cc := &ChatContext{Conversation: conversationFromRow(conv)}
	rows, resetAt := afterContextReset(rows)
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
		if prev.Status == string(domain.RunDone) && prev.SessionRef != "" && prev.Agent == persona.Agent && prev.CreatedAt.Before(run.CreatedAt) &&
			!beforeReset(prev.CreatedAt, resetAt) {
			cc.ResumeSession = prev.SessionRef
		}
	case !isNoRows(err):
		return nil, fmt.Errorf("latest run of conversation: %w", err)
	}
	// A continuation resumes the session of the run it continues, whatever way that run ended (a retry starts clean).
	if run.ContinuationOfRunID != nil {
		cc.ResumeSession = ""
		if run.ContinuationReason.ResumesSession() {
			if of, oerr := q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: run.CompanyID, ID: *run.ContinuationOfRunID}); oerr == nil &&
				of.SessionRef != "" && of.Agent == persona.Agent && !beforeReset(of.CreatedAt, resetAt) {
				cc.ResumeSession = of.SessionRef
			}
		}
	}
	return cc, nil
}

// afterContextReset corta a lista (cronológica) no último marcador de /new: devolve só o que veio depois dele e o
// instante do marcador (zero se não há). O que veio antes continua na conversa, mas fora do contexto da run.
func afterContextReset(rows []store.ConversationMessage) ([]store.ConversationMessage, time.Time) {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Marker != nil {
			return rows[i+1:], rows[i].CreatedAt
		}
	}
	return rows, time.Time{}
}

// beforeReset diz se uma run foi criada antes do último /new: a sessão dela não é retomada.
func beforeReset(runCreated, resetAt time.Time) bool {
	return !resetAt.IsZero() && runCreated.Before(resetAt)
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
