//go:build integration

package heartbeat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/heartbeat"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// TestChannelJobsEndToEnd: pela fila de verdade, uma mensagem do Telegram vira o job channel_inbound (vínculo da pessoa), um
// gate vira o job channel_send com botões, e o clique no botão (outro channel_inbound) decide o gate como a pessoa.
// Espera por condição (com prazo largo), nunca por tempo fixo.
func TestChannelJobsEndToEnd(t *testing.T) {
	url := os.Getenv("HANKO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("HANKO_TEST_DATABASE_URL vazio")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, stdlib.OpenDBFromPool(pool), "."); err != nil {
		t.Fatalf("goose: %v", err)
	}
	if err := heartbeat.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	// Bot API falsa
	var mu sync.Mutex
	var sent []map[string]any
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sent = append(sent, body)
		}
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	t.Cleanup(tg.Close)
	texts := func() []string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for _, m := range sent {
			out = append(out, fmt.Sprint(m["text"]))
		}
		return out
	}
	callbacks := func() []string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for _, m := range sent {
			if rm, ok := m["reply_markup"].(map[string]any); ok {
				for _, row := range rm["inline_keyboard"].([]any) {
					for _, b := range row.([]any) {
						if d, ok := b.(map[string]any)["callback_data"].(string); ok {
							out = append(out, d)
						}
					}
				}
			}
		}
		return out
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("esperando %s; textos enviados: %q", what, texts())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	broker := events.NewBroker()
	hb, err := heartbeat.New(pool, heartbeat.Options{Workers: 2, PeriodicInterval: time.Minute, RunTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-chave-de-teste", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations.Secrets = box
	svc.Integrations.HTTP = http.DefaultClient
	hb.Attach(svc, adapter.Registry{"fake": fake.New(fake.Options{})}, memory.Noop{}, broker,
		auth.RunTokens{Secret: []byte("integration-secret-0123456789")})
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})

	uid := domain.NewID()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name, provider, password_hash, role) VALUES ($1,'ana@x.com','Ana','local','x','member')`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO company_members (company_id, user_id, role) VALUES ($1,$2,'member')`, co.ID, uid); err != nil {
		t.Fatal(err)
	}
	ana, err := svc.GetUserByEmail(ctx, "ana@x.com")
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"tg-token": "9:TOKEN", "tg-secret": "whsec"} {
		if _, err := svc.CreateSecret(ctx, domain.HumanActor, co.ID, service.CreateSecretInput{Name: name, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	ch, err := svc.CreateChannel(ctx, domain.HumanActor, co.ID, service.CreateChannelInput{Kind: domain.ChannelTelegram, Name: "Bot", Enabled: true,
		TokenRef: "${secret:tg-token}", WebhookSecretRef: "${secret:tg-secret}", Config: domain.ChannelConfig{APIBase: tg.URL}})
	if err != nil {
		t.Fatal(err)
	}
	ingest := func(body string) {
		t.Helper()
		if _, err := svc.IngestChannel(ctx, service.IngestInput{Token: ch.InboundToken, Header: http.Header{"X-Telegram-Bot-Api-Secret-Token": {"whsec"}}, Body: []byte(body)}); err != nil {
			t.Fatal(err)
		}
	}

	// vínculo: a entrada vira um job, a resposta vira outro
	aCtx := auth.WithIdentity(ctx, auth.Identity{User: ana, Via: auth.ViaSession})
	link, err := svc.CreateChannelLink(aCtx, ana.Actor(), co.ID, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	ingest(fmt.Sprintf(`{"update_id":1,"message":{"message_id":1,"from":{"id":42,"first_name":"Ana"},"chat":{"id":42,"type":"private"},"text":"/vincular %s"}}`, link.Code))
	waitFor("a confirmação do vínculo", func() bool {
		for _, s := range texts() {
			if strings.Contains(s, "Vinculado a Ana") || strings.Contains(s, "Vinculado a ana@x.com") {
				return true
			}
		}
		return false
	})

	// gate: o aviso com botões sai pela fila
	g, err := svc.RequestGate(ctx, domain.HumanActor, co.ID, service.RequestGateInput{Title: "Publicar a release?"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor("o aviso do gate", func() bool { return len(callbacks()) == 2 })
	if cb := callbacks(); cb[0] != "g|"+g.ID.String()+"|a" {
		t.Fatalf("botões: %v", cb)
	}
	// o clique decide o gate como a pessoa
	ingest(fmt.Sprintf(`{"update_id":2,"callback_query":{"id":"cb1","from":{"id":42,"first_name":"Ana"},"message":{"message_id":9,"chat":{"id":42,"type":"private"}},"data":%q}}`, "g|"+g.ID.String()+"|a"))
	waitFor("o gate aprovado", func() bool {
		got, err := svc.GetGate(ctx, co.ID, g.ID)
		return err == nil && got.Status == domain.GateApproved
	})
	got, _ := svc.GetGate(ctx, co.ID, g.ID)
	if got.DecidedBy == nil || got.DecidedBy.ID == nil || *got.DecidedBy.ID != uid {
		t.Fatalf("decidido por %+v", got.DecidedBy)
	}
	// todas as mensagens de saída chegaram ao estado final
	waitFor("as entregas terminarem", func() bool {
		ms, err := svc.ListChannelMessages(ctx, co.ID, ch.ID, 50)
		if err != nil {
			return false
		}
		for _, m := range ms {
			if m.Direction == domain.MessageOut && m.Status == domain.MsgPending || m.Direction == domain.MessageIn && m.Status == domain.MsgReceived {
				return false
			}
		}
		return len(ms) >= 5
	})
}
