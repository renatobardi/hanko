package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit roda o git "de verdade" para montar os repositórios de teste (tudo em diretórios temporários, sem rede).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// origin cria um repositório "remoto" com um commit em main e devolve a URL file://.
func origin(t *testing.T) (dir, url string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("alfa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "inicial")
	return dir, "file://" + dir
}

func newGit() *Git { return New(Options{AllowFile: true}) }

func TestSyncMirrorWorktreeCommitDiffLogAndCleanup(t *testing.T) {
	ctx := context.Background()
	src, url := origin(t)
	g := newGit()
	mirror := filepath.Join(t.TempDir(), "mirrors", "demo.git")

	m, err := g.SyncMirror(ctx, mirror, url, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.BaseBranch != "main" || len(m.HeadSHA) != 40 {
		t.Fatalf("espelho: %+v", m)
	}
	// o endereço gravado no espelho não leva credencial
	if cfg := runGit(t, mirror, "config", "--local", "remote.origin.url"); cfg != url {
		t.Fatalf("url gravada = %q", cfg)
	}

	wt := filepath.Join(t.TempDir(), "worktrees", "hnk-123")
	base, head, err := g.AddWorktree(ctx, mirror, wt, "hnk-123", RemoteRef(m.BaseBranch))
	if err != nil {
		t.Fatal(err)
	}
	if base != m.HeadSHA || head != base {
		t.Fatalf("base=%s head=%s", base, head)
	}
	if br, _ := g.CurrentBranch(ctx, wt); br != "hnk-123" {
		t.Fatalf("branch = %q", br)
	}

	// a persona edita e commita; também deixa um arquivo novo e uma edição solta
	if err := os.WriteFile(filepath.Join(wt, "src", "b.txt"), []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "feat: b")
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("# demo\nmais\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "novo.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := g.WorktreeStatus(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "hnk-123" || !st.Dirty() || len(st.Entries) != 2 {
		t.Fatalf("status: %+v", st)
	}
	p, err := g.DiffWorktree(ctx, wt, base, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, "+beta") || !strings.Contains(p.Text, "+mais") || p.Truncated || !strings.Contains(p.Stat, "src/b.txt") {
		t.Fatalf("diff:\n%s\nstat:\n%s", p.Text, p.Stat)
	}
	small, err := g.DiffWorktree(ctx, wt, base, 10)
	if err != nil || !small.Truncated || len(small.Text) > 10 {
		t.Fatalf("diff truncado: %+v %v", small, err)
	}
	log, err := g.Log(ctx, wt, base, "HEAD", 10)
	if err != nil || len(log) != 1 || log[0].Subject != "feat: b" || log[0].Author != "T" {
		t.Fatalf("log: %+v %v", log, err)
	}
	n, err := g.CountBetween(ctx, wt, base, "HEAD")
	if err != nil || n != 1 {
		t.Fatalf("count = %d %v", n, err)
	}

	// browser de arquivos do worktree
	ents, err := ListDir(wt, "")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range ents {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "src,README.md,novo.txt" {
		t.Fatalf("raiz: %v", names)
	}
	f, err := ReadFile(wt, "src/a.txt", 1000)
	if err != nil || f.Content != "alfa\n" {
		t.Fatalf("arquivo: %+v %v", f, err)
	}

	// limpeza: a pasta some, a branch e os commits ficam no espelho
	headNow, _ := g.RevParse(ctx, wt, "HEAD")
	if err := g.RemoveWorktree(ctx, mirror, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("a pasta deveria ter sumido: %v", err)
	}
	if ok, err := g.BranchExists(ctx, mirror, "hnk-123"); err != nil || !ok {
		t.Fatalf("branch deveria ficar: %v %v", ok, err)
	}
	p2, err := g.DiffCommits(ctx, mirror, base, headNow, 1<<20)
	if err != nil || !strings.Contains(p2.Text, "+beta") {
		t.Fatalf("diff pós-limpeza: %+v %v", p2, err)
	}
	tree, err := g.ListTree(ctx, mirror, headNow, "src")
	if err != nil || len(tree) != 2 || tree[0].Name != "a.txt" {
		t.Fatalf("árvore: %+v %v", tree, err)
	}
	sf, err := g.ShowFile(ctx, mirror, headNow, "src/b.txt", 100)
	if err != nil || sf.Content != "beta\n" {
		t.Fatalf("show: %+v %v", sf, err)
	}

	// a branch ainda não está integrada à base; depois do merge no remoto passa a estar
	ok, err := g.IsAncestor(ctx, mirror, headNow, RemoteRef("main"))
	if err != nil || ok {
		t.Fatalf("não deveria estar integrada: %v %v", ok, err)
	}
	runGit(t, src, "fetch", "-q", mirror, "hnk-123:hnk-123")
	runGit(t, src, "merge", "-q", "--ff-only", "hnk-123")
	if _, err := g.SyncMirror(ctx, mirror, url, "", nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := g.IsAncestor(ctx, mirror, headNow, RemoteRef("main")); err != nil || !ok {
		t.Fatalf("deveria estar integrada: %v %v", ok, err)
	}
	// a busca com --prune não apagou a branch da task
	if ok, _ := g.BranchExists(ctx, mirror, "hnk-123"); !ok {
		t.Fatal("o prune apagou a branch da task")
	}
	if err := g.DeleteBranch(ctx, mirror, "hnk-123"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := g.BranchExists(ctx, mirror, "hnk-123"); ok {
		t.Fatal("branch deveria ter sido apagada")
	}
}

func TestAddWorktreeReattachesExistingBranch(t *testing.T) {
	ctx := context.Background()
	_, url := origin(t)
	g := newGit()
	mirror := filepath.Join(t.TempDir(), "m.git")
	m, err := g.SyncMirror(ctx, mirror, url, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	wt1 := filepath.Join(root, "a")
	base, _, err := g.AddWorktree(ctx, mirror, wt1, "hnk-9", RemoteRef(m.BaseBranch))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt1, "x.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt1, "add", "-A")
	runGit(t, wt1, "commit", "-q", "-m", "x")
	want := runGit(t, wt1, "rev-parse", "HEAD")
	if err := g.RemoveWorktree(ctx, mirror, wt1); err != nil {
		t.Fatal(err)
	}
	// pasta apagada na mão: o prune cuida do registro e a branch é reaproveitada
	wt2 := filepath.Join(root, "b")
	base2, head2, err := g.AddWorktree(ctx, mirror, wt2, "hnk-9", RemoteRef(m.BaseBranch))
	if err != nil {
		t.Fatal(err)
	}
	if head2 != want || base2 != base {
		t.Fatalf("head=%s want=%s base=%s/%s", head2, want, base2, base)
	}
	if _, err := os.Stat(filepath.Join(wt2, "x.txt")); err != nil {
		t.Fatalf("o commit anterior deveria estar no worktree novo: %v", err)
	}
	if _, _, err := g.AddWorktree(ctx, mirror, wt2, "hnk-9", RemoteRef(m.BaseBranch)); err == nil {
		t.Fatal("caminho existente deveria ser recusado")
	}
}

func TestSyncMirrorErrors(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	dir := filepath.Join(t.TempDir(), "m.git")
	if _, err := g.SyncMirror(ctx, dir, "file:///nao/existe/mesmo", "", nil); err == nil {
		t.Fatal("remoto inexistente deveria falhar")
	}
	_, url := origin(t)
	if _, err := g.SyncMirror(ctx, dir, url, "nao-existe", nil); err == nil || !strings.Contains(err.Error(), "não existe") {
		t.Fatalf("branch inexistente: %v", err)
	}
	if _, err := g.SyncMirror(ctx, "relativo/m.git", url, "", nil); err == nil {
		t.Fatal("caminho relativo deveria falhar")
	}
	if _, err := New(Options{}).SyncMirror(ctx, dir, url, "", nil); err == nil {
		t.Fatal("file:// sem AllowFile deveria falhar")
	}
}

func TestParseRemote(t *testing.T) {
	o := Options{}
	good := map[string]string{
		"https://github.com/acme/app.git": "https",
		"ssh://git@github.com/acme/app":   "ssh",
		"git@github.com:acme/app.git":     "ssh",
	}
	for in, scheme := range good {
		rm, err := ParseRemote(in, o)
		if err != nil || rm.Scheme != scheme {
			t.Errorf("%q: %+v %v", in, rm, err)
		}
	}
	for _, in := range []string{
		"", "   ", "-oProxyCommand=x", "https://u:p@github.com/a/b", "https://tok@github.com/a/b", "http://github.com/a/b",
		"file:///tmp/x", "ftp://x/y", "https://github.com/a b", "https://localhost/a/b", "https://10.0.0.5/a/b",
		"https://github.com/a/b?x=1", "git@host:-evil", "ssh://u:p@host/a", "git://host/a", "https://x.internal/a",
	} {
		if _, err := ParseRemote(in, o); err == nil {
			t.Errorf("%q deveria ser recusado", in)
		}
	}
	if _, err := ParseRemote("https://10.0.0.5/a/b", Options{AllowPrivate: true}); err != nil {
		t.Errorf("AllowPrivate: %v", err)
	}
	if _, err := ParseRemote("file:///tmp/x", Options{AllowFile: true}); err != nil {
		t.Errorf("AllowFile: %v", err)
	}
	if _, err := ParseRemote("file://host/tmp/x", Options{AllowFile: true}); err == nil {
		t.Error("file:// com host deveria ser recusado")
	}
}

func TestCheckHostBlocksInternal(t *testing.T) {
	g := New(Options{})
	if err := g.checkHost(context.Background(), Remote{Scheme: "https", Host: "127.0.0.1"}); err == nil {
		t.Fatal("loopback deveria ser bloqueado")
	}
	if err := g.checkHost(context.Background(), Remote{Scheme: "ssh", Host: "localhost"}); err == nil {
		t.Fatal("localhost deveria ser bloqueado")
	}
	if err := New(Options{AllowPrivate: true}).checkHost(context.Background(), Remote{Scheme: "ssh", Host: "10.1.1.1"}); err != nil {
		t.Fatalf("AllowPrivate: %v", err)
	}
}

func TestAuthForKeepsCredentialOutOfArgvAndConfig(t *testing.T) {
	g := newGit()
	rm := Remote{Raw: "https://github.com/acme/app.git", Scheme: "https", Host: "github.com"}
	env, proto, cleanup, err := g.authFor(rm, &Credential{Value: "tok-secreto"})
	defer cleanup()
	if err != nil || len(proto) != 1 || proto[0] != "https" {
		t.Fatalf("%v %v", proto, err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader") || !strings.Contains(joined, "Authorization: Basic ") {
		t.Fatalf("env: %v", env)
	}
	if strings.Contains(joined, "tok-secreto") {
		t.Fatal("o token não deveria aparecer em claro no ambiente (só em base64 no cabeçalho)")
	}
	// usuário:senha
	env, _, c2, err := g.authFor(rm, &Credential{Value: "ci:senha"})
	defer c2()
	if err != nil || !strings.Contains(strings.Join(env, "\n"), "Basic ") {
		t.Fatalf("%v %v", env, err)
	}
	// chave SSH num endereço https é recusada, e token num ssh também
	if _, _, _, err := g.authFor(rm, &Credential{Value: "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----"}); err == nil {
		t.Fatal("chave num https deveria ser recusada")
	}
	srm := Remote{Raw: "git@github.com:acme/app.git", Scheme: "ssh", Host: "github.com"}
	if _, _, _, err := g.authFor(srm, &Credential{Value: "tok"}); err == nil {
		t.Fatal("token num ssh deveria ser recusado")
	}
	// chave SSH: arquivo 0600 que some no cleanup
	env, _, c3, err := g.authFor(srm, &Credential{Value: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"})
	if err != nil || len(env) != 1 || !strings.HasPrefix(env[0], "GIT_SSH_COMMAND=ssh -i '") || !strings.Contains(env[0], "StrictHostKeyChecking=accept-new") {
		t.Fatalf("%v %v", env, err)
	}
	keyFile := strings.Split(strings.TrimPrefix(env[0], "GIT_SSH_COMMAND=ssh -i '"), "'")[0]
	st, err := os.Stat(keyFile)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("chave: %v %v", st, err)
	}
	c3()
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatal("a chave deveria ser apagada")
	}
	// sem credencial
	env, _, c4, err := g.authFor(rm, nil)
	defer c4()
	if err != nil || len(env) != 0 {
		t.Fatalf("%v %v", env, err)
	}
}

func TestErrorsRedactSecrets(t *testing.T) {
	g := newGit()
	_, err := g.run(context.Background(), call{dir: t.TempDir(), redact: []string{"s3cr3t"}, args: []string{"fetch", "https://s3cr3t.invalid/x"}})
	if err == nil {
		t.Fatal("deveria falhar")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("segredo vazou: %v", err)
	}
	if !strings.Contains(scrub("a s3cr3t b", []string{"s3cr3t", ""}).Error(), "a *** b") {
		t.Fatal("scrub")
	}
	if v := secretVariants("ci:senha"); len(v) != 2 {
		t.Fatalf("variantes: %v", v)
	}
	if secretVariants("-----BEGIN KEY") != nil || secretVariants(" ") != nil {
		t.Fatal("chave/vazio não têm variantes")
	}
}

func TestBrowseIsReadOnlyAndConfined(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "segredo.txt"), []byte("fora"), 0o600); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, ".git"), 0o750))
	must(os.WriteFile(filepath.Join(root, ".git", "config"), []byte("x"), 0o600))
	must(os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ola"), 0o600))
	must(os.WriteFile(filepath.Join(root, "bin.dat"), []byte{1, 0, 2}, 0o600))
	must(os.WriteFile(filepath.Join(root, "grande.txt"), []byte(strings.Repeat("é", 50)), 0o600))
	must(os.Symlink(outside, filepath.Join(root, "link-dir")))
	must(os.Symlink(filepath.Join(outside, "segredo.txt"), filepath.Join(root, "link.txt")))

	ents, err := ListDir(root, "")
	must(err)
	for _, e := range ents {
		if e.Name == ".git" {
			t.Fatal(".git não deveria aparecer")
		}
		if e.Name == "link-dir" && e.Dir {
			t.Fatal("link para pasta não deve virar pasta navegável")
		}
	}
	for _, bad := range []string{"../x", "link-dir", ".git", "a/../../x", ".GIT/config"} {
		if _, err := ListDir(root, bad); err == nil {
			t.Errorf("ListDir(%q) deveria falhar", bad)
		}
	}
	for _, bad := range []string{"link.txt", ".git/config", "../x", "link-dir/segredo.txt"} {
		if _, err := ReadFile(root, bad, 100); err == nil {
			t.Errorf("ReadFile(%q) deveria falhar", bad)
		}
	}
	if _, err := ReadFile(root, ".", 100); err == nil {
		t.Error("pasta deveria falhar")
	}
	f, err := ReadFile(root, "bin.dat", 100)
	if err != nil || !f.Binary || f.Content != "" {
		t.Fatalf("binário: %+v %v", f, err)
	}
	f, err = ReadFile(root, "grande.txt", 11)
	if err != nil || !f.Truncated || f.Binary || f.Content != strings.Repeat("é", 5) {
		t.Fatalf("truncado: %+v %v", f, err)
	}
}

func TestValidBranchAndRemoteRef(t *testing.T) {
	for _, ok := range []string{"main", "hnk-123", "feat/x_y.z"} {
		if !ValidBranch(ok) {
			t.Errorf("%q deveria valer", ok)
		}
	}
	for _, bad := range []string{"", "-x", "a..b", "a//b", "a/", "a.", "a.lock", "a b", strings.Repeat("a", 101)} {
		if ValidBranch(bad) {
			t.Errorf("%q não deveria valer", bad)
		}
	}
	if RemoteRef("main") != "refs/remotes/origin/main" {
		t.Fatal("RemoteRef")
	}
	g := newGit()
	if _, err := g.RevParse(context.Background(), t.TempDir(), "--foo"); err == nil {
		t.Fatal("rev começando com - deveria ser recusada")
	}
	if _, err := g.Log(context.Background(), t.TempDir(), "", "", 5); err == nil {
		t.Fatal("log sem to deveria falhar")
	}
}
