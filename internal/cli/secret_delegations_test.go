package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

const delegTarget = "0198f2a0-ffff-7000-8000-000000000002"

var delegation1 = api.SecretDelegation{ID: "0198f2a0-eeee-7000-8000-000000000001", Secret: "gw", TargetType: "task", TargetID: delegTarget,
	ExpiresAt: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), Active: true, CreatedAt: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)}

func TestDelegationCreateSendsTargetAndTTLAndNeverTheValue(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST /api/v1/companies/acme/user-secrets/gw/delegations": jsonResp(201, delegation1)})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "delegation", "create", "gw", "--target-type", "task", "--target", delegTarget, "--expires", "168h")
	if code != 0 || !strings.Contains(out, "Delegação criada: gw para task "+delegTarget) {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	h := f.last()
	if h.Method != "POST" || !strings.Contains(h.Body, `"target_type":"task"`) || !strings.Contains(h.Body, `"target_id":"`+delegTarget+`"`) ||
		!strings.Contains(h.Body, `"expires_in_seconds":604800`) {
		t.Fatalf("corpo enviado: %+v", h)
	}
}

func TestDelegationCreateWithoutExpiresLeavesTheServerDefault(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST /api/v1/companies/acme/user-secrets/gw/delegations": jsonResp(201, delegation1)})
	if code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "delegation", "create", "gw", "--target-type", "task", "--target", delegTarget); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if b := f.last().Body; strings.Contains(b, "expires_in_seconds") {
		t.Fatalf("sem --expires o corpo não leva prazo (o servidor põe 24 h): %s", b)
	}
}

func TestDelegationCreateRefusesMoreThanSevenDaysBeforeCallingTheAPI(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{})
	code, _, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "delegation", "create", "gw", "--target-type", "task", "--target", delegTarget, "--expires", "169h")
	if code != 2 || !strings.Contains(errs, "prazo inválido") {
		t.Fatalf("%d %q", code, errs)
	}
	if len(f.hits) != 0 {
		t.Fatalf("a CLI não pode chamar a API com prazo acima de 7 dias: %+v", f.hits)
	}
}

func TestDelegationLsShowsStateAndRevokeRemovesIt(t *testing.T) {
	revoked := delegation1
	revoked.ID, revoked.RevokedAt, revoked.Active = "0198f2a0-eeee-7000-8000-000000000009", &revoked.CreatedAt, false
	expired := delegation1 // vencida: não foi revogada, mas já não vale
	expired.ID, expired.Active = "0198f2a0-eeee-7000-8000-00000000000a", false
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/me/delegations": jsonResp(200, list(delegation1, revoked, expired)),
		"DELETE /api/v1/companies/acme/me/delegations/" + delegation1.ID: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "delegation", "ls")
	if code != 0 || !strings.Contains(out, "ativa") || !strings.Contains(out, "revogada") || !strings.Contains(out, "inativa") ||
		!strings.Contains(out, "task "+delegTarget) {
		t.Fatalf("ls: %d %q %q", code, out, errs)
	}
	code, out, errs = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "delegation", "revoke", delegation1.ID)
	if code != 0 || !strings.Contains(out, "Delegação "+delegation1.ID+" revogada.") || f.last().Method != "DELETE" {
		t.Fatalf("revoke: %d %q %q %+v", code, out, errs, f.last())
	}
}

func TestDelegationLsEmpty(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/me/delegations": jsonResp(200, list[api.SecretDelegation]())})
	if code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "delegation", "ls"); code != 0 || !strings.Contains(out, "Nenhuma delegação.") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestDelegationAPIErrorsFailTheCommandWithoutPrintingSuccess(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/user-secrets/gw/delegations":        jsonResp(422, map[string]string{"title": "alvo inválido"}),
		"DELETE /api/v1/companies/acme/me/delegations/" + delegation1.ID: jsonResp(404, map[string]string{"title": "não encontrada"}),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "delegation", "create", "gw", "--target-type", "task", "--target", delegTarget)
	if code == 0 || strings.Contains(out, "Delegação criada") {
		t.Fatalf("create com erro da API: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "delegation", "revoke", delegation1.ID)
	if code == 0 || strings.Contains(out, "revogada") {
		t.Fatalf("revoke com erro da API: %d %q", code, out)
	}
}
