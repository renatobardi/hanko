package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/webpush"
	"github.com/renatobardi/hanko/internal/webpush/webpushtest"
)

// Menções, toasts por usuário e web push (ADR-0042 §A.5–A.7), contra um serviço de push falso (httptest).

type pushPayload struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Key   string `json:"key"`
}

// pushEnv liga o web push no service: chave de cifragem e o serviço de push falso no lugar da rede.
func pushEnv(t *testing.T) (e *env, ana, bruno domain.User, push *webpushtest.Service) {
	t.Helper()
	e, ana, bruno = caixaEnv(t)
	box, err := secretbox.New("chave-de-teste", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	push = webpushtest.NewService(t)
	e.svc.Push = service.PushConfig{HTTP: push.Client(), AllowInsecureEndpoints: true, Subject: "mailto:ops@example.org"}
	return e, ana, bruno, push
}

func (e *env) settle(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	e.svc.WaitNotifications(ctx)
}

func subscribe(t *testing.T, e *env, u domain.User, d *webpushtest.Device) {
	t.Helper()
	if _, err := e.svc.AddPushSubscription(as(u), d.Sub, "teste"); err != nil {
		t.Fatalf("assinar push de %s: %v", u.Email, err)
	}
}

func decode(t *testing.T, m webpushtest.Message) pushPayload {
	t.Helper()
	if m.Err != "" {
		t.Fatalf("mensagem inválida no serviço de push: %s", m.Err)
	}
	var p pushPayload
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		t.Fatalf("payload: %v (%q)", err, m.Payload)
	}
	return p
}

// A demo do marco: uma @menção chega como push (no serviço falso).
func TestMentionReachesTheMentionedUserAsPush(t *testing.T) {
	e, ana, bruno, push := pushEnv(t)
	bg := context.Background()
	dev := push.Device("/push/bruno")
	subscribe(t, e, bruno, dev)
	anaDev := push.Device("/push/ana")
	subscribe(t, e, ana, anaDev)
	toast, cancel := e.broker.Subscribe(service.UserTopic(e.co.ID, bruno.ID), 8)
	defer cancel()

	tk := e.task(t, service.CreateTaskInput{Title: "revisar contrato"})
	cm, err := e.svc.AddComment(as(ana), ana.Actor(), e.co.ID, tk.ID, service.AddCommentInput{Body: "Olá @bruno, dá uma olhada? E @ana-nao-existe também."})
	if err != nil {
		t.Fatal(err)
	}
	e.settle(t)

	msgs := push.Messages()
	if len(msgs) != 1 || msgs[0].Path != "/push/bruno" {
		t.Fatalf("só Bruno recebe (a autora não se menciona): %+v", msgs)
	}
	p := decode(t, msgs[0])
	if p.Kind != "mention" || !strings.Contains(p.Title, tk.Key) || !strings.Contains(p.Body, "dá uma olhada") || p.URL != "/c/"+e.co.Slug+"/tasks/"+tk.Key || p.Key != tk.Key {
		t.Fatalf("payload: %+v", p)
	}
	if msgs[0].VAPIDKey == "" {
		t.Fatal("o envio vai assinado com a chave VAPID")
	}

	// toast ao vivo só no tópico do Bruno
	select {
	case m := <-toast:
		if m.Type != "notify" {
			t.Fatalf("toast: %+v", m)
		}
	default:
		t.Fatal("Bruno deveria receber o toast")
	}

	// a menção está na Caixa dele, não lida, e não na da autora
	in, err := e.svc.Inbox(as(bruno), e.co.ID)
	if err != nil || len(in.Mentions) != 1 || in.Mentions[0].TaskKey != tk.Key || in.Mentions[0].Author == "" || in.Count() != 1 || in.Unread != 1 {
		t.Fatalf("Caixa de Bruno: %+v %v", in.Mentions, err)
	}
	if in, _ := e.svc.Inbox(as(ana), e.co.ID); len(in.Mentions) != 0 {
		t.Fatal("a autora não recebe a própria menção")
	}
	// arquivar a menção limpa a Caixa sem tocar na task
	ref := service.InboxRef{Kind: domain.InboxMention, ID: in.Mentions[0].ID}
	if _, err := e.svc.SetInboxState(as(bruno), bruno.Actor(), e.co.ID, domain.InboxArchive, []service.InboxRef{ref}); err != nil {
		t.Fatal(err)
	}
	if in, _ := e.svc.Inbox(as(bruno), e.co.ID); in.Count() != 0 {
		t.Fatal("menção arquivada sai da Caixa")
	}
	if cur, _ := e.svc.GetTask(bg, e.co.ID, tk.ID.String()); !cur.Status.Open() {
		t.Fatal("a task segue aberta")
	}
	// fato da menção na trilha
	acts, _ := e.svc.ListActivity(bg, e.co.ID, service.ActivityFilter{SubjectType: "task", SubjectID: &tk.ID, Limit: 50})
	found := false
	for _, a := range acts {
		found = found || a.Verb == "comment.mentioned"
	}
	if !found || cm.ID == (domain.ID{}) {
		t.Fatal("comment.mentioned deveria constar")
	}
}

