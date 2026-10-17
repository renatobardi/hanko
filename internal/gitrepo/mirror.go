package gitrepo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Mirror descreve o clone espelho depois de uma sincronização.
type Mirror struct {
	// BaseBranch é o branch de que os worktrees partem.
	BaseBranch string
	// HeadSHA é o commit de BaseBranch no remoto.
	HeadSHA string
}

// RemoteRef é o nome do branch remoto dentro do espelho (refs/remotes/origin/<branch>).
func RemoteRef(branch string) string { return "refs/remotes/origin/" + branch }

var branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// ValidBranch confere um nome de branch (subconjunto seguro das regras do git, o mesmo do ADR-0025).
func ValidBranch(b string) bool {
	return b != "" && len(b) <= 100 && branchRe.MatchString(b) && !strings.Contains(b, "..") && !strings.Contains(b, "//") &&
		!strings.HasSuffix(b, "/") && !strings.HasSuffix(b, ".") && !strings.HasSuffix(b, ".lock")
}

// SyncMirror cria (primeira vez) ou atualiza o espelho bare em dir a partir de remote. Os branches do remoto ficam
// em refs/remotes/origin/*: assim a busca com --prune nunca apaga nem recusa as branches de trabalho das tasks
// (refs/heads/*), que moram no mesmo repositório. wantBranch vazio usa o HEAD do remoto. A configuração gravada
// em dir leva o endereço sem credencial.
func (g *Git) SyncMirror(ctx context.Context, dir, remote, wantBranch string, cred *Credential) (Mirror, error) {
	rm, err := ParseRemote(remote, g.o)
	if err != nil {
		return Mirror{}, err
	}
	if wantBranch != "" && !ValidBranch(wantBranch) {
		return Mirror{}, errors.New("branch inválido")
	}
	if err := g.checkHost(ctx, rm); err != nil {
		return Mirror{}, err
	}
	if !filepath.IsAbs(dir) {
		return Mirror{}, errors.New("o caminho do espelho precisa ser absoluto")
	}
	rctx, cancel := g.opCtx(ctx, "sincronizar o espelho")
	defer cancel()

	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
			return Mirror{}, fmt.Errorf("criar pasta do espelho: %w", err)
		}
		if _, err := g.run(rctx, call{args: []string{"init", "--bare", "--quiet", "--template=", "--", dir}}); err != nil {
			return Mirror{}, err
		}
	}
	for _, kv := range [][2]string{
		{"remote.origin.url", rm.Raw},
		{"remote.origin.fetch", "+refs/heads/*:" + RemoteRef("*")},
		{"remote.origin.tagOpt", "--no-tags"},
		{"user.name", "Hanko"},
		{"user.email", "hanko@localhost"},
	} {
		if _, err := g.run(rctx, call{dir: dir, args: []string{"config", "--local", kv[0], kv[1]}}); err != nil {
			return Mirror{}, err
		}
	}

	env, proto, cleanup, err := g.authFor(rm, cred)
	if err != nil {
		return Mirror{}, err
	}
	defer cleanup()
	redact := []string{}
	if cred != nil {
		redact = append(redact, cred.Value)
		redact = append(redact, secretVariants(cred.Value)...)
	}
	if _, err := g.run(rctx, call{dir: dir, env: env, proto: proto, redact: redact,
		args: []string{"fetch", "--quiet", "--prune", "--no-tags", "origin"}}); err != nil {
		return Mirror{}, err
	}
	base := wantBranch
	if base == "" {
		base = g.remoteHead(rctx, dir, env, proto, redact)
	}
	if base == "" {
		base, err = g.firstRemoteBranch(rctx, dir)
		if err != nil {
			return Mirror{}, err
		}
	}
	sha, err := g.RevParse(rctx, dir, RemoteRef(base))
	if err != nil {
		return Mirror{}, fmt.Errorf("o branch %q não existe no repositório", base)
	}
	return Mirror{BaseBranch: base, HeadSHA: sha}, nil
}

