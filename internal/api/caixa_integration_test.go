//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/webpush/webpushtest"
)

// Caixa por pessoa, avisos, visões "Minhas", atividade por ator, busca estendida e metas em árvore pela API (ADR-0042).

func decodeJSON[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("JSON inválido: %v\n%s", err, body)
	}
	return v
}

type inboxResp struct {
	Items []struct {
		Kind     string `json:"kind"`
		ID       string `json:"id"`
		Title    string `json:"title"`
		Read     bool   `json:"read"`
		Archived bool   `json:"archived"`
	} `json:"items"`
	Count    int  `json:"count"`
	Unread   int  `json:"unread"`
	Archived int  `json:"archived"`
	Personal bool `json:"personal"`
}

func TestAPIInboxStateAndPersonalViews(t *testing.T) {
	e := setupAuth(t)
	c := "/companies/" + e.co.Slug
	ctx := context.Background()
	tk, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "decidir o orçamento"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "liberar verba?", TaskID: &tk.ID})
	if err != nil {
		t.Fatal(err)
	}

	code, body := e.call(t, "member", "GET", c+"/inbox", "")
	in := decodeJSON[inboxResp](t, body)
	if code != 200 || !in.Personal || in.Count != 1 || in.Unread != 1 || len(in.Items) != 1 || in.Items[0].Kind != "gate" || in.Items[0].ID != g.ID.String() {
		t.Fatalf("Caixa do membro: %d %s", code, body)
	}
	// a outra pessoa tem a própria Caixa
	state := `{"action":"archive","items":[{"kind":"gate","id":"` + g.ID.String() + `"}]}`
	if code, body := e.call(t, "member", "POST", c+"/inbox/state", state); code != 200 || !strings.Contains(body, `"updated":1`) {
		t.Fatalf("arquivar: %d %s", code, body)
	}
	_, body = e.call(t, "member", "GET", c+"/inbox", "")
	if in := decodeJSON[inboxResp](t, body); in.Count != 0 || in.Archived != 1 || len(in.Items) != 0 {
		t.Fatalf("Caixa do membro depois de arquivar: %s", body)
	}
	_, body = e.call(t, "member", "GET", c+"/inbox?view=archived", "")
	if in := decodeJSON[inboxResp](t, body); len(in.Items) != 1 || !in.Items[0].Archived {
		t.Fatalf("arquivados: %s", body)
	}
	_, body = e.call(t, "admin", "GET", c+"/inbox?view=unread", "")
	if in := decodeJSON[inboxResp](t, body); len(in.Items) != 1 || in.Items[0].Archived || in.Items[0].Read {
		t.Fatalf("o admin segue vendo o gate como não lido: %s", body)
	}
	// o gate segue pendente
	if _, body := e.call(t, "owner", "GET", c+"/gates?status=pending", ""); !strings.Contains(body, g.ID.String()) {
		t.Fatalf("o gate segue pendente: %s", body)
	}
	// erros
	for name, tc := range map[string]struct {
		who, body string
		want      int
	}{
		"item inexistente": {"member", `{"action":"read","items":[{"kind":"gate","id":"` + domain.NewID().String() + `"}]}`, 404},
		"ação inválida":    {"member", `{"action":"dance","items":[]}`, 422},
		"id inválido":      {"member", `{"action":"read","items":[{"kind":"gate","id":"lixo"}]}`, 422},
		"estranho":         {"stranger", `{"action":"read_all"}`, 404},
	} {
		if code, b := e.call(t, tc.who, "POST", c+"/inbox/state", tc.body); code != tc.want {
			t.Errorf("%s: %d (quer %d): %s", name, code, tc.want, b)
		}
	}
	if code, _ := e.call(t, "member", "GET", c+"/inbox?view=nada", ""); code != 422 {
		t.Errorf("view inválida: %d", code)
	}
	if code, _ := e.call(t, "stranger", "GET", c+"/inbox", ""); code != 404 {
		t.Errorf("estranho: %d", code)
	}
	if code, body := e.call(t, "member", "POST", c+"/inbox/state", `{"action":"read_all"}`); code != 200 || !strings.Contains(body, `"updated"`) {
		t.Errorf("read_all: %d %s", code, body)
	}
}

