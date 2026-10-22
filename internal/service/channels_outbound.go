package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/channel"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Saída dos canais (ADR-0046): fila de mensagens com estado de entrega, espelhos (gate, chat, task) e review do GitHub.

// outPayload é o que uma mensagem de saída guarda além do texto.
type outPayload struct {
	Buttons     [][]channel.Button `json:"buttons,omitempty"`
	ArtifactIDs []domain.ID        `json:"artifact_ids,omitempty"`
	Review      string             `json:"review,omitempty"`
}

// outSpec descreve uma mensagem a enfileirar.
type outSpec struct {
	kind        string
	peer        string
	body        string
	payload     outPayload
	subjectType string
	subjectID   *domain.ID
	identityID  *domain.ID
}

// maxChannelOutRunes é o teto de uma mensagem de saída (o driver parte o que passa do limite do provedor).
const maxChannelOutRunes = 30000

// queueChannelOut grava a mensagem de saída pendente e enfileira a entrega, na transação do fato que a causou.
func (t *txc) queueChannelOut(ch store.Channel, sp outSpec) (store.ChannelMessage, error) {
	raw, err := json.Marshal(sp.payload)
	if err != nil {
		return store.ChannelMessage{}, fmt.Errorf("marshal channel payload: %w", err)
	}
	m, err := t.q.InsertChannelMessage(t.ctx, store.InsertChannelMessageParams{ID: domain.NewID(), CompanyID: ch.CompanyID, ChannelID: ch.ID,
		Direction: string(domain.MessageOut), Kind: sp.kind, Peer: sp.peer, IdentityID: sp.identityID, Body: truncateRunes(sp.body, maxChannelOutRunes),
		Payload: raw, SubjectType: sp.subjectType, SubjectID: sp.subjectID, Status: string(domain.MsgPending), Now: t.now})
	if err != nil {
		return store.ChannelMessage{}, mapErr(err, "queue channel message")
	}
	if cj, ok := t.s.Jobs.(ChannelJobs); ok {
		if err := cj.SendChannelMessageTx(t.ctx, t.tx, m.ID); err != nil {
			return m, fmt.Errorf("enqueue channel message: %w", err)
		}
	}
	return m, nil
}

// replyChannel enfileira uma resposta do sistema ao interlocutor (fora de uma transação de fato).
func (s *Service) replyChannel(ctx context.Context, ch store.Channel, peer, text string, buttons [][]channel.Button, identityID *domain.ID) error {
	return s.withTx(ctx, func(t *txc) error {
		_, err := t.queueChannelOut(ch, outSpec{kind: "notice", peer: peer, body: text, payload: outPayload{Buttons: buttons}, identityID: identityID})
		return err
	})
}

// ChannelDelivery é o resultado de uma tentativa de entrega.
type ChannelDelivery struct {
	Sent   bool // entregue
	GaveUp bool // falhou de vez (permanente ou sem tentativas)
}

