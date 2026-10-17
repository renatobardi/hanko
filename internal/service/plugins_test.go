package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

const pluginTestKey = "chave-de-teste-dos-plugins-com-32-bytes-ou-mais"

// O plugin de exemplo (examples/plugins/hello) é compilado uma vez por execução do pacote.
var (
	helloOnce sync.Once
	helloBin  string
	helloErr  error
)

func buildHello(t *testing.T) string {
	t.Helper()
	helloOnce.Do(func() {
		dir, err := os.MkdirTemp("", "hanko-hello-bin-")
		if err != nil {
			helloErr = err
			return
		}
		helloBin = filepath.Join(dir, "hello")
		cmd := exec.Command("go", "build", "-o", helloBin, "./examples/plugins/hello") //nolint:gosec // teste
		cmd.Dir = filepath.Join("..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			helloErr = errors.New(string(out))
		}
	})
	if helloErr != nil {
		t.Fatalf("compilar o plugin de exemplo: %v", helloErr)
	}
	return helloBin
}

// installHelloDir copia o plugin de exemplo para root/hello, com o manifesto alterado por mod.
func installHelloDir(t *testing.T, root string, mod func(m map[string]any)) {
	t.Helper()
	bin := buildHello(t)
	dir := filepath.Join(root, "hello")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "plugins", "hello", domain.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if mod != nil {
		mod(m)
	}
	out, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, domain.ManifestFile), out, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(bin) //nolint:gosec // binário compilado acima
	if err != nil {
		t.Fatal(err)
	}
	// grava ao lado e renomeia: o binário antigo pode estar rodando (atualização com o plugin de pé)
	tmp := filepath.Join(dir, ".hello.new")
	if err := os.WriteFile(tmp, b, 0o755); err != nil { //nolint:gosec // executável do plugin de teste
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "hello")); err != nil {
		t.Fatal(err)
	}
}

// pluginJobs registra os eventos e rodadas de job enfileirados.
type pluginJobs struct {
	fakeJobs
	pmu    sync.Mutex
	events [][3]domain.ID
	runs   []domain.ID
}

func (j *pluginJobs) DeliverPluginEventTx(_ context.Context, _ pgx.Tx, pluginID, companyID, activityID domain.ID) error {
	j.pmu.Lock()
	defer j.pmu.Unlock()
	j.events = append(j.events, [3]domain.ID{pluginID, companyID, activityID})
	return nil
}

func (j *pluginJobs) RunPluginJobTx(_ context.Context, _ pgx.Tx, id domain.ID) error {
	j.pmu.Lock()
	defer j.pmu.Unlock()
	j.runs = append(j.runs, id)
	return nil
}

func (j *pluginJobs) takeEvents() [][3]domain.ID {
	j.pmu.Lock()
	defer j.pmu.Unlock()
	out := j.events
	j.events = nil
	return out
}

func (j *pluginJobs) takeRuns() []domain.ID {
	j.pmu.Lock()
	defer j.pmu.Unlock()
	out := j.runs
	j.runs = nil
	return out
}

type plugEnv struct {
	*env
	pj   *pluginJobs
	mgr  *plugin.Manager
	root string
	now  *time.Time
	mu   *sync.Mutex
}

func newPlugEnv(t *testing.T, withKey bool) *plugEnv {
	t.Helper()
	pool := testdb.New(t)
	jobs := &pluginJobs{}
	broker := events.NewBroker()
	svc := service.New(pool, jobs, broker)
	key := ""
	if withKey {
		key = pluginTestKey
	}
	box, err := secretbox.New(key, "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations = service.IntegrationsConfig{Secrets: box}
	now := time.Now().UTC()
	var mu sync.Mutex
	svc.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	root := t.TempDir()
	mgr := plugin.NewManager(plugin.Options{HostVersion: "test", Handler: svc.HandlePluginCall, OnLog: svc.PluginLogLine,
		OnState: svc.PluginProcessState, BackoffMin: 50 * time.Millisecond, ShutdownWait: 2 * time.Second, KillGrace: time.Second})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		mgr.StopAll(ctx)
	})
	svc.Plugins = service.PluginsConfig{Dir: root, DataDir: t.TempDir(), Runtime: mgr}
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme Corp"})
	if err != nil {
		t.Fatal(err)
	}
	return &plugEnv{env: &env{svc: svc, jobs: &jobs.fakeJobs, broker: broker, co: co}, pj: jobs, mgr: mgr, root: root, now: &now, mu: &mu}
}

