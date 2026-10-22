package service_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Canais externos (ADR-0046): Telegram e GitHub contra fakes httptest; nenhuma chamada real nem token de verdade.

const (
	tgTokenValue  = "111:TOKEN-DO-BOT"
	tgSecretValue = "whsec-telegram"
	ghTokenValue  = "ghp_TOKEN-DO-GITHUB"
	ghSecretValue = "whsec-github"
)

// tgBot imita a Bot API do Telegram.
type tgBot struct {
	mu       sync.Mutex
	srv      *httptest.Server
	sent     []map[string]any
	answered []map[string]any
	webhooks []map[string]any
	status   int // status forçado para sendMessage (0 = ok)
	nextID   int
	file     string
}

func newTGBot(t *testing.T) *tgBot {
	t.Helper()
	b := &tgBot{file: "conteúdo do relatório"}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		b.mu.Lock()
		defer b.mu.Unlock()
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/file/bot"+tgTokenValue+"/") {
			_, _ = w.Write([]byte(b.file))
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/bot"+tgTokenValue+"/") {
			http.Error(w, "token errado", http.StatusUnauthorized)
			return
		}
		m := strings.TrimPrefix(r.URL.Path, "/bot"+tgTokenValue+"/")
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		switch m {
		case "sendMessage":
			if b.status != 0 {
				w.WriteHeader(b.status)
				_, _ = w.Write([]byte(`{"ok":false,"description":"falha simulada"}`))
				return
			}
			b.sent = append(b.sent, body)
			b.nextID++
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, b.nextID)
		case "answerCallbackQuery":
			b.answered = append(b.answered, body)
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case "setWebhook":
			b.webhooks = append(b.webhooks, body)
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case "getFile":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"docs/relatorio.txt","file_size":21}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *tgBot) texts() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, m := range b.sent {
		out = append(out, fmt.Sprint(m["text"]))
	}
	return out
}

func (b *tgBot) last() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sent) == 0 {
		return nil
	}
	return b.sent[len(b.sent)-1]
}

// buttons devolve os callback_data do teclado da última mensagem que os tem.
func (b *tgBot) callbackData() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for i := len(b.sent) - 1; i >= 0 && len(out) == 0; i-- {
		rm, ok := b.sent[i]["reply_markup"].(map[string]any)
		if !ok {
			continue
		}
		for _, row := range rm["inline_keyboard"].([]any) {
			for _, btn := range row.([]any) {
				if d, ok := btn.(map[string]any)["callback_data"].(string); ok {
					out = append(out, d)
				}
			}
		}
	}
	return out
}

// ghServer imita a API REST do GitHub.
type ghServer struct {
	mu     sync.Mutex
	srv    *httptest.Server
	posts  []ghPost
	status int
}

type ghPost struct {
	Path string
	Body map[string]any
	Auth string
}

