package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/webhook"
)

// Variáveis, gatilhos, revisões e arquivar de fluxos (ADR-0047).

func m19Env(t *testing.T) (*env, domain.Persona, time.Time) {
	t.Helper()
	e := newEnv(t)
	box, err := secretbox.New(keyOne, "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	now := time.Date(2026, 10, 21, 13, 30, 0, 0, time.UTC) // quarta; semana ISO 43
	e.svc.Now = func() time.Time { return now }
	return e, e.persona(t, "Ada", nil), now
}

func vars(vs ...domain.FlowVariable) []domain.FlowVariable { return vs }

func newVarFlow(t *testing.T, e *env, p domain.Persona, cron string) service.FlowView {
	t.Helper()
	f, err := e.svc.CreateFlow(context.Background(), domain.HumanActor, e.co.ID, service.CreateFlowInput{
		Name: "Revisar PR", Cron: cron, PersonaID: p.ID,
		Variables:       vars(domain.FlowVariable{Name: "repo", Required: true}, domain.FlowVariable{Name: "ambiente", Default: "staging"}),
		TaskTitle:       "Revisar {{repo}} em {{ambiente}} ({{disparo}}, {{semana}})",
		TaskDescription: "Dia {{data}}, disparo {{n}}, repo {{repo}}"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFlowVariablesValidation(t *testing.T) {
	ctx := context.Background()
	e, p, _ := m19Env(t)
	create := func(in service.CreateFlowInput) error {
		in.Name, in.PersonaID = "F"+fmt.Sprint(time.Now().UnixNano()), p.ID
		_, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, in)
		return err
	}
	wantErr(t, create(service.CreateFlowInput{Variables: vars(domain.FlowVariable{Name: "Repo"})}), domain.ErrInvalid)
	wantErr(t, create(service.CreateFlowInput{Variables: vars(domain.FlowVariable{Name: "data"})}), domain.ErrInvalid)
	wantErr(t, create(service.CreateFlowInput{Variables: vars(domain.FlowVariable{Name: "a"}, domain.FlowVariable{Name: "a"})}), domain.ErrInvalid)
	wantErr(t, create(service.CreateFlowInput{TaskTitle: "{{desconhecida}}"}), domain.ErrInvalid)
	wantErr(t, create(service.CreateFlowInput{TaskDescription: "x {{outra}}"}), domain.ErrInvalid)
	// a agenda exige default nas obrigatórias; sem agenda não
	wantErr(t, create(service.CreateFlowInput{Cron: "0 9 * * *", Variables: vars(domain.FlowVariable{Name: "x", Required: true})}), domain.ErrInvalid)
	if err := create(service.CreateFlowInput{Variables: vars(domain.FlowVariable{Name: "x", Required: true})}); err != nil {
		t.Fatalf("sem agenda, obrigatória sem default: %v", err)
	}
	if err := create(service.CreateFlowInput{Cron: "0 9 * * *", Variables: vars(domain.FlowVariable{Name: "x", Required: true, Default: "1"}), TaskTitle: "{{x}} {{n}}"}); err != nil {
		t.Fatalf("agenda com default: %v", err)
	}

	f := newVarFlow(t, e, p, "")
	// editar o cron para ter agenda com obrigatória sem default é recusado
	_, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Cron: ptr("0 9 * * *")})
	wantErr(t, err, domain.ErrInvalid)
	// tirar uma variável citada no título é recusado
	_, err = e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Variables: &[]domain.FlowVariable{{Name: "repo", Required: true}}})
	wantErr(t, err, domain.ErrInvalid)
}

