package service

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/store"
)

// Anexos (ADR-0052). Um anexo é um artefato com kind = 'attachment': mesmo conteúdo (BlobStore), mesma cota e mesmo
// limite por arquivo do artefato; sem cadeia de versões (sempre a versão 1) e com ligação a task/comentário ou a
// conversa/mensagem. PutArtifact despacha para cá quando PutArtifactInput.Class é attachment.

// maxAttachmentsPerTarget limita quantos anexos uma task ou conversa aceita (a lista do briefing e da tela fica útil).
const maxAttachmentsPerTarget = 200

// uniqueAttachmentName devolve name, ou "base (2).ext", "base (3).ext"... se o nome já existe na task/conversa.
func (t *txc) uniqueAttachmentName(companyID domain.ID, taskID, conversationID *domain.ID, name string) (string, error) {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if base == "" { // ".bashrc": a extensão é o nome inteiro
		base, ext = name, ""
	}
	cand := name
	for n := 2; n <= maxAttachmentsPerTarget+1; n++ {
		taken, err := t.q.AttachmentNameTaken(t.ctx, store.AttachmentNameTakenParams{CompanyID: companyID, Name: cand,
			TaskID: taskID, ConversationID: conversationID})
		if err != nil {
			return "", fmt.Errorf("attachment name: %w", err)
		}
		if !taken {
			return cand, nil
		}
		suffix := fmt.Sprintf(" (%d)", n)
		// o nome tem teto de 200 caracteres: corta a base, não a extensão
		if over := len([]rune(base+suffix+ext)) - maxArtifactNameRunes; over > 0 {
			r := []rune(base)
			base = string(r[:max(1, len(r)-over)])
		}
		cand = base + suffix + ext
	}
	return "", domain.Invalid("name", "too many attachments with this name")
}

