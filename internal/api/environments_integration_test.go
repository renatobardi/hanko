//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/secretbox"
)

const apiEnvKey = "-----BEGIN OPENSSH PRIVATE KEY-----\\nchave-privada-da-api\\n-----END OPENSSH PRIVATE KEY-----"

func TestAPIEnvironments(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	e.svc.Environments = execenv.Config{SSHAllowHosts: []string{".example.com"}, Sandbox: "docker", RemoteURL: "https://hanko.example.com",
		LookPath: func(s string) (string, error) { return s, nil }}
	base := e.base() + "/environments"
	e.persona(e.co, "Ana", nil)

	var ev api.Environment
	raw := e.want(201, "POST", base, `{"name":"Build","driver":"ssh","private_key":"`+apiEnvKey+`",
		"ssh":{"host":"build.example.com","username":"hanko","remote_workspace_root":"/srv/hanko","known_hosts":"build.example.com ssh-ed25519 AAAA"},
		"env":{"API_TOKEN":"segredo-da-api"}}`, &ev)
	if ev.Slug != "build" || ev.Driver != "ssh" || !ev.KeySet || !ev.Available || ev.SSH == nil || ev.SSH.Port != 22 || len(ev.EnvNames) != 1 {
		t.Fatalf("criado: %s", raw)
	}
	if strings.Contains(string(raw), "chave-privada") || strings.Contains(string(raw), "segredo-da-api") || strings.Contains(string(raw), "private_key") {
		t.Fatalf("segredo na resposta: %s", raw)
	}
	e.want(409, "POST", base, `{"name":"Build","driver":"sandbox","sandbox":{"image":"alpine:3"}}`, nil)
	for name, body := range map[string]string{
		"sem known_hosts": `{"name":"A","driver":"ssh","private_key":"` + apiEnvKey + `","ssh":{"host":"a.example.com","username":"u","remote_workspace_root":"/r","known_hosts":""}}`,
		"host fora":       `{"name":"B","driver":"ssh","private_key":"` + apiEnvKey + `","ssh":{"host":"evil.com","username":"u","remote_workspace_root":"/r","known_hosts":"evil.com ssh-ed25519 A"}}`,
		"driver docker":   `{"name":"C","driver":"docker"}`,
		"slug reservado":  `{"name":"Probe","driver":"sandbox","sandbox":{"image":"alpine:3"}}`,
	} {
		if status, b := e.call("POST", base, body, nil); status != 422 && status != 400 {
			t.Errorf("%s: status %d: %s", name, status, b)
		}
	}
	var sb api.Environment
	e.want(201, "POST", base, `{"name":"Caixa","driver":"sandbox","sandbox":{"image":"ghcr.io/acme/agente:1","network":"bridge"}}`, &sb)
	if sb.Sandbox == nil || sb.Sandbox.Network != "bridge" || sb.Sandbox.MemoryMiB != 2048 || len(sb.Warnings) == 0 {
		t.Fatalf("sandbox: %+v", sb)
	}

	var list api.ListBody[api.Environment]
	e.want(200, "GET", base, "", &list)
	if len(list.Items) != 2 {
		t.Fatalf("lista: %+v", list.Items)
	}
	e.want(200, "GET", base+"/build", "", &ev)
	e.want(404, "GET", base+"/nao-existe", "", nil)

	// padrão e persona
	e.want(204, "PUT", base+"/default", `{"environment":"build"}`, nil)
	e.want(200, "GET", base+"/build", "", &ev)
	if !ev.IsDefault {
		t.Fatal("padrão não marcado")
	}
	var p api.Persona
	e.want(200, "PATCH", e.base()+"/personas/ana", `{"environment":"caixa"}`, &p)
	if p.EnvironmentID != sb.ID {
		t.Fatalf("ambiente da persona: %+v", p)
	}
	e.want(200, "GET", base+"/caixa", "", &sb)
	if len(sb.Personas) != 1 || sb.Personas[0].Slug != "ana" {
		t.Fatalf("personas do ambiente: %+v", sb.Personas)
	}
	e.want(404, "PATCH", e.base()+"/personas/ana", `{"environment":"nao-existe"}`, nil)

	// editar: troca a variável, a chave fica
	e.want(200, "PATCH", base+"/build", `{"description":"farm","set_env":{"OUTRA":"x"},"drop_env":["API_TOKEN"]}`, &ev)
	if ev.Description != "farm" || !ev.KeySet || len(ev.EnvNames) != 1 || ev.EnvNames[0] != "OUTRA" {
		t.Fatalf("patch: %+v", ev)
	}

	// arquivar move as personas para o padrão
	var arch api.Environment
	e.want(200, "DELETE", base+"/caixa?move_personas_to=local", "", &arch)
	if arch.Status != "archived" {
		t.Fatalf("arquivar: %+v", arch)
	}
	e.want(200, "GET", e.base()+"/personas/ana", "", &p)
	if p.EnvironmentID != "" {
		t.Fatalf("persona deveria voltar ao padrão: %+v", p.EnvironmentID)
	}
	e.want(422, "PATCH", e.base()+"/personas/ana", `{"environment":"caixa"}`, nil)

	// usos (nenhum) e teste de rascunho com o SSH desligado pelo operador
	var leases api.ListBody[api.EnvironmentLease]
	e.want(200, "GET", base+"/build/leases", "", &leases)
	if len(leases.Items) != 0 {
		t.Fatalf("usos: %+v", leases.Items)
	}
	e.svc.Environments.SSHAllowHosts = nil
	if status, b := e.call("POST", base+"/probe", `{"name":"R","driver":"ssh","private_key":"`+apiEnvKey+`",
		"ssh":{"host":"build.example.com","username":"hanko","remote_workspace_root":"/srv","known_hosts":"build.example.com ssh-ed25519 A"}}`, nil); status != 422 {
		t.Fatalf("rascunho com ssh desligado: %d %s", status, b)
	}
	e.want(200, "GET", base+"/build", "", &ev)
	if ev.Available || !strings.Contains(ev.Unavailable, "HANKO_ENV_SSH_ALLOW_HOSTS") {
		t.Fatalf("indisponível: %+v", ev)
	}
	e.want(204, "PUT", base+"/default", `{"environment":"local"}`, nil)

	// persona com run token não usa os endpoints
	tok := e.personaToken(e.co, e.persona(e.co, "Bia", nil))
	if status, _ := e.call("GET", base, "", tok); status != 403 {
		t.Fatalf("run token listou ambientes: %d", status)
	}
}
