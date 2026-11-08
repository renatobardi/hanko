package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// RemoteHostOf devolve o host de um endereço de repositório ("" se não der para reconhecer), para log e atividade.
func RemoteHostOf(raw string) string {
	rm, err := ParseRemote(raw, Options{AllowFile: true})
	if err != nil {
		return ""
	}
	return rm.Host
}

// Push descreve o envio de uma branch ao remoto.
type Push struct {
	// SHA é o commit da branch que ficou no remoto.
	SHA string
	// UpToDate: o remoto já tinha esse commit; nada foi enviado.
	UpToDate bool
}

// PushBranch envia a branch de trabalho (refs/heads/<branch> do espelho) ao remoto com o mesmo nome, sem força: um
// remoto que já tenha a branch com commits que não estão aqui recusa o envio (o erro diz "rejected"). A credencial
// segue o mesmo caminho da busca (cabeçalho escopado ao host ou chave temporária), fora do argv e do disco. Depois
// do envio, refs/remotes/origin/<branch> passa a apontar para o commit enviado, como uma busca faria.
func (g *Git) PushBranch(ctx context.Context, mirror, remote, branch string, cred *Credential) (Push, error) {
	rm, err := ParseRemote(remote, g.o)
	if err != nil {
		return Push{}, err
	}
	if !ValidBranch(branch) {
		return Push{}, errors.New("branch inválido")
	}
	if !filepath.IsAbs(mirror) {
		return Push{}, errors.New("o caminho do espelho precisa ser absoluto")
	}
	if err := g.checkHost(ctx, rm); err != nil {
		return Push{}, err
	}
	rctx, cancel := g.opCtx(ctx, "enviar a branch")
	defer cancel()
	sha, err := g.RevParse(rctx, mirror, "refs/heads/"+branch)
	if err != nil {
		return Push{}, fmt.Errorf("a branch %q não existe no espelho", branch)
	}
	if remoteSHA, rerr := g.RevParse(rctx, mirror, RemoteRef(branch)); rerr == nil && remoteSHA == sha {
		return Push{SHA: sha, UpToDate: true}, nil
	}
	env, proto, cleanup, err := g.authFor(rm, cred)
	if err != nil {
		return Push{}, err
	}
	defer cleanup()
	redact := []string{}
	if cred != nil {
		redact = append(redact, cred.Value)
		redact = append(redact, secretVariants(cred.Value)...)
	}
	refspec := "refs/heads/" + branch + ":refs/heads/" + branch
	if _, err := g.run(rctx, call{dir: mirror, env: env, proto: proto, redact: redact,
		args: []string{"push", "--quiet", "--no-verify", "--", "origin", refspec}}); err != nil {
		var ee *ExitError
		if errors.As(err, &ee) && strings.Contains(ee.Stderr, "rejected") {
			return Push{}, fmt.Errorf("o remoto recusou a branch %q (ela tem commits que não estão aqui; sincronize o espelho e peça à persona para integrar): %w", branch, err)
		}
		return Push{}, err
	}
	if _, err := g.run(rctx, call{dir: mirror, args: []string{"update-ref", RemoteRef(branch), sha}}); err != nil {
		return Push{}, err
	}
	return Push{SHA: sha}, nil
}
