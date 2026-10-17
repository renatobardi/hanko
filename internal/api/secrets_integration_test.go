//go:build integration

package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/settings"
)

const apiSentinel = "SENTINELA-api-valor-secreto-5521"

func TestAPISecretsNeverReturnValuesExceptOnce(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	base := e.base() + "/secrets"
	ana := e.persona(e.co, "Ana", nil)

	// criar com valor: o valor não volta
	var s api.Secret
	raw := e.want(201, "POST", base, `{"name":"gateway-token","description":"token do gateway","value":"`+apiSentinel+`"}`, &s)
	if s.Name != "gateway-token" || s.Reference != "${secret:gateway-token}" || s.Version != 1 || s.Provider != "local" || s.Revealed || s.Value != "" ||
		strings.Contains(string(raw), apiSentinel) {
		t.Fatalf("create: %s", raw)
	}
	var list api.ListBody[api.Secret]
	raw = e.want(200, "GET", base, "", &list)
	if len(list.Items) != 1 || strings.Contains(string(raw), apiSentinel) {
		t.Fatalf("list: %s", raw)
	}
	raw = e.want(200, "GET", base+"/gateway-token", "", &s)
	if strings.Contains(string(raw), apiSentinel) {
		t.Fatalf("get vazou: %s", raw)
	}

	// revelar: uma vez
	var rev api.Secret
	e.want(200, "POST", base+"/gateway-token/reveal", "", &rev)
	if rev.Value != apiSentinel || !rev.Revealed {
		t.Fatalf("reveal: %+v", rev)
	}
	e.want(409, "POST", base+"/gateway-token/reveal", "", nil)

	// gerar: o valor sai na criação, uma vez
	var gen api.Secret
	e.want(201, "POST", base, `{"name":"gerado","generate":true}`, &gen)
	if !strings.HasPrefix(gen.Value, "hk_") || !gen.Revealed {
		t.Fatalf("generate: %+v", gen)
	}
	raw = e.want(200, "GET", base+"/gerado", "", &s)
	if strings.Contains(string(raw), gen.Value) {
		t.Fatal("o GET devolveu o valor gerado")
	}

	// rotacionar
	e.want(200, "POST", base+"/gateway-token/rotate", `{"value":"outro-valor-qualquer"}`, &s)
	if s.Version != 2 || s.Revealed {
		t.Fatalf("rotate: %+v", s)
	}

	// validações e conflitos
	e.want(409, "POST", base, `{"name":"gateway-token","value":"x"}`, nil)
	for name, body := range map[string]string{
		"nome inválido": `{"name":"1x","value":"x"}`,
		"sem valor":     `{"name":"semvalor"}`,
		"cofre ausente": `{"name":"cofre","provider":"vault","external_ref":"a#b"}`,
	} {
		if status, b := e.call("POST", base, body, nil); status != 422 && status != 400 {
			t.Errorf("%s: status %d: %s", name, status, b)
		}
	}
	e.want(404, "GET", base+"/nao-existe", "", nil)

	// em uso por uma persona: apagar é 409 e force apaga
	e.want(201, "POST", e.base()+"/personas", `{"name":"Beto","agent":"fake","adapter_config":{"env":{"X_TOKEN":"${secret:gateway-token}"}}}`, nil)
	raw = e.want(200, "GET", base+"/gateway-token", "", &s)
	if len(s.UsedBy) != 1 || s.UsedBy[0].Kind != "persona" || s.UsedBy[0].Slug != "beto" {
		t.Fatalf("used_by: %s", raw)
	}
	e.want(409, "DELETE", base+"/gateway-token", "", nil)
	e.want(204, "DELETE", base+"/gateway-token?force=true", "", nil)

	// o env em claro de uma persona nova vira segredo e a API devolve só a referência
	var p api.Persona
	raw = e.want(201, "POST", e.base()+"/personas", `{"name":"Carla","agent":"fake","adapter_config":{"env":{"MY_TOKEN":"`+apiSentinel+`"}}}`, &p)
	if strings.Contains(string(raw), apiSentinel) || !strings.Contains(string(raw), "${secret:persona.carla.MY_TOKEN}") {
		t.Fatalf("persona: %s", raw)
	}

	// token de run (persona) nunca toca segredos
	hdr := e.personaToken(e.co, ana)
	for _, c := range []struct{ method, path, body string }{
		{"GET", base, ""}, {"POST", base, `{"name":"p","value":"x"}`}, {"POST", base + "/gerado/reveal", ""},
		{"POST", base + "/gerado/rotate", `{"value":"y"}`}, {"DELETE", base + "/gerado", ""},
		{"GET", e.base() + "/user-secrets", ""},
	} {
		if status, b := e.call(c.method, c.path, c.body, hdr); status != 403 {
			t.Errorf("%s %s com token de persona: %d %s", c.method, c.path, status, b)
		}
	}

	// segredo de usuário exige pessoa autenticada (aqui, modo trusted: sem identidade)
	if status, b := e.call("POST", e.base()+"/user-secrets", `{"name":"pessoal","value":"x"}`, nil); status != 422 {
		t.Errorf("user-secret sem identidade: %d %s", status, b)
	}
}

