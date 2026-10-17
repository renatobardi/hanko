package settings

import (
	"encoding/json"
	"testing"
	"time"
)

func newMgr(envSet ...string) *Manager {
	base := map[string]any{
		ACPAllow: false, ACPCommands: []string{"x"}, ArtifactsMaxBytes: int64(5 << 20), ArtifactsQuotaBytes: int64(512 << 20),
		RunReserveUSD: 1.0, AlertsSilentFactor: int64(3), AlertsRunStuckAfter: 15 * time.Minute,
	}
	env := map[string]bool{}
	for _, e := range envSet {
		env[e] = true
	}
	return NewManager(base, env)
}

func TestPrecedence(t *testing.T) {
	m := newMgr()
	m.Load(map[string]json.RawMessage{ACPAllow: json.RawMessage(`true`), ArtifactsMaxBytes: json.RawMessage(`1048576`)})
	if !m.Bool(ACPAllow) || m.Int(ArtifactsMaxBytes) != 1<<20 {
		t.Fatal("o banco deve valer sem env")
	}
	if _, src := m.Effective(ACPAllow); src != FromDB {
		t.Fatalf("source = %s", src)
	}
	if m.Int(ArtifactsQuotaBytes) != 512<<20 {
		t.Fatal("sem banco vale o padrão da configuração")
	}

	locked := newMgr("HANKO_ACP_ALLOW")
	locked.Load(map[string]json.RawMessage{ACPAllow: json.RawMessage(`true`)})
	if locked.Bool(ACPAllow) {
		t.Fatal("env definida vence o banco (aqui o valor de env é false)")
	}
	if !locked.Locked(ACPAllow) {
		t.Fatal("a chave deveria estar travada")
	}
	if _, src := locked.Effective(ACPAllow); src != FromEnv {
		t.Fatalf("source = %s", src)
	}
}

func TestLoadSkipsBadRows(t *testing.T) {
	m := newMgr()
	skipped := m.Load(map[string]json.RawMessage{"nope": json.RawMessage(`1`), AlertsSilentFactor: json.RawMessage(`"x"`), ACPAllow: json.RawMessage(`true`)})
	if len(skipped) != 2 || !m.Bool(ACPAllow) || m.Int(AlertsSilentFactor) != 3 {
		t.Fatalf("skipped=%v allow=%v silent=%d", skipped, m.Bool(ACPAllow), m.Int(AlertsSilentFactor))
	}
}

func TestCheck(t *testing.T) {
	m := newMgr()
	bad := map[string]string{
		ACPAllow:            `"sim"`,
		AlertsSilentFactor:  `0`,
		RunReserveUSD:       `-1`,
		AlertsRunStuckAfter: `"abc"`,
		ArtifactsMaxBytes:   `33554432`, // acima de 16 MiB no postgres
		ACPCommands:         `"a"`,
	}
	for k, v := range bad {
		if _, err := m.Check(k, json.RawMessage(v)); err == nil {
			t.Errorf("%s=%s deveria falhar", k, v)
		}
	}
	m.SetMaxArtifactBytes(64 << 20)
	if _, err := m.Check(ArtifactsMaxBytes, json.RawMessage(`33554432`)); err != nil {
		t.Fatalf("com s3 vale até 64 MiB: %v", err)
	}
	if _, err := m.Check("outra.chave", json.RawMessage(`1`)); err != ErrUnknownKey {
		t.Fatalf("chave fora do registro: %v", err)
	}
	if v, err := m.Check(ACPCommands, json.RawMessage(`[" a ","","b"]`)); err != nil || len(v.([]string)) != 2 {
		t.Fatalf("lista: %v %v", v, err)
	}
}

func TestFromTextRoundTrip(t *testing.T) {
	for _, d := range Defs {
		var in string
		switch d.Kind {
		case Bool:
			in = "true"
		case Int:
			in = "7"
		case Float:
			in = "2.5"
		case Duration:
			in = "30m"
		default:
			in = "a, b"
		}
		raw, err := FromText(d, in)
		if err != nil {
			t.Fatalf("%s: %v", d.Key, err)
		}
		if _, err := Parse(d, raw); err != nil && d.Key != ArtifactsMaxBytes {
			t.Fatalf("%s: %v", d.Key, err)
		}
	}
	d, _ := Lookup(AlertsRunStuckAfter)
	raw, _ := FromText(d, "90s")
	v, _ := Parse(d, raw)
	if v.(time.Duration) != 90*time.Second {
		t.Fatalf("duration = %v", v)
	}
	if string(ToJSON(90*time.Second)) != `"1m30s"` {
		t.Fatalf("ToJSON duration = %s", ToJSON(90*time.Second))
	}
}

// Nenhuma chave registrada pode ter nome de credencial: segredo só por env ou pelo cofre (ADR-0032).
func TestRegistryHasNoCredentialKeys(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Defs {
		if SensitiveName(d.Key) {
			t.Errorf("a chave %q parece credencial", d.Key)
		}
		if seen[d.Key] {
			t.Errorf("chave duplicada: %s", d.Key)
		}
		seen[d.Key] = true
		if d.Env == "" || d.Label == "" || d.Group == "" {
			t.Errorf("definição incompleta: %+v", d)
		}
	}
}