func TestAPINotificationPreferencesAndWebPush(t *testing.T) {
	e := setupAuth(t)
	c := "/companies/" + e.co.Slug

	// preferências: padrões e mudança
	code, body := e.call(t, "member", "GET", c+"/notifications", "")
	prefs := decodeJSON[struct {
		Items []struct {
			Kind        string `json:"kind"`
			Toast, Push bool
		} `json:"items"`
	}](t, body)
	if code != 200 || len(prefs.Items) != 4 || prefs.Items[0].Kind != "mention" || !prefs.Items[0].Push {
		t.Fatalf("preferências: %d %s", code, body)
	}
	if code, body := e.call(t, "member", "PUT", c+"/notifications/mention", `{"toast":true,"push":false}`); code != 200 || !strings.Contains(body, `"push":false`) {
		t.Fatalf("PUT: %d %s", code, body)
	}
	if code, body := e.call(t, "member", "GET", c+"/notifications", ""); code != 200 || !strings.Contains(body, `"push":false`) {
		t.Fatalf("GET depois do PUT: %d %s", code, body)
	}
	if code, _ := e.call(t, "member", "PUT", c+"/notifications/outro", `{"toast":true,"push":true}`); code != 422 {
		t.Errorf("tipo inválido: %d", code)
	}

	// web push: sem chave de cifragem do servidor, desligado
	if code, _ := e.call(t, "member", "GET", "/push/vapid-key", ""); code != 422 {
		t.Errorf("sem HANKO_SECRET_KEY o push fica desligado (422): %d", code)
	}
	box, _ := secretbox.New("chave-de-teste", "")
	e.svc.Integrations.Secrets = box
	push := webpushtest.NewService(t)
	e.svc.Push = service.PushConfig{HTTP: push.Client(), AllowInsecureEndpoints: true}

	code, body = e.call(t, "member", "GET", "/push/vapid-key", "")
	key := decodeJSON[struct {
		PublicKey string `json:"public_key"`
	}](t, body)
	if code != 200 || len(key.PublicKey) != 87 {
		t.Fatalf("chave VAPID: %d %s", code, body)
	}
	dev := push.Device("/push/membro")
	sub := `{"endpoint":"` + dev.Sub.Endpoint + `","keys":{"p256dh":"` + dev.Sub.P256dh + `","auth":"` + dev.Sub.Auth + `"},"user_agent":"Firefox"}`
	code, body = e.call(t, "member", "POST", "/me/push-subscriptions", sub)
	saved := decodeJSON[struct {
		ID string `json:"id"`
	}](t, body)
	if code != 201 || saved.ID == "" || strings.Contains(body, dev.Sub.Auth) {
		t.Fatalf("assinar: %d %s (nenhuma chave volta na resposta)", code, body)
	}
	if code, body := e.call(t, "member", "GET", "/me/push-subscriptions", ""); code != 200 || !strings.Contains(body, saved.ID) || strings.Contains(body, dev.Sub.P256dh) {
		t.Fatalf("listar: %d %s", code, body)
	}
	// o aviso de teste chega ao serviço de push falso (mention com push ligado de novo)
	e.call(t, "member", "PUT", c+"/notifications/mention", `{"toast":true,"push":true}`)
	if code, _ := e.call(t, "member", "POST", c+"/notifications/test", ""); code != 204 && code != 200 {
		t.Fatalf("aviso de teste: %d", code)
	}
	e.svc.WaitNotifications(context.Background())
	msgs := push.Messages()
	if len(msgs) != 1 || msgs[0].Err != "" || !strings.Contains(string(msgs[0].Payload), "Aviso de teste") {
		t.Fatalf("push de teste: %+v", msgs)
	}
	// endpoint interno/http: recusado; chaves ruins: recusadas
	if code, _ := e.call(t, "member", "POST", "/me/push-subscriptions", `{"endpoint":"http://x/y","keys":{"p256dh":"a","auth":"b"}}`); code != 422 {
		t.Errorf("chaves ruins: %d", code)
	}
	// remover
	if code, _ := e.call(t, "member", "DELETE", "/me/push-subscriptions/"+saved.ID, ""); code != 204 && code != 200 {
		t.Errorf("remover: %d", code)
	}
	if code, _ := e.call(t, "member", "DELETE", "/me/push-subscriptions/"+saved.ID, ""); code != 404 {
		t.Errorf("remover de novo: %d", code)
	}
}

