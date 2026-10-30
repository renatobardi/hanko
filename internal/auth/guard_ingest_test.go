package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// A chave de ingestão só vale no listener de ingestão (ADR-0076 item 3): na API pública e no /mcp ela é recusada, e
// a chave full do mesmo usuário continua passando.
func TestGuardIngestKeyStaysOnTheIngestListener(t *testing.T) {
	ana := domain.User{ID: domain.NewID(), Email: "ana@x.com"}
	res := scopedResolver{
		fakeResolver: fakeResolver{keys: map[string]domain.User{"hk_ingest": ana, "hk_full": ana}},
		scope: map[string]domain.KeyScope{
			"hk_ingest": domain.KeyScopeIngest,
			"hk_full":   domain.KeyScopeFull,
		},
	}
	h, _ := guarded(res)
	do := func(method, path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, c := range []struct {
		method, path, key string
		want              int
	}{
		{http.MethodGet, "/api/v1/companies", "hk_ingest", http.StatusUnauthorized},
		{http.MethodPost, "/api/v1/companies", "hk_ingest", http.StatusUnauthorized},
		{http.MethodPost, "/mcp", "hk_ingest", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/companies", "hk_full", http.StatusOK},
	} {
		if got := do(c.method, c.path, c.key).Code; got != c.want {
			t.Errorf("%s %s com %s = %d, quero %d", c.method, c.path, c.key, got, c.want)
		}
	}
	if body := do(http.MethodGet, "/api/v1/companies", "hk_ingest").Body.String(); !strings.Contains(body, "listener de ingestão") {
		t.Errorf("a recusa deveria dizer onde a chave vale: %s", body)
	}
}

func TestKeyScopeIngestIsValidAndAllowsNothingOnThePublicAPI(t *testing.T) {
	if !domain.KeyScopeIngest.Valid() {
		t.Fatal("o escopo ingest deveria ser conhecido")
	}
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		if domain.KeyScopeIngest.Allows(m, "/api/v1/companies") {
			t.Errorf("ingest liberou %s na API pública", m)
		}
	}
	if domain.KeyScopeIngest.Allows(http.MethodPost, "/mcp") {
		t.Error("ingest liberou /mcp")
	}
}
