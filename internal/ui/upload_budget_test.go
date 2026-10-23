package ui

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O envio soma no máximo 10 arquivos no limite por arquivo, sem passar de 128 MiB (ADR-0065).
func TestUploadBudget(t *testing.T) {
	for _, c := range []struct{ maxFile, want int64 }{
		{5 << 20, 50 << 20},
		{16 << 20, 128 << 20},
		{64 << 20, 128 << 20},
	} {
		if got := uploadBudget(c.maxFile); got != c.want {
			t.Errorf("uploadBudget(%d) = %d, want %d", c.maxFile, got, c.want)
		}
	}
}

func TestFileExt(t *testing.T) {
	for in, want := range map[string]string{"relatório.pdf": "PDF", "dados.final.xlsx": "XLSX", "sem-extensao": "", "arquivo.muitolongo": "", ".env": "ENV", "foto.PNG": "PNG"} {
		if got := fileExt(in); got != want {
			t.Errorf("fileExt(%q) = %q, want %q", in, got, want)
		}
	}
}

// Imagem pequena vira miniatura; imagem grande e PDF viram cartão (a página não baixa uma foto de 20 MiB para mostrar 72 px).
func TestAttachmentRowsThumbs(t *testing.T) {
	list := []service.ArtifactView{
		{Artifact: domain.Artifact{ID: domain.NewID(), Name: "a.png", Mime: "image/png", SizeBytes: 1 << 20}},
		{Artifact: domain.Artifact{ID: domain.NewID(), Name: "b.jpg", Mime: "image/jpeg", SizeBytes: maxThumbBytes + 1}},
		{Artifact: domain.Artifact{ID: domain.NewID(), Name: "c.pdf", Mime: "application/pdf", SizeBytes: 1 << 20}},
	}
	rows := attachmentRows(context.Background(), list, "/c/acme", "", time.Now(), time.UTC)
	if !rows[0].Thumb || rows[1].Thumb || rows[2].Thumb {
		t.Fatalf("Thumb = %v %v %v", rows[0].Thumb, rows[1].Thumb, rows[2].Thumb)
	}
	if rows[2].Ext != "PDF" {
		t.Fatalf("Ext = %q", rows[2].Ext)
	}
}
