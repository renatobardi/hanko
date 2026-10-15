package heartbeat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// usableWorkspaceDir decide se o caminho do workspace pode ser usado como diretório de trabalho. Sem raiz
// configurada, basta existir. Com raiz, o caminho e a raiz têm os symlinks resolvidos e o resultado precisa
// ficar estritamente dentro da raiz (revalidação no uso: fecha a janela entre salvar o workspace e rodar).
// Devolve o diretório a usar (já resolvido quando há raiz), se pode e, quando não pode, o aviso para o log da run.
func usableWorkspaceDir(path, root string) (dir string, ok bool, why string) {
	missing := fmt.Sprintf("workspace %q não encontrado no disco; usando diretório temporário", path)
	if root == "" {
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			return path, true, ""
		}
		return "", false, missing
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, missing
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", false, missing
	}
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		realRoot = filepath.Clean(root)
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, fmt.Sprintf("workspace %q resolve para fora da raiz dos workspaces; usando diretório temporário", path)
	}
	return real, true, ""
}