func (e *plugEnv) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	*e.now = e.now.Add(d)
}

func (e *plugEnv) install(t *testing.T) service.PluginView {
	t.Helper()
	installHelloDir(t, e.root, nil)
	v, err := e.svc.InstallPlugin(context.Background(), "hello")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if v.Status != domain.PluginReady {
		t.Fatalf("status depois de instalar = %s (%s)", v.Status, v.LastError)
	}
	return v
}

func (e *plugEnv) enable(t *testing.T, cfg map[string]any) service.CompanyPlugin {
	t.Helper()
	on := true
	cp, err := e.svc.UpdateCompanyPlugin(context.Background(), domain.HumanActor, e.co.ID, "hello", service.UpdateCompanyPluginInput{Enabled: &on, Config: cfg})
	if err != nil {
		t.Fatalf("ligar: %v", err)
	}
	return cp
}

func TestPluginLifecycle(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	installHelloDir(t, e.root, nil)
	if err := os.MkdirAll(filepath.Join(e.root, "quebrado"), 0o755); err != nil {
		t.Fatal(err)
	}
	ov, err := e.svc.PluginOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Available) != 2 || ov.Available[0].Key != "hello" || ov.Available[0].Error != "" || ov.Available[1].Error == "" {
		t.Fatalf("disponíveis = %+v", ov.Available)
	}
	if _, err := e.svc.InstallPlugin(ctx, "quebrado"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("instalar sem manifesto = %v", err)
	}
	v := e.install(t)
	if strings.Join(v.GrantedCapabilities, ",") != "tasks.read,tasks.write,events.subscribe,agent.tools,widgets,jobs,state" {
		t.Errorf("capacidades = %v", v.GrantedCapabilities)
	}
	if _, err := e.svc.InstallPlugin(ctx, "hello"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("instalar de novo = %v", err)
	}
	// nenhuma empresa ligou: o processo de prova não fica de pé, e não há tools
	if st := e.mgr.Status("hello"); st.Running {
		t.Fatalf("processo sem empresa ligada: %+v", st)
	}
	if tools, _ := e.svc.PluginTools(ctx, e.co.ID); len(tools) != 0 {
		t.Fatalf("tools sem o plugin ligado: %+v", tools)
	}

	cp := e.enable(t, nil)
	if !cp.Enabled || !cp.Active {
		t.Fatalf("ligado = %+v", cp)
	}
	waitUntil(t, "processo de pé", func() bool { return e.mgr.Status("hello").Ready })
	tools, err := e.svc.PluginTools(ctx, e.co.ID)
	if err != nil || len(tools) != 1 || tools[0].Name != "plugin_hello_hello_echo" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	p := e.persona(t, "Ana", nil)
	res, err := e.svc.CallPluginTool(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PluginToolCall{Name: tools[0].Name,
		Arguments: json.RawMessage(`{"text":"oi"}`), PersonaID: &p.ID})
	if err != nil || res.Text != "Olá, ana: oi" {
		t.Fatalf("tool = %+v, %v", res, err)
	}
	res, err = e.svc.CallPluginTool(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PluginToolCall{Name: tools[0].Name, Arguments: json.RawMessage(`{}`)})
	if err != nil || !res.IsError {
		t.Fatalf("tool com erro = %+v, %v", res, err)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "plugin"})
	if n := pluginVerbCount(acts, "plugin.tool_called"); n != 2 || pluginVerbCount(acts, "plugin.enabled") != 1 {
		t.Fatalf("atividade = %d tool_called, %v", n, pluginVerbs(acts))
	}

	// desligar na instalação: o processo para e as tools somem
	if v, err := e.svc.SetPluginEnabled(ctx, "hello", false); err != nil || v.Status != domain.PluginDisabled {
		t.Fatalf("desligar = %+v, %v", v.Status, err)
	}
	if st := e.mgr.Status("hello"); st.Running {
		t.Fatal("processo continua depois de desligar")
	}
	if tools, _ := e.svc.PluginTools(ctx, e.co.ID); len(tools) != 0 {
		t.Fatal("tools continuam depois de desligar")
	}
	if _, err := e.svc.CallPluginTool(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PluginToolCall{Name: "plugin_hello_hello_echo"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tool com o plugin desligado = %v", err)
	}
	if _, err := e.svc.SetPluginEnabled(ctx, "hello", true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "processo de volta", func() bool { return e.mgr.Status("hello").Ready })

	// desinstalar apaga configuração e estado; o processo para
	if err := e.svc.UninstallPlugin(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if st := e.mgr.Status("hello"); st.Running {
		t.Fatal("processo continua depois de desinstalar")
	}
	list, _ := e.svc.ListCompanyPlugins(ctx, e.co.ID)
	if len(list) != 0 {
		t.Fatalf("plugins na empresa depois de desinstalar = %+v", list)
	}
	if err := e.svc.UninstallPlugin(ctx, "hello"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("desinstalar de novo = %v", err)
	}
	// reinstalar começa do zero (empresa desligada)
	if v, err := e.svc.InstallPlugin(ctx, "hello"); err != nil || v.Status != domain.PluginReady {
		t.Fatalf("reinstalar = %+v, %v", v, err)
	}
	cp, err = e.svc.GetCompanyPlugin(ctx, e.co.ID, "hello")
	if err != nil || cp.Enabled {
		t.Fatalf("depois de reinstalar = %+v, %v", cp, err)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("esperando %s", what)
}

func pluginVerbCount(acts []domain.ActivityEvent, verb string) int {
	n := 0
	for _, a := range acts {
		if a.Verb == verb {
			n++
		}
	}
	return n
}

func pluginVerbs(acts []domain.ActivityEvent) []string {
	out := make([]string, 0, len(acts))
	for _, a := range acts {
		out = append(out, a.Verb)
	}
	return out
}

func TestPluginRoles(t *testing.T) {
	e := newPlugEnv(t, true)
	installHelloDir(t, e.root, nil)
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
		u := mkUser(t, e.env, string(r)+"@x.com", domain.RoleMember)
		join(t, e.env, u, r)
		users[r] = u
	}
	installOwner := mkUser(t, e.env, "dono@x.com", domain.RoleOwner)
	outsider := mkUser(t, e.env, "fora@x.com", domain.RoleMember)

	// instalação: só o dono da instalação (nem o owner da empresa)
	for _, r := range []domain.MemberRole{domain.MemberAdmin, domain.MemberOwner} {
		if _, err := e.svc.InstallPlugin(as(users[r]), "hello"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("instalar como %s = %v", r, err)
		}
		if _, err := e.svc.PluginOverview(as(users[r])); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("visão da instalação como %s = %v", r, err)
		}
	}
	if _, err := e.svc.InstallPlugin(as(installOwner), "hello"); err != nil {
		t.Fatal(err)
	}
	// empresa: qualquer membro lê; só admin+ liga e configura; quem não é membro não vê
	on := true
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember} {
		if list, err := e.svc.ListCompanyPlugins(as(users[r]), e.co.ID); err != nil || len(list) != 1 {
			t.Fatalf("listar como %s = %v %v", r, list, err)
		}
		_, err := e.svc.UpdateCompanyPlugin(as(users[r]), users[r].Actor(), e.co.ID, "hello", service.UpdateCompanyPluginInput{Enabled: &on})
		wantErr(t, err, domain.ErrForbidden)
		_, err = e.svc.ListPluginState(as(users[r]), e.co.ID, "hello", "", 10)
		wantErr(t, err, domain.ErrForbidden)
	}
	if _, err := e.svc.UpdateCompanyPlugin(as(users[domain.MemberAdmin]), users[domain.MemberAdmin].Actor(), e.co.ID, "hello",
		service.UpdateCompanyPluginInput{Enabled: &on}); err != nil {
		t.Fatalf("ligar como admin: %v", err)
	}
	_, err := e.svc.ListCompanyPlugins(as(outsider), e.co.ID)
	wantErr(t, err, domain.ErrNotFound)
	_, err = e.svc.PluginWidgets(as(outsider), e.co.ID, service.PluginWidgetRef{Placement: domain.WidgetDashboard})
	wantErr(t, err, domain.ErrNotFound)
	for name, run := range map[string]func(ctx context.Context) error{
		"upgrade":   func(ctx context.Context) error { _, err := e.svc.UpgradePlugin(ctx, "hello"); return err },
		"approve":   func(ctx context.Context) error { _, err := e.svc.ApprovePluginUpgrade(ctx, "hello"); return err },
		"disable":   func(ctx context.Context) error { _, err := e.svc.SetPluginEnabled(ctx, "hello", false); return err },
		"uninstall": func(ctx context.Context) error { return e.svc.UninstallPlugin(ctx, "hello") },
		"logs":      func(ctx context.Context) error { _, err := e.svc.ListPluginLogs(ctx, "hello", 10); return err },
	} {
		if err := run(as(users[domain.MemberOwner])); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s como owner da empresa = %v", name, err)
		}
	}
}

