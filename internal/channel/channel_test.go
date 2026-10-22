package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

const tgToken = "123456:SEGREDO-DO-BOT"

// tgServer é uma Bot API falsa: guarda as chamadas e responde conforme o método.
type tgServer struct {
	mu     sync.Mutex
	calls  []tgCall
	status map[string]int // método → status forçado
	srv    *httptest.Server
}

type tgCall struct {
	Method string
	Body   map[string]any
	Raw    []byte
	CT     string
}

func newTG(t *testing.T) *tgServer {
	t.Helper()
	f := &tgServer{status: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/file/bot"+tgToken+"/") {
			f.mu.Lock()
			f.calls = append(f.calls, tgCall{Method: "download:" + r.URL.Path})
			f.mu.Unlock()
			_, _ = w.Write([]byte("conteúdo do arquivo"))
			return
		}
		prefix := "/bot" + tgToken + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.Error(w, "token errado", http.StatusUnauthorized)
			return
		}
		m := strings.TrimPrefix(r.URL.Path, prefix)
		c := tgCall{Method: m, Raw: b, CT: r.Header.Get("Content-Type")}
		if strings.HasPrefix(c.CT, "application/json") {
			_ = json.Unmarshal(b, &c.Body)
		}
		f.mu.Lock()
		f.calls = append(f.calls, c)
		forced := f.status[m]
		f.mu.Unlock()
		if forced != 0 {
			w.WriteHeader(forced)
			_, _ = w.Write([]byte(`{"ok":false,"description":"falha com ` + tgToken + `"}`))
			return
		}
		switch m {
		case "getFile":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"docs/a b.txt","file_size":20}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77}}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *tgServer) client() Client {
	return Client{HTTP: f.srv.Client(), Token: tgToken, Config: domain.ChannelConfig{APIBase: f.srv.URL}}
}

func (f *tgServer) last() tgCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func (f *tgServer) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func TestTelegramVerify(t *testing.T) {
	d := Telegram{}
	h := http.Header{}
	h.Set("X-Telegram-Bot-Api-Secret-Token", "s3cret")
	if err := d.Verify(h, nil, "s3cret"); err != nil {
		t.Error(err)
	}
	if !errors.Is(d.Verify(h, nil, "outro"), ErrUnauthorized) || !errors.Is(d.Verify(http.Header{}, nil, "s3cret"), ErrUnauthorized) ||
		!errors.Is(d.Verify(h, nil, ""), ErrUnauthorized) {
		t.Error("Verify aceitou o que não devia")
	}
}

