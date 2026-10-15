package config

import (
	"reflect"
	"testing"
)

func TestACPGate(t *testing.T) {
	const db = "postgres://u@h:5432/d"
	const secret = "0123456789abcdef0123456789abcdef"
	c, err := LoadFrom(envMap(map[string]string{"HANKO_DATABASE_URL": db, "HANKO_SECRET": secret}), files(nil))
	if err != nil || c.ACP.Allow || len(c.ACP.Commands) != 0 {
		t.Fatalf("padrão desligado: %+v err=%v", c.ACP, err)
	}
	c, err = LoadFrom(envMap(map[string]string{"HANKO_DATABASE_URL": db, "HANKO_SECRET": secret, "HANKO_ACP_ALLOW": "true", "HANKO_ACP_COMMANDS": "meu-agente,/opt/a/b"}), files(nil))
	if err != nil || !c.ACP.Allow || !reflect.DeepEqual(c.ACP.Commands, []string{"meu-agente", "/opt/a/b"}) {
		t.Fatalf("por env: %+v err=%v", c.ACP, err)
	}
	c, err = LoadFrom(envMap(map[string]string{"HANKO_DATABASE_URL": db, "HANKO_SECRET": secret, "HANKO_CONFIG": "/c.yaml"}),
		files(map[string]string{"/c.yaml": "acp:\n  allow: true\n  commands: [x]\n"}))
	if err != nil || !c.ACP.Allow || !reflect.DeepEqual(c.ACP.Commands, []string{"x"}) {
		t.Fatalf("por yaml: %+v err=%v", c.ACP, err)
	}
}
