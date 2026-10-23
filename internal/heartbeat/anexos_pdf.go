package heartbeat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// PDF grande (ADR-0066, adendo): a ferramenta de leitura de arquivos do harness recusa PDF acima de ~20 MB e abre poucas
// páginas por vez. Quando o anexo é grande, o heartbeat grava ao lado do original o texto extraído inteiro e o PDF
// dividido em partes pequenas, e o briefing manda ler o texto primeiro. Os utilitários (pdfinfo, pdftotext, qpdf) vêm
// na imagem do container; sem eles nada quebra: o PDF segue inteiro e o briefing não promete partes.

const (
	// pdfSplitBytes e pdfSplitPages: acima de qualquer um dos dois o PDF é extraído e dividido.
	pdfSplitBytes int64 = 15 << 20
	pdfSplitPages       = 100
	// pdfPartPages é o tamanho de uma parte (o que a ferramenta de leitura abre de uma vez); pdfPartBytes, o peso que se
	// espera de cada parte (um PDF pesado com poucas páginas, como um escaneado, também é dividido, página a página se
	// preciso); com mais de pdfMaxParts partes, as partes crescem para caber.
	pdfPartPages       = 20
	pdfPartBytes int64 = 10 << 20
	pdfMaxParts        = 50
	// pdfToolTimeout limita cada utilitário; pdfBudget, o tempo total gasto com um PDF.
	pdfToolTimeout = 2 * time.Minute
	pdfBudget      = 5 * time.Minute
)

// pdfExtras é o que foi gerado ao lado de um PDF grande; os caminhos são relativos ao diretório de trabalho.
type pdfExtras struct {
	Pages     int
	TextPath  string
	PartsGlob string // caminho das partes com NN no lugar do número ("…/nome.parte-NN.pdf")
	Parts     int
	PartPages int
	Bytes     int64 // o que as partes e o texto ocupam em disco
}

// runPDFTool e lookPDFTool existem para o teste trocar os utilitários.
var (
	lookPDFTool = exec.LookPath
	runPDFTool  = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, pdfToolTimeout)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).Output()
	}
)

var pdfPagesRe = regexp.MustCompile(`(?m)^Pages:\s+(\d+)`)

// pdfPartCount diz em quantas partes dividir: pelas páginas (pdfPartPages cada) ou pelo peso (pdfPartBytes cada), o que
// der mais, até pdfMaxParts. Menos de 2 = não divide.
func pdfPartCount(pages int, size int64) int {
	n := (pages + pdfPartPages - 1) / pdfPartPages
	if bySize := int((size + pdfPartBytes - 1) / pdfPartBytes); bySize > n {
		n = bySize
	}
	return min(n, pdfMaxParts, pages)
}

// splitPDF extrai o texto e divide o PDF de path (já gravado em disco). rel é o caminho de path relativo ao diretório de
// trabalho; derived é o nome-base (sem extensão) dos arquivos gerados, no mesmo diretório, já reservado pelo chamador para
// não colidir com outro anexo; budget é o espaço em disco que ainda cabe na run. Devolve nil quando o PDF não é grande
// ou quando nada pôde ser gerado; as notas vão para o log da run.
func splitPDF(ctx context.Context, path, rel, derived string, size, budget int64) (*pdfExtras, []string) {
	ctx, cancel := context.WithTimeout(ctx, pdfBudget)
	defer cancel()
	var notes []string
	pages := 0
	if bin, err := lookPDFTool("pdfinfo"); err == nil {
		if out, err := runPDFTool(ctx, bin, path); err == nil {
			if m := pdfPagesRe.FindSubmatch(out); m != nil {
				pages, _ = strconv.Atoi(string(m[1]))
			}
		}
	}
	if size <= pdfSplitBytes && pages <= pdfSplitPages {
		return nil, nil
	}
	dir, relDir := filepath.Dir(path), filepath.Dir(rel)
	ex := &pdfExtras{Pages: pages}

	txt := filepath.Join(dir, derived+".txt")
	if bin, err := lookPDFTool("pdftotext"); err != nil {
		notes = append(notes, "pdftotext ausente na imagem")
	} else if _, err := runPDFTool(ctx, bin, "-layout", "-enc", "UTF-8", path, txt); err != nil {
		notes = append(notes, "pdftotext falhou: "+err.Error())
		_ = os.Remove(txt)
	} else if st, err := os.Stat(txt); err == nil && st.Size() > 0 && st.Size() <= budget {
		ex.TextPath, ex.Bytes = filepath.ToSlash(filepath.Join(relDir, derived+".txt")), st.Size()
	} else {
		_ = os.Remove(txt)
		if err == nil && st.Size() > 0 {
			notes = append(notes, "o texto extraído passaria do teto de disco da run")
		} else {
			notes = append(notes, "o PDF não tem texto extraível (provavelmente escaneado)")
		}
	}

	if n := pdfPartCount(pages, size); n >= 2 {
		per := (pages + n - 1) / n
		parts, bytes, why := splitParts(ctx, path, dir, derived, per, budget-ex.Bytes)
		if why != "" {
			notes = append(notes, why)
		}
		if parts > 0 {
			ex.Parts, ex.PartPages, ex.Bytes = parts, per, ex.Bytes+bytes
			ex.PartsGlob = filepath.ToSlash(filepath.Join(relDir, derived+".parte-NN.pdf"))
		}
	}
	if ex.TextPath == "" && ex.Parts == 0 {
		return nil, notes
	}
	return ex, notes
}

// splitParts divide o PDF em partes de per páginas com um único qpdf (um só parse do arquivo grande) e renomeia a saída
// para <derived>.parte-NN.pdf. Tudo ou nada: qualquer falha, ou passar do orçamento de disco, apaga as partes e devolve 0,
// para o briefing nunca prometer páginas que não estão nas partes. Aviso do qpdf (saída 3) não é falha.
func splitParts(ctx context.Context, path, dir, derived string, per int, budget int64) (parts int, bytes int64, why string) {
	bin, err := lookPDFTool("qpdf")
	if err != nil {
		return 0, 0, "qpdf ausente na imagem"
	}
	tmp, err := os.MkdirTemp(dir, ".split-")
	if err != nil {
		return 0, 0, "não deu para criar a pasta das partes: " + err.Error()
	}
	defer os.RemoveAll(tmp)
	if _, err := runPDFTool(ctx, bin, "--warning-exit-0", fmt.Sprintf("--split-pages=%d", per), path, filepath.Join(tmp, "p.pdf")); err != nil {
		return 0, 0, "qpdf falhou: " + err.Error()
	}
	files, _ := filepath.Glob(filepath.Join(tmp, "p-*.pdf"))
	sort.Strings(files) // o qpdf numera com zeros à esquerda: a ordem alfabética é a das páginas
	if len(files) < 2 {
		return 0, 0, "qpdf não gerou as partes"
	}
	moved := make([]string, 0, len(files))
	undo := func() {
		for _, m := range moved {
			_ = os.Remove(m)
		}
	}
	for i, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			undo()
			return 0, 0, "qpdf: " + err.Error()
		}
		if bytes += st.Size(); bytes > budget {
			undo()
			return 0, 0, "as partes passariam do teto de disco da run; o PDF segue inteiro"
		}
		dst := filepath.Join(dir, fmt.Sprintf("%s.parte-%02d.pdf", derived, i+1))
		if err := os.Rename(f, dst); err != nil {
			undo()
			return 0, 0, "não deu para mover a parte " + strconv.Itoa(i+1) + ": " + err.Error()
		}
		moved = append(moved, dst)
	}
	return len(files), bytes, ""
}
