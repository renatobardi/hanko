package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/acp"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/settings"
)

// Settings da instância (ADR-0032 §C): env > banco > padrão, com trilha de auditoria.

func newSettingsEnv(t *testing.T, envSet ...string) *env {
	t.Helper()
	e := newEnv(t)
	base := map[string]any{
		settings.ACPAllow: false, settings.ACPCommands: []string{}, settings.IntegrationsAllowStdio: false,
		settings.IntegrationsAllowPriv: false, settings.ArtifactsMaxBytes: int64(5 << 20), settings.ArtifactsQuotaBytes: int64(512 << 20),
		settings.RunReserveUSD: 1.0, settings.AlertsSilentFactor: int64(3), settings.AlertsRunStuckAfter: 15 * time.Minute,
	}
	env := map[string]bool{}
	for _, k := range envSet {
		env[k] = true
	}
	m := settings.NewManager(base, env)
	e.svc.Settings = m
	box, _ := secretbox.New(keyOne, "")
	e.svc.Integrations = service.IntegrationsConfig{Secrets: box, Settings: m}
	e.svc.ACP = acp.Policy{}
	return e
}

func rawJSON(v string) json.RawMessage { return json.RawMessage(v) }

func TestInstanceSettingsOwnerOnlyAndAudit(t *testing.T) {
	e := newSettingsEnv(t)
	bg := context.Background()
	owner := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	admin := mkUser(t, e, "adm@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberOwner) // dono DA EMPRESA, mas não da instalação

	if _, err := e.svc.SetInstanceSetting(as(admin), domain.HumanActor, settings.ACPAllow, rawJSON("true")); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("quem não é dono da instalação: %v", err)
	}
	if _, err := e.svc.ListInstanceSettings(as(admin)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("listar: %v", err)
	}
	octx := as(owner)
	entry, err := e.svc.SetInstanceSetting(octx, domain.HumanActor, settings.ACPAllow, rawJSON("true"))
	if err != nil || entry.Value != true || entry.Source != settings.FromDB {
		t.Fatalf("set = %+v %v", entry, err)
	}
	// mesmo valor de novo: sem linha nova na trilha
	if _, err := e.svc.SetInstanceSetting(octx, domain.HumanActor, settings.ACPAllow, rawJSON("true")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetInstanceSetting(octx, domain.HumanActor, settings.AlertsRunStuckAfter, rawJSON(`"30m"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UnsetInstanceSetting(octx, domain.HumanActor, settings.ACPAllow); err != nil {
		t.Fatal(err)
	}
	audit, err := e.svc.ListInstanceSettingAudit(octx, 10)
	if err != nil || len(audit) != 3 {
		t.Fatalf("auditoria = %d %v", len(audit), err)
	}
	// do mais novo para o mais antigo: unset (true → nulo), set 30m, set true
	if audit[0].Key != settings.ACPAllow || string(audit[0].Old) != "true" || audit[0].New != nil {
		t.Fatalf("unset auditado: %+v", audit[0])
	}
	if audit[1].Key != settings.AlertsRunStuckAfter || string(audit[1].New) != `"30m0s"` || audit[2].Old != nil {
		t.Fatalf("auditoria: %+v", audit)
	}
	if audit[0].ActorID == nil || *audit[0].ActorID != owner.ID {
		t.Fatalf("autor da mudança: %+v", audit[0])
	}

	// modo trusted (sem identidade) vale como dono
	if _, err := e.svc.SetInstanceSetting(bg, domain.HumanActor, settings.AlertsSilentFactor, rawJSON("5")); err != nil {
		t.Fatalf("trusted: %v", err)
	}
	// persona nunca
	p := e.persona(t, "Ana", nil)
	if _, err := e.svc.SetInstanceSetting(bg, domain.PersonaActor(p.ID), settings.AlertsSilentFactor, rawJSON("9")); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("persona: %v", err)
	}
}

func TestInstanceSettingsValidationAndUnknownKeys(t *testing.T) {
	e := newSettingsEnv(t)
	bg := context.Background()
	bad := map[string]string{
		"nao.existe":                    "true",
		"database_url":                  `"postgres://x"`, // segredo/infra não é chave registrada
		"secret_key":                    `"abc"`,
		settings.ACPAllow:               `"sim"`,
		settings.AlertsSilentFactor:     `0`,
		settings.ArtifactsMaxBytes:      `33554432`, // 32 MiB: acima do teto do postgres
		settings.ArtifactsQuotaBytes:    `1`,        // menor que o máximo por arquivo
		settings.AlertsRunStuckAfter:    `"banana"`,
		settings.RunReserveUSD:          `-3`,
		settings.IntegrationsAllowStdio: `1`,
	}
	for k, v := range bad {
		if _, err := e.svc.SetInstanceSetting(bg, domain.HumanActor, k, rawJSON(v)); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s=%s: %v, want invalid", k, v, err)
		}
	}
	// nada disso ficou no banco
	var n int
	_ = e.svc.Pool.QueryRow(bg, `SELECT count(*) FROM instance_settings`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d linhas gravadas apesar dos erros", n)
	}
}

func TestInstanceSettingsEnvWinsAndLocks(t *testing.T) {
	e := newSettingsEnv(t, "HANKO_ACP_ALLOW")
	bg := context.Background()
	// o banco tem um valor antigo; a env definida vence
	if _, err := e.svc.Pool.Exec(bg, `INSERT INTO instance_settings (key, value) VALUES ('acp.allow', 'true')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LoadInstanceSettings(bg); err != nil {
		t.Fatal(err)
	}
	entries, err := e.svc.ListInstanceSettings(bg)
	if err != nil {
		t.Fatal(err)
	}
	var acpEntry settings.Entry
	for _, en := range entries {
		if en.Key == settings.ACPAllow {
			acpEntry = en
		}
	}
	if acpEntry.Value != false || !acpEntry.Locked || acpEntry.Source != settings.FromEnv || acpEntry.Stored != true {
		t.Fatalf("entrada travada: %+v", acpEntry)
	}
	_, err = e.svc.SetInstanceSetting(bg, domain.HumanActor, settings.ACPAllow, rawJSON("true"))
	wantErr(t, err, domain.ErrConflict)
	if err == nil || !strings.Contains(err.Error(), "HANKO_ACP_ALLOW") {
		t.Fatalf("a recusa deve citar a variável: %v", err)
	}
	// e o efeito real continua o da env: persona acp segue recusada
	_, err = e.svc.HirePersona(bg, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "X", Agent: "acp", AdapterConfig: rawJSON(`{"command":"meu-agente"}`)})
	wantErr(t, err, domain.ErrInvalid)
}

func TestInstanceSettingsTakeEffectWithoutRestart(t *testing.T) {
	e := newSettingsEnv(t)
	bg := context.Background()

	// ACP: desligado → recusa; ligado pela tela → contrata; lista de comandos restringe
	acpIn := service.HirePersonaInput{Name: "Agente", Agent: "acp", AdapterConfig: rawJSON(`{"command":"meu-agente"}`)}
	if _, err := e.svc.HirePersona(bg, domain.HumanActor, e.co.ID, acpIn); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("acp desligado: %v", err)
	}
	if _, err := e.svc.SetInstanceSetting(bg, domain.HumanActor, settings.ACPAllow, rawJSON("true")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.HirePersona(bg, domain.HumanActor, e.co.ID, acpIn); err != nil {
		t.Fatalf("acp ligado: %v", err)
	}
	if _, err := e.svc.SetInstanceSetting(bg, domain.HumanActor, settings.ACPCommands, rawJSON(`["outro"]`)); err != nil {
		t.Fatal(err)
	}
	acpIn.Name = "Agente2"
	if _, err := e.svc.HirePersona(bg, domain.HumanActor, e.co.ID, acpIn); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("comando fora da lista: %v", err)
	}

	// stdio: sem liberar, recusa; liberado, cria
	stdio := service.CreateIntegrationInput{Kind: domain.IntegrationMCP, Name: "Local", Transport: "stdio", Command: "npx", AllPersonas: true}
	if _, err := e.svc.CreateIntegration(bg, domain.HumanActor, e.co.ID, stdio); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("stdio desligado: %v", err)
	}
	if _, err := e.svc.SetInstanceSetting(bg, domain.HumanActor, settings.IntegrationsAllowStdio, rawJSON("true")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateIntegration(bg, domain.HumanActor, e.co.ID, stdio); err != nil {
		t.Fatalf("stdio ligado: %v", err)
	}
	if tg := e.svc.PlatformToggles(); !tg.AllowStdio || !tg.ACPAllow || len(tg.ACPCommands) != 1 {
		t.Fatalf("toggles efetivos: %+v", tg)
	}

	// cotas: o limite por arquivo vale na hora; voltar ao padrão desfaz
	if _, err := e.svc.SetInstanceSetting(bg, domain.HumanActor, settings.ArtifactsMaxBytes, rawJSON("10")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutArtifact(bg, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "a.txt", Content: []byte("12345678901")}); err == nil {
		t.Fatal("deveria recusar acima de 10 bytes")
	}
	if mx, _ := e.svc.ArtifactLimits(); mx != 10 {
		t.Fatalf("limite efetivo = %d", mx)
	}
	if _, err := e.svc.UnsetInstanceSetting(bg, domain.HumanActor, settings.ArtifactsMaxBytes); err != nil {
		t.Fatal(err)
	}
	if mx, _ := e.svc.ArtifactLimits(); mx != 5<<20 {
		t.Fatalf("depois de voltar ao padrão = %d", mx)
	}
}

// O boot relê o que o dono salvou: valores gravados sobrevivem ao reinício.
func TestInstanceSettingsReloadFromDatabase(t *testing.T) {
	e := newSettingsEnv(t)
	bg := context.Background()
	if _, err := e.svc.SetInstanceSetting(bg, domain.HumanActor, settings.AlertsSilentFactor, rawJSON("7")); err != nil {
		t.Fatal(err)
	}
	fresh := settings.NewManager(map[string]any{settings.AlertsSilentFactor: int64(3)}, nil)
	e.svc.Settings = fresh
	if fresh.Int(settings.AlertsSilentFactor) != 3 {
		t.Fatal("o gerenciador novo começa no padrão")
	}
	if skipped, err := e.svc.LoadInstanceSettings(bg); err != nil || len(skipped) != 0 {
		t.Fatalf("load: %v %v", skipped, err)
	}
	if fresh.Int(settings.AlertsSilentFactor) != 7 {
		t.Fatalf("depois do reload = %d", fresh.Int(settings.AlertsSilentFactor))
	}
	// uma linha ruim no banco não derruba a carga
	if _, err := e.svc.Pool.Exec(bg, `INSERT INTO instance_settings (key, value) VALUES ('alerts.run_stuck_after', '"lixo"')`); err != nil {
		t.Fatal(err)
	}
	skipped, err := e.svc.LoadInstanceSettings(bg)
	if err != nil || len(skipped) != 1 || skipped[0] != settings.AlertsRunStuckAfter {
		t.Fatalf("linha ruim: %v %v", skipped, err)
	}
	if fresh.Int(settings.AlertsSilentFactor) != 7 {
		t.Fatal("as demais chaves continuam valendo")
	}
}
