package ui

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Anexos e rascunhos na UI (ADR-0052). O envio é multipart (arrastar e soltar liga o <input type="file"> pelo
// hanko.js; sem script o seletor funciona igual), com o corpo limitado por http.MaxBytesReader. O conteúdo é de
// terceiros: a entrega continua por artifactsrv (a mesma rota de /artefatos/{id}/conteudo).

const (
	// maxUploadFiles é quantos arquivos um envio aceita.
	maxUploadFiles = 10
	// maxUploadBytes é a soma dos arquivos de um envio (ADR-0065): o limite por arquivo vale para cada um, e o envio
	// inteiro não passa disto, o que limita a memória de uma requisição.
	maxUploadBytes int64 = 128 << 20
)

type uploadedFile struct {
	Name string
	Data []byte
}

// draftBackend é o que a UI usa dos rascunhos (opcional: um backend sem eles só não mostra o autosave).
type draftBackend interface {
	GetDraft(ctx context.Context, companyID domain.ID, target domain.DraftTarget, targetID domain.ID) (domain.Draft, error)
	SaveDraft(ctx context.Context, companyID domain.ID, target domain.DraftTarget, targetID domain.ID, body string) (domain.Draft, error)
}

func (s *server) draftBE() (draftBackend, bool) {
	b, ok := s.be.(draftBackend)
	return b, ok
}

// sendWithBackend é o envio de mensagem com anexos já enviados (opcional como os demais).
type sendWithBackend interface {
	SendMessageWith(ctx context.Context, actor domain.Actor, companyID, conversationID domain.ID, body string, attachments []domain.ID) (domain.ConversationMessage, error)
}

// maxThumbBytes é o maior arquivo de imagem que vira miniatura na conversa: o <img> baixa o arquivo inteiro, então
// uma foto enorme aparece como cartão (abre ao clicar) em vez de pesar a página.
const maxThumbBytes = 5 << 20

// fileExt é a extensão do nome em maiúsculas, para o cartão do anexo ("relatório.pdf" → "PDF"); até 5 letras.
func fileExt(name string) string {
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	if ext == "" || len(ext) > 5 {
		return ""
	}
	return strings.ToUpper(ext)
}

// uploadHint é a dica do campo de arquivos.
func uploadHint(ctx context.Context, maxFile int64) string {
	return i18n.Tf(ctx, "Até %s por arquivo, %d arquivos e %s por envio.", fmtBytes(ctx, maxFile), maxUploadFiles, fmtBytes(ctx, uploadBudget(maxFile)))
}

// uploadBudget é o máximo que um envio soma: 10 arquivos no limite por arquivo, sem passar de maxUploadBytes.
func uploadBudget(maxFile int64) int64 {
	return min(int64(maxUploadFiles)*maxFile, maxUploadBytes)
}

// readForm lê o corpo do POST: multipart (com os arquivos do campo "files") ou urlencoded. O corpo é limitado ao
// orçamento do envio (uploadBudget). Chame cleanup (sempre devolvido) ao terminar.
func (s *server) readForm(w http.ResponseWriter, r *http.Request, co domain.Company) (files []uploadedFile, maxFile int64, cleanup func(), err error) {
	ctx := r.Context()
	cleanup = func() {}
	usage, err := s.be.GetArtifactUsage(ctx, co.ID)
	if err != nil {
		return nil, 0, cleanup, err
	}
	maxFile = usage.MaxFileBytes
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			return nil, maxFile, cleanup, domain.Invalid("form", i18n.T(ctx, "envio inválido"))
		}
		return nil, maxFile, cleanup, nil
	}
	budget := uploadBudget(maxFile)
	r.Body = http.MaxBytesReader(w, r.Body, budget+uploadFormOverhead)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, maxFile, cleanup, domain.Invalid("files", i18n.Tf(ctx, "o envio passa de %s; mande os arquivos em partes", fmtBytes(ctx, budget)))
		}
		return nil, maxFile, cleanup, domain.Invalid("files", i18n.T(ctx, "envio inválido"))
	}
	if r.MultipartForm != nil {
		cleanup = func() { _ = r.MultipartForm.RemoveAll() }
	}
	var hdrs []*multipart.FileHeader
	if r.MultipartForm != nil {
		hdrs = r.MultipartForm.File["files"]
	}
	var total int64
	for _, h := range hdrs {
		if h.Filename == "" && h.Size == 0 { // o navegador manda uma parte vazia quando nenhum arquivo foi escolhido
			continue
		}
		if len(files) >= maxUploadFiles {
			return nil, maxFile, cleanup, domain.Invalid("files", i18n.Tf(ctx, "no máximo %d arquivos por envio", maxUploadFiles))
		}
		if h.Size > maxFile {
			return nil, maxFile, cleanup, service.NewArtifactLimitError(false, maxFile, 0, h.Size)
		}
		if total += h.Size; total > budget {
			return nil, maxFile, cleanup, domain.Invalid("files", i18n.Tf(ctx, "o envio passa de %s; mande os arquivos em partes", fmtBytes(ctx, budget)))
		}
		f, err := h.Open()
		if err != nil {
			return nil, maxFile, cleanup, domain.Invalid("files", i18n.T(ctx, "não foi possível ler o arquivo"))
		}
		data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
		_ = f.Close()
		if err != nil {
			return nil, maxFile, cleanup, domain.Invalid("files", i18n.T(ctx, "não foi possível ler o arquivo"))
		}
		if int64(len(data)) > maxFile {
			return nil, maxFile, cleanup, service.NewArtifactLimitError(false, maxFile, 0, int64(len(data)))
		}
		files = append(files, uploadedFile{Name: uploadName("", h.Filename), Data: data})
	}
	return files, maxFile, cleanup, nil
}

