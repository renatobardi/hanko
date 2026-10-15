//go:build integration

package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

type authEnv struct {
	svc  *service.Service
	pool *pgxpool.Pool
	url  string
	co   domain.Company
	keys map[string]string // rótulo -> API key
}

// setupAuth monta a API real em modo authenticated (Guard + service) com usuários de cada papel.
func setupAuth(t *testing.T) *authEnv {
	t.Helper()
	url := os.Getenv("HANKO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definido")
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
	sdb, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(sdb, "."); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	svc := service.New(pool, noopJobs{}, broker)
	mux := http.NewServeMux()
	api.Mount(mux, api.Deps{Svc: svc, Broker: broker, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "it", DB: pool, Authenticated: true})
	ts := httptest.NewServer(auth.Guard(svc, nil)(mux))
	t.Cleanup(ts.Close)

	e := &authEnv{svc: svc, pool: pool, url: ts.URL, keys: map[string]string{}}
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	e.co = co
	mk := func(label string, inst domain.UserRole, member domain.MemberRole) {
		id := domain.NewID()
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name, provider, password_hash, role) VALUES ($1,$2,$2,'local','x',$3)`,
			id, label+"@x.com", string(inst)); err != nil {
			t.Fatal(err)
		}
		if member != "" {
			if _, err := pool.Exec(ctx, `INSERT INTO company_members (company_id, user_id, role) VALUES ($1,$2,$3)`, co.ID, id, string(member)); err != nil {
				t.Fatal(err)
			}
		}
		key, _, err := svc.CreateAPIKey(ctx, id, "k")
		if err != nil {
			t.Fatal(err)
		}
		e.keys[label] = key
	}
	mk("owner", domain.RoleMember, domain.MemberOwner)
	mk("admin", domain.RoleMember, domain.MemberAdmin)
	mk("member", domain.RoleMember, domain.MemberMember)
	mk("viewer", domain.RoleMember, domain.MemberViewer)
	mk("stranger", domain.RoleMember, "")
	mk("install", domain.RoleOwner, "")
	return e
}

func (e *authEnv) call(t *testing.T, who, method, path, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.url+"/api/v1"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.keys[who])
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAPIAutorizacaoPorPapel(t *testing.T) {
	e := setupAuth(t)
	c := "/companies/" + e.co.Slug
	want := func(who, method, path, body string, status int) {
		t.Helper()
		if got, resp := e.call(t, who, method, path, body); got != status {
			t.Errorf("%s %s %s como %s: %d, esperado %d: %s", method, path, body, who, got, status, resp)
		}
	}
	// leitura: todo papel; estranho recebe 404, nunca 403
	for _, who := range []string{"owner", "admin", "member", "viewer", "install"} {
		want(who, "GET", c+"/tasks", "", 200)
		want(who, "GET", c+"/members", "", 200)
	}
	want("stranger", "GET", c, "", 404)
	want("stranger", "GET", c+"/tasks", "", 404)
	want("stranger", "GET", c+"/members", "", 404)
	want("stranger", "POST", c+"/tasks", `{"title":"x"}`, 404)
	if code, body := e.call(t, "stranger", "GET", "/companies", ""); code != 200 || strings.Contains(body, e.co.Slug) {
		t.Errorf("lista de empresas do estranho: %d %s", code, body)
	}
	if code, body := e.call(t, "install", "GET", "/companies", ""); code != 200 || !strings.Contains(body, e.co.Slug) {
		t.Errorf("lista de empresas do dono da instalação: %d %s", code, body)
	}
	// escrita de tasks: member+
	want("viewer", "POST", c+"/tasks", `{"title":"x"}`, 403)
	want("member", "POST", c+"/tasks", `{"title":"x"}`, 201)
	// personas/assinaturas: admin+
	want("member", "POST", c+"/subscriptions", `{"name":"S1","agent":"claude","monthly_usd":20}`, 403)
	want("admin", "POST", c+"/subscriptions", `{"name":"S1","agent":"claude","monthly_usd":20}`, 201)
	// membros
	want("member", "GET", c+"/invites", "", 403)
	want("viewer", "POST", c+"/invites", `{"email":"n@x.com","role":"viewer"}`, 403)
	want("admin", "POST", c+"/invites", `{"email":"n@x.com","role":"admin"}`, 403)
	want("admin", "POST", c+"/invites", `{"email":"n@x.com","role":"viewer"}`, 201)
	want("owner", "POST", c+"/invites", `{"email":"nao-e-email","role":"viewer"}`, 422)
	want("owner", "PATCH", c+"/members/viewer@x.com", `{"role":"member"}`, 200)
	want("owner", "PATCH", c+"/members/viewer@x.com", `{"role":"rei"}`, 422)
	want("admin", "PATCH", c+"/members/owner@x.com", `{"role":"viewer"}`, 403)
	want("owner", "DELETE", c+"/members/ninguem@x.com", "", 404)
	// último owner
	want("owner", "PATCH", c+"/members/owner@x.com", `{"role":"admin"}`, 409)
	want("owner", "DELETE", c+"/members/owner@x.com", "", 409)
	want("owner", "DELETE", c+"/members/member@x.com", "", 204)
	// o removido virou estranho
	want("member", "GET", c+"/tasks", "", 404)
}

func TestAPIConviteFluxo(t *testing.T) {
	e := setupAuth(t)
	c := "/companies/" + e.co.Slug
	code, body := e.call(t, "owner", "POST", c+"/invites", `{"email":"Novo@Exemplo.com","role":"member"}`)
	if code != 201 {
		t.Fatalf("%d %s", code, body)
	}
	var inv api.Invite
	if err := json.Unmarshal([]byte(body), &inv); err != nil {
		t.Fatal(err)
	}
	if inv.Email != "novo@exemplo.com" || inv.Token == "" || inv.Path != "/convite/"+inv.Token {
		t.Fatalf("convite: %+v", inv)
	}
	// a listagem não devolve o token
	code, body = e.call(t, "owner", "GET", c+"/invites", "")
	if code != 200 || strings.Contains(body, inv.Token) || !strings.Contains(body, "novo@exemplo.com") {
		t.Fatalf("listagem: %d %s", code, body)
	}
	if code, _ := e.call(t, "owner", "DELETE", c+"/invites/"+inv.ID, ""); code != 204 {
		t.Fatalf("revogar: %d", code)
	}
	if code, _ := e.call(t, "owner", "DELETE", c+"/invites/"+inv.ID, ""); code != 404 {
		t.Fatalf("revogar de novo: %d", code)
	}
	if code, _ := e.call(t, "owner", "DELETE", c+"/invites/lixo", ""); code != 422 && code != 404 {
		t.Fatalf("id lixo: %d", code)
	}
	if _, err := e.svc.PreviewInvite(context.Background(), inv.Token); err == nil {
		t.Fatal("convite revogado ainda vale")
	}
}
