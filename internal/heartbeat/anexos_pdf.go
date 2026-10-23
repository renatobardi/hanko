package heartbeat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	// pdfPartPages é o tamanho de cada parte (o que a ferramenta de leitura abre de uma vez); com mais de pdfMaxParts
	// partes, as partes crescem para caber.
	pdfPartPages = 20
	pdfMaxParts  = 50
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

// splitPDF extrai o texto e divide o PDF de path (já gravado em disco). rel é o caminho de path relativo ao diretório de
// trabalho. Devolve nil quando o PDF não é grande ou quando nada pôde ser gerado.
func splitPDF(ctx context.Context, path, rel string, size int64) (*pdfExtras, []string) {
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
	base := strings.TrimSuffix(path, filepath.Ext(path))
	relBase := strings.TrimSuffix(rel, filepath.Ext(rel))
	ex := &pdfExtras{Pages: pages}

	if bin, err := lookPDFTool("pdftotext"); err != nil {
		notes = append(notes, "pdftotext ausente na imagem")
	} else if _, err := runPDFTool(ctx, bin, "-layout", "-enc", "UTF-8", path, base+".txt"); err != nil {
		notes = append(notes, "pdftotext falhou: "+err.Error())
		_ = os.Remove(base + ".txt")
	} else if st, err := os.Stat(base + ".txt"); err == nil && st.Size() > 0 {
		ex.TextPath, ex.Bytes = filepath.ToSlash(relBase+".txt"), ex.Bytes+st.Size()
	} else {
		_ = os.Remove(base + ".txt") // PDF só de imagens (escaneado): sem texto a extrair
		notes = append(notes, "o PDF não tem texto extraível (provavelmente escaneado)")
	}

	if pages > pdfPartPages {
		per := pdfPartPages
		if n := (pages + per - 1) / per; n > pdfMaxParts {
			per = (pages + pdfMaxParts - 1) / pdfMaxParts
		}
		if bin, err := lookPDFTool("qpdf"); err != nil {
			notes = append(notes, "qpdf ausente na imagem")
		} else {
			parts := 0
			for from := 1; from <= pages; from += per {
				to := min(from+per-1, pages)
				out := fmt.Sprintf("%s.parte-%02d.pdf", base, parts+1)
				if _, err := runPDFTool(ctx, bin, path, "--pages", ".", fmt.Sprintf("%d-%d", from, to), "--", out); err != nil {
					notes = append(notes, "qpdf falhou: "+err.Error())
					_ = os.Remove(out)
					break
				}
				if st, err := os.Stat(out); err == nil {
					ex.Bytes += st.Size()
				}
				parts++
			}
			if parts > 0 {
				ex.Parts, ex.PartPages = parts, per
				ex.PartsGlob = filepath.ToSlash(relBase) + ".parte-NN.pdf"
			}
		}
	}
	if ex.TextPath == "" && ex.Parts == 0 {
		return nil, notes
	}
	return ex, notes
}