// attachAll envia os arquivos como anexos (class attachment) de in (task ou conversa) e devolve os ids, na ordem.
func (s *server) attachAll(ctx context.Context, co domain.Company, files []uploadedFile, in service.PutArtifactInput) ([]domain.ID, error) {
	ids := make([]domain.ID, 0, len(files))
	for _, f := range files {
		one := in
		one.Name, one.Content, one.Class = f.Name, f.Data, domain.ClassAttachment
		v, err := s.be.PutArtifact(ctx, actorFrom(ctx), co.ID, one)
		if err != nil {
			return ids, err
		}
		ids = append(ids, v.ID)
	}
	return ids, nil
}

// attachmentRows prepara a lista de anexos para a tela. deleteBase vazio = sem botão de apagar.
func attachmentRows(ctx context.Context, list []service.ArtifactView, base, deleteBase string, now time.Time, loc *time.Location) []views.AttachmentRow {
	out := make([]views.AttachmentRow, 0, len(list))
	for _, a := range list {
		row := views.AttachmentRow{ID: a.ID.String(), Name: a.Name, Href: base + "/artefatos/" + a.ID.String() + "/conteudo",
			DetailHref: base + "/artefatos/" + a.ID.String(), Size: fmtBytes(ctx, a.SizeBytes), Kind: artifactKindLabel(ctx, a.Mime),
			Author: actorLabel(ctx, a.Author), When: agoCtx(ctx, a.CreatedAt, now, loc),
			Thumb: domain.PreviewOf(a.Mime) == domain.PreviewImage && a.SizeBytes <= maxThumbBytes, Ext: fileExt(a.Name)}
		if deleteBase != "" {
			row.DeleteAction = deleteBase + a.ID.String() + "/excluir"
		}
		out = append(out, row)
	}
	return out
}

// taskAttachBox monta o cartão de anexos da task.
func (s *server) taskAttachBox(ctx context.Context, co domain.Company, t service.TaskView, list []service.ArtifactView, maxFile int64) views.AttachBox {
	base := "/c/" + co.Slug
	del := ""
	if views.RoleFrom(ctx).AtLeast(domain.MemberAdmin) {
		del = base + "/tasks/" + t.Key + "/anexos/"
	}
	box := views.AttachBox{Rows: attachmentRows(ctx, list, base, del, s.now(), loadLoc(co.Timezone)), Hint: uploadHint(ctx, maxFile)}
	if views.RoleFrom(ctx).AtLeast(domain.MemberMember) {
		box.UploadAction = base + "/tasks/" + t.Key + "/anexos"
	}
	return box
}