// putAttachment grava o anexo na transação de PutArtifact (que já subiu o conteúdo para o driver não transacional,
// se for o caso, e cuida da compensação). Devolve o id do anexo criado.
func (t *txc) putAttachment(actor domain.Actor, companyID domain.ID, in PutArtifactInput, name, mt, digest string, quota int64,
	newID domain.ID, drv storage.BlobStore, blobKey storage.Key) (domain.ID, error) {
	ctx := t.ctx
	taskID, convID, runID := in.TaskID, in.ConversationID, in.RunID
	var run *store.Run
	if runID != nil {
		r, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: *runID})
		if err != nil {
			return domain.ID{}, domain.Invalid("run_id", "run not found in company")
		}
		run = &r
		// o alvo padrão é o da run: a task dela ou, numa run de chat, a conversa
		if taskID == nil && convID == nil {
			taskID = run.TaskID
			convID = run.ConversationID
		}
	}
	if (taskID == nil) == (convID == nil) {
		return domain.ID{}, domain.Invalid("task_id", "an attachment belongs to a task or to a conversation (exactly one)")
	}
	var projectID *domain.ID
	if taskID != nil {
		task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: *taskID})
		if err != nil {
			return domain.ID{}, domain.Invalid("task_id", "task not found in company")
		}
		projectID = task.ProjectID
		if run != nil && (run.TaskID == nil || *run.TaskID != *taskID) {
			return domain.ID{}, domain.Invalid("run_id", "run belongs to another task")
		}
		if actor.Type == domain.ActorPersona && actor.ID != nil {
			own := task.PersonaID != nil && *task.PersonaID == *actor.ID
			ownRun := run != nil && run.TaskID != nil && *run.TaskID == *taskID && run.PersonaID == *actor.ID
			if !own && !ownRun {
				return domain.ID{}, fmt.Errorf("attach: a persona attaches only to its own task or the task of its own run: %w", domain.ErrNotAllowed)
			}
		}
	} else {
		if _, err := t.q.GetConversation(ctx, store.GetConversationParams{CompanyID: companyID, ID: *convID}); err != nil {
			return domain.ID{}, domain.Invalid("conversation_id", "conversation not found in company")
		}
		if run != nil && (run.ConversationID == nil || *run.ConversationID != *convID) {
			return domain.ID{}, domain.Invalid("run_id", "run belongs to another conversation")
		}
		if actor.Type == domain.ActorPersona && actor.ID != nil && (run == nil || run.PersonaID != *actor.ID || run.ConversationID == nil) {
			return domain.ID{}, fmt.Errorf("attach: a persona attaches to a conversation only from its own chat run: %w", domain.ErrNotAllowed)
		}
	}
	if in.CommentID != nil {
		if taskID == nil {
			return domain.ID{}, domain.Invalid("comment_id", "only a task attachment links to a comment")
		}
		cm, err := t.q.GetCommentByID(ctx, store.GetCommentByIDParams{CompanyID: companyID, ID: *in.CommentID})
		if err != nil || cm.TaskID != *taskID {
			return domain.ID{}, domain.Invalid("comment_id", "comment not found on this task")
		}
	}
	if in.MessageID != nil {
		if convID == nil {
			return domain.ID{}, domain.Invalid("message_id", "only a conversation attachment links to a message")
		}
		m, err := t.q.GetConversationMessageByID(ctx, store.GetConversationMessageByIDParams{CompanyID: companyID, ID: *in.MessageID})
		if err != nil || m.ConversationID != *convID {
			return domain.ID{}, domain.Invalid("message_id", "message not found in this conversation")
		}
	}
	var personaID *domain.ID
	if actor.Type == domain.ActorPersona && actor.ID != nil {
		if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *actor.ID}); err != nil {
			return domain.ID{}, domain.Invalid("persona", "persona not found in company")
		}
		personaID = actor.ID
	}

	// a cota é por empresa: lida e consumida sob a trava da empresa (como no artefato)
	if err := t.q.LockArtifactCompany(ctx, companyID); err != nil {
		return domain.ID{}, fmt.Errorf("lock artifact company: %w", err)
	}
	usage, err := t.q.ArtifactCompanyUsage(ctx, companyID)
	if err != nil {
		return domain.ID{}, fmt.Errorf("artifact usage: %w", err)
	}
	if usage.UsedBytes+int64(len(in.Content)) > quota {
		return domain.ID{}, NewArtifactLimitError(true, quota, usage.UsedBytes, int64(len(in.Content)))
	}
	n, err := t.q.CountAttachmentsOf(ctx, store.CountAttachmentsOfParams{CompanyID: companyID, TaskID: taskID, ConversationID: convID})
	if err != nil {
		return domain.ID{}, fmt.Errorf("count attachments: %w", err)
	}
	if n >= maxAttachmentsPerTarget {
		return domain.ID{}, domain.Invalid("name", fmt.Sprintf("too many attachments (max %d)", maxAttachmentsPerTarget))
	}
	unique, err := t.uniqueAttachmentName(companyID, taskID, convID, name)
	if err != nil {
		return domain.ID{}, err
	}
	at := t.now
	if !in.createdAt.IsZero() {
		at = in.createdAt.UTC()
	}
	row, err := t.q.CreateArtifact(ctx, store.CreateArtifactParams{ID: newID, CompanyID: companyID, TaskID: taskID,
		RunID: runID, ProjectID: projectID, PersonaID: personaID, AuthorType: string(actor.Type), AuthorID: actor.ID,
		Name: unique, Mime: mt, SizeBytes: int64(len(in.Content)), Sha256: digest, Version: 1, CreatedAt: at, Storage: drv.Name(),
		Kind: string(domain.ClassAttachment), CommentID: in.CommentID, ConversationID: convID, MessageID: in.MessageID})
	if err != nil {
		return domain.ID{}, mapErr(err, "create attachment")
	}
	if drv.Transactional() {
		if err := drv.Put(storage.WithTx(ctx, t.q), blobKey, in.Content, mt); err != nil {
			return domain.ID{}, fmt.Errorf("create attachment blob: %w", err)
		}
	}
	return row.ID, t.activity(actor, companyID, "attachment.added", "artifact", row.ID, map[string]any{
		"name": unique, "mime": mt, "size_bytes": len(in.Content), "task_id": taskID, "conversation_id": convID,
		"comment_id": in.CommentID, "message_id": in.MessageID, "run_id": runID}, "")
}

// attachmentBriefs lista os anexos que a run deve conhecer: os da task (run de task) ou os da conversa (run de chat).
func attachmentBriefs(ctx context.Context, q *store.Queries, rc RunContext) ([]AttachmentBrief, error) {
	attachment := string(domain.ClassAttachment)
	p := store.ListArtifactViewsParams{CompanyID: rc.Company.ID, Class: &attachment, LatestOnly: false, Lim: MaxBriefingAttachments}
	newMsg := map[domain.ID]bool{}
	switch {
	case rc.Chat != nil:
		id := rc.Chat.Conversation.ID
		p.ConversationID = &id
		for _, m := range rc.Chat.New {
			newMsg[m.ID] = true
		}
	case rc.Task.ID != (domain.ID{}):
		id := rc.Task.ID
		p.TaskID = &id
	default:
		return nil, nil
	}
	rows, err := q.ListArtifactViews(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	out := make([]AttachmentBrief, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // a consulta vem do mais novo; o briefing lê do mais antigo
		a := rows[i].Artifact
		b := AttachmentBrief{ID: a.ID, Name: a.Name, Mime: a.Mime, SizeBytes: a.SizeBytes, Author: actorOf(a.AuthorType, a.AuthorID),
			CreatedAt: a.CreatedAt, CommentID: a.CommentID, MessageID: a.MessageID}
		b.New = a.MessageID != nil && newMsg[*a.MessageID]
		out = append(out, b)
	}
	return out, nil
}