func TestTelegramParse(t *testing.T) {
	d := Telegram{}
	parse := func(body string) Inbound {
		t.Helper()
		in, err := d.Parse(nil, []byte(body), domain.ChannelConfig{})
		if err != nil || len(in) != 1 {
			t.Fatalf("parse %s: %v %v", body, in, err)
		}
		return in[0]
	}
	msg := func(chat, text string) string {
		return `{"update_id":10,"message":{"message_id":1,"from":{"id":42,"username":"ana"},"chat":{"id":42,"type":"` + chat + `"},` + text + `}}`
	}
	if in := parse(msg("private", `"text":"olá"`)); in.Kind != InText || in.Text != "olá" || in.Peer != "42" || in.Sender != "42" || in.SenderLabel != "@ana" || in.ExternalID != "10" {
		t.Errorf("texto: %+v", in)
	}
	if in := parse(msg("private", `"text":"/vincular@meu_bot  ab12-cd34 "`)); in.Kind != InCommand || in.Command != "vincular" || in.Arg != "ab12-cd34" {
		t.Errorf("comando: %+v", in)
	}
	if in := parse(msg("private", `"text":"/AJUDA"`)); in.Kind != InCommand || in.Command != "ajuda" || in.Arg != "" {
		t.Errorf("comando sem arg: %+v", in)
	}
	in := parse(msg("private", `"caption":"veja","document":{"file_id":"D1","file_name":"a.pdf","mime_type":"application/pdf","file_size":9},"photo":[{"file_id":"P1","width":10,"height":10,"file_size":1},{"file_id":"P2","width":90,"height":80,"file_size":5}]`))
	if in.Kind != InText || in.Text != "veja" || len(in.Attachments) != 2 || in.Attachments[0].Ref != "D1" || in.Attachments[1].Ref != "P2" || in.Attachments[1].Mime != "image/jpeg" {
		t.Errorf("anexos: %+v", in)
	}
	if in := parse(msg("private", `"document":{"file_id":"D2"}`)); len(in.Attachments) != 1 || in.Attachments[0].Name != "documento-10" {
		t.Errorf("documento sem nome: %+v", in)
	}
	for name, body := range map[string]string{
		"grupo":      msg("group", `"text":"oi"`),
		"vazio":      msg("private", `"entities":[]`),
		"bot":        `{"update_id":1,"message":{"from":{"id":9,"is_bot":true},"chat":{"id":9,"type":"private"},"text":"x"}}`,
		"sem from":   `{"update_id":1,"message":{"chat":{"id":9,"type":"private"},"text":"x"}}`,
		"outro tipo": `{"update_id":1,"my_chat_member":{}}`,
		"botão fora": `{"update_id":1,"callback_query":{"id":"c","from":{"id":1},"data":"x"}}`,
	} {
		if in := parse(body); in.Kind != InIgnore || in.Reason == "" {
			t.Errorf("%s: %+v", name, in)
		}
	}
	cb := parse(`{"update_id":5,"callback_query":{"id":"cb1","from":{"id":42,"first_name":"Ana"},"message":{"chat":{"id":42,"type":"private"}},"data":"g|abc|a"}}`)
	if cb.Kind != InAction || cb.Action != "g|abc|a" || cb.CallbackID != "cb1" || cb.Sender != "42" || cb.Peer != "42" || cb.SenderLabel != "Ana" {
		t.Errorf("callback: %+v", cb)
	}
	if _, err := d.Parse(nil, []byte(`{`), domain.ChannelConfig{}); err == nil {
		t.Error("json inválido aceito")
	}
}

