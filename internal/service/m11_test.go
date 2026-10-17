package service_test

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/service"
)

// fakeCloud é um CloudDriver mínimo: o service só precisa dele ligado para aceitar o driver cloud.
type fakeCloud struct{}

func (fakeCloud) Name() string                     { return "daytona" }
func (fakeCloud) KnownHosts() string               { return "gw.example.com ssh-ed25519 AAAA" }
func (fakeCloud) New(string) execenv.CloudProvider { return nil }

func localInput(name string) service.CreateEnvironmentInput {
	return service.CreateEnvironmentInput{Name: name, Driver: domain.EnvLocal, Env: map[string]string{"MINHA_VAR": "valor-local"}}
}

func checkout(t *testing.T, e *env, p domain.Persona, projectID *domain.ID) domain.Run {
	t.Helper()
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID, ProjectID: projectID})
	run, err := e.svc.CheckoutRun(context.Background(), service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestExplicitLocalEnvironment(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	loc, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, localInput("Servidor"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Driver != domain.EnvLocal || loc.Unavailable != "" || loc.KeySet || strings.Join(loc.Config.EnvNames, ",") != "MINHA_VAR" {
		t.Fatalf("local explícito: %+v", loc)
	}
	// local não aceita configuração de ssh/sandbox/cloud
	bad := localInput("Mistura")
	bad.SSH = &domain.SSHConfig{Host: "x.example.com"}
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("local com ssh: %v", err)
	}
	bad = localInput("Mistura 2")
	bad.APIKey = "k"
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("local com chave de api: %v", err)
	}

	// padrão da empresa remoto; a persona força o local explícito e vence
	build, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Build"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetDefaultEnvironment(ctx, domain.HumanActor, e.co.ID, &build.ID); err != nil {
		t.Fatal(err)
	}
	ana := e.persona(t, "Ana", nil)
	if _, err := e.svc.SetPersonaEnvironment(ctx, domain.HumanActor, e.co.ID, ana.ID, &loc.ID); err != nil {
		t.Fatal(err)
	}
	run := checkout(t, e, ana, nil)
	if run.EnvironmentSource != domain.EnvFromPersona || run.EnvironmentID == nil || *run.EnvironmentID != loc.ID {
		t.Fatalf("origem gravada na run: %+v", run)
	}
	re, err := e.svc.RunEnvironmentFor(ctx, e.co.ID, run, nil)
	if err != nil || re.Environment == nil || re.Environment.Driver != domain.EnvLocal || re.Source != "persona" || re.Target.Env["MINHA_VAR"] != "valor-local" {
		t.Fatalf("executor deveria receber o local explícito com as variáveis: %+v %v", re, err)
	}
}