// DeliverChannelMessage faz uma tentativa de entrega de uma mensagem de saída (job channel_send). attempt é 1-based. Falha
// transitória devolve erro para a fila tentar de novo; permanente ou na última tentativa marca a mensagem como failed.
func (s *Service) DeliverChannelMessage(ctx context.Context, id domain.ID, attempt, maxAttempts int) (out ChannelDelivery, err error) {
	ctx, span := otel.Tracer("hanko/channels").Start(ctx, "hanko.channel.send", trace.WithAttributes(attribute.Int("hanko.channel.attempt", attempt)))
	kind := domain.ChannelKind("")
	defer func() {
		result := "sent"
		switch {
		case err != nil:
			result = "retry"
			span.SetStatus(codes.Error, "channel send")
		case out.GaveUp:
			result = "failed"
		case !out.Sent:
			result = "skipped"
		}
		countChannel(ctx, "out", kind, result)
		span.End()
	}()
	q := s.read()
	msg, err := q.GetChannelMessage(ctx, id)
	if err != nil {
		if isNoRows(err) {
			return ChannelDelivery{}, nil
		}
		return ChannelDelivery{}, fmt.Errorf("get channel message: %w", err)
	}
	if domain.ChannelMessageStatus(msg.Status) != domain.MsgPending || domain.MessageDirection(msg.Direction) != domain.MessageOut {
		return ChannelDelivery{}, nil
	}
	ch, err := q.GetChannel(ctx, store.GetChannelParams{CompanyID: msg.CompanyID, ID: msg.ChannelID})
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("get channel: %w", err)
	}
	kind = domain.ChannelKind(ch.Kind)
	giveUp := func(reason string) (ChannelDelivery, error) {
		reason = truncateRunes(reason, 500)
		if ferr := s.withTx(ctx, func(t *txc) error {
			if err := t.q.MarkChannelMessageFailed(ctx, store.MarkChannelMessageFailedParams{ID: id, LastError: reason, Now: t.now}); err != nil {
				return fmt.Errorf("mark channel message failed: %w", err)
			}
			return t.activity(domain.SystemActor, msg.CompanyID, "channel.message_failed", "channel", ch.ID,
				map[string]any{"message_id": id, "kind": msg.Kind, "error": reason, "attempts": msg.Attempts + 1}, "")
		}); ferr != nil {
			return ChannelDelivery{}, ferr
		}
		return ChannelDelivery{GaveUp: true}, nil
	}
	if !ch.Enabled {
		return giveUp(errChannelDisabled.Error())
	}
	d, cl, cerr := s.channelClient(ctx, ch)
	if cerr != nil {
		return giveUp(cerr.Error())
	}
	var pl outPayload
	_ = json.Unmarshal(msg.Payload, &pl)
	o := channel.Outbound{Peer: msg.Peer, Text: msg.Body, Buttons: pl.Buttons}
	if pl.Review != "" {
		o.Review = &channel.Review{Event: domain.GitHubReviewEvent(pl.Review)}
	}
	for _, aid := range pl.ArtifactIDs {
		a, content, aerr := s.ArtifactContent(ctx, msg.CompanyID, aid)
		if aerr != nil {
			return giveUp("anexo indisponível: " + aerr.Error())
		}
		o.Documents = append(o.Documents, channel.Document{Name: a.Name, Mime: a.Mime, Content: content})
	}
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	providerID, serr := d.Send(sctx, cl, o)
	if serr == nil {
		if err := s.withTx(ctx, func(t *txc) error {
			if err := t.q.MarkChannelMessageSent(ctx, store.MarkChannelMessageSentParams{ID: id, ProviderMsgID: providerID, Now: &t.now}); err != nil {
				return fmt.Errorf("mark channel message sent: %w", err)
			}
			return t.activity(domain.SystemActor, msg.CompanyID, "channel.message_sent", "channel", ch.ID,
				map[string]any{"message_id": id, "kind": msg.Kind, "peer": msg.Peer}, "")
		}); err != nil {
			return ChannelDelivery{}, err
		}
		return ChannelDelivery{Sent: true}, nil
	}
	if ctx.Err() != nil {
		return ChannelDelivery{}, ctx.Err()
	}
	if channel.IsPermanent(serr) || attempt >= maxAttempts {
		return giveUp(serr.Error())
	}
	if err := s.withTx(ctx, func(t *txc) error {
		return t.q.MarkChannelMessageAttempt(ctx, store.MarkChannelMessageAttemptParams{ID: id, LastError: truncateRunes(serr.Error(), 500), Now: t.now})
	}); err != nil {
		return ChannelDelivery{}, err
	}
	return ChannelDelivery{}, serr
}

// GiveUpChannelMessage marca como failed a mensagem cuja fila esgotou as tentativas sem o service ter fechado (job
// perdido, processo caído). Idempotente.
func (s *Service) GiveUpChannelMessage(ctx context.Context, id domain.ID, cause error) {
	_ = s.withTx(ctx, func(t *txc) error {
		msg, err := t.q.GetChannelMessageForUpdate(ctx, id)
		if err != nil || domain.ChannelMessageStatus(msg.Status) != domain.MsgPending {
			return nil //nolint:nilerr // nada a fechar
		}
		reason := "tentativas esgotadas"
		if cause != nil {
			reason = truncateRunes(cause.Error(), 500)
		}
		if err := t.q.MarkChannelMessageFailed(ctx, store.MarkChannelMessageFailedParams{ID: id, LastError: reason, Now: t.now}); err != nil {
			return fmt.Errorf("mark channel message failed: %w", err)
		}
		return t.activity(domain.SystemActor, msg.CompanyID, "channel.message_failed", "channel", msg.ChannelID,
			map[string]any{"message_id": id, "kind": msg.Kind, "error": reason}, "")
	})
}

