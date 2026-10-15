package acp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPolicyCheck(t *testing.T) {
	if err := (Policy{}).Check("meu-agente"); err == nil || !strings.Contains(err.Error(), "HANKO_ACP_ALLOW") {
		t.Fatalf("o padrão recusa: %v", err)
	}
	if err := (Policy{Allow: true}).Check("/bin/sh"); err != nil {
		t.Fatalf("liberado sem lista: %v", err)
	}
	p := Policy{Allow: true, Commands: []string{"meu-agente", "/opt/agentes/outro", " "}}
	for cmd, ok := range map[string]bool{
		"meu-agente":                    true,
		"/opt/agentes/outro":            true,
		"/opt/agentes/../agentes/outro": true,
		"/bin/sh":                       false,
		"sh":                            false,
		"/usr/bin/meu-agente":           false, // nome simples na lista não vale para outro caminho
		"./meu-agente":                  false,
		"sub/meu-agente":                false,
		"outro":                         false,
	} {
		err := p.Check(cmd)
		if (err == nil) != ok {
			t.Errorf("Check(%q) = %v, quer ok=%v", cmd, err, ok)
		}
		if err != nil && !strings.Contains(err.Error(), "HANKO_ACP_COMMANDS") {
			t.Errorf("Check(%q): motivo sem a variável: %v", cmd, err)
		}
	}
}

func TestRunRefusedByOperatorPolicy(t *testing.T) {
	spec := fakeSpec(t, "happy", nil, nil)
	for name, pol := range map[string]Policy{
		"desligado":     {},
		"fora da lista": {Allow: true, Commands: []string{"outro-agente"}},
	} {
		a := New(Options{Grace: 300 * time.Millisecond, Policy: pol})
		col := &collector{}
		res, err := a.Run(context.Background(), spec, col)
		if err == nil || !res.IsError || res.ExitCode != -1 || !strings.Contains(res.Error, "HANKO_ACP_") {
			t.Errorf("%s: err=%v res=%+v", name, err, res)
		}
		if n := len(col.all()); n != 0 {
			t.Errorf("%s: o agente não pode ter sido iniciado (%d eventos)", name, n)
		}
	}
}
