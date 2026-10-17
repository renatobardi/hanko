package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Cliente S3 mínimo (ADR-0051): PutObject, GetObject, DeleteObject e HeadObject com assinatura SigV4 própria, só
// stdlib. Serve qualquer endpoint compatível (S3, MinIO, R2).

const (
	s3Timeout = 60 * time.Second
	// MaxS3Object é o maior objeto que o driver lê para a memória (o limite de arquivo de artefato é menor).
	MaxS3Object = 128 << 20
	sigAlgo     = "AWS4-HMAC-SHA256"
	emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

var (
	bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	prefixRe = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
	regionRe = regexp.MustCompile(`^[a-z0-9-]{2,40}$`)
)

// S3Config configura o driver s3. As credenciais vêm do ambiente do servidor (HANKO_S3_*), nunca do banco.
type S3Config struct {
	Endpoint     string // https://s3.exemplo:9000 (http só em loopback)
	Region       string // padrão us-east-1
	Bucket       string
	AccessKey    string
	SecretKey    string
	SessionToken string // opcional
	Prefix       string // opcional; prefixo das chaves
	// PathStyle usa https://endpoint/bucket/chave (padrão dos compatíveis); falso usa https://bucket.endpoint/chave.
	PathStyle bool
	// HTTP substitui o cliente (testes).
	HTTP *http.Client
	// Now é o relógio da assinatura (testes).
	Now func() time.Time
}

// S3 é o driver de armazenamento de objetos.
type S3 struct {
	cfg    S3Config
	base   *url.URL
	client *http.Client
	now    func() time.Time
}

// NewS3 valida a configuração e monta o driver.
func NewS3(cfg S3Config) (*S3, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("s3: HANKO_S3_ENDPOINT inválido: use https://host[:porta]")
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return nil, errors.New("s3: HANKO_S3_ENDPOINT deve ser https fora de loopback")
	}
	if cfg.Region = strings.TrimSpace(cfg.Region); cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if !regionRe.MatchString(cfg.Region) {
		return nil, errors.New("s3: HANKO_S3_REGION inválida")
	}
	if !bucketRe.MatchString(cfg.Bucket) {
		return nil, errors.New("s3: HANKO_S3_BUCKET inválido")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3: HANKO_S3_ACCESS_KEY e HANKO_S3_SECRET_KEY são obrigatórios")
	}
	cfg.Prefix = strings.Trim(cfg.Prefix, "/")
	if !prefixRe.MatchString(cfg.Prefix) || strings.Contains(cfg.Prefix, "..") {
		return nil, errors.New("s3: HANKO_S3_PREFIX inválido (use letras, números, . _ - e /)")
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: s3Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &S3{cfg: cfg, base: u, client: hc, now: now}, nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Name implementa BlobStore.
func (s *S3) Name() string { return DriverS3 }

// Transactional implementa BlobStore: o objeto sobe antes da transação.
func (s *S3) Transactional() bool { return false }

// ObjectKey é a chave do objeto no bucket: <prefixo>/<empresa>/<artefato>.
func (s *S3) ObjectKey(k Key) string {
	key := k.CompanyID.String() + "/" + k.ArtifactID.String()
	if s.cfg.Prefix != "" {
		key = s.cfg.Prefix + "/" + key
	}
	return key
}

// objectURL monta a URL e o Host (path-style ou virtual-hosted) do objeto.
func (s *S3) objectURL(key string) *url.URL {
	u := *s.base
	if s.cfg.PathStyle {
		u.Path = strings.TrimRight(s.base.Path, "/") + "/" + s.cfg.Bucket + "/" + key
	} else {
		u.Host = s.cfg.Bucket + "." + s.base.Host
		u.Path = strings.TrimRight(s.base.Path, "/") + "/" + key
	}
	u.RawPath = encodePath(u.Path)
	return &u
}

func (s *S3) do(ctx context.Context, method string, k Key, body []byte, contentType string) (*http.Response, error) {
	u := s.objectURL(s.ObjectKey(k))
	var rdr io.Reader = http.NoBody
	payload := emptySHA256
	if body != nil {
		rdr = bytes.NewReader(body)
		sum := sha256.Sum256(body)
		payload = hex.EncodeToString(sum[:])
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	if body != nil {
		req.ContentLength = int64(len(body))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("x-amz-content-sha256", payload)
	req.Header.Set("x-amz-date", s.now().UTC().Format("20060102T150405Z"))
	if s.cfg.SessionToken != "" {
		req.Header.Set("x-amz-security-token", s.cfg.SessionToken)
	}
	SignV4(req, payload, s.cfg.Region, "s3", s.cfg.AccessKey, s.cfg.SecretKey)
	resp, err := s.client.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, errors.New("s3: tempo esgotado")
		}
		return nil, errors.New("s3: endpoint inacessível")
	}
	return resp, nil
}

func s3Err(op string, resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := ""
	if code := between(string(b), "<Code>", "</Code>"); code != "" && len(code) < 80 {
		msg = ": " + code
	}
	return fmt.Errorf("s3: %s: HTTP %d%s", op, resp.StatusCode, msg)
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// Put implementa BlobStore.
func (s *S3) Put(ctx context.Context, k Key, data []byte, contentType string) error {
	if data == nil {
		data = []byte{}
	}
	resp, err := s.do(ctx, http.MethodPut, k, data, contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return s3Err("gravar", resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

// Get implementa BlobStore.
func (s *S3) Get(ctx context.Context, k Key) ([]byte, error) {
	resp, err := s.do(ctx, http.MethodGet, k, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, s3Err("ler", resp)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxS3Object+1))
	if err != nil {
		return nil, errors.New("s3: leitura interrompida")
	}
	if len(b) > MaxS3Object {
		return nil, errors.New("s3: objeto maior que o limite do driver")
	}
	return b, nil
}

// Delete implementa BlobStore (idempotente).
func (s *S3) Delete(ctx context.Context, k Key) error {
	resp, err := s.do(ctx, http.MethodDelete, k, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return s3Err("apagar", resp)
	}
	return nil
}

// Stat implementa BlobStore (HEAD).
func (s *S3) Stat(ctx context.Context, k Key) (int64, error) {
	resp, err := s.do(ctx, http.MethodHead, k, nil, "")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return 0, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return 0, s3Err("consultar", resp)
	}
	n, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		return 0, errors.New("s3: resposta sem Content-Length")
	}
	return n, nil
}

// ---------- SigV4 ----------

// SignV4 assina req (cabeçalho Authorization) com AWS Signature V4. A requisição já deve ter x-amz-date e
// x-amz-content-sha256. Todos os cabeçalhos presentes entram na assinatura, exceto os que o transporte altera.
func SignV4(req *http.Request, payloadHash, region, service, accessKey, secretKey string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string]string{"host": host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		switch lk {
		case "authorization", "user-agent", "accept-encoding", "connection", "content-length", "expect":
			continue
		}
		vals := make([]string, len(v))
		for i, x := range v {
			vals[i] = strings.Join(strings.Fields(x), " ")
		}
		headers[lk] = strings.Join(vals, ",")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canon strings.Builder
	for _, n := range names {
		canon.WriteString(n + ":" + headers[n] + "\n")
	}
	signed := strings.Join(names, ";")
	date := req.Header.Get("x-amz-date")
	scope := date[:8] + "/" + region + "/" + service + "/aws4_request"
	creq := strings.Join([]string{req.Method, canonicalURI(req.URL), canonicalQuery(req.URL), canon.String(), signed, payloadHash}, "\n")
	h := sha256.Sum256([]byte(creq))
	sts := sigAlgo + "\n" + date + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	sig := hex.EncodeToString(hmacSHA256(signingKey(secretKey, date[:8], region, service), sts))
	req.Header.Set("Authorization", sigAlgo+" Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func signingKey(secret, day, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), day)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, "aws4_request")
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

func canonicalURI(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}

func canonicalQuery(u *url.URL) string {
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// encodePath codifica cada segmento do caminho pela regra do SigV4 (barra preservada).
func encodePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = awsEscape(s)
	}
	return strings.Join(segs, "/")
}

// awsEscape codifica tudo menos A-Z a-z 0-9 - _ . ~ (RFC 3986), em maiúsculas.
func awsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