// ResendChannelMessage recoloca na fila uma mensagem de saída que falhou (admin).
func (s *Service) ResendChannelMessage(ctx context.Context, actor domain.Actor, companyID, messageID domain.ID) (domain.ChannelMessage, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ChannelMessage{}, err
	}
	var out domain.ChannelMessage
	err := s.withTx(ctx, func(t *txc) error {
		m, err := t.q.RequeueChannelMessage(ctx, store.RequeueChannelMessageParams{CompanyID: companyID, ID: messageID, Now: t.now})
		if err != nil {
			if isNoRows(err) {
				return fmt.Errorf("só uma mensagem de saída que falhou pode ser reenviada: %w", domain.ErrConflict)
			}
			return fmt.Errorf("requeue channel message: %w", err)
		}
		if cj, ok := t.s.Jobs.(ChannelJobs); ok {
			if err := cj.SendChannelMessageTx(ctx, t.tx, m.ID); err != nil {
				return fmt.Errorf("enqueue channel message: %w", err)
			}
		}
		out = channelMessageFromRow(m)
		return t.activity(actor, companyID, "channel.message_requeued", "channel", m.ChannelID, map[string]any{"message_id": m.ID}, "")
	})
	return out, err
}

// SendChannelMessage enfileira uma mensagem avulsa do admin a um interlocutor do canal (opcionalmente com artefatos).
func (s *Service) SendChannelMessage(ctx context.Context, actor domain.Actor, companyID domain.ID, channelRef, peer, text string, artifactIDs []domain.ID) (domain.ChannelMessage, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ChannelMessage{}, err
	}
	if actor.Type == domain.ActorPersona {
		return domain.ChannelMessage{}, domain.ErrForbidden
	}
	text = strings.TrimSpace(text)
	if text == "" && len(artifactIDs) == 0 {
		return domain.ChannelMessage{}, domain.Invalid("text", "informe o texto ou um artefato")
	}
	if strings.TrimSpace(peer) == "" {
		return domain.ChannelMessage{}, domain.Invalid("peer", "informe o destino (chat do Telegram ou dono/repo#N)")
	}
	var out domain.ChannelMessage
	err := s.withTx(ctx, func(t *txc) error {
		ch, err := s.channelRow(ctx, t.q, companyID, channelRef)
		if err != nil {
			return err
		}
		if !ch.Enabled {
			return fmt.Errorf("%s: %w", errChannelDisabled, domain.ErrConflict)
		}
		if domain.ChannelKind(ch.Kind) == domain.ChannelGitHub {
			repo, _, ok := domain.ParseGitHubPeer(peer)
			if !ok || !domain.RepoAllowed(channelFromRow(ch).Config.Repos, repo) {
				return domain.Invalid("peer", "use dono/repo#N de um repositório do canal")
			}
		}
		for _, id := range artifactIDs {
			if _, err := t.q.GetArtifactByID(ctx, store.GetArtifactByIDParams{CompanyID: companyID, ID: id}); err != nil {
				return domain.Invalid("artifact_ids", "artefato não encontrado na empresa")
			}
		}
		m, err := t.queueChannelOut(ch, outSpec{kind: "notice", peer: strings.TrimSpace(peer), body: text, payload: outPayload{ArtifactIDs: artifactIDs}})
		if err != nil {
			return err
		}
		out = channelMessageFromRow(m)
		return nil
	})
	return out, err
}

// ---------- espelhos ----------

// gateButtonKinds são os tipos de gate que se decidem por botão (aprovar/rejeitar sem nota nem leitura longa).
func gateButtonKind(k domain.GateKind) bool {
	switch k {
	case domain.GateApproval, domain.GateTaskReview, domain.GateTool, domain.GateMemory:
		return true
	}
	return false
}

// gateAdminOnly: contratar e mudar instruções são decisões de admin (ADR-0021).
func gateAdminOnly(k domain.GateKind) bool {
	return k == domain.GateHire || k == domain.GateInstruction
}

// gateCallback monta os dados do botão de um gate: g|<id>|a (aprovar) ou g|<id>|r (rejeitar).
func gateCallback(id domain.ID, verb string) string { return "g|" + id.String() + "|" + verb }