func TestAPIMineActivityByActorAndSearchTypes(t *testing.T) {
	e := setupAuth(t)
	c := "/companies/" + e.co.Slug

	code, body := e.call(t, "member", "POST", c+"/tasks", `{"title":"feita pelo membro"}`)
	if code != 201 {
		t.Fatalf("criar task: %d %s", code, body)
	}
	task := decodeJSON[struct {
		Key string `json:"key"`
	}](t, body)
	e.call(t, "admin", "POST", c+"/tasks", `{"title":"feita pelo admin"}`)

	// Minhas: só as do membro, com o motivo
	code, body = e.call(t, "member", "GET", c+"/tasks?mine=true", "")
	mine := decodeJSON[struct {
		Items []struct {
			Key           string   `json:"key"`
			Participation []string `json:"participation"`
		} `json:"items"`
	}](t, body)
	if code != 200 || len(mine.Items) != 1 || mine.Items[0].Key != task.Key || len(mine.Items[0].Participation) != 1 || mine.Items[0].Participation[0] != "created" {
		t.Fatalf("Minhas: %d %s", code, body)
	}
	if _, body := e.call(t, "member", "GET", c+"/tasks", ""); strings.Count(body, `"key"`) < 2 {
		t.Fatalf("sem mine vêm todas: %s", body)
	}
	if code, body := e.call(t, "member", "GET", c+"/gates?mine=true", ""); code != 200 || strings.Contains(body, `"id"`) {
		t.Fatalf("gates Minhas: %d %s", code, body)
	}

	// atividade por ator
	code, body = e.call(t, "member", "GET", c+"/activity?actor=human:member@x.com&subject_type=task", "")
	act := decodeJSON[struct {
		Items []struct {
			Verb  string `json:"verb"`
			Actor struct {
				Type string `json:"type"`
			} `json:"actor"`
		} `json:"items"`
	}](t, body)
	if code != 200 || len(act.Items) == 0 {
		t.Fatalf("atividade do membro: %d %s", code, body)
	}
	for _, it := range act.Items {
		if it.Actor.Type != "human" {
			t.Fatalf("só humanos: %s", body)
		}
	}
	_, all := e.call(t, "member", "GET", c+"/activity?subject_type=task", "")
	if strings.Count(all, `"verb"`) <= len(act.Items) {
		t.Errorf("o filtro por ator deveria reduzir: %d vs %d", len(act.Items), strings.Count(all, `"verb"`))
	}
	if code, _ := e.call(t, "member", "GET", c+"/activity?actor=persona:fantasma", ""); code != 422 {
		t.Errorf("ator desconhecido: %d", code)
	}

	// busca: os tipos novos aparecem no contrato e funcionam
	e.call(t, "admin", "POST", c+"/goals", `{"title":"Meta do checkout","start_value":0,"target_value":10}`)
	code, body = e.call(t, "member", "GET", c+"/search?q=checkout&types=goal", "")
	if code != 200 || !strings.Contains(body, `"type":"goal"`) || !strings.Contains(body, `"ref":"meta-do-checkout"`) {
		t.Fatalf("busca de metas: %d %s", code, body)
	}
	if code, _ := e.call(t, "member", "GET", c+"/search?q=checkout&types=nada", ""); code != http.StatusUnprocessableEntity {
		t.Errorf("tipo desconhecido: %d", code)
	}
}

func TestAPIGoalTree(t *testing.T) {
	e := setupAuth(t)
	c := "/companies/" + e.co.Slug
	type goal struct {
		Slug            string  `json:"slug"`
		Depth           int     `json:"depth"`
		ParentSlug      string  `json:"parent_slug"`
		Rollup          bool    `json:"rollup"`
		ProgressPercent float64 `json:"progress_percent"`
		Status          string  `json:"status"`
		Path            []struct {
			Slug, Title string
		} `json:"path"`
		Children []struct {
			Slug string `json:"slug"`
		} `json:"children"`
	}
	mk := func(who, body string) (int, goal) {
		code, b := e.call(t, who, "POST", c+"/goals", body)
		var g goal
		_ = json.Unmarshal([]byte(b), &g)
		return code, g
	}
	code, root := mk("admin", `{"title":"Visão","start_value":0,"target_value":100}`)
	if code != 201 || root.Depth != 1 {
		t.Fatalf("raiz: %d %+v", code, root)
	}
	code, child := mk("admin", `{"title":"Receita","start_value":0,"target_value":100,"parent":"`+root.Slug+`"}`)
	if code != 201 || child.Depth != 2 || child.ParentSlug != root.Slug || len(child.Path) != 1 {
		t.Fatalf("filha: %d %+v", code, child)
	}
	// progresso na filha move o agregado do pai; no pai direto é recusado
	if code, b := e.call(t, "member", "POST", c+"/goals/"+child.Slug+"/progress", `{"value":50}`); code != 201 {
		t.Fatalf("progresso na filha: %d %s", code, b)
	}
	if code, b := e.call(t, "member", "POST", c+"/goals/"+root.Slug+"/progress", `{"value":10}`); code != 422 {
		t.Fatalf("progresso no pai deveria ser 422: %d %s", code, b)
	}
	_, b := e.call(t, "viewer", "GET", c+"/goals/"+root.Slug, "")
	var got goal
	_ = json.Unmarshal([]byte(b), &got)
	if !got.Rollup || got.ProgressPercent != 50 || len(got.Children) != 1 || got.Children[0].Slug != child.Slug {
		t.Fatalf("pai agregado: %s", b)
	}
	// ciclo e profundidade
	if code, _ := e.call(t, "admin", "PATCH", c+"/goals/"+root.Slug, `{"parent":"`+child.Slug+`"}`); code != 422 {
		t.Errorf("ciclo: %d", code)
	}
	if code, b := e.call(t, "member", "PATCH", c+"/goals/"+child.Slug, `{"clear_parent":true}`); code != 403 {
		t.Errorf("só admin muda a árvore: %d %s", code, b)
	}
	if code, b := e.call(t, "admin", "PATCH", c+"/goals/"+child.Slug, `{"clear_parent":true}`); code != 200 || !strings.Contains(b, `"depth":1`) {
		t.Errorf("virar raiz: %d %s", code, b)
	}
	_, b = e.call(t, "viewer", "GET", c+"/goals/"+root.Slug, "")
	got = goal{}
	_ = json.Unmarshal([]byte(b), &got)
	if got.Rollup {
		t.Errorf("sem filhos o pai volta ao próprio valor: %s", b)
	}
}