func TestPluginSecretsEncryptedAndNeverReturned(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.install(t)
	const secret = "tok-SUPER-SECRETO-123"
	cp := e.enable(t, map[string]any{"greeting": "Oi", "api_token": secret, "comment_new_tasks": "false"})
	if cp.Config["greeting"] != "Oi" || cp.Config["comment_new_tasks"] != false || strings.Join(cp.SecretsSet, ",") != "api_token" {
		t.Fatalf("config = %+v secrets=%v", cp.Config, cp.SecretsSet)
	}
	if _, ok := cp.Config["api_token"]; ok {
		t.Fatal("segredo voltou na configuração")
	}
	// no banco: config sem o segredo; secret_enc cifrado
	var cfg, enc string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT config::text, secret_enc FROM plugin_company_settings`).Scan(&cfg, &enc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg, secret) || strings.Contains(enc, secret) || !strings.HasPrefix(enc, "v1:") {
		t.Fatalf("segredo em claro no banco: %s / %s", cfg, enc)
	}
	// atividade com o nome do campo, nunca o valor
	var acts string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT string_agg(data::text, ' ') FROM activity_events WHERE subject_type = 'plugin'`).Scan(&acts); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(acts, secret) || !strings.Contains(acts, "api_token") {
		t.Fatalf("atividade = %s", acts)
	}
	// o próprio plugin recebe o segredo pelo config.get
	res, rerr := e.svc.HandlePluginCall(ctx, "hello", "config.get", json.RawMessage(`{"company":"`+e.co.Slug+`"}`))
	if rerr != nil {
		t.Fatal(rerr)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), secret) {
		t.Fatalf("config.get = %s", b)
	}
	// log do plugin mascara o segredo
	if _, rerr := e.svc.HandlePluginCall(ctx, "hello", "log", json.RawMessage(`{"company":"`+e.co.Slug+`","message":"token=`+secret+`"}`)); rerr != nil {
		t.Fatal(rerr)
	}
	logs, err := e.svc.ListPluginLogs(ctx, "hello", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range logs {
		if strings.Contains(l.Message, secret) {
			t.Fatalf("segredo no log: %s", l.Message)
		}
	}
	// segredo vazio mantém; clear apaga
	cp, err = e.svc.UpdateCompanyPlugin(ctx, domain.HumanActor, e.co.ID, "hello", service.UpdateCompanyPluginInput{Config: map[string]any{"api_token": ""}})
	if err != nil || len(cp.SecretsSet) != 1 {
		t.Fatalf("segredo vazio = %+v %v", cp.SecretsSet, err)
	}
	cp, err = e.svc.UpdateCompanyPlugin(ctx, domain.HumanActor, e.co.ID, "hello", service.UpdateCompanyPluginInput{ClearSecrets: []string{"api_token"}})
	if err != nil || len(cp.SecretsSet) != 0 {
		t.Fatalf("apagar segredo = %+v %v", cp.SecretsSet, err)
	}
	// validação: campo desconhecido, tipo errado, recusa do próprio plugin
	for _, in := range []map[string]any{{"nada": "x"}, {"comment_new_tasks": "talvez"}, {"greeting": "isso é proibido"}} {
		if _, err := e.svc.UpdateCompanyPlugin(ctx, domain.HumanActor, e.co.ID, "hello", service.UpdateCompanyPluginInput{Config: in}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("config %v = %v", in, err)
		}
	}
}