// channelNotifyGate avisa pelo Telegram, na transação do gate, cada pessoa vinculada que pode decidi-lo.
func (t *txc) channelNotifyGate(companyID domain.ID, g domain.Gate, taskKey, taskTitle string) error {
	chs, err := t.q.ListEnabledChannelsByKind(t.ctx, store.ListEnabledChannelsByKindParams{CompanyID: companyID, Kind: string(domain.ChannelTelegram)})
	if err != nil {
		return fmt.Errorf("list telegram channels: %w", err)
	}
	if len(chs) == 0 {
		return nil
	}
	co, err := t.q.GetCompanyByID(t.ctx, companyID)
	if err != nil {
		return mapErr(err, "company")
	}
	text, rows := t.s.gateOutMessage(g, taskKey, taskTitle, co.Slug)
	gid := g.ID
	for _, ch := range chs {
		ids, err := t.q.ListLinkedChannelIdentitiesWithUser(t.ctx, ch.ID)
		if err != nil {
			return fmt.Errorf("list channel identities: %w", err)
		}
		for _, ident := range ids {
			if ident.ExternalID == nil {
				continue
			}
			u, err := t.q.GetUserByID(t.ctx, ident.UserID)
			if err != nil {
				continue
			}
			role, err := roleOf(t.ctx, t.q, userFromRow(u), companyID)
			if err != nil || !gateDecidableBy(g, u.ID, role) {
				continue
			}
			idID := ident.ID
			if _, err := t.queueChannelOut(ch, outSpec{kind: "gate", peer: *ident.ExternalID, body: text, payload: outPayload{Buttons: rows},
				subjectType: "gate", subjectID: &gid, identityID: &idID}); err != nil {
				return err
			}
		}
	}
	return nil
}

// channelMirrorChat espelha a resposta da persona (ou o aviso do sistema) de uma conversa ligada a um canal.
func (t *txc) channelMirrorChat(conv store.Conversation, body string) error {
	threads, err := t.q.GetChannelThreadByConversation(t.ctx, &conv.ID)
	if err != nil {
		return fmt.Errorf("list channel threads: %w", err)
	}
	for _, th := range threads {
		ch, err := t.q.GetChannel(t.ctx, store.GetChannelParams{CompanyID: conv.CompanyID, ID: th.ChannelID})
		if err != nil || !ch.Enabled {
			continue
		}
		cid := conv.ID
		if _, err := t.queueChannelOut(ch, outSpec{kind: "reply", peer: th.Peer, body: body, subjectType: "conversation", subjectID: &cid}); err != nil {
			return err
		}
	}
	return nil
}

// channelMirrorTask publica no PR/issue o resumo que a persona deixou numa task nascida de um comentário do GitHub.
func (t *txc) channelMirrorTask(companyID, taskID domain.ID, body string) error {
	threads, err := t.q.GetChannelThreadsByTask(t.ctx, &taskID)
	if err != nil {
		return fmt.Errorf("list channel threads: %w", err)
	}
	if len(threads) == 0 {
		return nil
	}
	key := taskID.String()
	if task, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID}); err == nil {
		if co, err := t.q.GetCompanyByID(t.ctx, companyID); err == nil {
			key = domain.TaskKey(co.TaskPrefix, task.Number)
		}
	}
	for _, th := range threads {
		ch, err := t.q.GetChannel(t.ctx, store.GetChannelParams{CompanyID: companyID, ID: th.ChannelID})
		if err != nil || !ch.Enabled || domain.ChannelKind(ch.Kind) != domain.ChannelGitHub {
			continue
		}
		tid := taskID
		if _, err := t.queueChannelOut(ch, outSpec{kind: "reply", peer: th.Peer, body: body + "\n\n— Hanko, task " + key,
			subjectType: "task", subjectID: &tid}); err != nil {
			return err
		}
	}
	return nil
}

// ---------- review do GitHub ----------

// PublishReviewInput pede a publicação de uma review (ou comentário de review) num PR.
type PublishReviewInput struct {
	// Channel é o id ou nome do canal GitHub; vazio resolve pelo TaskID (a thread da task) ou pelo único canal GitHub ligado.
	Channel string
	// Peer é "dono/repo#N"; vazio resolve pelo TaskID.
	Peer   string
	TaskID *domain.ID
	Event  domain.GitHubReviewEvent
	Body   string
}

