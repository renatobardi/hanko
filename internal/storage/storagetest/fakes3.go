// Package storagetest oferece um S3 falso (httptest) que confere a assinatura SigV4 de cada requisição. Só testes.
package storagetest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// FakeS3 guarda objetos em memória e rejeita requisição com assinatura, hash de corpo ou relógio inválidos.
type FakeS3 struct {
	Server *httptest.Server

	access, secret, bucket, region string

	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	// FailPut e FailDelete, quando verdadeiros, fazem as operações responderem 500 (testes de compensação).
	failPut, failDelete bool
	calls               []string
}

// New sobe o servidor falso para o bucket (endereçamento por caminho).
func New(t testing.TB, access, secret, bucket, region string) *FakeS3 {
	t.Helper()
	f := &FakeS3{access: access, secret: secret, bucket: bucket, region: region, objects: map[string][]byte{}, types: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Server.Close)
	return f
}

// URL é o endpoint.
func (f *FakeS3) URL() string { return f.Server.URL }

// Keys lista as chaves guardadas, em ordem.
func (f *FakeS3) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.objects))
	for k := range f.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Object devolve o conteúdo de uma chave.
func (f *FakeS3) Object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	return b, ok
}

// ContentType devolve o Content-Type com que a chave foi gravada.
func (f *FakeS3) ContentType(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.types[key]
}

// Calls lista as chamadas recebidas ("PUT key", "GET key"...).
func (f *FakeS3) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// SetFailures liga ou desliga as falhas injetadas de gravação e exclusão.
func (f *FakeS3) SetFailures(put, del bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPut, f.failDelete = put, del
}

func (f *FakeS3) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if code := f.verify(r, body); code != "" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>" + code + "</Code></Error>"))
		return
	}
	prefix := "/" + f.bucket + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<Error><Code>NoSuchBucket</Code></Error>"))
		return
	}
	key := strings.TrimPrefix(r.URL.Path, prefix)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+key)
	switch r.Method {
	case http.MethodPut:
		if f.failPut {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.objects[key] = body
		f.types[key] = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	case http.MethodGet, http.MethodHead:
		b, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("<Error><Code>NoSuchKey</Code></Error>"))
			return
		}
		w.Header().Set("Content-Length", itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(b)
		}
	case http.MethodDelete:
		if f.failDelete {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// verify refaz a assinatura a partir do que chegou. Devolve o código de erro S3 ou "" se está tudo certo.
func (f *FakeS3) verify(r *http.Request, body []byte) string {
	auth := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(auth, "AWS4-HMAC-SHA256 ")
	if !ok {
		return "AccessDenied"
	}
	fields := map[string]string{}
	for _, p := range strings.Split(rest, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		fields[k] = v
	}
	cred := strings.Split(fields["Credential"], "/")
	if len(cred) != 5 || cred[0] != f.access || cred[2] != f.region || cred[3] != "s3" {
		return "InvalidAccessKeyId"
	}
	date := r.Header.Get("x-amz-date")
	t, err := time.Parse("20060102T150405Z", date)
	if err != nil || time.Since(t) > 15*time.Minute || time.Until(t) > 15*time.Minute || date[:8] != cred[1] {
		return "RequestTimeTooSkewed"
	}
	sum := sha256.Sum256(body)
	if r.Header.Get("x-amz-content-sha256") != hex.EncodeToString(sum[:]) {
		return "XAmzContentSHA256Mismatch"
	}
	signed := strings.Split(fields["SignedHeaders"], ";")
	var canon strings.Builder
	for _, h := range signed {
		v := r.Header.Get(h)
		if h == "host" {
			v = r.Host
		}
		canon.WriteString(h + ":" + strings.Join(strings.Fields(v), " ") + "\n")
	}
	creq := strings.Join([]string{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, canon.String(), fields["SignedHeaders"], r.Header.Get("x-amz-content-sha256")}, "\n")
	h := sha256.Sum256([]byte(creq))
	scope := strings.Join(cred[1:], "/")
	sts := "AWS4-HMAC-SHA256\n" + date + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	k := mac([]byte("AWS4"+f.secret), cred[1])
	k = mac(k, cred[2])
	k = mac(k, cred[3])
	k = mac(k, "aws4_request")
	if hex.EncodeToString(mac(k, sts)) != fields["Signature"] {
		return "SignatureDoesNotMatch"
	}
	return ""
}

func mac(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}