func TestPluginSecretNeedsServerKey(t *testing.T) {
	e := newPlugEnv(t, false)
	e.install(t)
	on := true
	_, err := e.svc.UpdateCompanyPlugin(context.Background(), domain.HumanActor, e.co.ID, "hello",
		service.UpdateCompanyPluginInput{Enabled: &on, Config: map[string]any{"api_token": "x"}})
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "HANKO_SECRET_KEY") {
		t.Fatalf("segredo sem chave = %v", err)
	}
}

func TestPluginHostDeniesOutsideCapabilities(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.install(t)
	call := func(method, params string) *plugin.RPCError {
		_, rerr := e.svc.HandlePluginCall(ctx, "hello", method, json.RawMessage(params))
		return rerr
	}
	co := `{"company":"` + e.co.Slug + `"}`
	// empresa que não ligou o plugin: negado (e não revela se a empresa existe)
	if r := call("tasks.list", co); r == nil || r.Code != plugin.CodeDenied {
		t.Fatalf("sem ligar = %v", r)
	}
	if r := call("tasks.list", `{"company":"nao-existe"}`); r == nil || r.Code != plugin.CodeDenied {
		t.Fatalf("empresa inexistente = %v", r)
	}
	e.enable(t, nil)
	if r := call("tasks.list", co); r != nil {
		t.Fatalf("tasks.list = %v", r)
	}
	// capacidade não concedida (o hello não pede personas.read nem activity.read)
	for _, m := range []string{"personas.list", "projects.list", "activity.list"} {
		if r := call(m, co); r == nil || r.Code != plugin.CodeDenied {
			t.Errorf("%s = %v", m, r)
		}
	}
	// métodos que não existem: decidir gate, orçamento, segredos
	for _, m := range []string{"gates.decide", "budget.set", "secrets.get", "db.query"} {
		if r := call(m, co); r == nil || r.Code != plugin.CodeMethodNotFound {
			t.Errorf("%s = %v", m, r)
		}
	}
	// o plugin age como sistema e o que ele cria não volta para ele (sem laço)
	e.pj.takeEvents()
	res, r := e.svc.HandlePluginCall(ctx, "hello", "tasks.create", json.RawMessage(`{"company":"`+e.co.Slug+`","title":"criada pelo plugin","event_id":"x1"}`))
	if r != nil {
		t.Fatal(r)
	}
	if evs := e.pj.takeEvents(); len(evs) != 0 {
		t.Fatalf("evento do próprio plugin voltou para ele: %v", evs)
	}
	b, _ := json.Marshal(res)
	var out struct {
		Task struct{ Key string } `json:"task"`
	}
	_ = json.Unmarshal(b, &out)
	task, err := e.svc.GetTask(ctx, e.co.ID, out.Task.Key)
	if err != nil || task.CreatedBy.Type != domain.ActorSystem || task.CreatedBy.ID == nil {
		t.Fatalf("task do plugin = %+v %v", task.CreatedBy, err)
	}
	// idempotência pelo event_id
	if _, r := e.svc.HandlePluginCall(ctx, "hello", "tasks.create", json.RawMessage(`{"company":"`+e.co.Slug+`","title":"criada pelo plugin","event_id":"x1"}`)); r != nil {
		t.Fatal(r)
	}
	ts, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(ts) != 1 {
		t.Fatalf("event_id repetido duplicou a task: %d", len(ts))
	}
	// estado: limites
	if r := call("state.set", `{"company":"`+e.co.Slug+`","key":"k","value":{"a":1}}`); r != nil {
		t.Fatal(r)
	}
	if r := call("state.set", `{"company":"`+e.co.Slug+`","key":"k","value":"`+strings.Repeat("x", domain.PluginStateValueMax)+`"}`); r == nil || r.Code != plugin.CodeInvalid {
		t.Errorf("valor grande = %v", r)
	}
	if r := call("state.set", `{"company":"`+e.co.Slug+`","key":"`+strings.Repeat("k", 201)+`","value":1}`); r == nil || r.Code != plugin.CodeInvalid {
		t.Errorf("chave grande = %v", r)
	}
	items, err := e.svc.ListPluginState(ctx, e.co.ID, "hello", "", 10)
	if err != nil || len(items) != 1 || string(items[0].Value) != `{"a": 1}` {
		t.Fatalf("estado = %+v %v", items, err)
	}
}