func TestRunFlowNowVariables(t *testing.T) {
	ctx := context.Background()
	e, p, now := m19Env(t)
	f := newVarFlow(t, e, p, "")
	if f.NextFireAt != nil || f.Cron != "" || len(f.Variables) != 2 {
		t.Fatalf("flow sem agenda: %+v", f.Flow)
	}
	// obrigatória faltando: nada é criado
	_, err := e.svc.RunFlowNowWith(ctx, domain.HumanActor, e.co.ID, f.ID, map[string]string{"ambiente": "prod"})
	wantErr(t, err, domain.ErrInvalid)
	// não declarada
	_, err = e.svc.RunFlowNowWith(ctx, domain.HumanActor, e.co.ID, f.ID, map[string]string{"repo": "a", "zzz": "b"})
	wantErr(t, err, domain.ErrInvalid)
	if tasks, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{}); len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0", len(tasks))
	}
	// o valor entra como texto: não é expandido de novo
	r, err := e.svc.RunFlowNowWith(ctx, domain.HumanActor, e.co.ID, f.ID, map[string]string{"repo": "{{ambiente}}/${secret:x}"})
	if err != nil || r.Task == nil {
		t.Fatalf("run: %+v %v", r, err)
	}
	if r.Task.Title != "Revisar {{ambiente}}/${secret:x} em staging (manual, 2026-W43)" {
		t.Fatalf("title = %q", r.Task.Title)
	}
	if !strings.Contains(r.Task.Description, "Dia 2026-10-21, disparo 1, repo {{ambiente}}") {
		t.Fatalf("description = %q", r.Task.Description)
	}
	if r.Fire.Source != domain.FireManual || r.Fire.Variables["repo"] != "{{ambiente}}/${secret:x}" || r.Fire.Variables["ambiente"] != "staging" {
		t.Fatalf("fire: %+v", r.Fire)
	}
	// com variáveis declaradas o dedupe é por segundo: outro valor no mesmo minuto cria outra task
	now = now.Add(2 * time.Second)
	e.svc.Now = func() time.Time { return now }
	r2, err := e.svc.RunFlowNowWith(ctx, domain.HumanActor, e.co.ID, f.ID, map[string]string{"repo": "outro", "ambiente": "prod"})
	if err != nil || r2.Duplicate || r2.Task == nil || r2.Task.Title != "Revisar outro em prod (manual, 2026-W43)" {
		t.Fatalf("segundo: %+v %v", r2, err)
	}
	fires, err := e.svc.ListFlowFires(ctx, e.co.ID, f.ID, 10)
	if err != nil || len(fires) != 2 || fires[0].Source != domain.FireManual {
		t.Fatalf("fires: %+v %v", fires, err)
	}
}

func TestFlowWithoutSchedule(t *testing.T) {
	ctx := context.Background()
	e, p, now := m19Env(t)
	f, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Manual", PersonaID: p.ID})
	if err != nil || f.NextFireAt != nil || f.Cron != "" {
		t.Fatalf("create: %+v %v", f, err)
	}
	if due, _ := e.svc.DueFlows(ctx, now.Add(24*time.Hour)); len(due) != 0 {
		t.Fatalf("due = %v", due)
	}
	sk, err := e.svc.FireFlow(ctx, f.ID, now)
	if err != nil || !sk.Skipped || sk.SkipReason != "no_schedule" {
		t.Fatalf("fire sem agenda: %+v %v", sk, err)
	}
	// ganhar e perder agenda
	up, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Cron: ptr("0 9 * * *")})
	if err != nil || up.NextFireAt == nil {
		t.Fatalf("ganha agenda: %+v %v", up, err)
	}
	up, err = e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Cron: ptr("")})
	if err != nil || up.NextFireAt != nil || up.Cron != "" {
		t.Fatalf("perde agenda: %+v %v", up, err)
	}
}

