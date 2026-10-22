//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/hooks"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/webhook"
)

const m19Key = "0123456789abcdef0123456789abcdef-chave-de-teste"

// hooksServer monta as rotas públicas de entrada (webhooks de fluxo e de canal) sobre o mesmo service.
func hooksServer(t *testing.T, svc *service.Service) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(hooks.FlowPattern, hooks.Flows(svc, nil))
	mux.Handle(hooks.ChannelPattern, hooks.Channels(svc, nil))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func postRaw(t *testing.T, url string, hdr map[string]string, body string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestAPIFlowVariablesTriggersAndHooks(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New(m19Key, "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	p := e.persona(e.co, "Ada", nil)
	base := e.base()
	hs := hooksServer(t, e.svc)

	// fluxo sem agenda, com variáveis
	var f api.Flow
	e.want(201, "POST", base+"/flows", `{"name":"Revisar PR","persona":"`+p.Slug+`","task_title":"Revisar {{repo}} ({{disparo}})","task_description":"Em {{ambiente}}",`+
		`"variables":[{"name":"repo","required":true},{"name":"ambiente","default":"staging"}]}`, &f)
	if f.Cron != "" || f.NextFireAt != nil || len(f.Variables) != 2 || !f.Variables[0].Required || f.Revision != 1 {
		t.Fatalf("fluxo: %+v", f)
	}
	if status, body := e.call("POST", base+"/flows", `{"name":"Ruim","persona":"`+p.Slug+`","task_title":"{{naodeclarada}}"}`, nil); status != 422 {
		t.Fatalf("modelo com variável não declarada: %d %s", status, body)
	}
	// rodar agora: sem a obrigatória é 422; com o valor, a task nasce com o título renderizado
	if status, body := e.call("POST", base+"/flows/revisar-pr/run", `{"variables":{"ambiente":"prod"}}`, nil); status != 422 || !strings.Contains(string(body), "repo") {
		t.Fatalf("run sem obrigatória: %d %s", status, body)
	}
	var run api.FlowRun
	e.want(200, "POST", base+"/flows/revisar-pr/run", `{"variables":{"repo":"acme/api"}}`, &run)
	if run.Task == nil || run.Task.Title != "Revisar acme/api (manual)" || run.Fire.Source != "manual" || run.Fire.Variables["ambiente"] != "staging" {
		t.Fatalf("run: %+v", run)
	}
	// o run sem corpo continua valendo para fluxo sem variáveis obrigatórias
	var plain api.Flow
	e.want(201, "POST", base+"/flows", `{"name":"Simples","persona":"`+p.Slug+`"}`, &plain)
	e.want(200, "POST", base+"/flows/simples/run", "", &run)

	// webhook: o segredo vem na criação e o HMAC autentica a entrega
	var tr api.FlowTrigger
	e.want(201, "POST", base+"/flows/revisar-pr/triggers", `{"kind":"webhook","name":"github","mapping":{"repo":"repository.full_name"}}`, &tr)
	if tr.SecretValue == "" || !strings.HasPrefix(tr.Path, "/hooks/flows/") || tr.RatePerMin != 30 {
		t.Fatalf("gatilho: %+v", tr)
	}
	var again api.ListBody[api.FlowTrigger]
	e.want(200, "GET", base+"/flows/revisar-pr/triggers", "", &again)
	if len(again.Items) != 1 || again.Items[0].SecretValue != "" {
		t.Fatalf("o segredo não pode voltar na listagem: %+v", again.Items)
	}
	payload := `{"repository":{"full_name":"acme/web"}}`
	sign := func(body string) map[string]string {
		return map[string]string{webhook.HeaderSignature: webhook.Sign(tr.SecretValue, e.svc.Now(), []byte(body)), webhook.HeaderDelivery: "entrega-1"}
	}
	status, body, _ := postRaw(t, hs.URL+tr.Path, sign(payload), payload)
	if status != http.StatusAccepted || !strings.Contains(body, `"task_key"`) {
		t.Fatalf("webhook: %d %s", status, body)
	}
	if status, body, _ = postRaw(t, hs.URL+tr.Path, sign(payload), payload); status != http.StatusOK || !strings.Contains(body, `"duplicate":true`) {
		t.Fatalf("webhook repetido: %d %s", status, body)
	}
	if status, _, _ = postRaw(t, hs.URL+tr.Path, map[string]string{webhook.HeaderSignature: "t=1,v1=00"}, payload); status != http.StatusUnauthorized {
		t.Fatalf("assinatura inválida: %d", status)
	}
	if status, _, _ = postRaw(t, hs.URL+tr.Path, nil, payload); status != http.StatusUnauthorized {
		t.Fatalf("sem assinatura: %d", status)
	}
	bad := `{"outra":"coisa"}`
	hd := sign(bad)
	hd[webhook.HeaderDelivery] = "entrega-2"
	if status, body, _ = postRaw(t, hs.URL+tr.Path, hd, bad); status != http.StatusUnprocessableEntity || !strings.Contains(body, "repo") {
		t.Fatalf("obrigatória faltando: %d %s", status, body)
	}
	if status, _, _ = postRaw(t, hs.URL+"/hooks/flows/"+domain.NewID().String(), sign(payload), payload); status != http.StatusNotFound {
		t.Fatalf("gatilho inexistente: %d", status)
	}
	if status, _, _ = postRaw(t, hs.URL+"/hooks/flows/nao-e-id", sign(payload), payload); status != http.StatusNotFound {
		t.Fatalf("id inválido: %d", status)
	}
	big := `{"repository":{"full_name":"` + strings.Repeat("x", domain.MaxFlowPayloadBytes) + `"}}`
	if status, _, _ = postRaw(t, hs.URL+tr.Path, sign(big), big); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("corpo grande: %d", status)
	}
	// limite por gatilho
	var tr2 api.FlowTrigger
	e.want(201, "POST", base+"/flows/simples/triggers", `{"kind":"webhook","name":"lento","rate_per_min":1}`, &tr2)
	one := func(delivery string) int {
		h := map[string]string{webhook.HeaderSignature: webhook.Sign(tr2.SecretValue, e.svc.Now(), []byte(`{}`)), webhook.HeaderDelivery: delivery}
		s, _, hdr := postRaw(t, hs.URL+tr2.Path, h, `{}`)
		if s == http.StatusTooManyRequests && hdr.Get("Retry-After") == "" {
			t.Error("429 sem Retry-After")
		}
		return s
	}
	if s := one("a"); s != http.StatusAccepted {
		t.Fatalf("primeira: %d", s)
	}
	if s := one("b"); s != http.StatusTooManyRequests {
		t.Fatalf("segunda: %d", s)
	}

	// evento interno + revisões + arquivar
	var ev api.FlowTrigger
	e.want(201, "POST", base+"/flows/simples/triggers", `{"kind":"event","name":"ao criar","event_verb":"task.created","event_filter":{"data.priority":"high"}}`, &ev)
	before, _ := e.svc.ListTasks(context.Background(), e.co.ID, service.TaskFilter{})
	e.task(e.co, "alta prioridade", nil, func(in *service.CreateTaskInput) { in.Priority = domain.PriorityHigh })
	tasks, _ := e.svc.ListTasks(context.Background(), e.co.ID, service.TaskFilter{})
	if len(tasks) != len(before)+2 {
		t.Fatalf("o evento deveria criar uma task do fluxo: %d -> %d", len(before), len(tasks))
	}
	e.want(200, "PATCH", base+"/flows/simples/triggers/"+ev.ID, `{"enabled":false}`, &ev)
	if ev.Enabled {
		t.Fatalf("gatilho continua ligado: %+v", ev)
	}
	var revs api.ListBody[api.FlowRevision]
	e.want(200, "GET", base+"/flows/simples/revisions", "", &revs)
	if len(revs.Items) < 3 || revs.Items[0].Revision < revs.Items[len(revs.Items)-1].Revision || revs.Items[len(revs.Items)-1].Origin != "create" {
		t.Fatalf("revisões: %+v", revs.Items)
	}
	var rev api.FlowRevision
	e.want(200, "GET", base+"/flows/simples/revisions/1", "", &rev)
	if rev.Snapshot.Name != "Simples" {
		t.Fatalf("revisão 1: %+v", rev)
	}
	e.want(200, "PATCH", base+"/flows/simples", `{"name":"Simples 2"}`, &plain)
	e.want(200, "POST", base+"/flows/simples/revisions/1/restore", "", &plain)
	if plain.Name != "Simples" {
		t.Fatalf("restaurar: %+v", plain)
	}
	e.want(204, "DELETE", base+"/flows/simples/triggers/"+ev.ID, "", nil)

	e.want(200, "POST", base+"/flows/simples/archive", "", &plain)
	if plain.ArchivedAt == nil {
		t.Fatalf("arquivar: %+v", plain)
	}
	var list api.ListBody[api.Flow]
	e.want(200, "GET", base+"/flows", "", &list)
	for _, x := range list.Items {
		if x.Slug == "simples" {
			t.Fatal("fluxo arquivado na lista padrão")
		}
	}
	e.want(200, "GET", base+"/flows?archived=true", "", &list)
	if len(list.Items) != 1 || list.Items[0].Slug != "simples" {
		t.Fatalf("arquivados: %+v", list.Items)
	}
	if status, _ := e.call("POST", base+"/flows/simples/run", "", nil); status != http.StatusConflict {
		t.Fatalf("run em arquivado: %d", status)
	}
	if status, _, _ := postRaw(t, hs.URL+tr2.Path, map[string]string{webhook.HeaderSignature: webhook.Sign(tr2.SecretValue, e.svc.Now().Add(time.Second), []byte(`{}`))}, `{}`); status != http.StatusConflict {
		t.Fatalf("webhook em arquivado: %d", status)
	}
	e.want(200, "POST", base+"/flows/simples/unarchive", "", &plain)
	if plain.ArchivedAt != nil || plain.Enabled {
		t.Fatalf("desarquivar: %+v", plain)
	}
}

func TestAPIChannels(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New(m19Key, "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	e.svc.Integrations.HTTP = http.DefaultClient
	p := e.persona(e.co, "Ada", nil)
	base := e.base()
	hs := hooksServer(t, e.svc)
	for name, v := range map[string]string{"tg-token": "9:T", "tg-secret": "whsec"} {
		if _, err := e.svc.CreateSecret(context.Background(), domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: name, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	tg := newFakeTelegram(t)

	var ch api.Channel
	if status, _ := e.call("POST", base+"/channels", `{"kind":"github","name":"GH"}`, nil); status != 422 {
		t.Fatalf("github sem repos: %d", status)
	}
	e.want(201, "POST", base+"/channels", `{"kind":"telegram","name":"Bot","persona":"`+p.Slug+`","token_ref":"${secret:tg-token}","webhook_secret_ref":"${secret:tg-secret}","config":{"api_base":"`+tg.URL+`"}}`, &ch)
	if ch.Enabled || ch.HookPath != "/hooks/channels/"+strings.TrimPrefix(ch.HookPath, "/hooks/channels/") || ch.PersonaName != "Ada" {
		t.Fatalf("canal: %+v", ch)
	}
	// desligado: o provedor recebe 404
	body := `{"update_id":1,"message":{"message_id":1,"from":{"id":5,"first_name":"Ana"},"chat":{"id":5,"type":"private"},"text":"oi"}}`
	sec := map[string]string{"X-Telegram-Bot-Api-Secret-Token": "whsec"}
	if status, _, _ := postRaw(t, hs.URL+ch.HookPath, sec, body); status != http.StatusNotFound {
		t.Fatalf("canal desligado: %d", status)
	}
	e.want(200, "PATCH", base+"/channels/Bot", `{"enabled":true}`, &ch)
	if !ch.Enabled {
		t.Fatalf("ligar: %+v", ch)
	}
	if status, _, _ := postRaw(t, hs.URL+ch.HookPath, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "errado"}, body); status != http.StatusUnauthorized {
		t.Fatalf("segredo errado: %d", status)
	}
	status, resp, _ := postRaw(t, hs.URL+ch.HookPath, sec, body)
	if status != http.StatusOK || !strings.Contains(resp, `"accepted":1`) {
		t.Fatalf("entrada: %d %s", status, resp)
	}
	if status, resp, _ = postRaw(t, hs.URL+ch.HookPath, sec, body); status != http.StatusOK || !strings.Contains(resp, `"duplicates":1`) {
		t.Fatalf("entrada repetida: %d %s", status, resp)
	}
	// corpo inválido depois de autenticado: 200 (não trava a fila do provedor)
	if status, _, _ = postRaw(t, hs.URL+ch.HookPath, sec, "{"); status != http.StatusOK {
		t.Fatalf("corpo inválido: %d", status)
	}
	var msgs api.ListBody[api.ChannelMessage]
	e.want(200, "GET", base+"/channels/"+ch.ID+"/messages", "", &msgs)
	var ins, outs int
	for _, m := range msgs.Items {
		if m.Direction == "in" {
			ins++
		} else {
			outs++
		}
	}
	if ins != 1 || outs != 1 { // a entrada e a resposta "vincule sua conta"
		t.Fatalf("mensagens: %+v", msgs.Items)
	}

	// vínculo exige uma pessoa autenticada (o modo trusted não tem)
	if status, _ := e.call("POST", base+"/channels/Bot/link", "", nil); status != 422 {
		t.Fatalf("link em trusted: %d", status)
	}
	var conn api.ChannelConnection
	e.want(200, "POST", base+"/channels/Bot/connect", "", &conn)
	if conn.Registered || conn.Note == "" {
		t.Fatalf("connect sem HANKO_PUBLIC_URL: %+v", conn)
	}
	var sent api.ChannelMessage
	e.want(201, "POST", base+"/channels/Bot/messages", `{"peer":"5","text":"olá"}`, &sent)
	if sent.Status != "pending" || sent.Direction != "out" {
		t.Fatalf("envio: %+v", sent)
	}
	if status, _ := e.call("POST", base+"/channels/Bot/messages/"+sent.ID+"/resend", "", nil); status != http.StatusConflict {
		t.Fatalf("reenviar o que não falhou: %d", status)
	}
	if status, _ := e.call("POST", base+"/channels/Bot/messages", `{"peer":"5"}`, nil); status != 422 {
		t.Fatalf("envio vazio: %d", status)
	}
	var ids api.ListBody[api.ChannelIdentity]
	e.want(200, "GET", base+"/channels/Bot/identities", "", &ids)
	if len(ids.Items) != 0 {
		t.Fatalf("identidades: %+v", ids.Items)
	}
	// review exige canal GitHub
	if status, _ := e.call("POST", base+"/channels/Bot/reviews", `{"peer":"a/b#1","event":"COMMENT","body":"x"}`, nil); status != 422 {
		t.Fatalf("review em canal Telegram: %d", status)
	}
	var list api.ListBody[api.Channel]
	e.want(200, "GET", base+"/channels", "", &list)
	if len(list.Items) != 1 {
		t.Fatalf("lista: %+v", list.Items)
	}
	e.want(204, "DELETE", base+"/channels/Bot", "", nil)
	if status, _ := e.call("GET", base+"/channels/Bot", "", nil); status != 404 {
		t.Fatalf("depois de apagar: %d", status)
	}
}

// newFakeTelegram é uma Bot API que aceita tudo (o teste de API só confere o desenho da resposta).
func newFakeTelegram(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1}})
	}))
	t.Cleanup(ts.Close)
	return ts
}