func TestPluginEventDelivered(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	v := e.install(t)
	// empresa sem o plugin ligado: nenhum evento
	e.pj.takeEvents()
	e.task(t, service.CreateTaskInput{Title: "antes"})
	if evs := e.pj.takeEvents(); len(evs) != 0 {
		t.Fatalf("evento sem o plugin ligado: %v", evs)
	}
	e.enable(t, map[string]any{"greeting": "Bom dia"})
	waitUntil(t, "processo", func() bool { return e.mgr.Status("hello").Ready })
	e.pj.takeEvents()
	tk := e.task(t, service.CreateTaskInput{Title: "nova"})
	evs := e.pj.takeEvents()
	if len(evs) != 1 || evs[0][0] != v.ID || evs[0][1] != e.co.ID {
		t.Fatalf("eventos = %v", evs)
	}
	// a entrega chama on_event; o plugin comenta na task (deduplicando pelo id do evento)
	for i := 0; i < 2; i++ {
		if err := e.svc.DeliverPluginEvent(ctx, evs[0][0], evs[0][1], evs[0][2]); err != nil {
			t.Fatalf("entrega %d: %v", i, err)
		}
	}
	cs, err := e.svc.ListComments(ctx, e.co.ID, tk.ID)
	if err != nil || len(cs) != 1 || cs[0].Body != "Bom dia! Task recebida pelo plugin hello." || cs[0].Author.Type != domain.ActorSystem {
		t.Fatalf("comentários = %+v %v", cs, err)
	}
	// verbo não assinado: não enfileira
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{Title: ptr("outro")}); err != nil {
		t.Fatal(err)
	}
	if evs := e.pj.takeEvents(); len(evs) != 0 {
		t.Fatalf("verbo não assinado entregue: %v", evs)
	}
	// plugin fora do ar: a entrega pede retry
	if err := e.mgr.Stop(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeliverPluginEvent(ctx, evs0(evs), e.co.ID, domain.NewID()); err != nil {
		t.Fatalf("atividade inexistente devia encerrar sem erro: %v", err)
	}
	tk2 := e.task(t, service.CreateTaskInput{Title: "com o plugin fora"})
	ev2 := e.pj.takeEvents()
	if len(ev2) != 1 {
		t.Fatalf("eventos = %v", ev2)
	}
	if err := e.svc.DeliverPluginEvent(ctx, ev2[0][0], ev2[0][1], ev2[0][2]); !errors.Is(err, service.ErrPluginRetry) {
		t.Fatalf("entrega com o plugin fora = %v", err)
	}
	_ = tk2
}