func TestScheduledFireWithDefaults(t *testing.T) {
	ctx := context.Background()
	e, p, now := m19Env(t)
	f, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Diário", Cron: "0 9 * * *", PersonaID: p.ID,
		Variables: vars(domain.FlowVariable{Name: "time", Required: true, Default: "plataforma"}), TaskTitle: "Resumo {{time}} {{data}}"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.FireFlow(ctx, f.ID, *f.NextFireAt)
	if err != nil || r.Task == nil || r.Task.Title != "Resumo plataforma "+f.NextFireAt.In(mustLoc(t, "America/Sao_Paulo")).Format("2006-01-02") {
		t.Fatalf("fire: %+v %v (now %v)", r, err, now)
	}
	if r.Fire.Source != domain.FireSchedule {
		t.Fatalf("source = %q", r.Fire.Source)
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func signed(secret string, now time.Time, body string) string {
	return webhook.Sign(secret, now, []byte(body))
}

func TestFlowWebhookTrigger(t *testing.T) {
	ctx := context.Background()
	e, p, now := m19Env(t)
	f := newVarFlow(t, e, p, "")
	tr, err := e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{
		Kind: domain.TriggerKindWebhook, Name: "github", Mapping: map[string]string{"repo": "repository.full_name"}})
	if err != nil {
		t.Fatal(err)
	}
	if tr.SecretValue == "" || !strings.HasPrefix(tr.SecretRef, "${secret:hook-") || tr.Path != "/hooks/flows/"+tr.ID.String() {
		t.Fatalf("trigger: %+v", tr)
	}
	// mapping para variável que não existe é recusado
	_, err = e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{
		Kind: domain.TriggerKindWebhook, Name: "ruim", Mapping: map[string]string{"nada": "x"}})
	wantErr(t, err, domain.ErrInvalid)
	// nome repetido
	_, err = e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{Kind: domain.TriggerKindWebhook, Name: "github"})
	wantErr(t, err, domain.ErrConflict)

	body := `{"repository":{"full_name":"acme/api"},"ambiente":"prod","extra":1}`
	fire := func(sig, delivery, b string) (service.FlowFireResult, error) {
		return e.svc.FireFlowWebhook(ctx, service.WebhookFireInput{TriggerID: tr.ID, Body: []byte(b), Signature: sig, Delivery: delivery})
	}
	// assinatura errada / ausente / vencida
	_, err = fire("t=1,v1=abc", "", body)
	if !errors.Is(err, service.ErrWebhookUnauthorized) {
		t.Fatalf("assinatura inválida: %v", err)
	}
	_, err = fire("", "", body)
	if !errors.Is(err, service.ErrWebhookUnauthorized) {
		t.Fatalf("sem assinatura: %v", err)
	}
	_, err = fire(signed(tr.SecretValue, now.Add(-time.Hour), body), "", body)
	if !errors.Is(err, service.ErrWebhookUnauthorized) {
		t.Fatalf("vencida: %v", err)
	}
	// corpo adulterado
	_, err = fire(signed(tr.SecretValue, now, body), "", body+" ")
	if !errors.Is(err, service.ErrWebhookUnauthorized) {
		t.Fatalf("corpo adulterado: %v", err)
	}
	// gatilho desconhecido
	_, err = e.svc.FireFlowWebhook(ctx, service.WebhookFireInput{TriggerID: domain.NewID(), Body: []byte(body), Signature: signed(tr.SecretValue, now, body)})
	wantErr(t, err, domain.ErrNotFound)

	// ok: variáveis da carga; ambiente vem da chave de mesmo nome
	r, err := fire(signed(tr.SecretValue, now, body), "d-1", body)
	if err != nil || r.Task == nil || r.Duplicate {
		t.Fatalf("fire: %+v %v", r, err)
	}
	if r.Task.Title != "Revisar acme/api em prod (webhook, 2026-W43)" || r.Task.CreatedBy.Type != domain.ActorSystem {
		t.Fatalf("task: %q by %v", r.Task.Title, r.Task.CreatedBy)
	}
	if r.Fire.Source != domain.FireWebhook || r.Fire.TriggerID == nil || *r.Fire.TriggerID != tr.ID {
		t.Fatalf("fire: %+v", r.Fire)
	}
	// a mesma entrega não cria outra task
	dup, err := fire(signed(tr.SecretValue, now, body), "d-1", body)
	if err != nil || !dup.Duplicate || dup.Task == nil || dup.Task.ID != r.Task.ID {
		t.Fatalf("dup: %+v %v", dup, err)
	}
	// entrega nova (sem id) cria outra
	r3, err := fire(signed(tr.SecretValue, now, body), "", body)
	if err != nil || r3.Duplicate || r3.Task == nil || r3.Task.ID == r.Task.ID {
		t.Fatalf("sem id: %+v %v", r3, err)
	}
	// obrigatória faltando: 422 e nada criado
	bad := `{"ambiente":"x"}`
	before, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	_, err = fire(signed(tr.SecretValue, now, bad), "d-2", bad)
	wantErr(t, err, domain.ErrInvalid)
	// corpo que não é objeto
	_, err = fire(signed(tr.SecretValue, now, "[1,2]"), "d-3", "[1,2]")
	wantErr(t, err, domain.ErrInvalid)
	after, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(after) != len(before) {
		t.Fatalf("tasks %d -> %d após entrega recusada", len(before), len(after))
	}
	// a entrega recusada não consome o id: reenviada corrigida, passa
	if r, err := fire(signed(tr.SecretValue, now, body), "d-2", body); err != nil || r.Duplicate {
		t.Fatalf("reenvio corrigido: %+v %v", r, err)
	}

	// gatilho desligado / fluxo desativado / arquivado → 409
	off := false
	if _, err := e.svc.UpdateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, tr.ID, service.UpdateFlowTriggerInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	_, err = fire(signed(tr.SecretValue, now, body), "", body)
	wantErr(t, err, domain.ErrConflict)
	on := true
	_, _ = e.svc.UpdateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, tr.ID, service.UpdateFlowTriggerInput{Enabled: &on})
	if _, err := e.svc.ArchiveFlow(ctx, domain.HumanActor, e.co.ID, f.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err = fire(signed(tr.SecretValue, now, body), "", body)
	wantErr(t, err, domain.ErrConflict)
}

