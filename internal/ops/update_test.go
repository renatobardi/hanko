package ops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v1.2.3", "1.2.3", 0, true},
		{"1.2.4", "1.2.3", 1, true},
		{"1.10.0", "1.9.9", 1, true},
		{"2.0.0", "1.99.99", 1, true},
		{"1.2.3-rc.1", "1.2.3", -1, true},
		{"1.2.3", "1.2.3-rc.1", 1, true},
		{"1.2.3-rc.2", "1.2.3-rc.10", -1, true},
		{"1.2.3-alpha", "1.2.3-1", 1, true},
		{"1.2.3-rc", "1.2.3-rc.1", -1, true},
		{"1.2.3+build5", "1.2.3", 0, true},
		{"dev", "1.2.3", 0, false},
		{"1.2", "1.2.3", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("Compare(%q,%q) = %d,%v quer %d,%v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

// ---------- fonte fake ----------

type fakeSource struct {
	rel     Release
	err     error
	files   map[string][]byte // URL -> conteúdo
	dlCalls []string
}

func (f *fakeSource) Latest(_ context.Context, _ string) (Release, error) { return f.rel, f.err }
func (f *fakeSource) Download(_ context.Context, a Asset, w io.Writer) error {
	f.dlCalls = append(f.dlCalls, a.Name)
	b, ok := f.files[a.URL]
	if !ok {
		return errors.New("404")
	}
	_, err := w.Write(b)
	return err
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, c := range files {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(c)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(c))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func release(t *testing.T, version string, binary string) *fakeSource {
	t.Helper()
	arch := tarGz(t, map[string]string{"hanko_x/LICENSE": "mit", "hanko_x/hanko": binary})
	sum := sha256.Sum256(arch)
	name := ArchiveName(version, "linux", "amd64")
	sums := fmt.Sprintf("%s  %s\n%s  outro.tar.gz\n", hex.EncodeToString(sum[:]), name, strings.Repeat("0", 64))
	return &fakeSource{
		rel:   Release{Version: version, Assets: []Asset{{Name: name, URL: "u/arch"}, {Name: "checksums.txt", URL: "u/sums"}}},
		files: map[string][]byte{"u/arch": arch, "u/sums": []byte(sums)},
	}
}

func newUpdater(t *testing.T, cur string, src Source) (*Updater, string) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "hanko")
	if err := os.WriteFile(exe, []byte("binario-velho"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{Current: cur, Exe: exe, GOOS: "linux", GOARCH: "amd64", Source: src}, exe
}

func TestUpdateHappyPathBacksUpFirst(t *testing.T) {
	src := release(t, "0.5.0", "binario-novo")
	u, exe := newUpdater(t, "v0.4.0", src)
	var order []string
	u.Backup = func(context.Context) (string, error) {
		order = append(order, "backup")
		if len(src.dlCalls) != 0 {
			t.Error("o backup tem de vir antes de qualquer download")
		}
		return "hanko-20260101T000000Z.tar", nil
	}
	u.Restart = func(context.Context) error { order = append(order, "restart"); return nil }
	p, err := u.Check(context.Background(), ChannelStable)
	if err != nil || !p.Newer || p.Latest != "0.5.0" {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	res, err := u.Apply(context.Background(), p, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "binario-novo" {
		t.Fatalf("binário = %q", got)
	}
	if prev, _ := os.ReadFile(res.Previous); string(prev) != "binario-velho" {
		t.Fatalf("prev = %q", prev)
	}
	if st, _ := os.Stat(exe); st.Mode().Perm()&0o100 == 0 {
		t.Fatal("binário novo sem permissão de execução")
	}
	if strings.Join(order, ",") != "backup,restart" || res.Backup == "" || !res.Restart {
		t.Fatalf("order=%v res=%+v", order, res)
	}
	// rollback
	if err := u.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "binario-velho" {
		t.Fatalf("depois do rollback = %q", got)
	}
}

func TestUpdateAbortsWhenBackupFails(t *testing.T) {
	src := release(t, "0.5.0", "novo")
	u, exe := newUpdater(t, "v0.4.0", src)
	u.Backup = func(context.Context) (string, error) { return "", errors.New("pg_dump ausente") }
	p, _ := u.Check(context.Background(), ChannelStable)
	_, err := u.Apply(context.Background(), p, ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "nada foi alterado") {
		t.Fatalf("err = %v", err)
	}
	if len(src.dlCalls) != 0 {
		t.Fatalf("baixou apesar do backup falho: %v", src.dlCalls)
	}
	if got, _ := os.ReadFile(exe); string(got) != "binario-velho" {
		t.Fatal("binário mudou")
	}
}

func TestUpdateNeedsBackupFuncUnlessSkipped(t *testing.T) {
	src := release(t, "0.5.0", "novo")
	u, exe := newUpdater(t, "v0.4.0", src)
	p, _ := u.Check(context.Background(), ChannelStable)
	if _, err := u.Apply(context.Background(), p, ApplyOptions{}); err == nil {
		t.Fatal("sem backup configurado deveria recusar")
	}
	if _, err := u.Apply(context.Background(), p, ApplyOptions{SkipBackup: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "novo" {
		t.Fatal("não trocou")
	}
}

func TestUpdateRejectsBadChecksum(t *testing.T) {
	src := release(t, "0.5.0", "novo")
	src.files["u/arch"][len(src.files["u/arch"])/2] ^= 0xff
	u, exe := newUpdater(t, "v0.4.0", src)
	p, _ := u.Check(context.Background(), ChannelStable)
	_, err := u.Apply(context.Background(), p, ApplyOptions{SkipBackup: true})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "binario-velho" {
		t.Fatal("binário mudou com checksum ruim")
	}
	if _, err := os.Stat(exe + ".prev"); !os.IsNotExist(err) {
		t.Fatal("não deveria ter criado .prev")
	}
}

func TestUpdateRefusals(t *testing.T) {
	src := release(t, "0.5.0", "novo")
	t.Run("já na última", func(t *testing.T) {
		u, _ := newUpdater(t, "v0.5.0", src)
		p, _ := u.Check(context.Background(), ChannelStable)
		if p.Newer {
			t.Fatal("não é mais nova")
		}
		if _, err := u.Apply(context.Background(), p, ApplyOptions{SkipBackup: true}); err == nil {
			t.Fatal("esperava recusa")
		}
	})
	t.Run("binário dev", func(t *testing.T) {
		u, _ := newUpdater(t, "dev", src)
		p, _ := u.Check(context.Background(), ChannelStable)
		if _, err := u.Apply(context.Background(), p, ApplyOptions{SkipBackup: true}); err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v", err)
		}
		if _, err := u.Apply(context.Background(), p, ApplyOptions{SkipBackup: true, Force: true}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("gerenciado por contêiner", func(t *testing.T) {
		u, _ := newUpdater(t, "v0.4.0", src)
		u.Managed = "instalação em contêiner: atualize a imagem"
		p, _ := u.Check(context.Background(), ChannelStable)
		if _, err := u.Apply(context.Background(), p, ApplyOptions{SkipBackup: true, Force: true}); err == nil || !strings.Contains(err.Error(), "contêiner") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("canal inválido", func(t *testing.T) {
		u, _ := newUpdater(t, "v0.4.0", src)
		if _, err := u.Check(context.Background(), "nightly"); err == nil {
			t.Fatal("esperava erro")
		}
	})
}

func TestCheckRequiresPlatformArchiveAndChecksums(t *testing.T) {
	src := release(t, "0.5.0", "novo")
	u, _ := newUpdater(t, "v0.4.0", src)
	u.GOARCH = "riscv64"
	if _, err := u.Check(context.Background(), ChannelStable); err == nil || !strings.Contains(err.Error(), "esta plataforma") {
		t.Fatalf("err = %v", err)
	}
	u.GOARCH = "amd64"
	src.rel.Assets = src.rel.Assets[:1]
	if _, err := u.Check(context.Background(), ChannelStable); err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(p, tarGz(t, map[string]string{"README": "x"}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractBinary(p, filepath.Join(dir, "out")); err == nil {
		t.Fatal("esperava erro")
	}
	if err := os.WriteFile(p, []byte("não é gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractBinary(p, filepath.Join(dir, "out")); err == nil {
		t.Fatal("esperava erro")
	}
}

// ---------- GitHub contra httptest ----------

func ghServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "sem token", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = io.WriteString(w, `[
		 {"tag_name":"v0.6.0-rc.1","prerelease":true,"assets":[{"name":"checksums.txt","url":"X/a","size":1}]},
		 {"tag_name":"v0.9.0","draft":true},
		 {"tag_name":"v0.5.0","assets":[{"name":"checksums.txt","url":"X/a","size":1}]},
		 {"tag_name":"v0.4.2"},
		 {"tag_name":"nao-semver"}]`)
	})
	mux.HandleFunc("/assets/1", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, "accept", http.StatusNotAcceptable)
			return
		}
		_, _ = io.WriteString(w, "conteudo")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubPicksLatestPerChannel(t *testing.T) {
	srv := ghServer(t, "tok")
	g := GitHub{BaseURL: srv.URL, Repo: "o/r", Token: "tok"}
	stable, err := g.Latest(context.Background(), ChannelStable)
	if err != nil || stable.Version != "0.5.0" || stable.Prerelease {
		t.Fatalf("stable = %+v, %v", stable, err)
	}
	beta, err := g.Latest(context.Background(), ChannelBeta)
	if err != nil || beta.Version != "0.6.0-rc.1" || !beta.Prerelease {
		t.Fatalf("beta = %+v, %v", beta, err)
	}
}

func TestGitHubAuthAndDownload(t *testing.T) {
	srv := ghServer(t, "tok")
	if _, err := (GitHub{BaseURL: srv.URL, Repo: "o/r"}).Latest(context.Background(), ChannelStable); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("sem token: %v", err)
	}
	g := GitHub{BaseURL: srv.URL, Repo: "o/r", Token: "tok"}
	var buf bytes.Buffer
	if err := g.Download(context.Background(), Asset{Name: "x", URL: srv.URL + "/assets/1"}, &buf); err != nil || buf.String() != "conteudo" {
		t.Fatalf("download = %q, %v", buf.String(), err)
	}
	if err := g.Download(context.Background(), Asset{Name: "x", URL: srv.URL + "/assets/404"}, &buf); err == nil {
		t.Fatal("esperava erro 404")
	}
	if _, err := (GitHub{}).Latest(context.Background(), ChannelStable); err == nil {
		t.Fatal("sem repo deveria falhar")
	}
}
