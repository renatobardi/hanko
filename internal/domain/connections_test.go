package domain

import (
	"testing"
	"time"
)

func TestConnectionStatus(t *testing.T) {
	cases := []struct {
		st                  ConnectionStatus
		valid, usable, auth bool
	}{
		{ConnDraft, true, false, true},
		{ConnPendingAuth, true, false, true},
		{ConnConnected, true, true, false},
		{ConnExpired, true, false, true},
		{ConnError, true, false, false},
		{ConnRevoked, true, false, true},
		{"", false, false, false},
		{"outro", false, false, false},
	}
	for _, c := range cases {
		if c.st.Valid() != c.valid || c.st.Usable() != c.usable || c.st.NeedsAuthorization() != c.auth {
			t.Errorf("%q: valid=%v usable=%v auth=%v", c.st, c.st.Valid(), c.st.Usable(), c.st.NeedsAuthorization())
		}
	}
	if !ConnIdentityCompany.Valid() || !ConnIdentityUser.Valid() || ConnectionIdentity("x").Valid() {
		t.Error("identidade")
	}
}

func TestConnectionSlugAndSecretNames(t *testing.T) {
	for s, ok := range map[string]bool{"docs": true, "docs-remoto": true, "a1": true, "": false, "1a": false, "Docs": false, "a_b": false, "a b": false} {
		if ValidConnectionSlug(s) != ok {
			t.Errorf("slug %q: want %v", s, ok)
		}
	}
	if ConnectionAccessSecret("docs") != "conn.docs.access" || ConnectionRefreshSecret("docs") != "conn.docs.refresh" {
		t.Error("nomes dos segredos")
	}
}

func TestConnectionNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	cases := []struct {
		name string
		c    Connection
		win  time.Duration
		want bool
	}{
		{"vence dentro da janela", Connection{Status: ConnConnected, ExpiresAt: at(time.Minute)}, 2 * time.Minute, true},
		{"fora da janela", Connection{Status: ConnConnected, ExpiresAt: at(time.Hour)}, 2 * time.Minute, false},
		{"já venceu", Connection{Status: ConnConnected, ExpiresAt: at(-time.Minute)}, time.Minute, true},
		{"sem validade", Connection{Status: ConnConnected}, time.Hour, false},
		{"não conectada", Connection{Status: ConnExpired, ExpiresAt: at(-time.Hour)}, time.Hour, false},
	}
	for _, c := range cases {
		if got := c.c.NeedsRefresh(now, c.win); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestConnectionNextSetup(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	future, past := now.Add(time.Minute), now.Add(-time.Minute)
	cases := []struct {
		c    Connection
		want Setup
	}{
		{Connection{Status: ConnDraft}, SetupAuthorize},
		{Connection{Status: ConnPendingAuth, AuthExpiresAt: &future}, SetupFinish},
		{Connection{Status: ConnPendingAuth, AuthExpiresAt: &past}, SetupAuthorize},
		{Connection{Status: ConnPendingAuth}, SetupAuthorize},
		{Connection{Status: ConnExpired}, SetupReconnect},
		{Connection{Status: ConnRevoked}, SetupReconnect},
		{Connection{Status: ConnError}, SetupProbe},
		{Connection{Status: ConnConnected}, SetupNone},
	}
	for _, c := range cases {
		if got := c.c.NextSetup(now); got != c.want {
			t.Errorf("%s: %q, quer %q", c.c.Status, got, c.want)
		}
	}
	if !(Connection{Status: ConnPendingAuth, AuthExpiresAt: &future}).AuthPending(now) || (Connection{Status: ConnDraft, AuthExpiresAt: &future}).AuthPending(now) {
		t.Error("AuthPending")
	}
}

func TestGateConnectionKind(t *testing.T) {
	if !GateConnection.Valid() {
		t.Error("connection deveria ser um tipo válido")
	}
	if GateConnection.Blocking() {
		t.Error("o gate de conexão não prende a persona (a task é que fica bloqueada)")
	}
}