func TestNotificationPreferencesGateToastAndPush(t *testing.T) {
	e, ana, bruno, push := pushEnv(t)
	dev := push.Device("/push/bruno")
	subscribe(t, e, bruno, dev)
	toast, cancel := e.broker.Subscribe(service.UserTopic(e.co.ID, bruno.ID), 8)
	defer cancel()
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	mention := func() {
		if _, err := e.svc.AddComment(as(ana), ana.Actor(), e.co.ID, tk.ID, service.AddCommentInput{Body: "oi @bruno"}); err != nil {
			t.Fatal(err)
		}
		e.settle(t)
	}

	prefs, err := e.svc.NotifyPrefs(as(bruno), e.co.ID)
	if err != nil || len(prefs) != len(domain.NotifyKinds) || !prefs[0].Toast || !prefs[0].Push || prefs[2].Push {
		t.Fatalf("padrões: %+v %v", prefs, err)
	}
	// push desligado para menção: só o toast
	if err := e.svc.SetNotifyPref(as(bruno), e.co.ID, domain.NotifyPref{Kind: domain.NotifyMention, Toast: true, Push: false}); err != nil {
		t.Fatal(err)
	}
	mention()
	if len(push.Messages()) != 0 || len(toast) != 1 {
		t.Fatalf("só toast: push=%d toasts=%d", len(push.Messages()), len(toast))
	}
	<-toast
	// toast desligado e push ligado
	if err := e.svc.SetNotifyPref(as(bruno), e.co.ID, domain.NotifyPref{Kind: domain.NotifyMention, Toast: false, Push: true}); err != nil {
		t.Fatal(err)
	}
	mention()
	if len(push.Messages()) != 1 || len(toast) != 0 {
		t.Fatalf("só push: push=%d toasts=%d", len(push.Messages()), len(toast))
	}
	// tudo desligado
	if err := e.svc.SetNotifyPref(as(bruno), e.co.ID, domain.NotifyPref{Kind: domain.NotifyMention}); err != nil {
		t.Fatal(err)
	}
	mention()
	if len(push.Messages()) != 1 || len(toast) != 0 {
		t.Fatal("nada chega com tudo desligado")
	}
	if in, _ := e.svc.Inbox(as(bruno), e.co.ID); len(in.Mentions) != 3 {
		t.Fatalf("a Caixa recebe as menções mesmo sem aviso: %d", len(in.Mentions))
	}
	// validação
	wantErr(t, e.svc.SetNotifyPref(as(bruno), e.co.ID, domain.NotifyPref{Kind: "outro"}), domain.ErrInvalid)
	wantErr(t, e.svc.SetNotifyPref(context.Background(), e.co.ID, domain.NotifyPref{Kind: domain.NotifyGate}), domain.ErrInvalid)
	if _, err := e.svc.NotifyPrefs(context.Background(), e.co.ID); err == nil {
		t.Fatal("sem pessoa não há preferências")
	}
}