// PublishChannelReview enfileira a review de um pull request. A persona só publica em PRs que a acordaram (a thread
// existe) e nunca aprova: APPROVE é ato de uma pessoa admin. Repositório fora da lista do canal é recusado.
func (s *Service) PublishChannelReview(ctx context.Context, actor domain.Actor, companyID domain.ID, in PublishReviewInput) (domain.ChannelMessage, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.ChannelMessage{}, err
	}
	if !in.Event.Valid() {
		return domain.ChannelMessage{}, domain.Invalid("event", "use COMMENT, REQUEST_CHANGES ou APPROVE")
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return domain.ChannelMessage{}, domain.Invalid("body", "obrigatório")
	}
	if in.Event == domain.GHReviewApprove {
		switch {
		case actor.Type != domain.ActorHuman:
			return domain.ChannelMessage{}, fmt.Errorf("aprovar um pull request é ato de uma pessoa: %w", domain.ErrForbidden)
		default:
			if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
				return domain.ChannelMessage{}, err
			}
		}
	}
	var out domain.ChannelMessage
	err := s.withTx(ctx, func(t *txc) error {
		ch, peer, err := s.resolveReviewTarget(t, companyID, in)
		if err != nil {
			return err
		}
		if !ch.Enabled {
			return fmt.Errorf("%s: %w", errChannelDisabled, domain.ErrConflict)
		}
		repo, _, ok := domain.ParseGitHubPeer(peer)
		if !ok {
			return domain.Invalid("peer", "use dono/repo#N")
		}
		if !domain.RepoAllowed(channelFromRow(ch).Config.Repos, repo) {
			return domain.Invalid("peer", "o repositório não está na lista do canal")
		}
		if actor.Type == domain.ActorPersona {
			if _, err := t.q.GetChannelThread(ctx, store.GetChannelThreadParams{ChannelID: ch.ID, Peer: peer}); err != nil {
				if isNoRows(err) {
					return fmt.Errorf("a persona só publica review em pull requests que a acordaram: %w", domain.ErrForbidden)
				}
				return fmt.Errorf("get channel thread: %w", err)
			}
		}
		m, err := t.queueChannelOut(ch, outSpec{kind: "review", peer: peer, body: body, payload: outPayload{Review: string(in.Event)}})
		if err != nil {
			return err
		}
		out = channelMessageFromRow(m)
		return t.activity(actor, companyID, "channel.review_requested", "channel", ch.ID,
			map[string]any{"message_id": m.ID, "peer": peer, "event": in.Event}, "")
	})
	return out, err
}

func (s *Service) resolveReviewTarget(t *txc, companyID domain.ID, in PublishReviewInput) (store.Channel, string, error) {
	ctx := t.ctx
	peer := strings.TrimSpace(in.Peer)
	var ch store.Channel
	switch {
	case strings.TrimSpace(in.Channel) != "":
		row, err := s.channelRow(ctx, t.q, companyID, strings.TrimSpace(in.Channel))
		if err != nil {
			return ch, "", err
		}
		ch = row
		if peer == "" && in.TaskID != nil {
			ths, err := t.q.GetChannelThreadsByTask(ctx, in.TaskID)
			if err != nil {
				return ch, "", fmt.Errorf("list channel threads: %w", err)
			}
			for _, th := range ths {
				if th.ChannelID == ch.ID {
					peer = th.Peer
				}
			}
		}
	case in.TaskID != nil:
		ths, err := t.q.GetChannelThreadsByTask(ctx, in.TaskID)
		if err != nil {
			return ch, "", fmt.Errorf("list channel threads: %w", err)
		}
		for _, th := range ths {
			c, err := t.q.GetChannel(ctx, store.GetChannelParams{CompanyID: companyID, ID: th.ChannelID})
			if err == nil && domain.ChannelKind(c.Kind) == domain.ChannelGitHub && (peer == "" || peer == th.Peer) {
				ch, peer = c, th.Peer
				break
			}
		}
		if ch.ID == (domain.ID{}) {
			return ch, "", domain.Invalid("channel", "a task não veio de um canal GitHub; informe channel e peer")
		}
	default:
		cs, err := t.q.ListEnabledChannelsByKind(ctx, store.ListEnabledChannelsByKindParams{CompanyID: companyID, Kind: string(domain.ChannelGitHub)})
		if err != nil {
			return ch, "", fmt.Errorf("list github channels: %w", err)
		}
		if len(cs) != 1 {
			return ch, "", domain.Invalid("channel", "informe o canal GitHub (há zero ou mais de um ligado)")
		}
		ch = cs[0]
	}
	if domain.ChannelKind(ch.Kind) != domain.ChannelGitHub {
		return ch, "", domain.Invalid("channel", "review só existe em canal GitHub")
	}
	if peer == "" {
		return ch, "", domain.Invalid("peer", "informe o pull request (dono/repo#N)")
	}
	return ch, peer, nil
}