func TestAPIInstanceSettings(t *testing.T) {
	e := newAPIEnv(t)
	m := settings.NewManager(map[string]any{settings.AlertsSilentFactor: int64(3), settings.ACPAllow: true, settings.ArtifactsMaxBytes: int64(5 << 20),
		settings.ArtifactsQuotaBytes: int64(512 << 20), settings.AlertsRunStuckAfter: 15 * time.Minute}, map[string]bool{"HANKO_ACP_ALLOW": true})
	e.svc.Settings = m
	e.svc.Integrations.Settings = m

	var list api.ListBody[api.InstanceSetting]
	e.want(200, "GET", "/api/v1/instance/settings", "", &list)
	byKey := map[string]api.InstanceSetting{}
	for _, s := range list.Items {
		byKey[s.Key] = s
	}
	if len(byKey) != len(settings.Defs) || !byKey["acp.allow"].Locked || byKey["acp.allow"].Source != "env" || byKey["alerts.silent_factor"].Source != "default" {
		t.Fatalf("settings: %+v", byKey)
	}
	for _, s := range list.Items {
		if settings.SensitiveName(s.Key) {
			t.Errorf("chave sensível exposta: %s", s.Key)
		}
	}

	var got api.InstanceSetting
	e.want(200, "PUT", "/api/v1/instance/settings/alerts.silent_factor", `{"value":9}`, &got)
	if got.Source != "db" || got.Value != float64(9) {
		t.Fatalf("set: %+v", got)
	}
	e.want(409, "PUT", "/api/v1/instance/settings/acp.allow", `{"value":false}`, nil) // travada por env
	for _, c := range []struct{ key, body string }{{"alerts.silent_factor", `{"value":0}`}, {"nao.existe", `{"value":1}`}, {"acp.commands", `{"value":"x"}`}} {
		if status, b := e.call("PUT", "/api/v1/instance/settings/"+c.key, c.body, nil); status != 422 {
			t.Errorf("%s: %d %s", c.key, status, b)
		}
	}
	e.want(200, "DELETE", "/api/v1/instance/settings/alerts.silent_factor", "", &got)
	if got.Source != "default" || got.Value != float64(3) {
		t.Fatalf("unset: %+v", got)
	}
	var audit api.ListBody[api.InstanceSettingAudit]
	e.want(200, "GET", "/api/v1/instance/settings-audit", "", &audit)
	if len(audit.Items) != 2 || audit.Items[0].Key != "alerts.silent_factor" {
		t.Fatalf("audit: %+v", audit.Items)
	}

	var bs api.BlobStatus
	e.want(200, "GET", "/api/v1/instance/blobs", "", &bs)
	if bs.Active != "postgres" || len(bs.Available) != 1 {
		t.Fatalf("blobs: %+v", bs)
	}
	_ = service.DefaultArtifactMaxBytes
}