func evs0(evs [][3]domain.ID) domain.ID { return evs[0][0] }

func TestPluginJobsWithoutOverlap(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.install(t)
	e.enable(t, nil)
	waitUntil(t, "processo", func() bool { return e.mgr.Status("hello").Ready })
	n, err := e.svc.PluginTick(ctx)
	if err != nil || n != 1 {
		t.Fatalf("tick = %d %v", n, err)
	}
	runs := e.pj.takeRuns()
	if len(runs) != 1 {
		t.Fatalf("rodadas = %v", runs)
	}
	// rodada em andamento: o tick seguinte não sobrepõe, nem depois do intervalo
	e.advance(10 * time.Minute)
	if n, _ := e.svc.PluginTick(ctx); n != 0 {
		t.Fatalf("sobreposição: %d", n)
	}
	if err := e.svc.RunPluginJob(ctx, runs[0]); err != nil {
		t.Fatal(err)
	}
	cp, err := e.svc.GetCompanyPlugin(ctx, e.co.ID, "hello")
	if err != nil || len(cp.JobRuns) != 1 || cp.JobRuns[0].Status != domain.PluginJobDone {
		t.Fatalf("rodadas = %+v %v", cp.JobRuns, err)
	}
	items, _ := e.svc.ListPluginState(ctx, e.co.ID, "hello", "last_", 10)
	if len(items) != 1 {
		t.Fatalf("o job não gravou estado: %+v", items)
	}
	// encerrada e vencida: roda de novo; dentro do intervalo, não
	if n, _ := e.svc.PluginTick(ctx); n != 1 {
		t.Fatalf("não rodou depois de encerrar a vencida: %d", n)
	}
	second := e.pj.takeRuns()
	if err := e.svc.RunPluginJob(ctx, second[0]); err != nil {
		t.Fatal(err)
	}
	e.advance(2 * time.Minute)
	if n, _ := e.svc.PluginTick(ctx); n != 0 {
		t.Fatalf("rodou antes do intervalo: %d", n)
	}
	e.advance(4 * time.Minute)
	if n, _ := e.svc.PluginTick(ctx); n != 1 {
		t.Fatalf("não rodou depois do intervalo: %d", n)
	}
	// RunPluginJob é idempotente para uma rodada já encerrada
	if err := e.svc.RunPluginJob(ctx, runs[0]); err != nil {
		t.Fatal(err)
	}
}

