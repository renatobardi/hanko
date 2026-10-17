package storage_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/storage/storagetest"
)

// Vetor publicado na documentação do SigV4 (GET Object com Range): confere o assinador contra um valor externo.
func TestSignV4PublishedVector(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	req.Header.Set("Range", "bytes=0-9")
	req.Header.Set("x-amz-content-sha256", empty)
	req.Header.Set("x-amz-date", "20130524T000000Z")
	storage.SignV4(req, empty, "us-east-1", "s3", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization =\n%s\nwant\n%s", got, want)
	}
}

func newS3(t *testing.T, mutate func(*storage.S3Config)) (*storage.S3, *storagetest.FakeS3) {
	t.Helper()
	f := storagetest.New(t, "AK", "SK/secret", "hanko-art", "us-east-1")
	cfg := storage.S3Config{Endpoint: f.URL(), Bucket: "hanko-art", AccessKey: "AK", SecretKey: "SK/secret", PathStyle: true, Prefix: "dev"}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := storage.NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestS3RoundTrip(t *testing.T) {
	s, f := newS3(t, nil)
	ctx := context.Background()
	k := storage.Key{CompanyID: domain.NewID(), ArtifactID: domain.NewID()}
	data := bytes.Repeat([]byte("conteúdo do artefato\n"), 1000)

	if _, err := s.Get(ctx, k); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get antes do Put: %v", err)
	}
	if err := s.Put(ctx, k, data, "text/plain"); err != nil {
		t.Fatal(err)
	}
	if want := "dev/" + k.CompanyID.String() + "/" + k.ArtifactID.String(); len(f.Keys()) != 1 || f.Keys()[0] != want {
		t.Fatalf("chaves = %v, want [%s]", f.Keys(), want)
	}
	if got := f.ContentType(f.Keys()[0]); got != "text/plain" {
		t.Fatalf("content-type = %q", got)
	}
	got, err := s.Get(ctx, k)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Get = %d bytes, %v", len(got), err)
	}
	n, err := s.Stat(ctx, k)
	if err != nil || n != int64(len(data)) {
		t.Fatalf("Stat = %d, %v", n, err)
	}
	if err := s.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, k); err != nil {
		t.Fatalf("apagar duas vezes deve ser idempotente: %v", err)
	}
	if _, err := s.Stat(ctx, k); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat depois de apagar: %v", err)
	}
	if s.Transactional() || s.Name() != storage.DriverS3 {
		t.Fatal("o driver s3 não é transacional")
	}
}

func TestS3WrongCredentialsAndSkew(t *testing.T) {
	s, _ := newS3(t, func(c *storage.S3Config) { c.SecretKey = "outra-chave" })
	k := storage.Key{CompanyID: domain.NewID(), ArtifactID: domain.NewID()}
	err := s.Put(context.Background(), k, []byte("x"), "")
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("segredo errado: %v", err)
	}
	if strings.Contains(err.Error(), "outra-chave") {
		t.Fatal("o erro não pode carregar a chave")
	}
	skew, _ := newS3(t, func(c *storage.S3Config) { c.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) } })
	if err := skew.Put(context.Background(), k, []byte("x"), ""); err == nil || !strings.Contains(err.Error(), "RequestTimeTooSkewed") {
		t.Fatalf("relógio: %v", err)
	}
}

func TestS3VirtualHostedURL(t *testing.T) {
	s, err := storage.NewS3(storage.S3Config{Endpoint: "https://s3.exemplo.com", Bucket: "meu-bucket", AccessKey: "a", SecretKey: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.ObjectKey(storage.Key{CompanyID: domain.NewID(), ArtifactID: domain.NewID()}); strings.HasPrefix(got, "/") {
		t.Fatalf("chave começa com /: %q", got)
	}
}

func TestNewS3Validation(t *testing.T) {
	ok := storage.S3Config{Endpoint: "https://s3.exemplo.com", Bucket: "meu-bucket", AccessKey: "a", SecretKey: "b"}
	cases := map[string]func(*storage.S3Config){
		"endpoint vazio":     func(c *storage.S3Config) { c.Endpoint = "" },
		"http fora loopback": func(c *storage.S3Config) { c.Endpoint = "http://s3.exemplo.com" },
		"com usuário":        func(c *storage.S3Config) { c.Endpoint = "https://u:p@s3.exemplo.com" },
		"bucket inválido":    func(c *storage.S3Config) { c.Bucket = "Meu_Bucket" },
		"sem chaves":         func(c *storage.S3Config) { c.AccessKey = "" },
		"prefixo inválido":   func(c *storage.S3Config) { c.Prefix = "../x" },
		"região inválida":    func(c *storage.S3Config) { c.Region = "US EAST" },
	}
	for name, mut := range cases {
		cfg := ok
		mut(&cfg)
		if _, err := storage.NewS3(cfg); err == nil {
			t.Errorf("%s: deveria falhar", name)
		}
	}
	if _, err := storage.NewS3(ok); err != nil {
		t.Fatal(err)
	}
	loop := ok
	loop.Endpoint = "http://127.0.0.1:9000"
	if _, err := storage.NewS3(loop); err != nil {
		t.Fatalf("http em loopback é permitido: %v", err)
	}
}

func TestRegistry(t *testing.T) {
	s, _ := newS3(t, nil)
	r := storage.NewRegistry(s)
	if r.Active().Name() != storage.DriverS3 {
		t.Fatal("o ativo deveria ser s3")
	}
	if _, err := r.Driver(storage.DriverPostgres); err == nil {
		t.Fatal("postgres não foi registrado")
	}
	d, err := r.Driver(storage.DriverS3)
	if err != nil {
		t.Fatal(err)
	}
	k := storage.Key{CompanyID: domain.NewID(), ArtifactID: domain.NewID()}
	if err := d.Put(context.Background(), k, []byte("abc"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if b, err := d.Get(context.Background(), k); err != nil || string(b) != "abc" {
		t.Fatalf("Get pelo registro = %q, %v", b, err)
	}
}
