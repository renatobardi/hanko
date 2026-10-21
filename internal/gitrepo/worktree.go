package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RevParse resolve rev para um commit completo.
func (g *Git) RevParse(ctx context.Context, dir, rev string) (string, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return "", errors.New("revisão inválida")
	}
	out, err := g.run(ctx, call{dir: dir, args: []string{"rev-parse", "--verify", "--quiet", "--end-of-options", rev + "^{commit}"}})
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if len(sha) != 40 {
		return "", fmt.Errorf("commit inesperado: %q", sha)
	}
	return sha, nil
}

// BranchExists diz se refs/heads/<branch> existe no repositório.
func (g *Git) BranchExists(ctx context.Context, dir, branch string) (bool, error) {
	if !ValidBranch(branch) {
		return false, errors.New("branch inválido")
	}
	_, err := g.run(ctx, call{dir: dir, args: []string{"show-ref", "--verify", "--quiet", "refs/heads/" + branch}})
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == 1 {
		return false, nil
	}
	return err == nil, err
}

// AddWorktree cria o worktree de uma task em wtPath com a branch (nova a partir de baseRef, ou existente se já
// houver: a task foi retomada depois de a pasta ser limpa). Devolve o commit de partida (o de baseRef quando a
// branch é nova; o merge-base com baseRef quando já existia) e o commit atual da branch.
func (g *Git) AddWorktree(ctx context.Context, mirror, wtPath, branch, baseRef string) (baseSHA, headSHA string, err error) {
	if !ValidBranch(branch) {
		return "", "", errors.New("branch inválido")
	}
	if !filepath.IsAbs(wtPath) {
		return "", "", errors.New("o caminho do worktree precisa ser absoluto")
	}
	if _, err := os.Lstat(wtPath); err == nil {
		return "", "", fmt.Errorf("o caminho %s já existe", wtPath)
	}
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o750); err != nil {
		return "", "", fmt.Errorf("criar pasta do worktree: %w", err)
	}
	rctx, cancel := g.opCtx(ctx, "criar o worktree")
	defer cancel()
	// entradas de worktrees cuja pasta sumiu atrapalham o `add`
	if _, err := g.run(rctx, call{dir: mirror, args: []string{"worktree", "prune"}}); err != nil {
		return "", "", err
	}
	exists, err := g.BranchExists(rctx, mirror, branch)
	if err != nil {
		return "", "", err
	}
	baseTip, err := g.RevParse(rctx, mirror, baseRef)
	if err != nil {
		return "", "", fmt.Errorf("a base %s não existe no espelho", strings.TrimPrefix(baseRef, "refs/remotes/origin/"))
	}
	if exists {
		if _, err := g.run(rctx, call{dir: mirror, args: []string{"worktree", "add", "--quiet", "--", wtPath, branch}}); err != nil {
			return "", "", err
		}
		head, err := g.RevParse(rctx, mirror, "refs/heads/"+branch)
		if err != nil {
			return "", "", err
		}
		base := baseTip
		if mb, merr := g.run(rctx, call{dir: mirror, args: []string{"merge-base", baseTip, head}}); merr == nil && len(strings.TrimSpace(string(mb))) == 40 {
			base = strings.TrimSpace(string(mb))
		}
		return base, head, nil
	}
	if _, err := g.run(rctx, call{dir: mirror, args: []string{"worktree", "add", "--quiet", "--no-track", "-b", branch, "--", wtPath, baseTip}}); err != nil {
		return "", "", err
	}
	return baseTip, baseTip, nil
}

// RemoveWorktree tira o worktree do disco e do registro do espelho. A branch fica (os commits ficam
// recuperáveis). Pasta já ausente não é erro.
func (g *Git) RemoveWorktree(ctx context.Context, mirror, wtPath string) error {
	if !filepath.IsAbs(wtPath) {
		return errors.New("o caminho do worktree precisa ser absoluto")
	}
	rctx, cancel := g.opCtx(ctx, "remover o worktree")
	defer cancel()
	if _, err := os.Lstat(wtPath); err == nil {
		if _, err := g.run(rctx, call{dir: mirror, args: []string{"worktree", "remove", "--force", "--", wtPath}}); err != nil {
			// último recurso: a pasta pode estar corrompida; apaga e deixa o prune limpar o registro
			if rerr := os.RemoveAll(wtPath); rerr != nil {
				return fmt.Errorf("%w; apagar a pasta: %w", err, rerr)
			}
		}
	}
	_, err := g.run(rctx, call{dir: mirror, args: []string{"worktree", "prune"}})
	return err
}

// DeleteBranch apaga refs/heads/<branch> (só chamada quando o trabalho já está integrado à base).
func (g *Git) DeleteBranch(ctx context.Context, mirror, branch string) error {
	if !ValidBranch(branch) {
		return errors.New("branch inválido")
	}
	_, err := g.run(ctx, call{dir: mirror, args: []string{"update-ref", "-d", "refs/heads/" + branch}})
	return err
}

// IsAncestor diz se a é ancestral de b (a já está contido em b).
func (g *Git) IsAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	_, err := g.run(ctx, call{dir: dir, args: []string{"merge-base", "--is-ancestor", a, b}})
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == 1 {
		return false, nil
	}
	return err == nil, err
}

// CountBetween conta os commits em to que não estão em from (from..to).
func (g *Git) CountBetween(ctx context.Context, dir, from, to string) (int, error) {
	out, err := g.run(ctx, call{dir: dir, args: []string{"rev-list", "--count", from + ".." + to}})
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); err != nil {
		return 0, fmt.Errorf("contagem inesperada: %q", out)
	}
	return n, nil
}

// CommitAll registra como commit tudo o que está solto no worktree (respeita o .gitignore). Serve de rede de
// segurança antes de apagar a pasta: o trabalho não commitado fica na branch em vez de se perder. Devolve false se
// não havia nada a registrar.
func (g *Git) CommitAll(ctx context.Context, wt, message string) (bool, error) {
	st, err := g.WorktreeStatus(ctx, wt)
	if err != nil {
		return false, err
	}
	if !st.Dirty() {
		return false, nil
	}
	if _, err := g.run(ctx, call{dir: wt, args: []string{"add", "-A", "--"}}); err != nil {
		return false, err
	}
	if _, err := g.run(ctx, call{dir: wt, args: []string{"commit", "--quiet", "--no-verify", "--no-gpg-sign", "-m", message}}); err != nil {
		return false, err
	}
	return true, nil
}

// CurrentBranch devolve o branch em que o worktree está ("" se HEAD solto).
func (g *Git) CurrentBranch(ctx context.Context, wt string) (string, error) {
	out, err := g.run(ctx, call{dir: wt, args: []string{"symbolic-ref", "--short", "--quiet", "HEAD"}})
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == 1 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