func TestPluginWidgetsCached(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.install(t)
	e.enable(t, nil)
	waitUntil(t, "processo", func() bool { return e.mgr.Status("hello").Ready })
	get := func() service.PluginWidgetView {
		t.Helper()
		ws, err := e.svc.PluginWidgets(ctx, e.co.ID, service.PluginWidgetRef{Placement: domain.WidgetDashboard})
		if err != nil || len(ws) != 1 {
			t.Fatalf("widgets = %+v %v", ws, err)
		}
		return ws[0]
	}
	w := get()
	if !strings.Contains(w.Markdown, "**0** evento(s)") || w.Table == nil || w.Table.Columns[0] != "Job" || w.Error != "" {
		t.Fatalf("widget = %+v", w)
	}
	if _, rerr := e.svc.HandlePluginCall(ctx, "hello", "state.set", json.RawMessage(`{"company":"`+e.co.Slug+`","key":"events","value":7}`)); rerr != nil {
		t.Fatal(rerr)
	}
	if w := get(); !strings.Contains(w.Markdown, "**0**") {
		t.Fatalf("cache não segurou: %s", w.Markdown)
	}
	e.advance(61 * time.Second)
	if w := get(); !strings.Contains(w.Markdown, "**7**") {
		t.Fatalf("cache não expirou: %s", w.Markdown)
	}
	tk := e.task(t, service.CreateTaskInput{Title: "x"})
	ws, err := e.svc.PluginWidgets(ctx, e.co.ID, service.PluginWidgetRef{Placement: domain.WidgetTask, Ref: tk.Key})
	if err != nil || len(ws) != 1 || !strings.Contains(ws[0].Markdown, tk.Key) {
		t.Fatalf("widget da task = %+v %v", ws, err)
	}
	// plugin fora do ar: o widget vira aviso, a página não quebra
	_ = e.mgr.Stop(ctx, "hello")
	e.advance(2 * time.Minute)
	if w := get(); w.Error == "" {
		t.Fatalf("widget sem aviso com o plugin fora: %+v", w)
	}
}