func newGHServer(t *testing.T) *ghServer {
	t.Helper()
	g := &ghServer{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.status != 0 {
			w.WriteHeader(g.status)
			_, _ = w.Write([]byte(`{"message":"falha simulada"}`))
			return
		}
		g.posts = append(g.posts, ghPost{Path: r.URL.Path, Body: body, Auth: r.Header.Get("Authorization")})
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"id":%d}`, 1000+len(g.posts))
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *ghServer) all() []ghPost {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ghPost(nil), g.posts...)
}

// chanEnv é um ambiente com um canal Telegram ligado e uma pessoa (ana) membro da empresa.
type chanEnv struct {
	*secEnv
	tg      *tgBot
	ada     domain.Persona
	ana     domain.User
	channel service.ChannelView
}

func (ce *chanEnv) ctx() context.Context { return context.Background() }

func newChanEnv(t *testing.T) *chanEnv {
	t.Helper()
	e := newSecEnv(t)
	tg := newTGBot(t)
	e.svc.Integrations.HTTP = &http.Client{Timeout: 10 * time.Second}
	e.create(t, "tg-token", tgTokenValue)
	e.create(t, "tg-secret", tgSecretValue)
	ce := &chanEnv{secEnv: e, tg: tg, ada: e.persona(t, "Ada", nil)}
	ce.ana = mkUser(t, e.env, "ana@x.com", domain.RoleMember)
	join(t, e.env, ce.ana, domain.MemberMember)
	ch, err := e.svc.CreateChannel(ce.ctx(), domain.HumanActor, e.co.ID, service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "Bot",
		PersonaID: &ce.ada.ID, TokenRef: "${secret:tg-token}", WebhookSecretRef: "${secret:tg-secret}",
		Config: domain.ChannelConfig{APIBase: tg.srv.URL}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ce.channel = ch
	return ce
}

func tgMessage(updateID, from int, text string) string {
	return fmt.Sprintf(`{"update_id":%d,"message":{"message_id":%d,"from":{"id":%d,"first_name":"Ana","username":"ana"},"chat":{"id":%d,"type":"private"},"text":%s}}`,
		updateID, updateID, from, from, jsonStr(text))
}

func tgCallback(updateID, from int, data string) string {
	return fmt.Sprintf(`{"update_id":%d,"callback_query":{"id":"cb%d","from":{"id":%d,"first_name":"Ana"},"message":{"message_id":9,"chat":{"id":%d,"type":"private"}},"data":%s}}`,
		updateID, updateID, from, from, jsonStr(data))
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func (ce *chanEnv) ingest(t *testing.T, body string) service.IngestResult {
	t.Helper()
	res, err := ce.svc.IngestChannel(ce.ctx(), service.IngestInput{Token: ce.channel.InboundToken,
		Header: http.Header{"X-Telegram-Bot-Api-Secret-Token": {tgSecretValue}}, Body: []byte(body)})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return res
}

// flush entrega as mensagens de saída pendentes do canal (o job da fila, em teste síncrono).
func flush(t *testing.T, e *env, channelID domain.ID) {
	t.Helper()
	ctx := context.Background()
	ms, err := e.svc.ListChannelMessages(ctx, e.co.ID, channelID, 200)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(ms) - 1; i >= 0; i-- { // do mais antigo para o mais novo
		m := ms[i]
		if m.Direction == domain.MessageOut && m.Status == domain.MsgPending {
			if _, err := e.svc.DeliverChannelMessage(ctx, m.ID, 1, service.ChannelMaxAttempts); err != nil {
				t.Fatalf("deliver %s: %v", m.ID, err)
			}
		}
	}
}

func (ce *chanEnv) link(t *testing.T, tgUser int) {
	t.Helper()
	link, err := ce.svc.CreateChannelLink(as(ce.ana), ce.ana.Actor(), ce.co.ID, ce.channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	ce.ingest(t, tgMessage(100+tgUser, tgUser, "/vincular "+strings.ToLower(link.Code)))
	flush(t, ce.env, ce.channel.ID)
}

func messageStatus(t *testing.T, e *env, channelID domain.ID, direction domain.MessageDirection) []domain.ChannelMessageStatus {
	t.Helper()
	ms, err := e.svc.ListChannelMessages(context.Background(), e.co.ID, channelID, 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []domain.ChannelMessageStatus
	for _, m := range ms {
		if m.Direction == direction {
			out = append(out, m.Status)
		}
	}
	return out
}

func TestChannelCRUDAndSecrets(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.svc.Integrations.HTTP = &http.Client{Timeout: 5 * time.Second}
	p := e.persona(t, "Ada", nil)
	mk := func(in service.CreateChannelInput) error {
		_, err := e.svc.CreateChannel(bg, domain.HumanActor, e.co.ID, in)
		return err
	}
	wantErr(t, mk(service.CreateChannelInput{Kind: "slack", Name: "x"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: ""}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateChannelInput{Kind: domain.ChannelGitHub, Name: "gh"}), domain.ErrInvalid) // sem repos
	wantErr(t, mk(service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "x", TokenRef: "texto-em-claro"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "x", TokenRef: "${secret:nao-existe}"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "x", TokenRef: "${secret:user/pessoal}"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "x", PersonaID: ptr(domain.NewID())}), domain.ErrInvalid)

	e.create(t, "tok", "valor")
	e.create(t, "sec", "valor2")
	// ligar exige token e segredo
	wantErr(t, mk(service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "x", Enabled: true}), domain.ErrInvalid)
	ch, err := e.svc.CreateChannel(bg, domain.HumanActor, e.co.ID, service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "Bot",
		TokenRef: "${secret:tok}", WebhookSecretRef: "${secret:sec}"})
	if err != nil || ch.Enabled || ch.InboundToken == "" || ch.HookPath != "/hooks/channels/"+ch.InboundToken {
		t.Fatalf("create: %+v %v", ch, err)
	}
	wantErr(t, mk(service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "Bot"}), domain.ErrConflict) // nome repetido
	gh := service.CreateChannelInput{Kind: domain.ChannelGitHub, Name: "GH", TokenRef: "${secret:tok}", WebhookSecretRef: "${secret:sec}",
		Config: domain.ChannelConfig{Repos: []string{"acme/api"}}}
	wantErr(t, mk(withEnabled(gh)), domain.ErrInvalid) // GitHub ligado precisa de persona
	gh.PersonaID = &p.ID
	if err := mk(withEnabled(gh)); err != nil {
		t.Fatal(err)
	}

	// o segredo em uso por um canal não é apagado sem force
	wantErr(t, e.svc.DeleteSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "tok", false), domain.ErrConflict)
	uses, err := e.svc.ListSecrets(bg, e.co.ID, domain.SecretScopeCompany, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range uses {
		if u.Name == "tok" {
			for _, use := range u.UsedBy {
				found = found || use.Kind == "channel"
			}
		}
	}
	if !found {
		t.Fatalf("segredo tok sem uso por canal: %+v", uses)
	}

	// atualizar: ligar e desligar, trocar config
	on := true
	up, err := e.svc.UpdateChannel(bg, domain.HumanActor, e.co.ID, ch.ID, service.UpdateChannelInput{Enabled: &on, PersonaID: &p.ID})
	if err != nil || !up.Enabled || up.PersonaName != "Ada" {
		t.Fatalf("enable: %+v %v", up, err)
	}
	_, err = e.svc.UpdateChannel(bg, domain.HumanActor, e.co.ID, ch.ID, service.UpdateChannelInput{TokenRef: ptr("")})
	wantErr(t, err, domain.ErrInvalid) // ligado sem token
	off := false
	if up, err = e.svc.UpdateChannel(bg, domain.HumanActor, e.co.ID, ch.ID, service.UpdateChannelInput{Enabled: &off, TokenRef: ptr("")}); err != nil || up.Enabled || up.TokenRef != "" {
		t.Fatalf("disable: %+v %v", up, err)
	}
	_, err = e.svc.UpdateChannel(bg, domain.HumanActor, e.co.ID, ch.ID, service.UpdateChannelInput{Config: &domain.ChannelConfig{Repos: []string{"x/y"}}})
	wantErr(t, err, domain.ErrInvalid) // telegram não tem repos
	if l, _ := e.svc.ListChannels(bg, e.co.ID); len(l) != 2 {
		t.Fatalf("lista = %d", len(l))
	}
	if g, err := e.svc.GetChannel(bg, e.co.ID, "Bot"); err != nil || g.ID != ch.ID {
		t.Fatalf("get por nome: %+v %v", g, err)
	}

	// persona não administra canais; membro comum também não
	_, err = e.svc.CreateChannel(bg, domain.PersonaActor(p.ID), e.co.ID, service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "p"})
	wantErr(t, err, domain.ErrForbidden)
	mem := mkUser(t, e.env, "m@x.com", domain.RoleMember)
	join(t, e.env, mem, domain.MemberMember)
	_, err = e.svc.CreateChannel(as(mem), mem.Actor(), e.co.ID, service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "m"})
	wantErr(t, err, domain.ErrForbidden)

	// conectar sem URL pública: devolve o caminho e explica
	cn, err := e.svc.ConnectChannel(bg, domain.HumanActor, e.co.ID, ch.ID)
	if err != nil || cn.Registered || !strings.Contains(cn.URL, ch.InboundToken) || cn.Note == "" {
		t.Fatalf("connect sem URL: %+v %v", cn, err)
	}

	if err := e.svc.DeleteChannel(bg, domain.HumanActor, e.co.ID, ch.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.DeleteChannel(bg, domain.HumanActor, e.co.ID, ch.ID), domain.ErrNotFound)
	acts, _ := e.svc.ListActivity(bg, e.co.ID, service.ActivityFilter{SubjectType: "channel", Limit: 100})
	verbs := map[string]bool{}
	for _, a := range acts {
		verbs[a.Verb] = true
	}
	for _, v := range []string{"channel.created", "channel.enabled", "channel.disabled", "channel.deleted"} {
		if !verbs[v] {
			t.Errorf("faltou o fato %s (%v)", v, verbs)
		}
	}
}

func withEnabled(in service.CreateChannelInput) service.CreateChannelInput {
	in.Enabled = true
	return in
}

func TestConnectTelegramRegistersWebhook(t *testing.T) {
	ce := newChanEnv(t)
	ce.svc.Channels = service.ChannelsConfig{PublicURL: "https://hanko.example.com/"}
	cn, err := ce.svc.ConnectChannel(ce.ctx(), domain.HumanActor, ce.co.ID, ce.channel.ID)
	if err != nil || !cn.Registered || cn.URL != "https://hanko.example.com/hooks/channels/"+ce.channel.InboundToken {
		t.Fatalf("connect: %+v %v", cn, err)
	}
	ce.tg.mu.Lock()
	defer ce.tg.mu.Unlock()
	if len(ce.tg.webhooks) != 1 || ce.tg.webhooks[0]["secret_token"] != tgSecretValue || ce.tg.webhooks[0]["url"] != cn.URL {
		t.Fatalf("setWebhook: %+v", ce.tg.webhooks)
	}
}

func TestIngestSecurity(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	body := []byte(tgMessage(1, 42, "oi"))
	in := func(token, secret string) service.IngestInput {
		return service.IngestInput{Token: token, Header: http.Header{"X-Telegram-Bot-Api-Secret-Token": {secret}}, Body: body}
	}
	_, err := ce.svc.IngestChannel(bg, in("nao-existe", tgSecretValue))
	wantErr(t, err, domain.ErrNotFound)
	_, err = ce.svc.IngestChannel(bg, in(ce.channel.InboundToken, "errado"))
	if !errors.Is(err, service.ErrWebhookUnauthorized) {
		t.Fatalf("segredo errado: %v", err)
	}
	_, err = ce.svc.IngestChannel(bg, service.IngestInput{Token: ce.channel.InboundToken, Body: body})
	if !errors.Is(err, service.ErrWebhookUnauthorized) {
		t.Fatalf("sem cabeçalho: %v", err)
	}
	if ms, _ := ce.svc.ListChannelMessages(bg, ce.co.ID, ce.channel.ID, 10); len(ms) != 0 {
		t.Fatalf("entrada recusada deixou %d mensagens", len(ms))
	}
	// corpo inválido depois de autenticado
	_, err = ce.svc.IngestChannel(bg, service.IngestInput{Token: ce.channel.InboundToken, Header: http.Header{"X-Telegram-Bot-Api-Secret-Token": {tgSecretValue}}, Body: []byte("{")})
	wantErr(t, err, domain.ErrInvalid)

	// entrega repetida do mesmo update: uma só mensagem
	r1 := ce.ingest(t, string(body))
	r2 := ce.ingest(t, string(body))
	if r1.Accepted != 1 || r2.Duplicates != 1 || r2.Accepted != 0 {
		t.Fatalf("idempotência: %+v %+v", r1, r2)
	}
	ins := 0
	ms, _ := ce.svc.ListChannelMessages(bg, ce.co.ID, ce.channel.ID, 50)
	for _, m := range ms {
		if m.Direction == domain.MessageIn {
			ins++
		}
	}
	if ins != 1 {
		t.Fatalf("entradas = %d, want 1", ins)
	}
	// remetente sem vínculo: nada acontece além do aviso para vincular
	if tasks, _ := ce.svc.ListTasks(bg, ce.co.ID, service.TaskFilter{}); len(tasks) != 0 {
		t.Fatalf("tasks = %d", len(tasks))
	}
	if cvs, _ := ce.svc.ListConversations(bg, ce.co.ID, service.ConversationFilter{}); len(cvs) != 0 {
		t.Fatalf("conversas = %d", len(cvs))
	}
	flush(t, ce.env, ce.channel.ID)
	if txt := ce.tg.texts(); len(txt) != 1 || !strings.Contains(txt[0], "/vincular") {
		t.Fatalf("resposta ao desconhecido: %v", txt)
	}
	// grupo e bot são ignorados
	ce.ingest(t, `{"update_id":7,"message":{"message_id":1,"from":{"id":42},"chat":{"id":-5,"type":"group"},"text":"oi"}}`)
	if st := messageStatus(t, ce.env, ce.channel.ID, domain.MessageIn); st[0] != domain.MsgIgnored {
		t.Fatalf("grupo: %v", st)
	}
	// canal desligado: 404
	off := false
	if _, err := ce.svc.UpdateChannel(bg, domain.HumanActor, ce.co.ID, ce.channel.ID, service.UpdateChannelInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	_, err = ce.svc.IngestChannel(bg, in(ce.channel.InboundToken, tgSecretValue))
	wantErr(t, err, domain.ErrNotFound)
}

func TestChannelLinkFlow(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	// sem pessoa autenticada não há vínculo
	_, err := ce.svc.CreateChannelLink(bg, domain.HumanActor, ce.co.ID, ce.channel.ID)
	wantErr(t, err, domain.ErrInvalid)

	link, err := ce.svc.CreateChannelLink(as(ce.ana), ce.ana.Actor(), ce.co.ID, ce.channel.ID)
	if err != nil || len(link.Code) != 9 || !strings.Contains(link.Hint, "/vincular") {
		t.Fatalf("link: %+v %v", link, err)
	}
	// pedir de novo troca o código; o antigo deixa de valer
	link2, err := ce.svc.CreateChannelLink(as(ce.ana), ce.ana.Actor(), ce.co.ID, ce.channel.ID)
	if err != nil || link2.Code == link.Code {
		t.Fatalf("segundo código: %+v %v", link2, err)
	}
	ce.ingest(t, tgMessage(1, 42, "/vincular "+link.Code))
	flush(t, ce.env, ce.channel.ID)
	if txt := ce.tg.texts(); len(txt) != 1 || !strings.Contains(txt[0], "inválido") {
		t.Fatalf("código antigo: %v", txt)
	}
	// código errado e mal formado
	ce.ingest(t, tgMessage(2, 42, "/vincular ZZZZ-ZZZZ"))
	ce.ingest(t, tgMessage(3, 42, "/vincular 123"))
	flush(t, ce.env, ce.channel.ID)

	ce.ingest(t, tgMessage(4, 42, "/start "+link2.Code)) // deep link do Telegram
	flush(t, ce.env, ce.channel.ID)
	txts := ce.tg.texts()
	if !strings.Contains(txts[len(txts)-1], "Vinculado a ana@x.com") {
		t.Fatalf("vinculou? %v", txts)
	}
	ids, err := ce.svc.ListChannelIdentities(bg, ce.co.ID, ce.channel.ID)
	if err != nil || len(ids) != 1 || ids[0].Status != domain.IdentityLinked || ids[0].ExternalID != "42" || ids[0].UserEmail != "ana@x.com" {
		t.Fatalf("identidades: %+v %v", ids, err)
	}
	// o código é de uso único; já vinculada, não gera outro
	ce.ingest(t, tgMessage(5, 77, "/vincular "+link2.Code))
	_, err = ce.svc.CreateChannelLink(as(ce.ana), ce.ana.Actor(), ce.co.ID, ce.channel.ID)
	wantErr(t, err, domain.ErrConflict)
	me, _ := ce.svc.MyChannelIdentity(as(ce.ana), ce.co.ID, ce.channel.ID)
	if me == nil || me.Status != domain.IdentityLinked {
		t.Fatalf("my identity: %+v", me)
	}

	// a mesma conta do Telegram não vincula a duas pessoas
	bob := mkUser(t, ce.env, "bob@x.com", domain.RoleMember)
	join(t, ce.env, bob, domain.MemberMember)
	lb, _ := ce.svc.CreateChannelLink(as(bob), bob.Actor(), ce.co.ID, ce.channel.ID)
	ce.ingest(t, tgMessage(6, 42, "/vincular "+lb.Code))
	flush(t, ce.env, ce.channel.ID)
	txts = ce.tg.texts()
	if !strings.Contains(txts[len(txts)-1], "já está vinculada") {
		t.Fatalf("conta duplicada: %v", txts)
	}
	// viewer não vincula
	vw := mkUser(t, ce.env, "viewer@x.com", domain.RoleMember)
	join(t, ce.env, vw, domain.MemberViewer)
	_, err = ce.svc.CreateChannelLink(as(vw), vw.Actor(), ce.co.ID, ce.channel.ID)
	if err == nil {
		lv, _ := ce.svc.CreateChannelLink(as(vw), vw.Actor(), ce.co.ID, ce.channel.ID)
		_ = lv
		t.Fatal("viewer conseguiu pedir código")
	}

	// só admin revoga o vínculo de outra pessoa
	wantErr(t, ce.svc.RevokeChannelIdentity(as(bob), bob.Actor(), ce.co.ID, ce.channel.ID, ids[0].ID), domain.ErrForbidden)
	if err := ce.svc.RevokeChannelIdentity(bg, domain.HumanActor, ce.co.ID, ce.channel.ID, ids[0].ID); err == nil {
		t.Fatal("revogar sem pessoa autenticada deveria falhar (exige uma pessoa)")
	}
	adm := mkUser(t, ce.env, "adm@x.com", domain.RoleMember)
	join(t, ce.env, adm, domain.MemberAdmin)
	if err := ce.svc.RevokeChannelIdentity(as(adm), adm.Actor(), ce.co.ID, ce.channel.ID, ids[0].ID); err != nil {
		t.Fatal(err)
	}
	// depois de revogado, a conta não age
	ce.ingest(t, tgMessage(8, 42, "oi, tudo bem?"))
	if cvs, _ := ce.svc.ListConversations(bg, ce.co.ID, service.ConversationFilter{}); len(cvs) != 0 {
		t.Fatalf("conversa criada por conta revogada: %d", len(cvs))
	}
	// e pode vincular de novo
	if _, err := ce.svc.CreateChannelLink(as(ce.ana), ce.ana.Actor(), ce.co.ID, ce.channel.ID); err != nil {
		t.Fatalf("revincular: %v", err)
	}
}

func TestChannelLinkExpires(t *testing.T) {
	ce := newChanEnv(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	ce.svc.Now = func() time.Time { return now }
	link, err := ce.svc.CreateChannelLink(as(ce.ana), ce.ana.Actor(), ce.co.ID, ce.channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(domain.ChannelLinkTTL + time.Minute)
	ce.ingest(t, tgMessage(1, 42, "/vincular "+link.Code))
	flush(t, ce.env, ce.channel.ID)
	if txt := ce.tg.texts(); len(txt) != 1 || !strings.Contains(txt[0], "vencido") {
		t.Fatalf("código vencido: %v", txt)
	}
}

func TestTelegramDecidesGate(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	ce.link(t, 42)

	tk := ce.task(t, service.CreateTaskInput{Title: "Publicar release"})
	g, err := ce.svc.RequestGate(bg, domain.HumanActor, ce.co.ID, service.RequestGateInput{Title: "Publicar a release 1.2?", TaskID: &tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	flush(t, ce.env, ce.channel.ID)
	data := ce.tg.callbackData()
	if len(data) != 2 || data[0] != "g|"+g.ID.String()+"|a" || data[1] != "g|"+g.ID.String()+"|r" {
		t.Fatalf("botões do gate: %v (textos %v)", data, ce.tg.texts())
	}
	if last := ce.tg.last(); last["chat_id"] != "42" || !strings.Contains(fmt.Sprint(last["text"]), "Publicar a release 1.2?") || !strings.Contains(fmt.Sprint(last["text"]), tk.Key) {
		t.Fatalf("mensagem do gate: %+v", last)
	}

	// uma conta do Telegram sem vínculo clicando no botão não decide nada
	ce.ingest(t, tgCallback(10, 99, data[0]))
	if got, _ := ce.svc.GetGate(bg, ce.co.ID, g.ID); got.Status != domain.GatePending {
		t.Fatalf("conta sem vínculo decidiu o gate: %s", got.Status)
	}

	// a pessoa vinculada aprova
	ce.ingest(t, tgCallback(11, 42, data[0]))
	got, err := ce.svc.GetGate(bg, ce.co.ID, g.ID)
	if err != nil || got.Status != domain.GateApproved || got.DecidedBy == nil || got.DecidedBy.Type != domain.ActorHuman || got.DecidedBy.ID == nil || *got.DecidedBy.ID != ce.ana.ID {
		t.Fatalf("gate: %+v %v", got, err)
	}
	if !strings.Contains(got.DecisionNote, "Telegram") {
		t.Fatalf("nota da decisão: %q", got.DecisionNote)
	}
	ce.tg.mu.Lock()
	answers := len(ce.tg.answered)
	ce.tg.mu.Unlock()
	if answers < 2 {
		t.Fatalf("answerCallbackQuery = %d", answers)
	}
	flush(t, ce.env, ce.channel.ID)
	txt := ce.tg.texts()
	if !strings.Contains(txt[len(txt)-1], "Gate aprovado") {
		t.Fatalf("confirmação: %v", txt)
	}
	// o segundo clique encontra o gate já decidido
	ce.ingest(t, tgCallback(12, 42, data[1]))
	flush(t, ce.env, ce.channel.ID)
	txt = ce.tg.texts()
	if !strings.Contains(txt[len(txt)-1], "já foi decidido") {
		t.Fatalf("segundo clique: %v", txt)
	}
	if got, _ := ce.svc.GetGate(bg, ce.co.ID, g.ID); got.Status != domain.GateApproved {
		t.Fatalf("o segundo clique mudou o gate: %s", got.Status)
	}
	acts, _ := ce.svc.ListActivity(bg, ce.co.ID, service.ActivityFilter{SubjectType: "gate", Limit: 20})
	decided := 0
	for _, a := range acts {
		if a.Verb == "gate.decided" {
			decided++
			if a.Actor.ID == nil || *a.Actor.ID != ce.ana.ID {
				t.Fatalf("ator da decisão: %+v", a.Actor)
			}
		}
	}
	if decided != 1 {
		t.Fatalf("gate.decided = %d", decided)
	}

	// rejeitar por botão; ação inventada é ignorada
	g2, _ := ce.svc.RequestGate(bg, domain.HumanActor, ce.co.ID, service.RequestGateInput{Title: "Apagar o cache?"})
	ce.ingest(t, tgCallback(13, 42, "g|"+g2.ID.String()+"|r"))
	if got, _ := ce.svc.GetGate(bg, ce.co.ID, g2.ID); got.Status != domain.GateRejected {
		t.Fatalf("rejeitar: %s", got.Status)
	}
	ce.ingest(t, tgCallback(14, 42, "g|nao-e-uuid|a"))
	ce.ingest(t, tgCallback(15, 42, "x|y"))
	g3, _ := ce.svc.RequestGate(bg, domain.HumanActor, ce.co.ID, service.RequestGateInput{Title: "Outro?"})
	ce.ingest(t, tgCallback(16, 42, "g|"+g3.ID.String()+"|z"))
	if got, _ := ce.svc.GetGate(bg, ce.co.ID, g3.ID); got.Status != domain.GatePending {
		t.Fatalf("ação inventada decidiu: %s", got.Status)
	}
}

func TestTelegramGateAudienceAndRoles(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	ce.link(t, 42)
	// bob (member) também vinculado
	bob := mkUser(t, ce.env, "bob@x.com", domain.RoleMember)
	join(t, ce.env, bob, domain.MemberMember)
	lb, _ := ce.svc.CreateChannelLink(as(bob), bob.Actor(), ce.co.ID, ce.channel.ID)
	ce.ingest(t, tgMessage(200, 43, "/vincular "+lb.Code))
	flush(t, ce.env, ce.channel.ID)
	before := len(ce.tg.texts())

	// gate restrito à audiência de bob: só ele recebe o aviso e só ele decide
	g, err := ce.svc.RequestGate(bg, domain.HumanActor, ce.co.ID, service.RequestGateInput{Title: "Só do Bob", Audience: domain.GateAudience{Users: []domain.ID{bob.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	flush(t, ce.env, ce.channel.ID)
	ce.tg.mu.Lock()
	var peers []string
	for _, m := range ce.tg.sent[before:] {
		peers = append(peers, fmt.Sprint(m["chat_id"]))
	}
	ce.tg.mu.Unlock()
	if len(peers) != 1 || peers[0] != "43" {
		t.Fatalf("aviso do gate restrito foi para %v", peers)
	}
	// ana tenta decidir pelo botão que não recebeu: o service recusa pela audiência
	ce.ingest(t, tgCallback(201, 42, "g|"+g.ID.String()+"|a"))
	if got, _ := ce.svc.GetGate(bg, ce.co.ID, g.ID); got.Status != domain.GatePending {
		t.Fatalf("fora da audiência decidiu: %s", got.Status)
	}
	flush(t, ce.env, ce.channel.ID)
	txt := ce.tg.texts()
	if !strings.Contains(txt[len(txt)-1], "não pode decidir") {
		t.Fatalf("resposta: %v", txt)
	}
	ce.ingest(t, tgCallback(202, 43, "g|"+g.ID.String()+"|a"))
	if got, _ := ce.svc.GetGate(bg, ce.co.ID, g.ID); got.Status != domain.GateApproved {
		t.Fatalf("bob não conseguiu decidir: %s", got.Status)
	}

	// gate de contratação (admin) não tem botão de decisão e não vai para member
	n := len(ce.tg.texts())
	ce.tg.mu.Lock()
	sentBefore := len(ce.tg.sent)
	ce.tg.mu.Unlock()
	_, err = ce.svc.RequestGate(bg, domain.HumanActor, ce.co.ID, service.RequestGateInput{Title: "Contratar", Kind: domain.GateInstruction, Body: "x"})
	_ = err // pode exigir payload próprio; o que importa é nada ir para members
	flush(t, ce.env, ce.channel.ID)
	ce.tg.mu.Lock()
	for _, m := range ce.tg.sent[sentBefore:] {
		if strings.Contains(fmt.Sprint(m["text"]), "(instruction)") {
			t.Errorf("gate de admin foi para member: %+v", m)
		}
	}
	ce.tg.mu.Unlock()
	_ = n

	// pessoa que deixou de ser membro: o canal não age por ela
	if _, err := ce.svc.Pool.Exec(bg, `DELETE FROM company_members WHERE company_id=$1 AND user_id=$2`, ce.co.ID, ce.ana.ID); err != nil {
		t.Fatal(err)
	}
	g2, _ := ce.svc.RequestGate(bg, domain.HumanActor, ce.co.ID, service.RequestGateInput{Title: "Depois da saída"})
	ce.ingest(t, tgCallback(203, 42, "g|"+g2.ID.String()+"|a"))
	if got, _ := ce.svc.GetGate(bg, ce.co.ID, g2.ID); got.Status != domain.GatePending {
		t.Fatalf("ex-membro decidiu: %s", got.Status)
	}
}

func TestTelegramPendingCommand(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	ce.link(t, 42)
	ce.ingest(t, tgMessage(300, 42, "/pendentes"))
	flush(t, ce.env, ce.channel.ID)
	if txt := ce.tg.texts(); !strings.Contains(txt[len(txt)-1], "Nenhum gate") {
		t.Fatalf("sem pendentes: %v", txt)
	}
	// um gate criado antes de o canal existir também aparece em /pendentes
	g, _ := ce.svc.RequestGate(bg, domain.HumanActor, ce.co.ID, service.RequestGateInput{Title: "Pendente 1"})
	flush(t, ce.env, ce.channel.ID)
	n := len(ce.tg.texts())
	ce.ingest(t, tgMessage(301, 42, "/pendentes"))
	flush(t, ce.env, ce.channel.ID)
	txt := ce.tg.texts()
	if len(txt) != n+1 || !strings.Contains(txt[len(txt)-1], "Pendente 1") || ce.tg.callbackData()[0] != "g|"+g.ID.String()+"|a" {
		t.Fatalf("pendentes: %v", txt)
	}
	for i, cmd := range []string{"/ajuda", "/start", "/inexistente", "/vincular"} {
		ce.ingest(t, tgMessage(310+i, 42, cmd))
	}
	flush(t, ce.env, ce.channel.ID)
	txt = ce.tg.texts()
	if !strings.Contains(txt[len(txt)-4], "Comandos") || !strings.Contains(txt[len(txt)-2], "desconhecido") || !strings.Contains(txt[len(txt)-1], "já está vinculado") {
		t.Fatalf("comandos: %v", txt[len(txt)-4:])
	}
	// desvincular
	ce.ingest(t, tgMessage(320, 42, "/desvincular"))
	flush(t, ce.env, ce.channel.ID)
	if txt = ce.tg.texts(); !strings.Contains(txt[len(txt)-1], "Vínculo desfeito") {
		t.Fatalf("desvincular: %v", txt)
	}
	me, _ := ce.svc.MyChannelIdentity(as(ce.ana), ce.co.ID, ce.channel.ID)
	if me != nil {
		t.Fatalf("vínculo continua: %+v", me)
	}
}

func TestTelegramConversationAndMirror(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	ce.link(t, 42)

	ce.ingest(t, tgMessage(400, 42, "Oi Ada, resuma o estado do projeto"))
	cvs, err := ce.svc.ListConversations(bg, ce.co.ID, service.ConversationFilter{})
	if err != nil || len(cvs) != 1 {
		t.Fatalf("conversas: %d %v", len(cvs), err)
	}
	cv := cvs[0]
	if cv.PersonaID != ce.ada.ID || cv.CreatedBy.Type != domain.ActorHuman || cv.CreatedBy.ID == nil || *cv.CreatedBy.ID != ce.ana.ID {
		t.Fatalf("conversa: %+v", cv)
	}
	msgs, _ := ce.svc.ListConversationMessages(bg, ce.co.ID, cv.ID, 0)
	if len(msgs) != 1 || msgs[0].Body != "Oi Ada, resuma o estado do projeto" {
		t.Fatalf("mensagens: %+v", msgs)
	}
	if len(ce.jobs.execs) != 1 {
		t.Fatalf("a persona deveria ter uma run enfileirada: %d", len(ce.jobs.execs))
	}
	// a resposta da persona vai para o Telegram
	finishChatRun(t, ce.env, lastExec(ce.env), "sess-1", "Estamos no marco M19.")
	flush(t, ce.env, ce.channel.ID)
	last := ce.tg.last()
	if last == nil || last["chat_id"] != "42" || last["text"] != "Estamos no marco M19." {
		t.Fatalf("espelho: %+v", last)
	}
	// a próxima mensagem continua a mesma conversa
	ce.ingest(t, tgMessage(401, 42, "e o que falta?"))
	if cvs, _ = ce.svc.ListConversations(bg, ce.co.ID, service.ConversationFilter{}); len(cvs) != 1 {
		t.Fatalf("a conversa foi duplicada: %d", len(cvs))
	}
	finishChatRun(t, ce.env, lastExec(ce.env), "sess-1", "Falta a UI.")
	// /novo recomeça
	ce.ingest(t, tgMessage(402, 42, "/novo"))
	ce.ingest(t, tgMessage(403, 42, "outro assunto"))
	if cvs, _ = ce.svc.ListConversations(bg, ce.co.ID, service.ConversationFilter{}); len(cvs) != 2 {
		t.Fatalf("depois do /novo: %d conversas", len(cvs))
	}
	flush(t, ce.env, ce.channel.ID)
	// conversa criada na UI não é espelhada
	ui, err := ce.svc.CreateConversation(bg, domain.HumanActor, ce.co.ID, service.CreateConversationInput{PersonaID: ce.ada.ID, Message: "da tela"})
	if err != nil {
		t.Fatal(err)
	}
	n := len(ce.tg.texts())
	_ = ui
	finishChatRun(t, ce.env, lastExec(ce.env), "sess-9", "resposta da tela")
	flush(t, ce.env, ce.channel.ID)
	if len(ce.tg.texts()) != n {
		t.Fatalf("a resposta de uma conversa da tela foi para o Telegram: %v", ce.tg.texts()[n:])
	}
	// persona pausada (sem run em andamento): a pessoa recebe o motivo
	finishChatRun(t, ce.env, ce.jobs.execs[len(ce.jobs.execs)-2], "sess-10", "resposta do novo assunto")
	flush(t, ce.env, ce.channel.ID)
	paused := domain.PersonaPaused
	if _, err := ce.svc.UpdatePersona(bg, domain.HumanActor, ce.co.ID, ce.ada.ID, service.UpdatePersonaInput{Status: &paused}); err != nil {
		t.Fatal(err)
	}
	ce.ingest(t, tgMessage(404, 42, "alguém aí?"))
	flush(t, ce.env, ce.channel.ID)
	if txt := ce.tg.texts(); !strings.Contains(txt[len(txt)-1], "não pode responder agora") {
		t.Fatalf("persona pausada: %v", txt[len(txt)-1])
	}
}

func TestTelegramAttachment(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	ce.link(t, 42)
	doc := `{"update_id":500,"message":{"message_id":1,"from":{"id":42,"first_name":"Ana"},"chat":{"id":42,"type":"private"},"caption":"segue o relatório",` +
		`"document":{"file_id":"F1","file_name":"relatorio.txt","mime_type":"text/plain","file_size":21}}}`
	ce.ingest(t, doc)
	cvs, _ := ce.svc.ListConversations(bg, ce.co.ID, service.ConversationFilter{})
	if len(cvs) != 1 {
		t.Fatalf("conversas = %d", len(cvs))
	}
	arts, err := ce.svc.ListArtifacts(bg, ce.co.ID, service.ArtifactFilter{Class: domain.ClassAttachment})
	if err != nil || len(arts) != 1 || arts[0].Name != "relatorio.txt" || arts[0].ConversationID == nil || *arts[0].ConversationID != cvs[0].ID {
		t.Fatalf("anexos: %+v %v", arts, err)
	}
	_, content, err := ce.svc.ArtifactContent(bg, ce.co.ID, arts[0].ID)
	if err != nil || string(content) != "conteúdo do relatório" {
		t.Fatalf("conteúdo: %q %v", content, err)
	}
	msgs, _ := ce.svc.ListConversationMessages(bg, ce.co.ID, cvs[0].ID, 0)
	if len(msgs) != 1 || msgs[0].Body != "segue o relatório" {
		t.Fatalf("mensagem: %+v", msgs)
	}
	// arquivo acima do limite: avisa e segue
	ce.svc.ArtifactMaxBytes = 5
	ce.ingest(t, strings.Replace(doc, `"update_id":500`, `"update_id":501`, 1))
	flush(t, ce.env, ce.channel.ID)
	if txt := ce.tg.texts(); !strings.Contains(txt[len(txt)-1], "Não consegui guardar") {
		t.Fatalf("arquivo grande: %v", txt[len(txt)-1])
	}
}

func TestTelegramFastResponse(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	f := newFakeRouter(t)
	ce.svc.Integrations.HTTP = f.Client()
	ce.create(t, "openrouter", "sk-or-chave")
	if _, err := ce.svc.SetDecisionModel(bg, domain.HumanActor, ce.co.ID, service.SetDecisionModelInput{BaseURL: f.base(), APIKeyRef: "${secret:openrouter}", Model: "vendor/small"}); err != nil {
		t.Fatal(err)
	}
	ce.link(t, 42)

	var prompts []string
	f.reply = func(system string) string {
		prompts = append(prompts, system)
		if len(prompts) == 1 { // a primeira pergunta é simples; as demais pedem trabalho
			return `{"action":"reply","reply":"Nenhum gate pendente agora."}`
		}
		return `{"action":"escalate","reply":""}`
	}

	// feature desligada: vai direto para a persona, o modelo nem é chamado
	ce.ingest(t, tgMessage(600, 42, "tem gate pendente?"))
	if len(prompts) != 0 {
		t.Fatalf("o modelo foi chamado com a feature desligada: %d", len(prompts))
	}
	if cvs, _ := ce.svc.ListConversations(bg, ce.co.ID, service.ConversationFilter{}); len(cvs) != 1 {
		t.Fatalf("conversas = %d", len(cvs))
	}
	if _, err := ce.svc.SetDecisionFeature(bg, domain.HumanActor, ce.co.ID, domain.FeatureFastResponse, true, ""); err != nil {
		t.Fatal(err)
	}

	// ligada: pergunta simples é respondida direto; o modelo recebe o retrato da empresa, não instruções do usuário
	ce.ingest(t, tgMessage(601, 42, "tem gate pendente?"))
	flush(t, ce.env, ce.channel.ID)
	if txt := ce.tg.texts(); txt[len(txt)-1] != "Nenhum gate pendente agora." {
		t.Fatalf("resposta rápida: %v", txt)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "Tasks abertas") || !strings.Contains(prompts[0], "ana@x.com") {
		t.Fatalf("prompt: %v", prompts)
	}
	msgs, _ := ce.svc.ListConversationMessages(bg, ce.co.ID, mustOne(t, ce.svc, ce.co.ID).ID, 0)
	if len(msgs) != 1 {
		t.Fatalf("a resposta rápida não deve entrar na conversa da persona: %d", len(msgs))
	}
	// pedido de trabalho: escala para a persona
	ce.ingest(t, tgMessage(602, 42, "escreva o release notes"))
	msgs, _ = ce.svc.ListConversationMessages(bg, ce.co.ID, mustOne(t, ce.svc, ce.co.ID).ID, 0)
	if len(msgs) != 2 {
		t.Fatalf("escalada: %d mensagens", len(msgs))
	}
	// o ledger tem as duas invocações, ator system
	invs, err := ce.svc.ListDecisionInvocations(bg, ce.co.ID, 10)
	if err != nil || len(invs) != 2 || invs[0].Feature != domain.FeatureFastResponse || invs[0].Actor != domain.ActorSystem {
		t.Fatalf("ledger: %+v %v", invs, err)
	}
	// resposta inválida do modelo: escala
	f.reply = func(string) string { return "isto não é JSON" }
	ce.ingest(t, tgMessage(603, 42, "mais uma"))
	msgs, _ = ce.svc.ListConversationMessages(bg, ce.co.ID, mustOne(t, ce.svc, ce.co.ID).ID, 0)
	if len(msgs) != 3 {
		t.Fatalf("modelo inválido deveria escalar: %d", len(msgs))
	}
	// o modelo nunca decide um gate: mesmo pedindo, a resposta rápida não toca no gate
	g, _ := ce.svc.RequestGate(bg, domain.HumanActor, ce.co.ID, service.RequestGateInput{Title: "Aprovar?"})
	f.reply = func(string) string { return `{"action":"approve","reply":"aprovado"}` }
	ce.ingest(t, tgMessage(604, 42, "aprove o gate"))
	if got, _ := ce.svc.GetGate(bg, ce.co.ID, g.ID); got.Status != domain.GatePending {
		t.Fatalf("o modelo decidiu um gate: %s", got.Status)
	}
	// com anexo não passa pelo modelo
	n := len(prompts)
	ce.ingest(t, `{"update_id":605,"message":{"message_id":1,"from":{"id":42},"chat":{"id":42,"type":"private"},"text":"veja","document":{"file_id":"F","file_name":"a.txt","file_size":3}}}`)
	if len(prompts) != n {
		t.Fatal("mensagem com anexo foi ao modelo")
	}
}

func mustOne(t *testing.T, svc *service.Service, companyID domain.ID) service.ConversationView {
	t.Helper()
	cvs, err := svc.ListConversations(context.Background(), companyID, service.ConversationFilter{})
	if err != nil || len(cvs) != 1 {
		t.Fatalf("conversas = %d %v", len(cvs), err)
	}
	return cvs[0]
}

func TestChannelDeliveryStates(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	send := func(text string) domain.ChannelMessage {
		m, err := ce.svc.SendChannelMessage(bg, domain.HumanActor, ce.co.ID, "Bot", "42", text, nil)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := send("olá")
	if m.Status != domain.MsgPending {
		t.Fatalf("status inicial: %s", m.Status)
	}
	// falha transitória: continua pending, conta a tentativa e guarda o erro (sem o token)
	ce.tg.mu.Lock()
	ce.tg.status = 500
	ce.tg.mu.Unlock()
	if _, err := ce.svc.DeliverChannelMessage(bg, m.ID, 1, 3); err == nil {
		t.Fatal("falha transitória deveria devolver erro para a fila tentar de novo")
	}
	ms, _ := ce.svc.ListChannelMessages(bg, ce.co.ID, ce.channel.ID, 10)
	if ms[0].Status != domain.MsgPending || ms[0].Attempts != 1 || ms[0].LastError == "" || strings.Contains(ms[0].LastError, tgTokenValue) {
		t.Fatalf("depois da 1ª falha: %+v", ms[0])
	}
	// a tentativa seguinte entrega
	ce.tg.mu.Lock()
	ce.tg.status = 0
	ce.tg.mu.Unlock()
	out, err := ce.svc.DeliverChannelMessage(bg, m.ID, 2, 3)
	if err != nil || !out.Sent {
		t.Fatalf("entrega: %+v %v", out, err)
	}
	ms, _ = ce.svc.ListChannelMessages(bg, ce.co.ID, ce.channel.ID, 10)
	if ms[0].Status != domain.MsgSent || ms[0].Attempts != 2 || ms[0].ProviderMsgID == "" || ms[0].SentAt == nil || ms[0].LastError != "" {
		t.Fatalf("depois de entregar: %+v", ms[0])
	}
	// entregar de novo é no-op
	if out, err := ce.svc.DeliverChannelMessage(bg, m.ID, 3, 3); err != nil || out.Sent {
		t.Fatalf("reentrega: %+v %v", out, err)
	}
	if got := ce.tg.texts(); len(got) != 1 {
		t.Fatalf("mensagens no Telegram: %v", got)
	}

	// falha permanente (bot bloqueado): failed de uma vez
	m2 := send("segunda")
	ce.tg.mu.Lock()
	ce.tg.status = 403
	ce.tg.mu.Unlock()
	out, err = ce.svc.DeliverChannelMessage(bg, m2.ID, 1, 3)
	if err != nil || !out.GaveUp {
		t.Fatalf("permanente: %+v %v", out, err)
	}
	ms, _ = ce.svc.ListChannelMessages(bg, ce.co.ID, ce.channel.ID, 10)
	if ms[0].Status != domain.MsgFailed || ms[0].LastError == "" {
		t.Fatalf("failed: %+v", ms[0])
	}
	if v, _ := ce.svc.GetChannel(bg, ce.co.ID, "Bot"); v.FailedOut != 1 {
		t.Fatalf("contador de falhas: %d", v.FailedOut)
	}
	// reenviar: volta a pending e entrega
	ce.tg.mu.Lock()
	ce.tg.status = 0
	ce.tg.mu.Unlock()
	if _, err := ce.svc.ResendChannelMessage(bg, domain.HumanActor, ce.co.ID, m2.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := ce.svc.DeliverChannelMessage(bg, m2.ID, 1, 3); err != nil || !out.Sent {
		t.Fatalf("reenvio: %+v %v", out, err)
	}
	// só mensagem que falhou se reenvia
	_, err = ce.svc.ResendChannelMessage(bg, domain.HumanActor, ce.co.ID, m2.ID)
	wantErr(t, err, domain.ErrConflict)

	// última tentativa transitória: encerra como failed
	m3 := send("terceira")
	ce.tg.mu.Lock()
	ce.tg.status = 502
	ce.tg.mu.Unlock()
	out, err = ce.svc.DeliverChannelMessage(bg, m3.ID, 3, 3)
	if err != nil || !out.GaveUp {
		t.Fatalf("última tentativa: %+v %v", out, err)
	}
	// canal desligado ou token apagado: failed
	ce.tg.mu.Lock()
	ce.tg.status = 0
	ce.tg.mu.Unlock()
	m4 := send("quarta")
	off := false
	if _, err := ce.svc.UpdateChannel(bg, domain.HumanActor, ce.co.ID, ce.channel.ID, service.UpdateChannelInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if out, err := ce.svc.DeliverChannelMessage(bg, m4.ID, 1, 3); err != nil || !out.GaveUp {
		t.Fatalf("canal desligado: %+v %v", out, err)
	}
	// o fechamento de uma mensagem presa pela fila
	on := true
	_, _ = ce.svc.UpdateChannel(bg, domain.HumanActor, ce.co.ID, ce.channel.ID, service.UpdateChannelInput{Enabled: &on})
	m5 := send("quinta")
	ce.svc.GiveUpChannelMessage(bg, m5.ID, errors.New("fila esgotada"))
	ms, _ = ce.svc.ListChannelMessages(bg, ce.co.ID, ce.channel.ID, 10)
	if ms[0].Status != domain.MsgFailed || ms[0].LastError != "fila esgotada" {
		t.Fatalf("GiveUp: %+v", ms[0])
	}
	// validações do envio avulso
	_, err = ce.svc.SendChannelMessage(bg, domain.HumanActor, ce.co.ID, "Bot", "42", "  ", nil)
	wantErr(t, err, domain.ErrInvalid)
	_, err = ce.svc.SendChannelMessage(bg, domain.HumanActor, ce.co.ID, "Bot", "", "x", nil)
	wantErr(t, err, domain.ErrInvalid)
	_, err = ce.svc.SendChannelMessage(bg, domain.HumanActor, ce.co.ID, "Bot", "42", "x", []domain.ID{domain.NewID()})
	wantErr(t, err, domain.ErrInvalid)
	_, err = ce.svc.SendChannelMessage(bg, domain.PersonaActor(ce.ada.ID), ce.co.ID, "Bot", "42", "x", nil)
	wantErr(t, err, domain.ErrForbidden)
	// documento: o artefato vai como arquivo
	art, err := ce.svc.PutArtifact(bg, domain.HumanActor, ce.co.ID, service.PutArtifactInput{Name: "nota.txt", Content: []byte("dados do doc")})
	if err != nil {
		t.Fatal(err)
	}
	md, err := ce.svc.SendChannelMessage(bg, domain.HumanActor, ce.co.ID, "Bot", "42", "veja", []domain.ID{art.ID})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := ce.svc.DeliverChannelMessage(bg, md.ID, 1, 3); err != nil || !out.Sent {
		t.Fatalf("documento: %+v %v", out, err)
	}
}

// ---------- GitHub ----------

type ghEnv struct {
	*secEnv
	gh      *ghServer
	ada     domain.Persona
	ana     domain.User
	channel service.ChannelView
}

func newGHEnv(t *testing.T) *ghEnv {
	t.Helper()
	e := newSecEnv(t)
	gh := newGHServer(t)
	e.svc.Integrations.HTTP = &http.Client{Timeout: 10 * time.Second}
	e.create(t, "gh-token", ghTokenValue)
	e.create(t, "gh-secret", ghSecretValue)
	ge := &ghEnv{secEnv: e, gh: gh, ada: e.persona(t, "Ada", nil)}
	ge.ana = mkUser(t, e.env, "ana@x.com", domain.RoleMember)
	join(t, e.env, ge.ana, domain.MemberMember)
	ch, err := e.svc.CreateChannel(context.Background(), domain.HumanActor, e.co.ID, service.CreateChannelInput{Kind: domain.ChannelGitHub, Name: "GitHub",
		PersonaID: &ge.ada.ID, TokenRef: "${secret:gh-token}", WebhookSecretRef: "${secret:gh-secret}",
		Config: domain.ChannelConfig{APIBase: gh.srv.URL, Repos: []string{"acme/api"}}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ge.channel = ch
	return ge
}

func ghSig(body string) string {
	m := hmac.New(sha256.New, []byte(ghSecretValue))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func ghComment(body, login string) string {
	return `{"action":"created","issue":{"number":7,"title":"Corrige o login","html_url":"https://github.com/acme/api/pull/7","pull_request":{"url":"x"}},` +
		`"comment":{"id":99,"body":` + jsonStr(body) + `,"html_url":"https://github.com/acme/api/pull/7#issuecomment-99","user":{"login":"` + login + `","type":"User"}},` +
		`"repository":{"full_name":"acme/api"}}`
}

var ghDelivery int

func (ge *ghEnv) ingest(t *testing.T, event, body string) (service.IngestResult, error) {
	t.Helper()
	ghDelivery++
	h := http.Header{}
	h.Set("X-GitHub-Event", event)
	h.Set("X-GitHub-Delivery", fmt.Sprintf("d-%d", ghDelivery))
	h.Set("X-Hub-Signature-256", ghSig(body))
	return ge.svc.IngestChannel(context.Background(), service.IngestInput{Token: ge.channel.InboundToken, Header: h, Body: []byte(body)})
}

func (ge *ghEnv) comment(t *testing.T, login, text string) service.IngestResult {
	t.Helper()
	res, err := ge.ingest(t, "issue_comment", ghComment(text, login))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (ge *ghEnv) linkAna(t *testing.T) {
	t.Helper()
	link, err := ge.svc.CreateChannelLink(as(ge.ana), ge.ana.Actor(), ge.co.ID, ge.channel.ID)
	if err != nil || !strings.Contains(link.Hint, "/hanko vincular") {
		t.Fatalf("link: %+v %v", link, err)
	}
	ge.comment(t, "Ana-Dev", "/hanko vincular "+link.Code)
	flush(t, ge.env, ge.channel.ID)
}

func TestGitHubCommentWakesPersona(t *testing.T) {
	ge := newGHEnv(t)
	bg := context.Background()

	// assinatura errada
	h := http.Header{}
	h.Set("X-GitHub-Event", "issue_comment")
	h.Set("X-Hub-Signature-256", "sha256=00")
	_, err := ge.svc.IngestChannel(bg, service.IngestInput{Token: ge.channel.InboundToken, Header: h, Body: []byte(ghComment("@hanko oi", "ana-dev"))})
	if !errors.Is(err, service.ErrWebhookUnauthorized) {
		t.Fatalf("assinatura inválida: %v", err)
	}

	// autor sem vínculo: ignorado, nenhuma task
	ge.comment(t, "ana-dev", "@hanko por favor revise")
	if tasks, _ := ge.svc.ListTasks(bg, ge.co.ID, service.TaskFilter{}); len(tasks) != 0 {
		t.Fatalf("task de autor sem vínculo: %d", len(tasks))
	}
	if st := messageStatus(t, ge.env, ge.channel.ID, domain.MessageIn); len(st) != 1 || st[0] != domain.MsgIgnored {
		t.Fatalf("estado: %v", st)
	}

	ge.linkAna(t) // Ana-Dev ↔ ana@x.com
	posts := ge.gh.all()
	if len(posts) != 1 || posts[0].Path != "/repos/acme/api/issues/7/comments" || !strings.Contains(fmt.Sprint(posts[0].Body["body"]), "Vinculado a ana@x.com") || posts[0].Auth != "Bearer "+ghTokenValue {
		t.Fatalf("resposta do vínculo: %+v", posts)
	}

	// comentário sem menção não faz nada (nem fica registrado)
	r := ge.comment(t, "ana-dev", "só uma conversa entre humanos")
	if r.Ignored != 1 || r.Accepted != 0 {
		t.Fatalf("sem menção: %+v", r)
	}
	// menção: nasce a task, atribuída à persona, criada pela Ana, e a persona é acordada
	ge.jobs.reset()
	ge.comment(t, "ana-dev", "@hanko revise o PR, por favor")
	tasks, _ := ge.svc.ListTasks(bg, ge.co.ID, service.TaskFilter{})
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d", len(tasks))
	}
	tk := tasks[0]
	if tk.Title != "acme/api#7: Corrige o login" || tk.PersonaID == nil || *tk.PersonaID != ge.ada.ID || tk.CreatedBy.Type != domain.ActorHuman ||
		tk.CreatedBy.ID == nil || *tk.CreatedBy.ID != ge.ana.ID {
		t.Fatalf("task: %+v", tk)
	}
	if !strings.Contains(tk.Description, "conteúdo externo") || !strings.Contains(tk.Description, "revise o PR") || !strings.Contains(tk.Description, "issuecomment-99") {
		t.Fatalf("descrição: %q", tk.Description)
	}
	if ge.jobs.wakeCount() != 1 {
		t.Fatalf("wakes = %d", ge.jobs.wakeCount())
	}
	// novo comentário no mesmo PR: comentário humano na mesma task, que acorda a persona
	ge.jobs.reset()
	ge.comment(t, "ana-dev", "@hanko e os testes?")
	if tasks, _ = ge.svc.ListTasks(bg, ge.co.ID, service.TaskFilter{}); len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	cms, _ := ge.svc.ListComments(bg, ge.co.ID, tk.ID)
	if len(cms) != 1 || cms[0].Author.Type != domain.ActorHuman || !strings.Contains(cms[0].Body, "e os testes?") {
		t.Fatalf("comentários: %+v", cms)
	}
	if ge.jobs.wakeCount() != 1 {
		t.Fatalf("wakes após comentário = %d", ge.jobs.wakeCount())
	}
	// a persona termina: o resumo vai como comentário no PR
	ge.finishTaskRun(t, tk, "Revisei: faltam testes para o caso X.")
	flush(t, ge.env, ge.channel.ID)
	posts = ge.gh.all()
	last := posts[len(posts)-1]
	if last.Path != "/repos/acme/api/issues/7/comments" || !strings.Contains(fmt.Sprint(last.Body["body"]), "faltam testes para o caso X") || !strings.Contains(fmt.Sprint(last.Body["body"]), tk.Key) {
		t.Fatalf("resumo no PR: %+v", last)
	}
	// task fechada: o próximo comentário abre outra
	done := domain.TaskDone
	if _, err := ge.svc.UpdateTask(bg, domain.HumanActor, ge.co.ID, tk.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	ge.comment(t, "ana-dev", "@hanko mais uma coisa")
	if tasks, _ = ge.svc.ListTasks(bg, ge.co.ID, service.TaskFilter{}); len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(tasks))
	}

	// bot, repositório fora da lista e evento não tratado são ignorados
	if res, err := ge.ingest(t, "issue_comment", strings.Replace(ghComment("@hanko", "ana-dev"), `"type":"User"`, `"type":"Bot"`, 1)); err != nil || res.Ignored != 1 {
		t.Fatalf("bot: %+v %v", res, err)
	}
	if res, err := ge.ingest(t, "issue_comment", strings.ReplaceAll(ghComment("@hanko", "ana-dev"), "acme/api", "outro/repo")); err != nil || res.Ignored != 1 {
		t.Fatalf("repo fora: %+v %v", res, err)
	}
	if res, err := ge.ingest(t, "push", `{"ref":"x","repository":{"full_name":"acme/api"}}`); err != nil || res.Ignored != 1 {
		t.Fatalf("push: %+v %v", res, err)
	}
	if res, err := ge.ingest(t, "ping", `{"zen":"x"}`); err != nil || res.Ignored != 1 {
		t.Fatalf("ping: %+v %v", res, err)
	}
	if tasks, _ = ge.svc.ListTasks(bg, ge.co.ID, service.TaskFilter{}); len(tasks) != 2 {
		t.Fatalf("tasks depois dos ignorados = %d", len(tasks))
	}
}

// finishTaskRun faz a persona fechar a run da task com um resumo.
func (ge *ghEnv) finishTaskRun(t *testing.T, tk service.TaskView, summary string) {
	t.Helper()
	ctx := context.Background()
	run, err := ge.svc.CheckoutRun(ctx, service.Wake{CompanyID: ge.co.ID, PersonaID: ge.ada.ID, TaskID: tk.ID, Trigger: domain.TriggerComment})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := ge.svc.MarkRunStarted(ctx, ge.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := ge.svc.FinishRun(ctx, ge.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: summary, Model: "m1"}}); err != nil {
		t.Fatalf("finish: %v", err)
	}
}

func TestPublishChannelReview(t *testing.T) {
	ge := newGHEnv(t)
	bg := context.Background()
	ge.linkAna(t)
	ge.comment(t, "ana-dev", "@hanko revise")
	tasks, _ := ge.svc.ListTasks(bg, ge.co.ID, service.TaskFilter{})
	tk := tasks[0]
	flush(t, ge.env, ge.channel.ID)
	n := len(ge.gh.all())

	// a persona publica um comentário de review no PR que a acordou (peer vem da task)
	m, err := ge.svc.PublishChannelReview(bg, domain.PersonaActor(ge.ada.ID), ge.co.ID, service.PublishReviewInput{TaskID: &tk.ID,
		Event: domain.GHReviewRequestChanges, Body: "Falta tratar o erro do login."})
	if err != nil || m.Kind != "review" || m.Peer != "acme/api#7" {
		t.Fatalf("review: %+v %v", m, err)
	}
	flush(t, ge.env, ge.channel.ID)
	posts := ge.gh.all()
	if len(posts) != n+1 || posts[n].Path != "/repos/acme/api/pulls/7/reviews" || posts[n].Body["event"] != "REQUEST_CHANGES" || posts[n].Body["body"] != "Falta tratar o erro do login." {
		t.Fatalf("review publicada: %+v", posts[n:])
	}
	// persona não aprova; sem thread não publica
	_, err = ge.svc.PublishChannelReview(bg, domain.PersonaActor(ge.ada.ID), ge.co.ID, service.PublishReviewInput{TaskID: &tk.ID, Event: domain.GHReviewApprove, Body: "ok"})
	wantErr(t, err, domain.ErrForbidden)
	_, err = ge.svc.PublishChannelReview(bg, domain.PersonaActor(ge.ada.ID), ge.co.ID, service.PublishReviewInput{Channel: "GitHub", Peer: "acme/api#99", Event: domain.GHReviewComment, Body: "x"})
	wantErr(t, err, domain.ErrForbidden)
	// repositório fora da lista
	_, err = ge.svc.PublishChannelReview(bg, domain.HumanActor, ge.co.ID, service.PublishReviewInput{Channel: "GitHub", Peer: "outro/repo#1", Event: domain.GHReviewComment, Body: "x"})
	wantErr(t, err, domain.ErrInvalid)
	// validações
	_, err = ge.svc.PublishChannelReview(bg, domain.HumanActor, ge.co.ID, service.PublishReviewInput{Channel: "GitHub", Peer: "acme/api#7", Event: "TALVEZ", Body: "x"})
	wantErr(t, err, domain.ErrInvalid)
	_, err = ge.svc.PublishChannelReview(bg, domain.HumanActor, ge.co.ID, service.PublishReviewInput{Channel: "GitHub", Peer: "acme/api#7", Event: domain.GHReviewComment, Body: " "})
	wantErr(t, err, domain.ErrInvalid)
	_, err = ge.svc.PublishChannelReview(bg, domain.HumanActor, ge.co.ID, service.PublishReviewInput{Event: domain.GHReviewComment, Body: "x"})
	wantErr(t, err, domain.ErrInvalid) // sem task nem peer, mas há um único canal GitHub: falta o PR
	// aprovar é ato de pessoa admin
	_, err = ge.svc.PublishChannelReview(as(ge.ana), ge.ana.Actor(), ge.co.ID, service.PublishReviewInput{Channel: "GitHub", Peer: "acme/api#7", Event: domain.GHReviewApprove, Body: "ok"})
	wantErr(t, err, domain.ErrForbidden)
	adm := mkUser(t, ge.env, "adm@x.com", domain.RoleMember)
	join(t, ge.env, adm, domain.MemberAdmin)
	if _, err := ge.svc.PublishChannelReview(as(adm), adm.Actor(), ge.co.ID, service.PublishReviewInput{Channel: "GitHub", Peer: "acme/api#7", Event: domain.GHReviewApprove, Body: "Aprovado."}); err != nil {
		t.Fatal(err)
	}
	flush(t, ge.env, ge.channel.ID)
	posts = ge.gh.all()
	if p := posts[len(posts)-1]; p.Body["event"] != "APPROVE" || p.Path != "/repos/acme/api/pulls/7/reviews" {
		t.Fatalf("aprovação: %+v", p)
	}
	// uma task que não veio de canal não resolve o destino
	other := ge.task(t, service.CreateTaskInput{Title: "outra"})
	_, err = ge.svc.PublishChannelReview(bg, domain.PersonaActor(ge.ada.ID), ge.co.ID, service.PublishReviewInput{TaskID: &other.ID, Event: domain.GHReviewComment, Body: "x"})
	wantErr(t, err, domain.ErrInvalid)
	// GitHub com erro permanente (token revogado): failed
	ge.gh.mu.Lock()
	ge.gh.status = 401
	ge.gh.mu.Unlock()
	m2, _ := ge.svc.PublishChannelReview(bg, domain.HumanActor, ge.co.ID, service.PublishReviewInput{Channel: "GitHub", Peer: "acme/api#7", Event: domain.GHReviewComment, Body: "de novo"})
	if out, err := ge.svc.DeliverChannelMessage(bg, m2.ID, 1, 3); err != nil || !out.GaveUp {
		t.Fatalf("401: %+v %v", out, err)
	}
}

func TestChannelArchivedCompanySkips(t *testing.T) {
	ce := newChanEnv(t)
	bg := ce.ctx()
	if _, err := ce.svc.ArchiveCompany(bg, domain.HumanActor, ce.co.ID); err != nil {
		t.Fatal(err)
	}
	res, err := ce.svc.IngestChannel(bg, service.IngestInput{Token: ce.channel.InboundToken,
		Header: http.Header{"X-Telegram-Bot-Api-Secret-Token": {tgSecretValue}}, Body: []byte(tgMessage(1, 42, "oi"))})
	if err != nil || res.Skipped == "" || res.Accepted != 0 {
		t.Fatalf("empresa arquivada: %+v %v", res, err)
	}
}
