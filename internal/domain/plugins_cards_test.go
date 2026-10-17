package domain

import (
	"testing"
	"time"
)

func TestPluginLookups(t *testing.T) {
	m := PluginManifest{
		Tools:   []PluginTool{{Name: "buscar"}},
		Widgets: []PluginWidget{{Key: "w1"}},
		Jobs:    []PluginJob{{Key: "j1", EveryMinutes: 5}},
	}
	if tl, ok := m.Tool("buscar"); !ok || tl.Name != "buscar" {
		t.Error("Tool existente")
	}
	if _, ok := m.Tool("nada"); ok {
		t.Error("Tool inexistente")
	}
	if w, ok := m.Widget("w1"); !ok || w.Key != "w1" {
		t.Error("Widget existente")
	}
	if _, ok := m.Widget("x"); ok {
		t.Error("Widget inexistente")
	}
	if j, ok := m.Job("j1"); !ok || j.EveryMinutes != 5 {
		t.Error("Job existente")
	}
	if _, ok := m.Job("x"); ok {
		t.Error("Job inexistente")
	}
}

func TestPluginStatusAndCapabilities(t *testing.T) {
	if !PluginReady.Valid() || !PluginUninstalled.Valid() || PluginStatus("x").Valid() {
		t.Error("PluginStatus.Valid")
	}
	p := Plugin{GrantedCapabilities: []string{"net", "fs"}}
	if !p.Has("net") || p.Has("exec") {
		t.Error("Plugin.Has")
	}
	for l, want := range map[string]bool{
		PluginLogDebug: true, PluginLogInfo: true, PluginLogWarn: true, PluginLogError: true,
		PluginLogStderr: false, PluginLogHost: false, "": false,
	} {
		if ValidPluginLogLevel(l) != want {
			t.Errorf("ValidPluginLogLevel(%q)", l)
		}
	}
}

func TestPluginTextLimits(t *testing.T) {
	if err := limitText("campo", "ok", 5); err != nil {
		t.Error(err)
	}
	if err := limitText("campo", "demasiado", 5); err == nil {
		t.Error("texto longo")
	}
	if err := limitText("campo", "a\xffb", 5); err == nil {
		t.Error("UTF-8 inválido")
	}
	if validPluginCommand("") == nil || validPluginCommand("a\x00b") == nil {
		t.Error("comando inválido aceito")
	}
	if validPluginCommand("bin/plugin") != nil {
		t.Error("comando válido recusado")
	}
}

func TestStatusCardStateAndPolicyDays(t *testing.T) {
	for _, s := range []StatusCardState{CardCompiling, CardActive, CardError, CardPausedBudget, CardPausedHours} {
		if !s.Valid() {
			t.Errorf("estado %s", s)
		}
	}
	if StatusCardState("x").Valid() {
		t.Error("estado inválido")
	}

	at := time.Date(2026, 10, 17, 2, 30, 0, 0, time.UTC) // 23:30 do dia 16 em São Paulo (UTC-3)
	utc := CardPolicy{}
	if got := utc.DayStart(at); !got.Equal(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("DayStart UTC = %v", got)
	}
	if got := utc.NextDay(at); !got.Equal(time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("NextDay UTC = %v", got)
	}
	sp := CardPolicy{ActiveHours: &ActiveHours{Start: "08:00", End: "18:00", TZ: "America/Sao_Paulo"}}
	if got := sp.DayStart(at); !got.Equal(time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("DayStart SP = %v", got)
	}
	if got := sp.NextDay(at); !got.Equal(time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("NextDay SP = %v", got)
	}
	// fuso inválido cai em UTC
	bad := CardPolicy{ActiveHours: &ActiveHours{Start: "08:00", End: "18:00", TZ: "Nao/Existe"}}
	if got := bad.DayStart(at); !got.Equal(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("DayStart fuso inválido = %v", got)
	}
}
