package heartbeat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Anexos em disco (ADR-0066): antes da run, os anexos da task (ou da conversa) são gravados no diretório de trabalho,
// em .hanko-anexos/<run>/, e o briefing dá o caminho de cada um. A persona os abre com as ferramentas de arquivo do
// harness (o Read do claude lê imagem e PDF), o que a tool MCP não permite: ela devolve binário só em base64, que o
// modelo não enxerga. Por ficar dentro do diretório de trabalho, o caminho vale também num ambiente remoto, para onde
// o diretório é sincronizado. Um .gitignore na pasta impede que os arquivos entrem num commit do workspace.

const (
	// attachmentsDirName é a pasta, no diretório de trabalho da run, onde os anexos ficam.
	attachmentsDirName = ".hanko-anexos"
	// maxDiskAttachmentsBytes é o máximo que uma run grava em disco; o que passar fica só no MCP.
	maxDiskAttachmentsBytes int64 = 512 << 20
)

// attachmentContentBackend lê o conteúdo de um anexo (opcional como os demais backends).
type attachmentContentBackend interface {
	ArtifactContent(ctx context.Context, companyID, id domain.ID) (domain.Artifact, []byte, error)
}

// materializeAttachments grava os anexos de rc em <dir>/.hanko-anexos/<run>/ e preenche LocalPath (relativo a dir)
// de cada um que coube. Um anexo que não pôde ser lido ou gravado vira aviso e segue só pelo MCP. Devolve a limpeza
// da pasta da run (nil quando nada foi gravado).
func (h *Heartbeat) materializeAttachments(ctx context.Context, rc *service.RunContext, dir string, emit func(domain.RunEventKind, string)) func() {
	cb, ok := h.svc.(attachmentContentBackend)
	if !ok || dir == "" || len(rc.Attachments) == 0 {
		return nil
	}
	parent := filepath.Join(dir, attachmentsDirName)
	runDir := filepath.Join(parent, rc.Run.ID.String())
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		emit(domain.EventSystem, fmt.Sprintf("anexos: não deu para criar %s (seguem só pelo MCP): %v", attachmentsDirName, err))
		return nil
	}
	cleanup := func() {
		_ = os.RemoveAll(runDir)
		_ = os.Remove(filepath.Join(parent, ".gitignore"))
		_ = os.Remove(parent) // só sai vazia: outra run no mesmo workspace pode estar usando a pasta
	}
	_ = os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("*\n"), 0o600)

	used := map[string]bool{}
	var total int64
	wrote := 0
	for i := range rc.Attachments {
		a := &rc.Attachments[i]
		if total+a.SizeBytes > maxDiskAttachmentsBytes {
			emit(domain.EventSystem, fmt.Sprintf("anexos: %q fica só pelo MCP (a run já gravou %d MiB em disco)", a.Name, total>>20))
			continue
		}
		_, body, err := cb.ArtifactContent(ctx, rc.Company.ID, a.ID)
		if err != nil {
			emit(domain.EventSystem, fmt.Sprintf("anexos: não deu para ler %q (segue só pelo MCP): %v", a.Name, err))
			continue
		}
		name := diskName(a.Name, a.ID, used)
		if err := os.WriteFile(filepath.Join(runDir, name), body, 0o600); err != nil {
			emit(domain.EventSystem, fmt.Sprintf("anexos: não deu para gravar %q (segue só pelo MCP): %v", a.Name, err))
			continue
		}
		total += int64(len(body))
		a.LocalPath = filepath.ToSlash(filepath.Join(attachmentsDirName, rc.Run.ID.String(), name))
		wrote++
	}
	if wrote == 0 {
		cleanup()
		return nil
	}
	return cleanup
}

// diskName é o nome do arquivo em disco: só o nome base (sem diretórios nem ponto inicial, que esconderia o arquivo),
// com o começo do id na frente quando dois anexos têm o mesmo nome.
func diskName(name string, id domain.ID, used map[string]bool) string {
	n := strings.TrimLeft(filepath.Base(strings.ReplaceAll(name, `\`, "/")), ".")
	if n == "" || n == "/" {
		n = "anexo"
	}
	if used[n] {
		n = id.String()[:8] + "-" + n
	}
	used[n] = true
	return n
}

// chainCleanup roda first e depois next (que pode ser nil).
func chainCleanup(first, next func()) func() {
	return func() {
		first()
		if next != nil {
			next()
		}
	}
}