func TestFlowWebhookRateLimit(t *testing.T) {
	ctx := context.Background()
	e, p, now := m19Env(t)
	f, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Lim", PersonaID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{Kind: domain.TriggerKindWebhook, Name: "h", RatePerMin: 2})
	if err != nil {
		t.Fatal(err)
	}
	fire := func() error {
		_, err := e.svc.FireFlowWebhook(ctx, service.WebhookFireInput{TriggerID: tr.ID, Body: []byte(`{}`), Signature: signed(tr.SecretValue, now, `{}`)})
		return err
	}
	for i := 0; i < 2; i++ {
		if err := fire(); err != nil {
			t.Fatalf("entrega %d: %v", i, err)
		}
	}
	if err := fire(); !errors.Is(err, service.ErrWebhookThrottled) {
		t.Fatalf("terceira: %v", err)
	}
	// passado um minuto o balde recarrega
	now = now.Add(time.Minute)
	e.svc.Now = func() time.Time { return now }
	if err := fire(); err != nil {
		t.Fatalf("após recarga: %v", err)
	}
}

func TestFlowTriggerValidation(t *testing.T) {
	ctx := context.Background()
	e, p, _ := m19Env(t)
	f, _ := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "V", PersonaID: p.ID})
	mk := func(in service.CreateFlowTriggerInput) error {
		_, err := e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, in)
		return err
	}
	wantErr(t, mk(service.CreateFlowTriggerInput{Kind: "x", Name: "a"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateFlowTriggerInput{Kind: domain.TriggerKindWebhook, Name: ""}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateFlowTriggerInput{Kind: domain.TriggerKindWebhook, Name: "a", RatePerMin: 9999}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateFlowTriggerInput{Kind: domain.TriggerKindWebhook, Name: "a", EventVerb: "task.created"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateFlowTriggerInput{Kind: domain.TriggerKindWebhook, Name: "a", SecretRef: "${secret:nao-existe}"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateFlowTriggerInput{Kind: domain.TriggerKindEvent, Name: "e", EventVerb: "flow.fired"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateFlowTriggerInput{Kind: domain.TriggerKindEvent, Name: "e", EventVerb: "nonsense"}), domain.ErrInvalid)
	wantErr(t, mk(service.CreateFlowTriggerInput{Kind: domain.TriggerKindEvent, Name: "e", EventVerb: "task.created", SecretRef: "${secret:x}"}), domain.ErrInvalid)
	if err := mk(service.CreateFlowTriggerInput{Kind: domain.TriggerKindEvent, Name: "ok", EventVerb: "task.*"}); err != nil {
		t.Fatal(err)
	}
	// segredo existente da empresa serve
	if _, err := e.svc.CreateSecret(ctx, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "meu-hmac", Value: "s3cret"}); err != nil {
		t.Fatal(err)
	}
	tr, err := e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{Kind: domain.TriggerKindWebhook, Name: "w", SecretRef: "${secret:meu-hmac}"})
	if err != nil || tr.SecretValue != "" || tr.SecretRef != "${secret:meu-hmac}" {
		t.Fatalf("secret existente: %+v %v", tr, err)
	}
	// o segredo em uso não pode ser apagado sem force
	if err := e.svc.DeleteSecret(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "meu-hmac", false); err == nil {
		t.Fatal("apagou segredo em uso por gatilho")
	}
	// persona não cria gatilho
	_, err = e.svc.CreateFlowTrigger(ctx, domain.PersonaActor(p.ID), e.co.ID, f.ID, service.CreateFlowTriggerInput{Kind: domain.TriggerKindEvent, Name: "p", EventVerb: "task.created"})
	wantErr(t, err, domain.ErrForbidden)

	list, err := e.svc.ListFlowTriggers(ctx, e.co.ID, f.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	fv, _ := e.svc.GetFlow(ctx, e.co.ID, f.Slug)
	if len(fv.Triggers) != 2 {
		t.Fatalf("GetFlow triggers = %d", len(fv.Triggers))
	}
	if err := e.svc.DeleteFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, tr.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.DeleteFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, tr.ID), domain.ErrNotFound)
}