func TestRunRecordsEnvironmentPrecedence(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	mk := func(name string) service.EnvironmentView {
		v, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput(name))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	def, projEnv, persEnv := mk("Amb Padrão da empresa"), mk("Do projeto"), mk("Da persona")
	ana := e.persona(t, "Ana", nil)
	pr := e.project(t, "Lançamento", nil)

	// 1. nada: local implícito, com a origem gravada
	run := checkout(t, e, ana, nil)
	if run.EnvironmentSource != domain.EnvFromLocal || run.EnvironmentID != nil {
		t.Fatalf("local implícito: %+v", run)
	}
	if re, err := e.svc.RunEnvironmentFor(ctx, e.co.ID, run, nil); err != nil || re.Environment != nil || re.Source != "local" {
		t.Fatalf("executor: %+v %v", re, err)
	}

	// 2. padrão da empresa
	if err := e.svc.SetDefaultEnvironment(ctx, domain.HumanActor, e.co.ID, &def.ID); err != nil {
		t.Fatal(err)
	}
	other := e.persona(t, "Bia", nil)
	run = checkout(t, e, other, nil)
	if run.EnvironmentSource != domain.EnvFromDefault || *run.EnvironmentID != def.ID {
		t.Fatalf("padrão: %+v", run)
	}

	// 3. projeto vence o padrão
	if _, err := e.svc.SetProjectEnvironment(ctx, domain.HumanActor, e.co.ID, pr.ID, &projEnv.ID); err != nil {
		t.Fatal(err)
	}
	cia := e.persona(t, "Cia", nil)
	run = checkout(t, e, cia, &pr.ID)
	if run.EnvironmentSource != domain.EnvFromProject || *run.EnvironmentID != projEnv.ID {
		t.Fatalf("projeto: %+v", run)
	}
	// a mesma persona numa task sem projeto cai no padrão
	dia := e.persona(t, "Dia", nil)
	if run = checkout(t, e, dia, nil); run.EnvironmentSource != domain.EnvFromDefault {
		t.Fatalf("sem projeto: %+v", run)
	}

	// 4. persona vence o projeto
	if _, err := e.svc.SetPersonaEnvironment(ctx, domain.HumanActor, e.co.ID, ana.ID, &persEnv.ID); err != nil {
		t.Fatal(err)
	}
	pr2 := e.project(t, "Outro", nil)
	if _, err := e.svc.SetProjectEnvironment(ctx, domain.HumanActor, e.co.ID, pr2.ID, &projEnv.ID); err != nil {
		t.Fatal(err)
	}
	run = checkout(t, e, ana, &pr2.ID)
	if run.EnvironmentSource != domain.EnvFromPersona || *run.EnvironmentID != persEnv.ID {
		t.Fatalf("persona: %+v", run)
	}

	// 5. ambiente do projeto arquivado: o arquivamento leva o projeto para o destino
	if _, err := e.svc.ArchiveEnvironment(ctx, domain.HumanActor, e.co.ID, projEnv.ID, &def.ID); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.GetProject(ctx, e.co.ID, pr.Slug)
	if err != nil || got.EnvironmentID == nil || *got.EnvironmentID != def.ID {
		t.Fatalf("projeto movido: %+v %v", got.EnvironmentID, err)
	}

	// 6. a decisão gravada na run vale; se o ambiente dela for arquivado depois, o executor resolve de novo e regrava
	eva := e.persona(t, "Eva", nil)
	run = checkout(t, e, eva, nil)
	if run.EnvironmentSource != domain.EnvFromDefault {
		t.Fatalf("pré-condição: %+v", run)
	}
	if _, err := e.svc.ArchiveEnvironment(ctx, domain.HumanActor, e.co.ID, def.ID, nil); err != nil {
		t.Fatal(err)
	}
	re, err := e.svc.RunEnvironmentFor(ctx, e.co.ID, run, nil)
	if err != nil || re.Environment != nil || re.Source != "local" || len(re.Warnings) == 0 {
		t.Fatalf("ambiente arquivado entre a fila e a execução: %+v %v", re, err)
	}
	var src string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT environment_source FROM runs WHERE id=$1`, run.ID).Scan(&src); err != nil || src != "local" {
		t.Fatalf("a run deve ser regravada: %q %v", src, err)
	}

	// papéis: só admin escolhe o ambiente do projeto
	u := mkUser(t, e, "membro@x.com", domain.RoleMember)
	join(t, e, u, domain.MemberMember)
	if _, err := e.svc.SetProjectEnvironment(as(u), domain.HumanActor, e.co.ID, pr.ID, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro escolhe ambiente do projeto: %v", err)
	}
	if _, err := e.svc.SetProjectEnvironment(ctx, domain.HumanActor, e.co.ID, pr.ID, &def.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("ambiente arquivado no projeto: %v", err)
	}
}

// Propriedade ponta a ponta: para atribuições aleatórias de ambiente (persona, projeto, padrão, com arquivados), a
// origem gravada na run é a que domain.ChooseEnvironment dá para os mesmos candidatos.
func TestEnvironmentPrecedenceProperty(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	envs := make([]service.EnvironmentView, 3)
	for i := range envs {
		v, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, localInput("Amb"+string(rune('A'+i))))
		if err != nil {
			t.Fatal(err)
		}
		envs[i] = v
	}
	archived := map[domain.ID]bool{}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 25; i++ {
		persona := e.persona(t, "P"+string(rune('a'+i%26))+string(rune('a'+i/26)), nil)
		pr := e.project(t, "Proj"+string(rune('a'+i%26))+string(rune('a'+i/26)), nil)
		pick := func() *service.EnvironmentView {
			if rng.Intn(3) == 0 {
				return nil
			}
			return &envs[rng.Intn(len(envs))]
		}
		// restaura (se arquivado) e escolhe de novo; o arquivamento sorteado vem depois das atribuições
		for j := range envs {
			if archived[envs[j].ID] {
				if _, err := e.svc.UpdateEnvironment(ctx, domain.HumanActor, e.co.ID, envs[j].ID, service.UpdateEnvironmentInput{Reactivate: true}); err != nil {
					t.Fatal(err)
				}
				delete(archived, envs[j].ID)
			}
		}
		pe, pj, df := pick(), pick(), pick()
		var peID, pjID, dfID *domain.ID
		if pe != nil {
			peID = &pe.ID
		}
		if pj != nil {
			pjID = &pj.ID
		}
		if df != nil {
			dfID = &df.ID
		}
		if _, err := e.svc.SetPersonaEnvironment(ctx, domain.HumanActor, e.co.ID, persona.ID, peID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.SetProjectEnvironment(ctx, domain.HumanActor, e.co.ID, pr.ID, pjID); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.SetDefaultEnvironment(ctx, domain.HumanActor, e.co.ID, dfID); err != nil {
			t.Fatal(err)
		}
		// arquiva um ambiente sorteado SEM mover (as atribuições a ele ficam apontando para um arquivado)
		if rng.Intn(2) == 0 {
			victim := envs[rng.Intn(len(envs))]
			if !archived[victim.ID] {
				// o arquivamento limpa persona/projeto/padrão que apontavam para ele; o oráculo precisa saber disso
				if _, err := e.svc.ArchiveEnvironment(ctx, domain.HumanActor, e.co.ID, victim.ID, nil); err != nil {
					t.Fatal(err)
				}
				archived[victim.ID] = true
				for _, p := range []**domain.ID{&peID, &pjID, &dfID} {
					if *p != nil && **p == victim.ID {
						*p = nil
					}
				}
			}
		}
		cand := func(src domain.EnvSource, id *domain.ID) domain.EnvCandidate {
			return domain.EnvCandidate{Source: src, ID: id, Active: id != nil && !archived[*id]}
		}
		want := domain.ChooseEnvironment(cand(domain.EnvFromPersona, peID), cand(domain.EnvFromProject, pjID), cand(domain.EnvFromDefault, dfID))
		run := checkout(t, e, persona, &pr.ID)
		if run.EnvironmentSource != want.Source || (run.EnvironmentID == nil) != (want.ID == nil) || (want.ID != nil && *run.EnvironmentID != *want.ID) {
			t.Fatalf("iteração %d: gravado %q/%v, esperado %q/%v (persona=%v projeto=%v padrão=%v arquivados=%v)",
				i, run.EnvironmentSource, run.EnvironmentID, want.Source, want.ID, peID, pjID, dfID, archived)
		}
	}
}

func TestCloudEnvironmentSecretsAndSwitch(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	in := service.CreateEnvironmentInput{Name: "Nuvem", Driver: domain.EnvCloud, Cloud: &domain.CloudConfig{}, APIKey: "dtn_chave-secreta-xyz",
		Env: map[string]string{"X": "1"}}
	// desligado pelo operador
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in); err == nil || !strings.Contains(err.Error(), "HANKO_ENV_CLOUD") {
		t.Fatalf("cloud desligado: %v", err)
	}
	e.svc.Environments.Cloud = fakeCloud{}
	v, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if v.Driver != domain.EnvCloud || !v.KeySet || v.Config.Cloud == nil || v.Config.Cloud.Provider != "daytona" || v.Config.Cloud.CPUs != 2 {
		t.Fatalf("cloud criado: %+v", v)
	}
	var enc, act string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT secret_enc FROM environments WHERE id=$1`, v.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Pool.QueryRow(ctx, `SELECT string_agg(data::text, ' ') FROM activity_events WHERE subject_type='environment'`).Scan(&act); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "chave-secreta") || strings.Contains(act, "chave-secreta") {
		t.Fatalf("chave de API em claro: %q %q", enc, act)
	}
	re, err := e.svc.RunEnvironment(ctx, e.co.ID, e.persona(t, "Ana", nil).ID)
	if err != nil || re.Environment != nil {
		t.Fatalf("sem atribuição é local: %+v %v", re, err)
	}
	// validações
	for name, mod := range map[string]func(*service.CreateEnvironmentInput){
		"sem chave":       func(i *service.CreateEnvironmentInput) { i.APIKey = "" },
		"chave c/ espaço": func(i *service.CreateEnvironmentInput) { i.APIKey = "a b" },
		"sem config":      func(i *service.CreateEnvironmentInput) { i.Cloud = nil },
		"provider ruim":   func(i *service.CreateEnvironmentInput) { i.Cloud = &domain.CloudConfig{Provider: "outro"} },
		"mistura ssh":     func(i *service.CreateEnvironmentInput) { i.PrivateKey = envTestKey },
	} {
		bad := in
		bad.Slug = "x" + strings.ReplaceAll(name, " ", "")
		bad.Name = name
		mod(&bad)
		if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, bad); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// referência a segredo da empresa: precisa existir
	ref := in
	ref.Name, ref.Slug, ref.APIKey = "Por ref", "por-ref", "${secret:daytona_key}"
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, ref); err == nil {
		t.Fatal("referência a segredo inexistente deveria falhar")
	}
	// troca da chave de API pelo update
	newKey := "dtn_outra"
	if _, err := e.svc.UpdateEnvironment(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateEnvironmentInput{APIKey: &newKey}); err != nil {
		t.Fatal(err)
	}
	if drivers := e.svc.EnvironmentDrivers(ctx); drivers[domain.EnvCloud] != "" {
		t.Fatalf("driver cloud deveria estar disponível: %v", drivers)
	}
}