// taskAttachments lê os anexos da task (todos, do mais novo ao mais antigo, até 200).
func (s *server) taskAttachments(ctx context.Context, co domain.Company, taskID domain.ID) ([]service.ArtifactView, error) {
	return s.be.ListArtifacts(ctx, co.ID, service.ArtifactFilter{TaskID: &taskID, Class: domain.ClassAttachment, Limit: 200})
}

// handleTaskAttach recebe arquivos soltos (ou escolhidos) no cartão de anexos da task: cada um vira um anexo da task.
func (s *server) handleTaskAttach(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	t, err := s.be.GetTask(ctx, co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	files, _, cleanup, err := s.readForm(w, r, co)
	defer cleanup()
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if len(files) == 0 {
		s.fail(w, r, &co, domain.Invalid("files", i18n.T(ctx, "escolha ao menos um arquivo")))
		return
	}
	if _, err := s.attachAll(ctx, co, files, service.PutArtifactInput{TaskID: &t.ID}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.afterAttachment(w, r, co, t)
}

// handleTaskAttachmentDelete apaga um anexo da task (admin; o service confere).
func (s *server) handleTaskAttachmentDelete(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	t, err := s.be.GetTask(ctx, co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	id, ok := parseID(r.PathValue("attachment"))
	if !ok || id == nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	a, err := s.be.GetArtifactView(ctx, co.ID, *id)
	if err != nil || a.TaskID == nil || *a.TaskID != t.ID || a.Class != domain.ClassAttachment {
		s.fail(w, r, &co, domain.ErrNotFound) // só apaga por aqui um anexo desta task
		return
	}
	if err := s.be.DeleteArtifact(ctx, actorFrom(ctx), co.ID, *id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.afterAttachment(w, r, co, t)
}

// afterAttachment responde ao envio ou à exclusão: o cartão de anexos (htmx) ou a página da task.
func (s *server) afterAttachment(w http.ResponseWriter, r *http.Request, co domain.Company, t service.TaskView) {
	ctx := r.Context()
	if !isHX(r) {
		redirect(w, r, "/c/"+co.Slug+"/tasks/"+t.Key+"#task-attachments")
		return
	}
	list, err := s.taskAttachments(ctx, co, t.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	usage, err := s.be.GetArtifactUsage(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.TaskAttachments(s.taskAttachBox(ctx, co, t, list, usage.MaxFileBytes), false))
}

// sendFromForm lê o formulário do compositor de uma conversa (chat ou sala), sobe os arquivos como anexos da conversa
// e envia a mensagem com eles (viram da mensagem na mesma transação, então a run já os vê). Em caso de erro já
// respondeu e devolve false. Só arquivos, sem texto, viram uma mensagem curta.
func (s *server) sendFromForm(w http.ResponseWriter, r *http.Request, co domain.Company, cv service.ConversationView) bool {
	ctx := r.Context()
	files, _, cleanup, err := s.readForm(w, r, co)
	defer cleanup()
	if err != nil {
		s.fail(w, r, &co, err)
		return false
	}
	body := strings.TrimSpace(r.PostFormValue("message"))
	if body == "" && len(files) == 0 {
		s.fail(w, r, &co, domain.Invalid(i18n.T(ctx, "mensagem"), i18n.T(ctx, "escreva algo antes de enviar")))
		return false
	}
	if body == "" {
		body = i18n.Tf(ctx, "Enviei %d anexo(s).", len(files))
	}
	ids, err := s.attachAll(ctx, co, files, service.PutArtifactInput{ConversationID: &cv.ID})
	if err != nil {
		s.fail(w, r, &co, err)
		return false
	}
	if sw, ok := s.be.(sendWithBackend); ok {
		_, err = sw.SendMessageWith(ctx, actorFrom(ctx), co.ID, cv.ID, body, ids)
	} else {
		_, err = s.be.SendMessage(ctx, actorFrom(ctx), co.ID, cv.ID, body)
	}
	if err != nil {
		s.fail(w, r, &co, err)
		return false
	}
	return true
}

// messageAttachments agrupa por mensagem os anexos da conversa (a UI mostra cada um sob a mensagem dele).
func (s *server) messageAttachments(ctx context.Context, co domain.Company, convID domain.ID, base string, loc *time.Location) (map[domain.ID][]views.AttachmentRow, error) {
	list, err := s.be.ListArtifacts(ctx, co.ID, service.ArtifactFilter{ConversationID: &convID, Class: domain.ClassAttachment, Limit: 200})
	if err != nil {
		return nil, err
	}
	by := map[domain.ID][]service.ArtifactView{}
	for _, a := range list {
		if a.MessageID != nil {
			by[*a.MessageID] = append(by[*a.MessageID], a)
		}
	}
	out := make(map[domain.ID][]views.AttachmentRow, len(by))
	for id, as := range by {
		// a consulta vem do mais novo; sob a mensagem, na ordem em que foram enviados
		for i, j := 0, len(as)-1; i < j; i, j = i+1, j-1 {
			as[i], as[j] = as[j], as[i]
		}
		out[id] = attachmentRows(ctx, as, base, "", s.now(), loc)
	}
	return out, nil
}

// ---- rascunhos ----

// saveDraft grava o rascunho vindo do autosave (campo field) e responde com o aviso curto que ocupa o indicador.
// Uma falha não derruba a página: o indicador só diz que o rascunho não foi salvo.
func (s *server) saveDraft(w http.ResponseWriter, r *http.Request, co domain.Company, target domain.DraftTarget, targetID domain.ID, field string) {
	ctx := r.Context()
	db, ok := s.draftBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	_ = r.ParseForm()
	d, err := db.SaveDraft(ctx, co.ID, target, targetID, r.PostFormValue(field))
	msg := i18n.T(ctx, "Rascunho não salvo")
	switch {
	case err == nil && d.Exists():
		msg = i18n.Tf(ctx, "Rascunho salvo às %s", fmtClock(d.UpdatedAt, loadLoc(co.Timezone)))
	case err == nil:
		msg = ""
	}
	s.render(w, r, http.StatusOK, views.DraftText(msg))
}

func (s *server) handleTaskDraft(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, err := s.be.GetTask(r.Context(), co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.saveDraft(w, r, co, domain.DraftTask, t.ID, "body")
}

func (s *server) handleChatDraft(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.chatConversation(w, r, co)
	if !ok {
		return
	}
	s.saveDraft(w, r, co, domain.DraftConversation, cv.ID, "message")
}

func (s *server) handleRoomDraft(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.roomConversation(w, r, co)
	if !ok {
		return
	}
	s.saveDraft(w, r, co, domain.DraftConversation, cv.ID, "message")
}

// loadDraft devolve o rascunho do usuário para o alvo (vazio se não há ou o backend não guarda).
func (s *server) loadDraft(ctx context.Context, co domain.Company, target domain.DraftTarget, targetID domain.ID) string {
	if db, ok := s.draftBE(); ok && views.RoleFrom(ctx).AtLeast(domain.MemberMember) {
		if d, err := db.GetDraft(ctx, co.ID, target, targetID); err == nil {
			return d.Body
		}
	}
	return ""
}

func (s *server) draftFields(ctx context.Context, co domain.Company, route string, target domain.DraftTarget, targetID domain.ID) views.DraftFields {
	if _, ok := s.draftBE(); !ok || !views.RoleFrom(ctx).AtLeast(domain.MemberMember) {
		return views.DraftFields{}
	}
	return views.DraftFields{Action: route, Text: s.loadDraft(ctx, co, target, targetID)}
}
