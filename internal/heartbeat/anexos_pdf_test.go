package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// pdfFake troca os utilitários por versões que só gravam arquivos: pages é o que o pdfinfo diz; text vazio simula um PDF
// escaneado; failQpdf faz o qpdf falhar; partSize é o peso de cada parte que o qpdf grava.
type pdfFake struct {
	calls    []string
	failQpdf bool
	partSize int
}

func fakePDFTools(t *testing.T, pages, text string, missing ...string) *pdfFake {
	t.Helper()
	f := &pdfFake{partSize: 10}
	oldLook, oldRun := lookPDFTool, runPDFTool
	t.Cleanup(func() { lookPDFTool, runPDFTool = oldLook, oldRun })
	lookPDFTool = func(name string) (string, error) {
		for _, m := range missing {
			if m == name {
				return "", errors.New("não encontrado")
			}
		}
		return name, nil
	}
	runPDFTool = func(_ context.Context, name string, args ...string) ([]byte, error) {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		switch name {
		case "pdfinfo":
			return []byte("Title: x\nPages:          " + pages + "\n"), nil
		case "pdftotext":
			return nil, os.WriteFile(args[len(args)-1], []byte(text), 0o600)
		case "qpdf":
			if f.failQpdf {
				return nil, errors.New("qpdf: exit status 2")
			}
			var per int
			for _, a := range args {
				if v, ok := strings.CutPrefix(a, "--split-pages="); ok {
					per, _ = strconv.Atoi(v)
				}
			}
			total, _ := strconv.Atoi(pages)
			out := strings.TrimSuffix(args[len(args)-1], ".pdf") // <tmp>/p → <tmp>/p-01.pdf …
			for i := 1; i <= (total+per-1)/per; i++ {
				if err := os.WriteFile(fmt.Sprintf("%s-%02d.pdf", out, i), make([]byte, f.partSize), 0o600); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}
		return nil, errors.New("utilitário inesperado " + name)
	}
	return f
}

func writePDF(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "Plano de teste.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

const budgetOK = 512 << 20

// Um PDF grande ganha o texto extraído e partes; um pequeno fica como está.
func TestSplitPDF(t *testing.T) {
	rel := ".hanko-anexos/r/Plano de teste.pdf"
	t.Run("grande por tamanho: texto e partes", func(t *testing.T) {
		f := fakePDFTools(t, "45", "conteúdo do documento")
		dir, path := writePDF(t)
		ex, notes := splitPDF(context.Background(), path, rel, "Plano de teste", 23<<20, budgetOK)
		if ex == nil || len(notes) != 0 {
			t.Fatalf("ex=%+v notes=%v", ex, notes)
		}
		if ex.Pages != 45 || ex.Parts != 3 || ex.PartPages != 15 || ex.TextPath != ".hanko-anexos/r/Plano de teste.txt" ||
			ex.PartsGlob != ".hanko-anexos/r/Plano de teste.parte-NN.pdf" {
			t.Fatalf("extras = %+v", ex)
		}
		for _, name := range []string{"Plano de teste.txt", "Plano de teste.parte-01.pdf", "Plano de teste.parte-03.pdf"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Errorf("faltou %s: %v", name, err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "Plano de teste.parte-04.pdf")); err == nil {
			t.Error("parte a mais")
		}
		if left, _ := filepath.Glob(filepath.Join(dir, ".split-*")); len(left) != 0 {
			t.Errorf("pasta temporária ficou: %v", left)
		}
		calls := strings.Join(f.calls, "\n")
		if !strings.Contains(calls, "--warning-exit-0") || strings.Count(calls, "qpdf ") != 1 {
			t.Errorf("qpdf deve rodar uma vez, com --warning-exit-0 (aviso não é falha):\n%s", calls)
		}
	})
	t.Run("pequeno fica inteiro", func(t *testing.T) {
		f := fakePDFTools(t, "12", "x")
		_, path := writePDF(t)
		if ex, _ := splitPDF(context.Background(), path, rel, "a", 2<<20, budgetOK); ex != nil {
			t.Fatalf("PDF pequeno foi dividido: %+v", ex)
		}
		if len(f.calls) != 1 { // só o pdfinfo
			t.Fatalf("chamadas = %v", f.calls)
		}
	})
	t.Run("muitas páginas, arquivo pequeno: divide em 20", func(t *testing.T) {
		fakePDFTools(t, "250", "texto")
		_, path := writePDF(t)
		ex, _ := splitPDF(context.Background(), path, rel, "a", 3<<20, budgetOK)
		if ex == nil || ex.Parts != 13 || ex.PartPages != 20 {
			t.Fatalf("extras = %+v", ex)
		}
	})
	t.Run("pesado e com poucas páginas (escaneado de 15 páginas): divide pelo peso", func(t *testing.T) {
		fakePDFTools(t, "15", "")
		dir, path := writePDF(t)
		ex, notes := splitPDF(context.Background(), path, rel, "a", 23<<20, budgetOK)
		if ex == nil || ex.TextPath != "" || ex.Parts != 3 || ex.PartPages != 5 || !strings.Contains(strings.Join(notes, ";"), "escaneado") {
			t.Fatalf("ex=%+v notes=%v", ex, notes)
		}
		if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
			t.Error("texto vazio ficou em disco")
		}
	})
	t.Run("milhares de páginas: partes crescem até o máximo", func(t *testing.T) {
		fakePDFTools(t, "2500", "texto")
		_, path := writePDF(t)
		ex, _ := splitPDF(context.Background(), path, rel, "a", 30<<20, budgetOK)
		if ex == nil || ex.Parts != pdfMaxParts || ex.PartPages != 50 {
			t.Fatalf("extras = %+v", ex)
		}
	})
	t.Run("sem qpdf: só o texto", func(t *testing.T) {
		fakePDFTools(t, "45", "texto", "qpdf")
		_, path := writePDF(t)
		ex, notes := splitPDF(context.Background(), path, rel, "a", 23<<20, budgetOK)
		if ex == nil || ex.TextPath == "" || ex.Parts != 0 || !strings.Contains(strings.Join(notes, ";"), "qpdf ausente") {
			t.Fatalf("ex=%+v notes=%v", ex, notes)
		}
	})
	t.Run("qpdf falha: nenhuma parte fica (nunca divisão incompleta)", func(t *testing.T) {
		f := fakePDFTools(t, "45", "texto")
		f.failQpdf = true
		dir, path := writePDF(t)
		ex, notes := splitPDF(context.Background(), path, rel, "a", 23<<20, budgetOK)
		if ex == nil || ex.Parts != 0 || ex.TextPath == "" || !strings.Contains(strings.Join(notes, ";"), "qpdf falhou") {
			t.Fatalf("ex=%+v notes=%v", ex, notes)
		}
		if parts, _ := filepath.Glob(filepath.Join(dir, "a.parte-*")); len(parts) != 0 {
			t.Errorf("partes ficaram: %v", parts)
		}
	})
	t.Run("partes passam do orçamento de disco: nada fica", func(t *testing.T) {
		f := fakePDFTools(t, "45", "texto")
		f.partSize = 1 << 20
		dir, path := writePDF(t)
		ex, notes := splitPDF(context.Background(), path, rel, "a", 23<<20, 2<<20) // cabem 2 MiB; as 3 partes pesam 3
		if ex == nil || ex.Parts != 0 || !strings.Contains(strings.Join(notes, ";"), "teto de disco") {
			t.Fatalf("ex=%+v notes=%v", ex, notes)
		}
		if parts, _ := filepath.Glob(filepath.Join(dir, "a.parte-*")); len(parts) != 0 {
			t.Errorf("partes ficaram: %v", parts)
		}
	})
	t.Run("sem nenhum utilitário: nada gerado", func(t *testing.T) {
		fakePDFTools(t, "45", "t", "pdfinfo", "pdftotext", "qpdf")
		_, path := writePDF(t)
		if ex, _ := splitPDF(context.Background(), path, rel, "a", 23<<20, budgetOK); ex != nil {
			t.Fatalf("extras sem utilitários: %+v", ex)
		}
	})
}

// Os arquivos gerados ao lado de um PDF nunca colidem com outro anexo da run.
func TestPDFDerivedBase(t *testing.T) {
	used := map[string]bool{}
	id := domain.NewID()
	if got := pdfDerivedBase("Plano.pdf", id, used); got != "Plano" {
		t.Fatalf("primeiro = %q", got)
	}
	if !used["Plano.txt"] || !used["Plano.parte-01.pdf"] || !used["Plano.parte-50.pdf"] {
		t.Fatal("nomes derivados não foram reservados")
	}
	// um anexo "Plano.txt" chegando depois recebe outro nome em disco (diskName respeita o mapa)
	if got := diskName("Plano.txt", id, used); got != id.String()[:8]+"-Plano.txt" {
		t.Fatalf("Plano.txt = %q", got)
	}
	// "Plano.PDF" depois de "Plano.pdf" gera o mesmo nome-base: ganha o prefixo
	if got := pdfDerivedBase("Plano.PDF", id, used); got != id.String()[:8]+"-Plano" {
		t.Fatalf("Plano.PDF = %q", got)
	}
	// um "Notas.txt" já gravado antes de "Notas.pdf"
	used2 := map[string]bool{"Notas.txt": true}
	if got := pdfDerivedBase("Notas.pdf", id, used2); got != id.String()[:8]+"-Notas" {
		t.Fatalf("Notas.pdf = %q", got)
	}
}

// O briefing manda ler o texto extraído primeiro e abrir só as partes que importam.
func TestAttachmentsSectionPDF(t *testing.T) {
	rc := service.RunContext{Attachments: []service.AttachmentBrief{{
		ID: domain.NewID(), Name: "Plano.pdf", Mime: "application/pdf", SizeBytes: 23 << 20, Author: domain.HumanActor,
		LocalPath: ".hanko-anexos/r/Plano.pdf", PDFPages: 45, PDFText: ".hanko-anexos/r/Plano.txt", PDFParts: 3, PDFPartPages: 15,
		PDFPartsPath: ".hanko-anexos/r/Plano.parte-NN.pdf",
	}}}
	got := attachmentsSection(rc)
	for _, want := range []string{"`.hanko-anexos/r/Plano.pdf`", "leia primeiro o texto extraído `.hanko-anexos/r/Plano.txt`",
		"45 páginas divididas em 3 partes de até 15 páginas", "`.hanko-anexos/r/Plano.parte-NN.pdf`, NN de 01 a 03"} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
}

// Com os utilitários de verdade (pdfinfo, pdftotext, qpdf), quando existem na máquina: um PDF de 45 páginas vira texto e
// partes que o qpdf numera sozinho, e a ordem das partes é a das páginas.
func TestSplitPDFWithRealTools(t *testing.T) {
	for _, tool := range []string{"pdfinfo", "pdftotext", "qpdf"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s ausente", tool)
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(src, minimalPDF(45), 0o600); err != nil {
		t.Fatal(err)
	}
	ex, notes := splitPDF(context.Background(), src, ".hanko-anexos/r/doc.pdf", "doc", 23<<20, budgetOK) // tamanho declarado grande: força a divisão
	if ex == nil || ex.Pages != 45 || ex.Parts != 3 || ex.PartPages != 15 {
		t.Fatalf("ex=%+v notes=%v", ex, notes)
	}
	for i, wantPages := range []string{"15", "15", "15"} {
		out, err := exec.Command("pdfinfo", filepath.Join(dir, fmt.Sprintf("doc.parte-%02d.pdf", i+1))).Output()
		if err != nil || !regexp.MustCompile(`Pages:\s+`+wantPages+`\b`).Match(out) {
			t.Fatalf("parte %d: %v\n%s", i+1, err, out)
		}
	}
	// a ordem: a parte 2 começa na página 16 (o texto de cada página diz o número)
	txt, err := exec.Command("pdftotext", filepath.Join(dir, "doc.parte-02.pdf"), "-").Output()
	if err != nil || !strings.Contains(string(txt), "Pagina 16") || strings.Contains(string(txt), "Pagina 15\n") {
		t.Fatalf("parte 2 não começa na página 16: %v\n%s", err, txt)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "doc.txt")); err != nil || !strings.Contains(string(b), "Pagina 45") {
		t.Fatalf("texto extraído: %v", err)
	}
}

// minimalPDF monta um PDF válido de n páginas, cada uma com o texto "Pagina N" (sem dependências).
func minimalPDF(n int) []byte {
	var b strings.Builder
	var offs []int
	obj := func(s string) {
		offs = append(offs, b.Len())
		b.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", len(offs), s))
	}
	b.WriteString("%PDF-1.4\n")
	kids := make([]string, n)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i := 1; i <= n; i++ {
		content := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (Pagina %d) Tj ET", i)
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*(i-1)))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	xref := b.Len()
	b.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(offs)+1))
	for _, o := range offs {
		b.WriteString(fmt.Sprintf("%010d 00000 n \n", o))
	}
	b.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref))
	return []byte(b.String())
}
