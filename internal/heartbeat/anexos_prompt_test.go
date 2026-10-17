package heartbeat

import (
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestAttachmentsSection(t *testing.T) {
	rc := service.RunContext{}
	if got := attachmentsSection(rc); got != "" {
		t.Fatalf("sem anexos deve ser vazio: %q", got)
	}
	cid := domain.NewID()
	at := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	rc.Comments = []domain.Comment{{ID: cid, Author: domain.HumanActor, CreatedAt: at}}
	rc.Attachments = []service.AttachmentBrief{
		{ID: domain.NewID(), Name: "spec.pdf", Mime: "application/pdf", SizeBytes: 2048, Author: domain.HumanActor, CreatedAt: at, CommentID: &cid},
		{ID: domain.NewID(), Name: "foto.png", Mime: "image/png", SizeBytes: 12, Author: domain.HumanActor, CreatedAt: at, New: true},
	}
	got := attachmentsSection(rc)
	for _, want := range []string{"## Anexos", "hanko_attachment_get", "`spec.pdf` (application/pdf, 2.0 KiB)", "no comentário de Humano", "`foto.png` (image/png, 12 B)", "NOVO nesta mensagem"} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
	// o briefing da task inclui a seção
	_, prompt := BuildPrompt(service.RunContext{Attachments: rc.Attachments}, "", "")
	if !strings.Contains(prompt, "## Anexos") {
		t.Error("BuildPrompt sem a seção de anexos")
	}
}