func TestFlowEventTrigger(t *testing.T) {
	ctx := context.Background()
	e, p, _ := m19Env(t)
	f, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Reage", PersonaID: p.ID,
		Variables: vars(domain.FlowVariable{Name: "chave", Required: true}, domain.FlowVariable{Name: "titulo"}),
		TaskTitle: "Novo: {{titulo}} ({{chave}}, {{disparo}})"})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{
		Kind: domain.TriggerKindEvent, Name: "ao criar", EventVerb: "task.created", EventFilter: map[string]string{"data.priority": "high"},
		Mapping: map[string]string{"chave": "data.key", "titulo": "data.title"}})
	if err != nil {
		t.Fatal(err)
	}
	other := e.persona(t, "Bob", nil)
	pid := other.ID
	// não casa o filtro
	e.task(t, service.CreateTaskInput{Title: "baixa", PersonaID: &pid, Priority: domain.PriorityLow})
	tasks, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1 (filtro)", len(tasks))
	}
	// casa: nasce a task do fluxo; e a task do fluxo (task.created) NÃO realimenta o gatilho
	src := e.task(t, service.CreateTaskInput{Title: "urgente", PersonaID: &pid, Priority: domain.PriorityHigh})
	tasks, _ = e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(tasks) != 3 {
		t.Fatalf("tasks = %d, want 3 (origem + baixa + do fluxo)", len(tasks))
	}
	var got *service.TaskView
	for i := range tasks {
		if strings.HasPrefix(tasks[i].Title, "Novo:") {
			got = &tasks[i]
		}
	}
	if got == nil || got.Title != "Novo: urgente ("+src.Key+", evento)" || got.CreatedBy.Type != domain.ActorSystem {
		t.Fatalf("task do fluxo: %+v", got)
	}
	fires, _ := e.svc.ListFlowFires(ctx, e.co.ID, f.ID, 10)
	if len(fires) != 1 || fires[0].Source != domain.FireEvent || fires[0].TriggerName != "ao criar" {
		t.Fatalf("fires: %+v", fires)
	}
	// desligar o gatilho ou o fluxo para de disparar; arquivado também
	off := false
	_, _ = e.svc.UpdateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, tr.ID, service.UpdateFlowTriggerInput{Enabled: &off})
	e.task(t, service.CreateTaskInput{Title: "urgente 2", PersonaID: &pid, Priority: domain.PriorityHigh})
	if fires, _ = e.svc.ListFlowFires(ctx, e.co.ID, f.ID, 10); len(fires) != 1 {
		t.Fatalf("gatilho desligado disparou: %d", len(fires))
	}
}