func TestGateAndFailureNoticesGoToMembers(t *testing.T) {
	e, ana, bruno, push := pushEnv(t)
	subscribe(t, e, ana, push.Device("/push/ana"))
	subscribe(t, e, bruno, push.Device("/push/bruno"))
	tk := e.task(t, service.CreateTaskInput{Title: "aprovar orçamento"})
	if _, err := e.svc.RequestGate(context.Background(), domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "liberar verba?", TaskID: &tk.ID}); err != nil {
		t.Fatal(err)
	}
	e.settle(t)
	msgs := push.Messages()
	if len(msgs) != 2 {
		t.Fatalf("o gate chega aos dois membros: %+v", msgs)
	}
	p := decode(t, msgs[0])
	if p.Kind != "gate" || !strings.Contains(p.Title, "liberar verba?") || !strings.Contains(p.URL, "/gates/") || p.Key != tk.Key {
		t.Fatalf("payload do gate: %+v", p)
	}
}

func TestPushSubscriptionLifecycle(t *testing.T) {
	e, _, bruno, push := pushEnv(t)
	bg := context.Background()
	dev := push.Device("/push/bruno")

	v, err := e.svc.AddPushSubscription(as(bruno), dev.Sub, "Firefox")
	if err != nil || v.Endpoint != dev.Sub.Endpoint || v.UserAgent != "Firefox" {
		t.Fatalf("assinar: %+v %v", v, err)
	}
	if again, err := e.svc.AddPushSubscription(as(bruno), dev.Sub, "Firefox 2"); err != nil || again.ID != v.ID {
		t.Fatalf("reassinar o mesmo endpoint atualiza: %+v %v", again, err)
	}
	list, _ := e.svc.ListPushSubscriptions(as(bruno))
	if len(list) != 1 {
		t.Fatalf("lista: %+v", list)
	}
	// endpoint interno / http / chaves ruins são recusados quando o operador não liberou o modo de teste
	strict := *e.svc
	strict.Push = service.PushConfig{}
	for name, sub := range map[string]webpush.Subscription{
		"http":     {Endpoint: "http://push.example/x", P256dh: dev.Sub.P256dh, Auth: dev.Sub.Auth},
		"loopback": {Endpoint: "https://127.0.0.1/x", P256dh: dev.Sub.P256dh, Auth: dev.Sub.Auth},
		"metadata": {Endpoint: "https://169.254.169.254/latest", P256dh: dev.Sub.P256dh, Auth: dev.Sub.Auth},
	} {
		if _, err := strict.AddPushSubscription(as(bruno), sub, ""); err == nil {
			t.Errorf("%s deveria ser recusado", name)
		}
	}
	wantErr(t, func() error {
		_, err := e.svc.AddPushSubscription(as(bruno), webpush.Subscription{Endpoint: dev.Sub.Endpoint + "x", P256dh: "xx", Auth: dev.Sub.Auth}, "")
		return err
	}(), domain.ErrInvalid)
	if _, err := e.svc.AddPushSubscription(bg, dev.Sub, ""); err == nil {
		t.Fatal("sem pessoa autenticada não assina")
	}

	// 404/410 do serviço de push apagam a assinatura
	ana := mkUser(t, e, "outra@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	push.SetStatus("/push/bruno", http.StatusGone)
	if _, err := e.svc.AddComment(as(ana), ana.Actor(), e.co.ID, tk.ID, service.AddCommentInput{Body: "@bruno"}); err != nil {
		t.Fatal(err)
	}
	e.settle(t)
	if list, _ := e.svc.ListPushSubscriptions(as(bruno)); len(list) != 0 {
		t.Fatalf("410 apaga a assinatura: %+v", list)
	}

	// falhas seguidas (500) apagam depois do teto
	subscribe(t, e, bruno, dev)
	push.SetStatus("/push/bruno", http.StatusInternalServerError)
	for i := 0; i < 5; i++ {
		if _, err := e.svc.AddComment(as(ana), ana.Actor(), e.co.ID, tk.ID, service.AddCommentInput{Body: "@bruno de novo"}); err != nil {
			t.Fatal(err)
		}
		e.settle(t)
		list, _ := e.svc.ListPushSubscriptions(as(bruno))
		if i < 4 && len(list) != 1 {
			t.Fatalf("falha %d ainda não apaga", i+1)
		}
	}
	if list, _ := e.svc.ListPushSubscriptions(as(bruno)); len(list) != 0 {
		t.Fatalf("5 falhas seguidas apagam: %+v", list)
	}

	// remover por id ou endpoint; inexistente é 404
	v, _ = e.svc.AddPushSubscription(as(bruno), dev.Sub, "")
	if err := e.svc.RemovePushSubscription(as(bruno), v.ID.String()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.RemovePushSubscription(as(bruno), v.ID.String()), domain.ErrNotFound)
	v, _ = e.svc.AddPushSubscription(as(bruno), dev.Sub, "")
	if err := e.svc.RemovePushSubscription(as(bruno), v.Endpoint); err != nil {
		t.Fatal(err)
	}
}

func TestVAPIDKeyIsGeneratedOncePersistedAndEncrypted(t *testing.T) {
	e, _, bruno, _ := pushEnv(t)
	bg := context.Background()
	k1, err := e.svc.PushVapidKey(as(bruno))
	if err != nil || len(k1) != 87 {
		t.Fatalf("chave pública (65 bytes em base64url): %q %v", k1, err)
	}
	var enc string
	if err := e.svc.Pool.QueryRow(bg, `SELECT value_enc FROM instance_secrets WHERE name = 'vapid'`).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc == "" || strings.Contains(enc, k1) {
		t.Fatal("a chave privada fica cifrada no banco")
	}
	// outro processo (service novo, mesma chave de cifragem) lê a mesma chave; não gera outra
	other := service.New(e.svc.Pool, e.jobs, e.broker)
	other.Integrations.Secrets = e.svc.Integrations.Secrets
	k2, err := other.PushVapidKey(as(bruno))
	if err != nil || k2 != k1 {
		t.Fatalf("mesma chave após reinício: %q %q %v", k2, k1, err)
	}
	var n int
	_ = e.svc.Pool.QueryRow(bg, `SELECT count(*) FROM instance_secrets`).Scan(&n)
	if n != 1 {
		t.Fatalf("uma linha só: %d", n)
	}
	// chave de cifragem errada não abre (e não gera outra por cima)
	bad, _ := secretbox.New("outra-chave", "")
	wrong := service.New(e.svc.Pool, e.jobs, e.broker)
	wrong.Integrations.Secrets = bad
	if _, err := wrong.PushVapidKey(as(bruno)); err == nil {
		t.Fatal("chave de cifragem errada deveria falhar")
	}
	// sem chave no servidor o push fica desligado
	none := service.New(e.svc.Pool, e.jobs, e.broker)
	if none.PushAvailable() {
		t.Fatal("sem HANKO_SECRET_KEY o push não está disponível")
	}
	_, err = none.PushVapidKey(as(bruno))
	wantErr(t, err, domain.ErrInvalid)
	_, err = none.AddPushSubscription(as(bruno), webpushtest.NewDevice("https://push.example/x").Sub, "")
	wantErr(t, err, domain.ErrInvalid)
	if _, err := e.svc.PushVapidKey(bg); err == nil {
		t.Fatal("sem pessoa autenticada não há chave")
	}
}

// @persona num comentário humano acorda a persona, mesmo que a task seja de outra; persona citando persona não acorda.
func TestMentionedPersonaIsWokenOnlyByHumans(t *testing.T) {
	e, ana, _ := caixaEnv(t)
	bg := context.Background()
	owner := e.persona(t, "Dona", nil)
	helper := e.persona(t, "Ajudante", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "tarefa", PersonaID: &owner.ID})
	e.jobs.reset()

	if _, err := e.svc.AddComment(as(ana), ana.Actor(), e.co.ID, tk.ID, service.AddCommentInput{Body: "@" + helper.Slug + " me ajuda aqui?"}); err != nil {
		t.Fatal(err)
	}
	var woke bool
	for _, w := range e.jobs.wakes {
		woke = woke || (w.PersonaID == helper.ID && w.TaskID == tk.ID && w.Trigger == domain.TriggerComment)
	}
	if !woke {
		t.Fatalf("a persona citada deveria ser acordada: %+v", e.jobs.wakes)
	}

	// o despacho aceita a persona citada na task que não é dela, sem mexer no status
	before, _ := e.svc.GetTask(bg, e.co.ID, tk.ID.String())
	run, err := e.svc.CheckoutRun(bg, service.Wake{CompanyID: e.co.ID, PersonaID: helper.ID, TaskID: tk.ID, Trigger: domain.TriggerComment})
	if err != nil || run.PersonaID != helper.ID {
		t.Fatalf("checkout por menção: %+v %v", run, err)
	}
	after, _ := e.svc.GetTask(bg, e.co.ID, tk.ID.String())
	if after.Status != before.Status || after.PersonaID == nil || *after.PersonaID != owner.ID {
		t.Fatalf("a run por menção não muda status nem responsável: %s → %s", before.Status, after.Status)
	}
	// sem menção, outra persona continua recusada
	third := e.persona(t, "Terceira", nil)
	_, err = e.svc.CheckoutRun(bg, service.Wake{CompanyID: e.co.ID, PersonaID: third.ID, TaskID: tk.ID, Trigger: domain.TriggerComment})
	wantErr(t, err, domain.ErrInvalid)

	// persona citando persona: sem wake
	e.jobs.reset()
	if _, err := e.svc.AddComment(bg, domain.PersonaActor(owner.ID), e.co.ID, tk.ID, service.AddCommentInput{Body: "@" + third.Slug + " vem cá"}); err != nil {
		t.Fatal(err)
	}
	for _, w := range e.jobs.wakes {
		if w.PersonaID == third.ID {
			t.Fatalf("persona não acorda persona por menção: %+v", w)
		}
	}
}

