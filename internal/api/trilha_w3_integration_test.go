//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/execenv"
)

// Trilha onda 3 (ADR-0061) contra o servidor HTTP real: onboarding, favoritos, novidades e perfil.

func TestAPIOnboardingFlow(t *testing.T) {
	e := newAPIEnv(t)
	e.svc.ReadinessRunner = func(context.Context, execenv.Target, string, map[string]string) ([]byte, error) {
		return []byte("bin=/usr/bin/claude\nversion=2.0\nauth=login\n"), nil
	}
	b := e.base()
	var st api.OnboardingState
	e.want(200, "GET", b+"/onboarding", "", &st)
	if st.Current != "harness" || st.Complete || len(st.Steps) != 4 {
		t.Fatalf("começo: %+v", st)
	}

	// agente fora da lista do assistente: 422
	e.want(422, "POST", b+"/onboarding/probe", `{"agent":"acp"}`, nil)
	var pr api.OnboardingProbe
	e.want(200, "POST", b+"/onboarding/probe", `{"agent":"claude","model":"claude-sonnet-4-5"}`, &pr)
	if pr.Status != "ready" || len(pr.Steps) != 4 {
		t.Fatalf("probe: %+v", pr)
	}
	e.want(200, "GET", b+"/onboarding", "", &st)
	if st.Current != "persona" || st.Probe == nil || st.Probe.Agent != "claude" {
		t.Fatalf("depois do probe: %+v", st)
	}

	var team api.OnboardingTeamInstalled
	e.want(200, "POST", b+"/onboarding/team", `{"slug":"squad-de-produto"}`, &team)
	if team.Lead == "" || team.Personas < 2 {
		t.Fatalf("pacote: %+v", team)
	}
	e.want(404, "POST", b+"/onboarding/team", `{"slug":"nao-existe"}`, nil)

	e.want(422, "POST", b+"/onboarding/task", `{"persona":"lixo","title":"x"}`, nil)
	var tk api.Task
	e.want(201, "POST", b+"/onboarding/task", `{"persona":"`+team.LeadID+`","title":"Resumir o README"}`, &tk)
	e.want(200, "GET", b+"/onboarding", "", &st)
	if !st.Complete || st.Current != "" {
		t.Fatalf("completo: %+v", st)
	}
	e.want(200, "POST", b+"/onboarding/dismiss", `{"dismissed":true}`, &st)
	if !st.Dismissed {
		t.Fatalf("dispensado: %+v", st)
	}
	if e.activityVerbs()["onboarding.first_task"] != 1 {
		t.Fatalf("fato da primeira task: %v", e.activityVerbs())
	}
}

func TestAPIFavoritesWhatsNewAndProfile(t *testing.T) {
	e := newAPIEnv(t)
	b := e.base()
	var nav api.NavState
	e.want(200, "PUT", b+"/nav/favorites", `{"path":"/tasks/HNK-1","title":"Revisar","favorite":true}`, &nav)
	if len(nav.Favorites) != 1 || nav.Favorites[0].Path != "/tasks/HNK-1" {
		t.Fatalf("favoritos: %+v", nav)
	}
	e.want(422, "PUT", b+"/nav/favorites", `{"path":"https://evil.example/x","favorite":true}`, nil)
	e.want(200, "PUT", b+"/nav/favorites", `{"path":"/tasks/HNK-1","favorite":false}`, &nav)
	if len(nav.Favorites) != 0 {
		t.Fatalf("removido: %+v", nav)
	}

	var w api.WhatsNew
	e.want(200, "GET", "/api/v1/whats-new", "", &w)
	if w.Version != "Unreleased" || w.Body == "" || w.Dismissed || w.Key != "it" {
		t.Fatalf("novidades: %+v", w)
	}
	e.want(204, "POST", "/api/v1/whats-new/dismiss", `{"key":"`+w.Key+`"}`, nil)
	e.want(200, "GET", "/api/v1/whats-new", "", &w)
	if !w.Dismissed {
		t.Fatal("deveria estar dispensado")
	}

	// modo trusted: não há conta, logo não há perfil
	e.want(422, "GET", "/api/v1/me/profile", "", nil)
}
