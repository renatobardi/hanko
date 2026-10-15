package claude

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/renatobardi/hanko/internal/domain"
)

// WriteSkills grava as skills em <tmp>/.claude/skills/<slug>/SKILL.md num diretório temporário e devolve
// a raiz <tmp>, que vai ao harness em --add-dir (o Claude Code carrega .claude/skills dos diretórios
// adicionais). O workspace do usuário não é tocado. Quem chama remove a raiz ao fim da run (ADR-0029).
func WriteSkills(skills []domain.Skill) (string, error) {
	root, err := os.MkdirTemp("", "hanko-skills-*")
	if err != nil {
		return "", err
	}
	for _, s := range skills {
		// o slug já é validado no service; aqui é defesa contra caminho fora da raiz
		if s.Slug == "" || s.Slug != filepath.Base(s.Slug) || s.Slug == "." || s.Slug == ".." {
			_ = os.RemoveAll(root)
			return "", fmt.Errorf("slug de skill inválido: %q", s.Slug)
		}
		dir := filepath.Join(root, ".claude", "skills", s.Slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			_ = os.RemoveAll(root)
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(s.MD()), 0o600); err != nil {
			_ = os.RemoveAll(root)
			return "", err
		}
	}
	return root, nil
}