// remoteHead pergunta ao remoto qual é o branch padrão (HEAD simbólico). Vazio se não conseguir.
func (g *Git) remoteHead(ctx context.Context, dir string, env, proto, redact []string) string {
	out, err := g.run(ctx, call{dir: dir, env: env, proto: proto, redact: redact, args: []string{"ls-remote", "--symref", "origin", "HEAD"}})
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(l, "ref: refs/heads/"); ok {
			name, _, _ := strings.Cut(rest, "\t")
			if ValidBranch(name) {
				return name
			}
		}
	}
	return ""
}

func (g *Git) firstRemoteBranch(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, call{dir: dir, args: []string{"for-each-ref", "--format=%(refname:strip=3)", "refs/remotes/origin/"}})
	if err != nil {
		return "", err
	}
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" && l != "HEAD" && ValidBranch(l) {
			names = append(names, l)
		}
	}
	if len(names) == 0 {
		return "", errors.New("o repositório não tem nenhum branch")
	}
	sort.Strings(names)
	for _, pref := range []string{"main", "master"} {
		for _, n := range names {
			if n == pref {
				return n, nil
			}
		}
	}
	return names[0], nil
}

// authFor monta o ambiente de autenticação da busca. Token HTTPS: cabeçalho Authorization escopado ao host,
// passado por GIT_CONFIG_* (fora do argv e do disco). Chave SSH: arquivo 0600 numa pasta temporária, removido no
// fim. Sem credencial, nada.
func (g *Git) authFor(rm Remote, cred *Credential) (env, proto []string, cleanup func(), err error) {
	cleanup = func() {}
	proto = []string{rm.Scheme}
	if rm.Scheme == "file" && !g.o.AllowFile {
		return nil, nil, cleanup, errors.New("file:// não é permitido")
	}
	if cred == nil || strings.TrimSpace(cred.Value) == "" {
		return nil, proto, cleanup, nil
	}
	switch {
	case rm.Scheme == "https" && !cred.IsSSHKey():
		u, perr := url.Parse(rm.Raw)
		if perr != nil {
			return nil, nil, cleanup, errors.New("URL inválida")
		}
		user, pass := "x-access-token", strings.TrimSpace(cred.Value)
		if a, b, ok := strings.Cut(pass, ":"); ok && a != "" {
			user, pass = a, b
		}
		hdr := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
		scope := u.Scheme + "://" + u.Host + "/"
		return []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http." + scope + ".extraHeader", "GIT_CONFIG_VALUE_0=" + hdr}, proto, cleanup, nil
	case rm.Scheme == "ssh" && cred.IsSSHKey():
		home, herr := g.homeDir()
		if herr != nil {
			return nil, nil, cleanup, herr
		}
		var rnd [8]byte
		if _, rerr := rand.Read(rnd[:]); rerr != nil {
			return nil, nil, cleanup, rerr
		}
		keyFile := filepath.Join(home, "key-"+hex.EncodeToString(rnd[:]))
		key := strings.TrimSpace(cred.Value) + "\n"
		if werr := os.WriteFile(keyFile, []byte(key), 0o600); werr != nil {
			return nil, nil, cleanup, fmt.Errorf("gravar a chave temporária: %w", werr)
		}
		cleanup = func() { _ = os.Remove(keyFile) }
		return []string{"GIT_SSH_COMMAND=" + sshCommand(keyFile, filepath.Join(home, "known_hosts"))}, proto, cleanup, nil
	}
	return nil, nil, cleanup, fmt.Errorf("a credencial não serve para um endereço %s (token para https, chave privada para ssh)", rm.Scheme)
}

// sshCommand é o comando ssh da busca: só a chave dada, sem prompt, host novo aceito e gravado numa pasta própria.
func sshCommand(keyFile, knownHosts string) string {
	return "ssh -i " + shellQuote(keyFile) + " -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + shellQuote(knownHosts)
}

// shellQuote protege um caminho para o GIT_SSH_COMMAND (o git o passa a um shell).
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// secretVariants são as formas derivadas do segredo que podem aparecer num erro (o cabeçalho em base64).
func secretVariants(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" || strings.Contains(v, "-----BEGIN") {
		return nil
	}
	user, pass := "x-access-token", v
	if a, b, ok := strings.Cut(v, ":"); ok && a != "" {
		user, pass = a, b
	}
	out := []string{base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))}
	if pass != v {
		out = append(out, pass)
	}
	return out
}
