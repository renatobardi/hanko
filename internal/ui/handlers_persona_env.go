package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Persona › variáveis de ambiente (#117 fatia 3, ADR-0068 §5.2). A tela põe e tira variável do adapter_config.env só
// com referência a segredo (${secret:} ou ${secretfile:}). Valor em claro de uma versão antiga aparece como "***" e
// não sai da página; a troca é pela própria tela, com o mesmo nome.

// personaEnvBackend é o que a tela precisa para gravar; o service o tem.
type personaEnvBackend interface {
	SetPersonaEnvRef(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, name, ref string) (domain.Persona, error)
}

// personaEnvHref é o caminho da seção de variáveis de uma persona (o POST que põe a variável).
func personaEnvHref(base, slug string) string {
	return base + "/personas/" + url.PathEscape(slug) + "/ambiente-variaveis"
}

// personaVarsVM monta a seção: uma linha por variável do adapter_config.env, em ordem de nome. Só a referência
// entra na linha; o que não for referência vira "***" e a tela não o mostra.
func personaVarsVM(base string, p domain.Persona, admin bool) views.PersonaVarsVM {
	var doc struct {
		Env map[string]any `json:"env"`
	}
	vm := views.PersonaVarsVM{Admin: admin, SetHref: personaEnvHref(base, p.Slug)}
	if json.Unmarshal(p.AdapterConfig, &doc) != nil {
		return vm
	}
	names := make([]string, 0, len(doc.Env))
	for name := range doc.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		row := views.PersonaVarRow{Name: name, RemoveHref: vm.SetHref + "/" + url.PathEscape(name) + "/remover"}
		if v, _ := doc.Env[name].(string); secretref.IsRef(v) {
			row.Ref = v
		} else {
			row.Masked = true
		}
		vm.Rows = append(vm.Rows, row)
	}
	return vm
}

// handlePersonaEnvSet põe ou troca uma variável: nome e referência vêm do formulário.
func (s *server) handlePersonaEnvSet(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.setPersonaEnv(w, r, co, trimForm(r, "nome"), trimForm(r, "referencia"), "variavel")
}

// handlePersonaEnvRemove tira uma variável; o nome vem do caminho.
func (s *server) handlePersonaEnvRemove(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.setPersonaEnv(w, r, co, r.PathValue("nome"), "", "variavel-removida")
}

// setPersonaEnv grava a variável pelo service (que confere o papel, a referência e o segredo) e volta à persona.
func (s *server) setPersonaEnv(w http.ResponseWriter, r *http.Request, co domain.Company, name, ref, ok string) {
	be, found := s.be.(personaEnvBackend)
	if !found {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := be.SetPersonaEnvRef(ctx, actorFrom(ctx), co.ID, p.ID, name, ref); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok="+ok)
}
