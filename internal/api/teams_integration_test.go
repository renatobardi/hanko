//go:build integration

package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// API dos pacotes de time (ADR-0044): catálogo, prévia, instalação, exportação e ida e volta.
func TestAPITeamPackages(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()

	var cat api.ListBody[api.TeamSummary]
	e.want(200, "GET", "/api/v1/team-catalog", "", &cat)
	if len(cat.Items) < 3 || cat.Items[0].Personas == 0 || cat.Items[0].Agents == nil {
		t.Fatalf("catálogo: %+v", cat.Items)
	}
	e.want(404, "GET", "/api/v1/team-catalog/nao-existe", "", nil)
	var one map[string]any
	e.want(200, "GET", "/api/v1/team-catalog/mesa-de-conteudo", "", &one)
	if one["format"] != "hanko.team.v1" {
		t.Fatalf("pacote do catálogo: %v", one["format"])
	}

	// prévia: nada gravado
	var prev api.TeamInstalled
	e.want(200, "POST", base+"/teams/preview", `{"catalog":"mesa-de-conteudo"}`, &prev)
	if !prev.DryRun || len(prev.Items) == 0 || prev.InstallID != "" || prev.SHA256 == "" || len(prev.Agents) != 1 {
		t.Fatalf("prévia: %+v", prev)
	}
	var personas api.ListBody[api.Persona]
	e.want(200, "GET", base+"/personas", "", &personas)
	if len(personas.Items) != 0 {
		t.Fatalf("a prévia gravou personas: %d", len(personas.Items))
	}

	// entradas inválidas
	e.want(422, "POST", base+"/teams/install", `{}`, nil)
	e.want(422, "POST", base+"/teams/install", `{"catalog":"mesa-de-conteudo","package":{"format":"hanko.team.v1"}}`, nil)
	e.want(404, "POST", base+"/teams/install", `{"catalog":"nao-existe"}`, nil)
	e.want(422, "POST", base+"/teams/install", `{"package":{"format":"hanko.company.v1"}}`, nil)
	e.want(422, "POST", base+"/teams/install", `{"package":{"format":"hanko.team.v1","campo_novo":1}}`, nil)

	// instala; personas entram pausadas
	var got api.TeamInstalled
	e.want(201, "POST", base+"/teams/install", `{"catalog":"mesa-de-conteudo"}`, &got)
	if got.DryRun || got.InstallID == "" || got.Counts["personas"] != 3 {
		t.Fatalf("instalação: %+v", got)
	}
	e.want(200, "GET", base+"/personas", "", &personas)
	if len(personas.Items) != 3 {
		t.Fatalf("personas = %d", len(personas.Items))
	}
	for _, p := range personas.Items {
		if p.Status != "paused" {
			t.Errorf("%s: %s", p.Slug, p.Status)
		}
	}
	var inst api.ListBody[api.TeamInstallRecord]
	e.want(200, "GET", base+"/team-installs", "", &inst)
	if len(inst.Items) != 1 || inst.Items[0].Slug != "mesa-de-conteudo" || inst.Items[0].Source != "catalog" {
		t.Fatalf("instalações: %+v", inst.Items)
	}

	// exporta um subset e reinstala numa empresa nova
	var pkg map[string]any
	e.want(200, "GET", base+"/team-export?personas=editora,redatora&name=Dupla", "", &pkg)
	raw, _ := json.Marshal(pkg)
	if !strings.Contains(string(raw), `"hanko.team.v1"`) || strings.Contains(string(raw), "revisora-de-texto") {
		t.Fatalf("exportação: %s", raw)
	}
	other := e.company("Outra", "outra")
	body, _ := json.Marshal(map[string]any{"package": pkg, "on_conflict": "skip"})
	e.want(201, "POST", "/api/v1/companies/"+other.Slug+"/teams/install", string(body), &got)
	if got.Counts["personas"] != 2 {
		t.Fatalf("segunda empresa: %+v", got)
	}
	e.want(422, "GET", base+"/team-export?personas=ninguem", "", nil)

	// run token não instala nem exporta
	ana := e.persona(e.co, "Ana", nil)
	hdr := e.personaToken(e.co, ana)
	if code, _ := e.call("POST", base+"/teams/install", `{"catalog":"mesa-de-conteudo"}`, hdr); code != 403 {
		t.Fatalf("persona instalando: %d", code)
	}
	if code, _ := e.call("GET", base+"/team-export", "", hdr); code != 403 {
		t.Fatalf("persona exportando: %d", code)
	}
}