// ---- readiness ----

func scriptRunner(out string, err error, calls *int) func(context.Context, execenv.Target, string, map[string]string) ([]byte, error) {
	return func(context.Context, execenv.Target, string, map[string]string) ([]byte, error) {
		if calls != nil {
			*calls++
		}
		return []byte(out), err
	}
}

func claudePersona(t *testing.T, e *env, name string) domain.Persona {
	t.Helper()
	return e.persona(t, name, func(in *service.HirePersonaInput) { in.Agent = "claude"; in.Model = "claude-sonnet-4-5" })
}

func TestReadinessBlocksDispatchAndAlerts(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	p := claudePersona(t, e, "Ana")
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	wake := service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign}

	// nunca testada não bloqueia
	if rd, err := e.svc.GetPersonaReadiness(ctx, e.co.ID, p.ID); err != nil || rd != nil {
		t.Fatalf("nunca testada: %+v %v", rd, err)
	}

	// auth quebrada: binário existe, login não
	e.svc.ReadinessRunner = scriptRunner("bin=/usr/bin/claude\nversion=2.0\nauth=none\n", nil, nil)
	rd, err := e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Status != domain.ReadinessNotReady || !strings.HasPrefix(rd.Reason, "auth:") || rd.Trigger != domain.ReadinessManual || rd.FailingSince == nil {
		t.Fatalf("auth quebrada deve ser detectada antes do run: %+v", rd)
	}
	names := []string{}
	for _, s := range rd.Steps {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "environment,binary,auth,model" {
		t.Fatalf("passos nomeados: %v", names)
	}
	// o despacho é recusado com o motivo
	_, err = e.svc.CheckoutRun(ctx, wake)
	if !errors.Is(err, domain.ErrPersonaNotReady) || !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "auth:") {
		t.Fatalf("despacho deveria ser bloqueado com o motivo: %v", err)
	}
	// o wake manual também diz o motivo na hora
	if err := e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, tv.ID); !errors.Is(err, domain.ErrPersonaNotReady) || !strings.Contains(err.Error(), "auth:") {
		t.Fatalf("wake manual deveria ser recusado com o motivo: %v", err)
	}
	// o alerta abre no próximo alert_tick
	if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	al := e.openAlerts(t, domain.AlertPersonaNotReady)
	if len(al) != 1 || al[0].Severity != domain.AlertCritical || al[0].SubjectID != p.ID || !strings.Contains(al[0].Message, "auth:") {
		t.Fatalf("alerta: %+v", al)
	}
	// chat também é recusado
	if _, err := e.svc.CreateConversation(ctx, domain.HumanActor, e.co.ID, service.CreateConversationInput{PersonaID: p.ID, Message: "oi"}); !errors.Is(err, domain.ErrPersonaNotReady) {
		t.Fatalf("chat com persona não pronta deveria ser bloqueado: %v", err)
	}

	// conserta o login: o teste passa, o alerta fecha e o despacho libera
	e.svc.ReadinessRunner = scriptRunner("bin=/usr/bin/claude\nversion=2.0\nauth=login\n", nil, nil)
	rd, err = e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, p.ID)
	if err != nil || rd.Status != domain.ReadinessReady || rd.FailingSince != nil || rd.Reason != "" {
		t.Fatalf("pronta: %+v %v", rd, err)
	}
	if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if al := e.openAlerts(t, domain.AlertPersonaNotReady); len(al) != 0 {
		t.Fatalf("alerta deveria fechar: %+v", al)
	}
	if _, err := e.svc.CheckoutRun(ctx, wake); err != nil {
		t.Fatalf("despacho liberado: %v", err)
	}
	// o modelo que passou entra no catálogo como visto pelo probe
	cat, err := e.svc.ModelCatalog(ctx, e.co.ID, "claude")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range cat {
		if m.Model == "claude-sonnet-4-5" {
			found = true
			if !m.Priced || m.PriceModel != "claude-sonnet" {
				t.Errorf("preço do modelo: %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("modelo visto pelo probe não está no catálogo: %+v", cat)
	}
}

func TestReadinessStaleResultDoesNotBlock(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	p := claudePersona(t, e, "Ana")
	e.svc.ReadinessRunner = scriptRunner("auth=none\n", nil, nil) // sem binário
	rd, err := e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, p.ID)
	if err != nil || rd.Status != domain.ReadinessNotReady {
		t.Fatalf("%+v %v", rd, err)
	}
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	wake := service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign}
	if _, err := e.svc.CheckoutRun(ctx, wake); !errors.Is(err, domain.ErrPersonaNotReady) {
		t.Fatalf("bloqueada: %v", err)
	}
	// mudar o modelo invalida o resultado (vale para o modelo testado, não para o novo) e o tick refaz o teste
	m := "claude-opus-4"
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Model: &m}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CheckoutRun(ctx, wake); err != nil {
		t.Fatalf("teste de outro modelo não vale: %v", err)
	}
	// o ambiente muda: o teste é apagado ("nunca testada")
	e.svc.ReadinessRunner = scriptRunner("auth=none\n", nil, nil)
	if _, err := e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	loc, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, localInput("Loc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetPersonaEnvironment(ctx, domain.HumanActor, e.co.ID, p.ID, &loc.ID); err != nil {
		t.Fatal(err)
	}
	if rd, _ := e.svc.GetPersonaReadiness(ctx, e.co.ID, p.ID); rd != nil {
		t.Fatalf("mudar o ambiente apaga o teste: %+v", rd)
	}
}