func TestTelegramSend(t *testing.T) {
	ctx := context.Background()
	f := newTG(t)
	d := Telegram{}
	id, err := d.Send(ctx, f.client(), Outbound{Peer: "42", Text: "oi", Buttons: [][]Button{{{Text: "Aprovar", Data: "g|x|a"}, {Text: "Abrir", URL: "https://h/x"}}}})
	if err != nil || id != "77" {
		t.Fatalf("send: %q %v", id, err)
	}
	c := f.last()
	kb := c.Body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)
	if c.Method != "sendMessage" || c.Body["chat_id"] != "42" || c.Body["text"] != "oi" || len(kb) != 2 || kb[0].(map[string]any)["callback_data"] != "g|x|a" || kb[1].(map[string]any)["url"] != "https://h/x" {
		t.Errorf("sendMessage: %+v", c)
	}
	if _, ok := c.Body["parse_mode"]; ok {
		t.Error("parse_mode definido: o texto deve ir sem formatação")
	}

	// texto longo: pedaços, botões só no último
	long := strings.Repeat("linha de texto\n", 600)
	before := f.count("sendMessage")
	if _, err := d.Send(ctx, f.client(), Outbound{Peer: "42", Text: long, Buttons: [][]Button{{{Text: "ok", Data: "k"}}}}); err != nil {
		t.Fatal(err)
	}
	n := f.count("sendMessage") - before
	if n < 2 {
		t.Fatalf("pedaços = %d", n)
	}
	if _, has := f.last().Body["reply_markup"]; !has {
		t.Error("o último pedaço perdeu os botões")
	}
	for _, p := range splitText(long) {
		if len([]rune(p)) > telegramMaxText {
			t.Fatalf("pedaço com %d caracteres", len([]rune(p)))
		}
	}
	if got := splitText("curto"); len(got) != 1 || got[0] != "curto" {
		t.Errorf("split curto: %v", got)
	}

	// documento
	if _, err := d.Send(ctx, f.client(), Outbound{Peer: "42", Documents: []Document{{Name: "r\"el.txt", Mime: "text/plain", Content: []byte("dados")}}}); err != nil {
		t.Fatal(err)
	}
	c = f.last()
	if c.Method != "sendDocument" || !strings.HasPrefix(c.CT, "multipart/form-data") || !strings.Contains(string(c.Raw), "dados") || !strings.Contains(string(c.Raw), `name="chat_id"`) {
		t.Errorf("sendDocument: %s %s", c.CT, c.Raw)
	}

	// destino e botão inválidos, mensagem vazia
	for name, out := range map[string]Outbound{
		"peer":   {Peer: "abc", Text: "x"},
		"botão":  {Peer: "42", Text: "x", Buttons: [][]Button{{{Text: "b", Data: strings.Repeat("d", 65)}}}},
		"vazio":  {Peer: "42"},
		"botão2": {Peer: "42", Text: "x", Buttons: [][]Button{{{Text: "b"}}}},
	} {
		if _, err := d.Send(ctx, f.client(), out); !IsPermanent(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTelegramErrors(t *testing.T) {
	ctx := context.Background()
	f := newTG(t)
	d := Telegram{}
	for status, permanent := range map[int]bool{400: true, 403: true, 404: true, 429: false, 500: false, 502: false} {
		f.mu.Lock()
		f.status["sendMessage"] = status
		f.mu.Unlock()
		_, err := d.Send(ctx, f.client(), Outbound{Peer: "42", Text: "x"})
		if err == nil || IsPermanent(err) != permanent {
			t.Errorf("status %d: err=%v permanent=%v, quer %v", status, err, IsPermanent(err), permanent)
		}
		if err != nil && strings.Contains(err.Error(), tgToken) {
			t.Errorf("status %d: o erro vaza o token: %v", status, err)
		}
	}
	// rede fora: o erro não traz a URL (que carrega o token)
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	_, err := d.Send(ctx, Client{HTTP: http.DefaultClient, Token: tgToken, Config: domain.ChannelConfig{APIBase: url}}, Outbound{Peer: "42", Text: "x"})
	if err == nil || IsPermanent(err) || strings.Contains(err.Error(), tgToken) || strings.Contains(err.Error(), url) {
		t.Errorf("rede fora: %v", err)
	}
	// contexto cancelado
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Send(cctx, f.client(), Outbound{Peer: "42", Text: "x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("contexto cancelado: %v", err)
	}
}

func TestTelegramFetchRegisterAck(t *testing.T) {
	ctx := context.Background()
	f := newTG(t)
	d := Telegram{}
	b, err := d.Fetch(ctx, f.client(), "FILE1", 1<<20)
	if err != nil || string(b) != "conteúdo do arquivo" {
		t.Fatalf("fetch: %q %v", b, err)
	}
	if got := f.last().Method; got != "download:/file/bot"+tgToken+"/docs/a%20b.txt" && got != "download:/file/bot"+tgToken+"/docs/a b.txt" {
		t.Errorf("download: %s", got)
	}
	if _, err := d.Fetch(ctx, f.client(), "FILE1", 5); !IsPermanent(err) {
		t.Errorf("limite de tamanho: %v", err)
	}
	if err := d.Register(ctx, f.client(), "https://h/hooks/channels/tok", "sec"); err != nil {
		t.Fatal(err)
	}
	c := f.last()
	if c.Method != "setWebhook" || c.Body["url"] != "https://h/hooks/channels/tok" || c.Body["secret_token"] != "sec" {
		t.Errorf("setWebhook: %+v", c.Body)
	}
	if err := d.Ack(ctx, f.client(), "cb1", "feito"); err != nil || f.last().Method != "answerCallbackQuery" || f.last().Body["callback_query_id"] != "cb1" {
		t.Errorf("ack: %v %+v", err, f.last())
	}
	n := f.count("answerCallbackQuery")
	if err := d.Ack(ctx, f.client(), "", "x"); err != nil || f.count("answerCallbackQuery") != n {
		t.Error("ack sem id deveria ser no-op")
	}
	if d.Kind() != domain.ChannelTelegram {
		t.Error("kind")
	}
}

// ---- GitHub ----

const ghToken = "ghp_SEGREDO"

func ghSign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestGitHubVerify(t *testing.T) {
	d := GitHub{}
	body := []byte(`{"a":1}`)
	h := http.Header{}
	h.Set("X-Hub-Signature-256", ghSign("whsec", body))
	if err := d.Verify(h, body, "whsec"); err != nil {
		t.Error(err)
	}
	if !errors.Is(d.Verify(h, body, "outro"), ErrUnauthorized) || !errors.Is(d.Verify(h, []byte(`{"a":2}`), "whsec"), ErrUnauthorized) ||
		!errors.Is(d.Verify(http.Header{}, body, "whsec"), ErrUnauthorized) || !errors.Is(d.Verify(h, body, ""), ErrUnauthorized) {
		t.Error("Verify aceitou o que não devia")
	}
}

func ghHeaders(event, delivery string) http.Header {
	h := http.Header{}
	h.Set("X-GitHub-Event", event)
	h.Set("X-GitHub-Delivery", delivery)
	return h
}

var ghCfg = domain.ChannelConfig{Repos: []string{"acme/api", "kubo/*"}, Mention: "@hanko"}

func ghIssueComment(body, login, typ, repo string, pr bool) string {
	prField := ""
	if pr {
		prField = `,"pull_request":{"url":"x"}`
	}
	return `{"action":"created","issue":{"number":7,"title":"Corrige X","html_url":"https://github.com/` + repo + `/pull/7"` + prField + `},` +
		`"comment":{"id":99,"body":` + jsonStr(body) + `,"html_url":"https://github.com/` + repo + `/pull/7#c99","user":{"login":"` + login + `","type":"` + typ + `"}},` +
		`"repository":{"full_name":"` + repo + `"}}`
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestGitHubParse(t *testing.T) {
	d := GitHub{}
	one := func(event, delivery, body string) Inbound {
		t.Helper()
		in, err := d.Parse(ghHeaders(event, delivery), []byte(body), ghCfg)
		if err != nil || len(in) != 1 {
			t.Fatalf("parse: %v %v", in, err)
		}
		return in[0]
	}
	in := one("issue_comment", "D1", ghIssueComment("@hanko revise isto, por favor", "Ana-Dev", "User", "acme/api", true))
	if in.Kind != InText || in.Peer != "acme/api#7" || in.Sender != "ana-dev" || in.SenderLabel != "Ana-Dev" || in.ExternalID != "D1" ||
		in.Meta["is_pr"] != "true" || in.Meta["title"] != "Corrige X" || in.Meta["comment_url"] == "" || !strings.Contains(in.Text, "revise isto") {
		t.Errorf("issue_comment: %+v", in)
	}
	if in := one("issue_comment", "D2", ghIssueComment("oi @HANKO!", "ana", "User", "kubo/web", false)); in.Kind != InText || in.Meta["is_pr"] != "false" {
		t.Errorf("menção em outra caixa / issue: %+v", in)
	}
	if in := one("issue_comment", "D3", ghIssueComment("/hanko resuma o diff", "ana", "User", "acme/api", true)); in.Kind != InText || in.Text != "resuma o diff" {
		t.Errorf("/hanko texto: %+v", in)
	}
	if in := one("issue_comment", "D4", ghIssueComment("/hanko vincular AB12-CD34", "ana", "User", "acme/api", true)); in.Kind != InCommand || in.Command != "vincular" || in.Arg != "AB12-CD34" {
		t.Errorf("/hanko vincular: %+v", in)
	}
	for name, body := range map[string]string{
		"sem menção":      ghIssueComment("só um comentário", "ana", "User", "acme/api", true),
		"e-mail":          ghIssueComment("fale com x@hanko.dev", "ana", "User", "acme/api", true),
		"outro nome":      ghIssueComment("@hanko-bot olhe", "ana", "User", "acme/api", true),
		"bot":             ghIssueComment("@hanko", "dependabot", "Bot", "acme/api", true),
		"bot pelo nome":   ghIssueComment("@hanko", "x[bot]", "User", "acme/api", true),
		"repo fora":       ghIssueComment("@hanko", "ana", "User", "outro/api", true),
		"/hanko vazio":    ghIssueComment("/hanko", "ana", "User", "acme/api", true),
		"/hankox":         ghIssueComment("/hankox algo", "ana", "User", "acme/api", true),
		"sem autor":       ghIssueComment("@hanko", "", "User", "acme/api", true),
		"/hanko e menção": ghIssueComment("/hanko", "ana", "User", "acme/api", true),
	} {
		got := one("issue_comment", "X", body)
		if got.Kind != InIgnore || got.Reason == "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	rc := one("pull_request_review_comment", "D5", `{"action":"created","pull_request":{"number":3,"title":"PR","html_url":"u"},"comment":{"id":1,"body":"@hanko aqui","html_url":"c","user":{"login":"Bia","type":"User"}},"repository":{"full_name":"acme/api"}}`)
	if rc.Kind != InText || rc.Peer != "acme/api#3" || rc.Meta["is_pr"] != "true" {
		t.Errorf("review_comment: %+v", rc)
	}
	rv := one("pull_request_review", "D6", `{"action":"submitted","pull_request":{"number":3,"title":"PR","html_url":"u"},"review":{"id":1,"body":"@hanko pode revisar?","html_url":"c","user":{"login":"Bia","type":"User"}},"repository":{"full_name":"acme/api"}}`)
	if rv.Kind != InText || rv.Peer != "acme/api#3" {
		t.Errorf("review: %+v", rv)
	}
	for name, ev := range map[string][2]string{
		"ping":        {"ping", `{"zen":"x"}`},
		"push":        {"push", `{"ref":"x"}`},
		"editado":     {"issue_comment", `{"action":"edited","issue":{"number":1},"comment":{"body":"@hanko"},"repository":{"full_name":"acme/api"}}`},
		"review-aber": {"pull_request_review", `{"action":"dismissed","pull_request":{"number":1},"review":{"body":"@hanko"},"repository":{"full_name":"acme/api"}}`},
	} {
		if got := one(ev[0], "E", ev[1]); got.Kind != InIgnore {
			t.Errorf("%s: %+v", name, got)
		}
	}
	if _, err := d.Parse(ghHeaders("issue_comment", "x"), []byte(`{`), ghCfg); err == nil {
		t.Error("json inválido aceito")
	}
	// sem delivery o id é derivado do corpo (estável)
	a, _ := d.Parse(http.Header{"X-Github-Event": {"ping"}}, []byte(`{"a":1}`), ghCfg)
	b, _ := d.Parse(http.Header{"X-Github-Event": {"ping"}}, []byte(`{"a":1}`), ghCfg)
	if a[0].ExternalID == "" || a[0].ExternalID != b[0].ExternalID {
		t.Errorf("id derivado: %q %q", a[0].ExternalID, b[0].ExternalID)
	}
	// menção personalizada
	cfg := ghCfg
	cfg.Mention = "@ada"
	in2, _ := d.Parse(ghHeaders("issue_comment", "M"), []byte(ghIssueComment("@ada olhe", "ana", "User", "acme/api", true)), cfg)
	if in2[0].Kind != InText {
		t.Errorf("menção personalizada: %+v", in2[0])
	}
}

type ghServer struct {
	mu     sync.Mutex
	method string
	path   string
	auth   string
	body   map[string]any
	status int
	reply  string
	srv    *httptest.Server
}

func newGH(t *testing.T) *ghServer {
	t.Helper()
	f := &ghServer{status: http.StatusCreated, reply: `{"id":555}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.method, f.path, f.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		f.body = nil
		_ = json.Unmarshal(b, &f.body)
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.reply))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *ghServer) client() Client {
	cfg := ghCfg
	cfg.APIBase = f.srv.URL
	return Client{HTTP: f.srv.Client(), Token: ghToken, Config: cfg}
}

func TestGitHubSend(t *testing.T) {
	ctx := context.Background()
	f := newGH(t)
	d := GitHub{}
	id, err := d.Send(ctx, f.client(), Outbound{Peer: "acme/api#7", Text: "Revisei: tudo certo."})
	if err != nil || id != "555" {
		t.Fatalf("comentário: %q %v", id, err)
	}
	if f.method != "POST" || f.path != "/repos/acme/api/issues/7/comments" || f.auth != "Bearer "+ghToken || f.body["body"] != "Revisei: tudo certo." {
		t.Errorf("comentário: %s %s %s %v", f.method, f.path, f.auth, f.body)
	}
	if _, err := d.Send(ctx, f.client(), Outbound{Peer: "acme/api#7", Text: "faltam testes", Review: &Review{Event: domain.GHReviewRequestChanges}}); err != nil {
		t.Fatal(err)
	}
	if f.path != "/repos/acme/api/pulls/7/reviews" || f.body["event"] != "REQUEST_CHANGES" || f.body["body"] != "faltam testes" {
		t.Errorf("review: %s %v", f.path, f.body)
	}
	long := strings.Repeat("a", 70000)
	if _, err := d.Send(ctx, f.client(), Outbound{Peer: "kubo/web#1", Text: long}); err != nil || len([]rune(f.body["body"].(string))) != githubMaxBody {
		t.Errorf("corte do corpo: %v %d", err, len([]rune(f.body["body"].(string))))
	}
	for name, out := range map[string]Outbound{
		"peer":      {Peer: "acme/api", Text: "x"},
		"repo fora": {Peer: "outro/api#1", Text: "x"},
		"vazio":     {Peer: "acme/api#1", Text: "  "},
		"veredito":  {Peer: "acme/api#1", Text: "x", Review: &Review{Event: "TALVEZ"}},
	} {
		if _, err := d.Send(ctx, f.client(), out); !IsPermanent(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	noTok := f.client()
	noTok.Token = ""
	if _, err := d.Send(ctx, noTok, Outbound{Peer: "acme/api#1", Text: "x"}); !IsPermanent(err) {
		t.Errorf("sem token: %v", err)
	}
	for status, permanent := range map[int]bool{401: true, 403: true, 404: true, 422: true, 429: false, 500: false, 503: false} {
		f.mu.Lock()
		f.status, f.reply = status, `{"message":"falha com `+ghToken+`"}`
		f.mu.Unlock()
		_, err := d.Send(ctx, f.client(), Outbound{Peer: "acme/api#1", Text: "x"})
		if err == nil || IsPermanent(err) != permanent || strings.Contains(err.Error(), ghToken) {
			t.Errorf("status %d: %v (permanente=%v)", status, err, IsPermanent(err))
		}
	}
	f.mu.Lock()
	f.status, f.reply = 403, `{"message":"API rate limit exceeded"}`
	f.mu.Unlock()
	if _, err := d.Send(ctx, f.client(), Outbound{Peer: "acme/api#1", Text: "x"}); err == nil {
		t.Error("403 por limite deveria falhar")
	}
	if _, err := d.Fetch(ctx, f.client(), "x", 1); !errors.Is(err, ErrUnsupported) {
		t.Error("Fetch")
	}
	if err := d.Register(ctx, f.client(), "u", "s"); !errors.Is(err, ErrUnsupported) {
		t.Error("Register")
	}
	if d.Ack(ctx, f.client(), "x", "y") != nil || d.Kind() != domain.ChannelGitHub {
		t.Error("Ack/Kind")
	}
}

func TestFor(t *testing.T) {
	for _, k := range []domain.ChannelKind{domain.ChannelTelegram, domain.ChannelGitHub} {
		d, ok := For(k)
		if !ok || d.Kind() != k {
			t.Errorf("For(%s)", k)
		}
	}
	if _, ok := For("slack"); ok {
		t.Error("driver inexistente")
	}
	if clip("abcdef", 3) != "abc" || clip("ab", 3) != "ab" || scrub("x TOKEN y", "TOKEN") != "x *** y" || scrub("x", "") != "x" {
		t.Error("helpers")
	}
	if err := classify(200, "x"); IsPermanent(err) {
		t.Error("classify 200")
	}
}
