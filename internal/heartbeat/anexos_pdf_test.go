package heartbeat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// fakePDFTools troca os utilitários por versões que só gravam arquivos: pages é o que o pdfinfo diz; missing lista os
// utilitários ausentes; text vazio simula um PDF escaneado.
func fakePDFTools(t *testing.T, pages string, text string, missing ...string) *[]string {
	t.Helper()
	oldLook, oldRun := lookPDFTool, runPDFTool
	t.Cleanup(func() { lookPDFTool, runPDFTool = oldLook, oldRun })
	var calls []string
	lookPDFTool = func(name string) (string, error) {
		for _, m := range missing {
			if m == name {
				return "", errors.New("não encontrado")
			}
		}
		return name, nil
	}
	runPDFTool = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args[len(args)-1:], ""))
		switch name {
		case "pdfinfo":
			return []byte("Title: x\nPages:          " + pages + "\n"), nil
		case "pdftotext":
			return nil, os.WriteFile(args[len(args)-1], []byte(text), 0o600)
		case "qpdf":
			return nil, os.WriteFile(args[len(args)-1], []byte("%PDF parte"), 0o600)
		}
		return nil, errors.New("utilitário inesperado " + name)
	}
	return &calls
}

func writePDF(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "Hanko — Paperclip.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// Um PDF grande ganha o texto extraído e partes de 20 páginas; um pequeno fica como está.
func TestSplitPDF(t *testing.T) {
	t.Run("grande por tamanho: texto e partes", func(t *testing.T) {
		fakePDFTools(t, "45", "conteúdo do documento")
		dir, path := writePDF(t)
		ex, notes := splitPDF(context.Background(), path, ".hanko-anexos/r/Hanko — Paperclip.pdf", 23<<20)
		if ex == nil || len(notes) != 0 {
			t.Fatalf("ex=%+v notes=%v", ex, notes)
		}
		if ex.Pages != 45 || ex.Parts != 3 || ex.PartPages != 20 || ex.TextPath != ".hanko-anexos/r/Hanko — Paperclip.txt" ||
			ex.PartsGlob != ".hanko-anexos/r/Hanko — Paperclip.parte-NN.pdf" {
			t.Fatalf("extras = %+v", ex)
		}
		for _, f := range []string{"Hanko — Paperclip.txt", "Hanko — Paperclip.parte-01.pdf", "Hanko — Paperclip.parte-03.pdf"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("faltou %s: %v", f, err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "Hanko — Paperclip.parte-04.pdf")); err == nil {
			t.Error("parte a mais")
		}
	})
	t.Run("pequeno fica inteiro", func(t *testing.T) {
		calls := fakePDFTools(t, "12", "x")
		_, path := writePDF(t)
		if ex, _ := splitPDF(context.Background(), path, "r/a.pdf", 2<<20); ex != nil {
			t.Fatalf("PDF pequeno foi dividido: %+v", ex)
		}
		if len(*calls) != 1 { // só o pdfinfo
			t.Fatalf("chamadas = %v", *calls)
		}
	})
	t.Run("muitas páginas, arquivo pequeno: divide", func(t *testing.T) {
		fakePDFTools(t, "250", "texto")
		_, path := writePDF(t)
		ex, _ := splitPDF(context.Background(), path, "r/a.pdf", 3<<20)
		if ex == nil || ex.Parts != 13 || ex.PartPages != 20 {
			t.Fatalf("extras = %+v", ex)
		}
	})
	t.Run("milhares de páginas: partes crescem até o máximo", func(t *testing.T) {
		fakePDFTools(t, "2500", "texto")
		_, path := writePDF(t)
		ex, _ := splitPDF(context.Background(), path, "r/a.pdf", 30<<20)
		if ex == nil || ex.Parts != pdfMaxParts || ex.PartPages != 50 {
			t.Fatalf("extras = %+v", ex)
		}
	})
	t.Run("sem qpdf: só o texto", func(t *testing.T) {
		fakePDFTools(t, "45", "texto", "qpdf")
		_, path := writePDF(t)
		ex, notes := splitPDF(context.Background(), path, "r/a.pdf", 23<<20)
		if ex == nil || ex.TextPath == "" || ex.Parts != 0 || !strings.Contains(strings.Join(notes, ";"), "qpdf ausente") {
			t.Fatalf("ex=%+v notes=%v", ex, notes)
		}
	})
	t.Run("escaneado: sem texto, só partes", func(t *testing.T) {
		fakePDFTools(t, "45", "")
		dir, path := writePDF(t)
		ex, notes := splitPDF(context.Background(), path, "r/a.pdf", 23<<20)
		if ex == nil || ex.TextPath != "" || ex.Parts != 3 || !strings.Contains(strings.Join(notes, ";"), "escaneado") {
			t.Fatalf("ex=%+v notes=%v", ex, notes)
		}
		if _, err := os.Stat(filepath.Join(dir, "Hanko — Paperclip.txt")); err == nil {
			t.Error("texto vazio ficou em disco")
		}
	})
	t.Run("sem nenhum utilitário: nada gerado", func(t *testing.T) {
		fakePDFTools(t, "45", "t", "pdfinfo", "pdftotext", "qpdf")
		_, path := writePDF(t)
		if ex, _ := splitPDF(context.Background(), path, "r/a.pdf", 23<<20); ex != nil {
			t.Fatalf("extras sem utilitários: %+v", ex)
		}
	})
}

// O briefing manda ler o texto extraído primeiro e abrir só as partes que importam.
func TestAttachmentsSectionPDF(t *testing.T) {
	rc := service.RunContext{Attachments: []service.AttachmentBrief{{
		ID: domain.NewID(), Name: "Plano.pdf", Mime: "application/pdf", SizeBytes: 23 << 20, Author: domain.HumanActor,
		LocalPath: ".hanko-anexos/r/Plano.pdf", PDFPages: 45, PDFText: ".hanko-anexos/r/Plano.txt", PDFParts: 3, PDFPartPages: 20,
		PDFPartsPath: ".hanko-anexos/r/Plano.parte-NN.pdf",
	}}}
	got := attachmentsSection(rc)
	for _, want := range []string{"`.hanko-anexos/r/Plano.pdf`", "leia primeiro o texto extraído `.hanko-anexos/r/Plano.txt`",
		"45 páginas divididas em 3 partes de até 20 páginas", "`.hanko-anexos/r/Plano.parte-NN.pdf`, NN de 01 a 03"} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
}