func TestFlowEventTriggerMissingVariableDoesNotFailFact(t *testing.T) {
	ctx := context.Background()
	e, p, _ := m19Env(t)
	f, _ := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Exige", PersonaID: p.ID,
		Variables: vars(domain.FlowVariable{Name: "x", Required: true})})
	if _, err := e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{
		Kind: domain.TriggerKindEvent, Name: "sem mapa", EventVerb: "task.created"}); err != nil {
		t.Fatal(err)
	}
	e.task(t, service.CreateTaskInput{Title: "qualquer"}) // o fato original passa
	tasks, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "flow"})
	found := false
	for _, a := range acts {
		if a.Verb == "flow.skipped" {
			found = true
		}
	}
	if !found {
		t.Fatal("faltou flow.skipped (variáveis)")
	}
}

func TestFlowEventTriggerThrottle(t *testing.T) {
	ctx := context.Background()
	e, p, _ := m19Env(t)
	f, _ := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Lento", PersonaID: p.ID})
	if _, err := e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{
		Kind: domain.TriggerKindEvent, Name: "t", EventVerb: "task.created", RatePerMin: 1}); err != nil {
		t.Fatal(err)
	}
	other := e.persona(t, "Bob", nil)
	pid := other.ID
	for i := 0; i < 3; i++ {
		e.task(t, service.CreateTaskInput{Title: fmt.Sprintf("t%d", i), PersonaID: &pid})
	}
	fires, _ := e.svc.ListFlowFires(ctx, e.co.ID, f.ID, 10)
	if len(fires) != 1 {
		t.Fatalf("disparos = %d, want 1 (limite 1/min)", len(fires))
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "flow"})
	n := 0
	for _, a := range acts {
		if a.Verb == "flow.throttled" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("flow.throttled = %d, want 1 (uma anotação por minuto)", n)
	}
}