func TestPluginUpgradeNeedsApprovalForNewCapabilities(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.install(t)
	e.enable(t, nil)
	if _, err := e.svc.UpgradePlugin(ctx, "hello"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("upgrade sem versão nova = %v", err)
	}
	// versão nova sem capacidade a mais: aplica direto
	installHelloDir(t, e.root, func(m map[string]any) { m["version"] = "1.1.0" })
	v, err := e.svc.UpgradePlugin(ctx, "hello")
	if err != nil || v.Version != "1.1.0" || v.Status != domain.PluginReady {
		t.Fatalf("upgrade simples = %+v %v", v, err)
	}
	waitUntil(t, "processo na versão nova", func() bool { st := e.mgr.Status("hello"); return st.Ready && st.Version == "1.1.0" })
	// versão que pede personas.read: fica pendente e continua atendendo com o que já foi aprovado
	installHelloDir(t, e.root, func(m map[string]any) {
		m["version"] = "2.0.0"
		m["capabilities"] = append(m["capabilities"].([]any), "personas.read")
	})
	ov, err := e.svc.PluginOverview(ctx)
	if err != nil || !ov.Available[0].Upgrade {
		t.Fatalf("visão sem a versão nova: %+v %v", ov.Available, err)
	}
	v, err = e.svc.UpgradePlugin(ctx, "hello")
	if err != nil || v.Status != domain.PluginUpgradePending || v.Version != "1.1.0" || v.PendingManifest == nil ||
		!strings.Contains(strings.Join(v.PendingCapabilities, ","), "personas.read") {
		t.Fatalf("upgrade com capacidade nova = %+v %v", v, err)
	}
	if tools, _ := e.svc.PluginTools(ctx, e.co.ID); len(tools) != 1 {
		t.Fatal("upgrade pendente parou de atender")
	}
	if _, rerr := e.svc.HandlePluginCall(ctx, "hello", "personas.list", json.RawMessage(`{"company":"`+e.co.Slug+`"}`)); rerr == nil || rerr.Code != plugin.CodeDenied {
		t.Fatalf("capacidade pendente já valia: %v", rerr)
	}
	v, err = e.svc.ApprovePluginUpgrade(ctx, "hello")
	if err != nil || v.Status != domain.PluginReady || v.Version != "2.0.0" || !v.Has(domain.CapPersonasRead) {
		t.Fatalf("aprovar = %+v %v", v, err)
	}
	if _, rerr := e.svc.HandlePluginCall(ctx, "hello", "personas.list", json.RawMessage(`{"company":"`+e.co.Slug+`"}`)); rerr != nil {
		t.Fatalf("capacidade aprovada não vale: %v", rerr)
	}
	if _, err := e.svc.ApprovePluginUpgrade(ctx, "hello"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("aprovar sem pendência = %v", err)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "plugin"})
	if pluginVerbCount(acts, "plugin.upgraded") != 2 {
		t.Fatalf("atividade = %v", pluginVerbs(acts))
	}
}

func TestPluginProcessStateTransitions(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.install(t)
	e.svc.PluginProcessState("hello", false, "caiu")
	v, _ := e.svc.GetPlugin(ctx, "hello")
	if v.Status != domain.PluginError || v.LastError != "caiu" {
		t.Fatalf("depois da queda = %s %q", v.Status, v.LastError)
	}
	if tools, _ := e.svc.PluginTools(ctx, e.co.ID); len(tools) != 0 {
		t.Fatal("tools com o plugin em erro")
	}
	e.svc.PluginProcessState("hello", true, "")
	v, _ = e.svc.GetPlugin(ctx, "hello")
	if v.Status != domain.PluginReady || v.LastError != "" {
		t.Fatalf("depois de voltar = %s %q", v.Status, v.LastError)
	}
	// desligado não volta sozinho
	if _, err := e.svc.SetPluginEnabled(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	e.svc.PluginProcessState("hello", true, "")
	if v, _ := e.svc.GetPlugin(ctx, "hello"); v.Status != domain.PluginDisabled {
		t.Fatalf("desligado virou %s", v.Status)
	}
}

func TestPluginsOffWithoutDir(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.InstallPlugin(ctx, "hello"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("instalar sem HANKO_PLUGINS_DIR = %v", err)
	}
	list, err := e.svc.ListCompanyPlugins(ctx, e.co.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("lista = %v %v", list, err)
	}
	if tools, err := e.svc.PluginTools(ctx, e.co.ID); err != nil || len(tools) != 0 {
		t.Fatalf("tools = %v %v", tools, err)
	}
}
