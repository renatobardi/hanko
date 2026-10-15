package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

const (
	inviteID = "0198f2a0-0000-7000-8000-0000000000aa"
)

func TestMemberList(t *testing.T) {
	ana := api.Member{UserID: "u1", Email: "ana@x.com", Name: "Ana", Role: "owner", CreatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	inv := api.Invite{ID: inviteID, Email: "novo@x.com", Role: "viewer", ExpiresAt: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/members": jsonResp(200, list(ana)),
		"GET /api/v1/companies/acme/invites": jsonResp(200, list(inv)),
	})
	code, out, errOut := exec(t, Runtime{}, nil, "", "--url", ts.URL, "-c", "acme", "member", "list")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	for _, want := range []string{"ana@x.com", "owner", "Convites abertos", "novo@x.com", "0198f2a0"} {
		if !strings.Contains(out, want) {
			t.Errorf("saída sem %q:\n%s", want, out)
		}
	}
	_, js, _ := exec(t, Runtime{}, nil, "", "--url", ts.URL, "-c", "acme", "--json", "member", "list")
	if !strings.Contains(js, `"members"`) || !strings.Contains(js, `"invites"`) {
		t.Fatalf("%s", js)
	}
}

func TestMemberListSemPermissaoParaConvites(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/members": jsonResp(200, list(api.Member{Email: "ana@x.com", Role: "member"})),
		"GET /api/v1/companies/acme/invites": jsonResp(403, map[string]any{"title": "Proibido", "status": 403, "detail": "papel insuficiente"}),
	})
	code, out, errOut := exec(t, Runtime{}, nil, "", "--url", ts.URL, "-c", "acme", "member", "list")
	if code != 0 || !strings.Contains(out, "ana@x.com") || strings.Contains(out, "Convites abertos") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
}

func TestMemberInvite(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/invites": jsonResp(201, api.Invite{ID: inviteID, Email: "novo@x.com", Role: "member",
			ExpiresAt: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), Token: "TOK", Path: "/convite/TOK"}),
	})
	code, out, errOut := exec(t, Runtime{}, nil, "", "--url", ts.URL, "-c", "acme", "member", "invite", "novo@x.com", "--role", "member")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if !strings.Contains(out, ts.URL+"/convite/TOK") || !strings.Contains(out, "não envia e-mail") && !strings.Contains(out, "só aparece agora") {
		t.Fatalf("link ausente:\n%s", out)
	}
	if b := f.last().Body; !strings.Contains(b, `"email":"novo@x.com"`) || !strings.Contains(b, `"role":"member"`) {
		t.Fatalf("corpo: %s", b)
	}
	code, _, errOut = exec(t, Runtime{}, nil, "", "--url", ts.URL, "-c", "acme", "member", "invite", "x@x.com", "--role", "rei")
	if code != 2 || !strings.Contains(errOut, "papel inválido") {
		t.Fatalf("papel inválido: %d %s", code, errOut)
	}
}

func TestMemberRoleRemoveRevoke(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/companies/acme/members/ana@x.com":    jsonResp(200, api.Member{Email: "ana@x.com", Role: "admin"}),
		"DELETE /api/v1/companies/acme/members/ana@x.com":   jsonResp(204, nil),
		"GET /api/v1/companies/acme/invites":                jsonResp(200, list(api.Invite{ID: inviteID, Email: "novo@x.com"})),
		"DELETE /api/v1/companies/acme/invites/" + inviteID: jsonResp(204, nil),
	})
	base := []string{"--url", ts.URL, "-c", "acme", "member"}
	code, out, errOut := exec(t, Runtime{}, nil, "", append(base, "role", "ana@x.com", "admin")...)
	if code != 0 || !strings.Contains(out, "ana@x.com agora é admin") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if !strings.Contains(f.last().Body, `"role":"admin"`) {
		t.Fatalf("%s", f.last().Body)
	}
	if code, _, _ := exec(t, Runtime{}, nil, "", append(base, "role", "ana@x.com", "rei")...); code != 2 {
		t.Fatalf("papel inválido: %d", code)
	}
	if code, out, errOut := exec(t, Runtime{}, nil, "", append(base, "remove", "ana@x.com")...); code != 0 || !strings.Contains(out, "removido") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if code, out, errOut := exec(t, Runtime{}, nil, "", append(base, "revoke", "0198f2a0")...); code != 0 || !strings.Contains(out, "revogado") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if code, _, errOut := exec(t, Runtime{}, nil, "", append(base, "revoke", "ffffffff")...); code != 1 || !strings.Contains(errOut, "não encontrado") {
		t.Fatalf("%d %s", code, errOut)
	}
	// o servidor recusa (último owner): o erro chega ao usuário e o código é 1
	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"DELETE /api/v1/companies/acme/members/ana@x.com": jsonResp(409, map[string]any{"title": "Conflito", "status": 409, "detail": "a empresa precisa de pelo menos um owner"}),
	})
	code, _, errOut = exec(t, Runtime{}, nil, "", "--url", ts2.URL, "-c", "acme", "member", "remove", "ana@x.com")
	if code != 1 || !strings.Contains(errOut, "pelo menos um owner") {
		t.Fatalf("%d %s", code, errOut)
	}
}