// Persona que cita um humano (no comentário ou no resumo da run) cria o item na Caixa dele.
func TestPersonaMentionsHumanInCommentAndRunSummary(t *testing.T) {
	e, ana, bruno := caixaEnv(t)
	bg := context.Background()
	p := e.persona(t, "Ajudante", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "tarefa", PersonaID: &p.ID})
	if _, err := e.svc.AddComment(bg, domain.PersonaActor(p.ID), e.co.ID, tk.ID, service.AddCommentInput{Body: "preciso do @bruno para decidir"}); err != nil {
		t.Fatal(err)
	}
	in, _ := e.svc.Inbox(as(bruno), e.co.ID)
	if len(in.Mentions) != 1 || in.Mentions[0].Author != "Ajudante" {
		t.Fatalf("menção de persona: %+v", in.Mentions)
	}
	// resumo da run
	run, err := e.svc.CheckoutRun(bg, wakeOf(p, tk, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishRun(bg, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "Feito. @ana-souza, confira o relatório."}}); err != nil {
		t.Fatal(err)
	}
	in, _ = e.svc.Inbox(as(ana), e.co.ID)
	if len(in.Mentions) != 1 || !strings.Contains(in.Mentions[0].Excerpt, "confira o relatório") {
		t.Fatalf("menção no resumo da run: %+v", in.Mentions)
	}
}
