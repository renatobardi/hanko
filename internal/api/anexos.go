package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Anexos (ADR-0052): artefatos com class=attachment, ligados a uma task (e a um comentário) ou a uma conversa do
// chat (e a uma mensagem). Mesmo envio JSON/base64, mesma cota e mesmo limite do artefato; o download e a exclusão
// usam as rotas de /artifacts/{id}.

type putAttachmentIn struct {
	CompanyPath
	Body struct {
		Name         string `json:"name" minLength:"1" maxLength:"200" example:"captura.png" doc:"Nome do arquivo (sem barras). Nome repetido na mesma task/conversa recebe sufixo: \"captura (2).png\"."`
		Mime         string `json:"mime,omitempty" example:"image/png" doc:"Tipo do conteúdo; vazio = deduzido do nome e do conteúdo."`
		Content      []byte `json:"content" doc:"Conteúdo do arquivo em base64." minLength:"1"`
		Task         string `json:"task,omitempty" doc:"Id ou chave da task. Run token: sempre a da execução."`
		Conversation string `json:"conversation,omitempty" format:"uuid" doc:"Conversa do chat (use task OU conversation). Run token de chat: sempre a da execução."`
		Comment      string `json:"comment_id,omitempty" format:"uuid" doc:"Comentário da task a que o anexo pertence."`
		Message      string `json:"message_id,omitempty" format:"uuid" doc:"Mensagem da conversa a que o anexo pertence."`
		Run          string `json:"run_id,omitempty" format:"uuid" doc:"Execução de origem. Run token: sempre a do token."`
	}
}

type listAttachmentsIn struct {
	CompanyPath
	Page
	Task         string `query:"task" doc:"Id ou chave da task."`
	Conversation string `query:"conversation" format:"uuid" doc:"Id da conversa."`
	Comment      string `query:"comment_id" format:"uuid" doc:"Só os anexos deste comentário."`
	Message      string `query:"message_id" format:"uuid" doc:"Só os anexos desta mensagem."`
}

func optUUID(ctx context.Context, s *server, field, v string) (*domain.ID, error) {
	if v == "" {
		return nil, nil
	}
	id, err := domain.ParseID(v)
	if err != nil {
		return nil, s.fail(ctx, domain.Invalid(field, "não é um uuid"))
	}
	return &id, nil
}

// parseIDList converte uma lista de uuids em texto.
func parseIDList(in []string) ([]domain.ID, error) {
	out := make([]domain.ID, 0, len(in))
	for _, v := range in {
		id, err := domain.ParseID(v)
		if err != nil {
			return nil, domain.Invalid("attachments", "não é um uuid: "+v)
		}
		out = append(out, id)
	}
	return out, nil
}

func (s *server) registerAttachments(api huma.API) {
	put := created(op("put-attachment", http.MethodPost, "/companies/{company}/attachments", "artifacts",
		"Anexa um arquivo a uma task (ou comentário) ou a uma conversa do chat"))
	put.MaxBodyBytes = s.artifactBodyLimit()
	huma.Register(api, put, func(ctx context.Context, in *putAttachmentIn) (*ItemOut[Artifact], error) {
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		cid := sc.Company.ID
		b := in.Body
		task, err := s.taskRef(ctx, cid, b.Task)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		conv, err := optUUID(ctx, s, "conversation", b.Conversation)
		if err != nil {
			return nil, err
		}
		comment, err := optUUID(ctx, s, "comment_id", b.Comment)
		if err != nil {
			return nil, err
		}
		message, err := optUUID(ctx, s, "message_id", b.Message)
		if err != nil {
			return nil, err
		}
		run, err := optUUID(ctx, s, "run_id", b.Run)
		if err != nil {
			return nil, err
		}
		if sc.Claims != nil { // o token só anexa à própria execução: a task dela ou, no chat, a conversa dela
			if task, run, err = runScope(sc, task, run); err != nil {
				return nil, err
			}
			if sc.Claims.ConversationID != "" {
				cv, perr := domain.ParseID(sc.Claims.ConversationID)
				if perr != nil || (conv != nil && *conv != cv) {
					return nil, problem(http.StatusForbidden, "o token só anexa à própria conversa")
				}
				conv = &cv
			}
		}
		v, err := s.svc.PutArtifact(ctx, sc.Actor, cid, service.PutArtifactInput{Name: b.Name, Mime: b.Mime, Content: b.Content,
			TaskID: task, RunID: run, Class: domain.ClassAttachment, CommentID: comment, ConversationID: conv, MessageID: message})
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		return &ItemOut[Artifact]{Body: artifactOut(v)}, nil
	})

	huma.Register(api, op("list-attachments", http.MethodGet, "/companies/{company}/attachments", "artifacts", "Lista anexos de uma task, comentário, conversa ou mensagem"),
		func(ctx context.Context, in *listAttachmentsIn) (*ListOut[Artifact], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			f := service.ArtifactFilter{Class: domain.ClassAttachment, Limit: in.Limit, Offset: in.Offset}
			if f.TaskID, err = s.taskRef(ctx, sc.Company.ID, in.Task); err != nil {
				return nil, s.fail(ctx, err)
			}
			if f.ConversationID, err = optUUID(ctx, s, "conversation", in.Conversation); err != nil {
				return nil, err
			}
			if f.CommentID, err = optUUID(ctx, s, "comment_id", in.Comment); err != nil {
				return nil, err
			}
			if f.MessageID, err = optUUID(ctx, s, "message_id", in.Message); err != nil {
				return nil, err
			}
			vs, err := s.svc.ListArtifacts(ctx, sc.Company.ID, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(vs, in.Page, artifactOut), nil
		})
}