func TestFlowRevisionsAndRestore(t *testing.T) {
	ctx := context.Background()
	e, p, _ := m19Env(t)
	f, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Rev", PersonaID: p.ID, TaskTitle: "um", Cron: "0 9 * * *"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Revision != 1 {
		t.Fatalf("revision = %d", f.Revision)
	}
	if _, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{TaskTitle: ptr("dois"), Name: ptr("Rev 2")}); err != nil {
		t.Fatal(err)
	}
	// sem mudança não cria revisão
	if _, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{TaskTitle: ptr("dois")}); err != nil {
		t.Fatal(err)
	}
	// gatilho cria revisão
	if _, err := e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{Kind: domain.TriggerKindEvent, Name: "e", EventVerb: "gate.*"}); err != nil {
		t.Fatal(err)
	}
	revs, err := e.svc.FlowRevisions(ctx, e.co.ID, f.ID, 10)
	if err != nil || len(revs) != 3 {
		t.Fatalf("revisões: %d %v", len(revs), err)
	}
	if revs[0].Revision != 3 || revs[0].Summary != "triggers" || len(revs[0].Snapshot.Triggers) != 1 {
		t.Fatalf("rev 3: %+v", revs[0])
	}
	if revs[1].Revision != 2 || revs[1].Summary != "name, task_title" || revs[1].Snapshot.TaskTitle != "dois" || revs[1].Snapshot.Name != "Rev 2" {
		t.Fatalf("rev 2: %+v", revs[1])
	}
	if revs[2].Origin != "create" || revs[2].Snapshot.TaskTitle != "um" {
		t.Fatalf("rev 1: %+v", revs[2])
	}
	// restaurar a 1 = edição nova (rev 4) com origin restore
	got, err := e.svc.RestoreFlowRevision(ctx, domain.HumanActor, e.co.ID, f.ID, 1)
	if err != nil || got.Name != "Rev" || got.TaskTitle != "um" || got.Revision != 4 {
		t.Fatalf("restore: %+v %v", got, err)
	}
	r4, _ := e.svc.GetFlowRevision(ctx, e.co.ID, f.ID, 4)
	if r4.Origin != "restore" || r4.RestoredFrom == nil || *r4.RestoredFrom != 1 {
		t.Fatalf("rev 4: %+v", r4)
	}
	// os gatilhos não são restaurados
	if trs, _ := e.svc.ListFlowTriggers(ctx, e.co.ID, f.ID); len(trs) != 1 {
		t.Fatalf("gatilhos após restore = %d", len(trs))
	}
	_, err = e.svc.GetFlowRevision(ctx, e.co.ID, f.ID, 99)
	wantErr(t, err, domain.ErrNotFound)
	// append-only: UPDATE direto é recusado pelo banco
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE flow_revisions SET summary = 'x' WHERE flow_id = $1`, f.ID); err == nil {
		t.Fatal("UPDATE em flow_revisions passou")
	}
}

func TestArchiveFlow(t *testing.T) {
	ctx := context.Background()
	e, p, now := m19Env(t)
	f, _ := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Arq", PersonaID: p.ID, Cron: "0 9 * * *"})
	a, err := e.svc.ArchiveFlow(ctx, domain.HumanActor, e.co.ID, f.ID, true)
	if err != nil || a.ArchivedAt == nil || a.Enabled || a.NextFireAt != nil {
		t.Fatalf("archive: %+v %v", a, err)
	}
	if l, _ := e.svc.ListFlows(ctx, e.co.ID); len(l) != 0 {
		t.Fatalf("lista padrão = %d", len(l))
	}
	if l, _ := e.svc.ListFlowsFiltered(ctx, e.co.ID, true); len(l) != 1 {
		t.Fatalf("arquivados = %d", len(l))
	}
	if due, _ := e.svc.DueFlows(ctx, now.Add(48*time.Hour)); len(due) != 0 {
		t.Fatal("arquivado vencido")
	}
	_, err = e.svc.RunFlowNow(ctx, domain.HumanActor, e.co.ID, f.ID)
	wantErr(t, err, domain.ErrConflict)
	_, err = e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Name: ptr("x")})
	wantErr(t, err, domain.ErrConflict)
	_, err = e.svc.CreateFlowTrigger(ctx, domain.HumanActor, e.co.ID, f.ID, service.CreateFlowTriggerInput{Kind: domain.TriggerKindEvent, Name: "e", EventVerb: "task.created"})
	wantErr(t, err, domain.ErrConflict)
	sk, err := e.svc.FireFlow(ctx, f.ID, now)
	if err != nil || !sk.Skipped || sk.SkipReason != "archived" {
		t.Fatalf("FireFlow arquivado: %+v %v", sk, err)
	}
	// ainda se lê por slug
	if g, err := e.svc.GetFlow(ctx, e.co.ID, "arq"); err != nil || g.ArchivedAt == nil {
		t.Fatalf("get: %+v %v", g, err)
	}
	// arquivar de novo é no-op; desarquivar volta desativado
	if _, err := e.svc.ArchiveFlow(ctx, domain.HumanActor, e.co.ID, f.ID, true); err != nil {
		t.Fatal(err)
	}
	u, err := e.svc.ArchiveFlow(ctx, domain.HumanActor, e.co.ID, f.ID, false)
	if err != nil || u.ArchivedAt != nil || u.Enabled {
		t.Fatalf("unarchive: %+v %v", u, err)
	}
	en, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Enabled: ptr(true)})
	if err != nil || !en.Enabled || en.NextFireAt == nil {
		t.Fatalf("reativar: %+v %v", en, err)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "flow"})
	verbs := map[string]bool{}
	for _, x := range acts {
		verbs[x.Verb] = true
	}
	if !verbs["flow.archived"] || !verbs["flow.unarchived"] {
		t.Fatalf("atividades: %v", verbs)
	}
	// membro comum não arquiva
	_, err = e.svc.ArchiveFlow(ctx, domain.HumanActor, domain.NewID(), f.ID, true)
	if err == nil {
		t.Fatal("arquivou fluxo de outra empresa")
	}
}