func TestReadinessEnvironmentDownAndTick(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	ana := claudePersona(t, e, "Ana")
	fk := e.persona(t, "Fake", nil)
	build, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, sshInput("Build"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetPersonaEnvironment(ctx, domain.HumanActor, e.co.ID, ana.ID, &build.ID); err != nil {
		t.Fatal(err)
	}
	// periódico desligado por padrão
	if n, err := e.svc.ReadinessTick(ctx); err != nil || n != 0 {
		t.Fatalf("tick desligado: %d %v", n, err)
	}
	e.svc.ReadinessEvery = time.Hour
	calls := 0
	e.svc.ReadinessRunner = func(_ context.Context, tg execenv.Target, script string, env map[string]string) ([]byte, error) {
		calls++
		if tg.Driver == domain.EnvSSH {
			if tg.SSH.Host != "build.example.com" || tg.PrivateKey != envTestKey || env == nil && false {
				t.Errorf("alvo do probe: %+v", tg)
			}
			return nil, errors.New("ssh: connect to host build.example.com port 22: Connection refused")
		}
		return []byte("bin=fake\nversion=fake\nauth=skip\n"), nil
	}
	n, err := e.svc.ReadinessTick(ctx)
	if err != nil || n != 2 {
		t.Fatalf("tick testa as duas personas nunca testadas: %d %v", n, err)
	}
	rd, _ := e.svc.GetPersonaReadiness(ctx, e.co.ID, ana.ID)
	if rd == nil || rd.Status != domain.ReadinessNotReady || !strings.HasPrefix(rd.Reason, "environment:") || rd.Trigger != domain.ReadinessPeriodic ||
		rd.EnvironmentSource != domain.EnvFromPersona || rd.EnvironmentID == nil || *rd.EnvironmentID != build.ID {
		t.Fatalf("ambiente fora: %+v", rd)
	}
	rf, _ := e.svc.GetPersonaReadiness(ctx, e.co.ID, fk.ID)
	if rf == nil || rf.Status != domain.ReadinessReady {
		t.Fatalf("fake pronta: %+v", rf)
	}
	// recém-testadas não são refeitas
	before := calls
	if n, err := e.svc.ReadinessTick(ctx); err != nil || n != 0 || calls != before {
		t.Fatalf("segundo tick: %d %v (chamadas %d→%d)", n, err, before, calls)
	}
	// o mapa por empresa
	all, err := e.svc.ListPersonaReadiness(ctx, e.co.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("lista: %+v %v", all, err)
	}
	// papéis: membro lê, só admin testa
	u := mkUser(t, e, "m@x.com", domain.RoleMember)
	join(t, e, u, domain.MemberMember)
	if _, err := e.svc.GetPersonaReadiness(as(u), e.co.ID, ana.ID); err != nil {
		t.Fatalf("membro lê: %v", err)
	}
	if _, err := e.svc.TestPersonaReadiness(as(u), domain.HumanActor, e.co.ID, ana.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro testa: %v", err)
	}
}

func TestModelCatalogHybrid(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	cat, err := e.svc.ModelCatalog(ctx, e.co.ID, "claude")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]domain.CatalogModel{}
	for _, m := range cat {
		by[m.Model] = m
	}
	if m := by["sonnet"]; !m.Priced || m.PriceModel != "claude-sonnet" {
		t.Fatalf("alias com preço da tabela semeada: %+v", m)
	}
	if _, ok := by["gpt-"]; ok {
		t.Fatal("prefixo de família não é modelo")
	}
	// um modelo usado numa run entra no catálogo, sem preço próprio
	p := e.persona(t, "Ana", func(in *service.HirePersonaInput) { in.Model = "modelo-novo-9" })
	checkout(t, e, p, nil)
	cat, _ = e.svc.ModelCatalog(ctx, e.co.ID, "")
	found := false
	for _, m := range cat {
		if m.Model == "modelo-novo-9" {
			found = true
			if m.Priced {
				t.Errorf("sem preço próprio: %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("modelo usado por uma run deveria aparecer: %+v", cat)
	}
	if prefix, priced, err := e.svc.ModelPriced(ctx, "claude-haiku-4-5"); err != nil || !priced || prefix != "claude-haiku" {
		t.Fatalf("ModelPriced: %q %v %v", prefix, priced, err)
	}
	if _, priced, _ := e.svc.ModelPriced(ctx, "modelo-novo-9"); priced {
		t.Fatal("sem preço próprio")
	}
}
