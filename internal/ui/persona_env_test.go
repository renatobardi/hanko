package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Variáveis de ambiente da persona pela tela (#117 fatia 3, ADR-0068 §5.2): a página mostra nome e referência, valor em
// claro de versão antiga sai como "***", e a gravação só aceita referência (quem confere é o service).

const personaEnvClaro = "SENTINELA-persona-env-claro-5b1e" // valor em claro de uma versão antiga: nunca pode aparecer

// personaEnvFake guarda o que a tela pediu ao service para gravar.
type personaEnvFake struct {
	*uitest.Fake
	sets []personaEnvSet
	err  error // erro que SetPersonaEnvRef devolve (nil = grava)
}

type personaEnvSet struct{ name, ref string }

// SetPersonaEnvRef registra o pedido e devolve o erro configurado.
func (f *personaEnvFake) SetPersonaEnvRef(_ context.Context, _ domain.Actor, _, _ domain.ID, name, ref string) (domain.Persona, error) {
	f.sets = append(f.sets, personaEnvSet{name, ref})
	return domain.Persona{}, f.err
}

func newPersonaEnvEnv(t *testing.T, f *uitest.Fake) (*env, *personaEnvFake) {
	t.Helper()
	pf := &personaEnvFake{Fake: f}
	b := events.NewBroker()
	s := &server{be: pf, broker: b, version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond, log: testLogger(t)}
	mux := http.NewServeMux()
	s.routes(mux)
	return &env{f: f, broker: b, h: mux}, pf
}

// personaComEnv põe no primeiro persona da amostra um adapter_config com duas referências e um valor em claro.
func personaComEnv(f *uitest.Fake) domain.Persona {
	p := f.Personas[0]
	p.AdapterConfig = json.RawMessage(`{"command":"agente","env":{"API_TOKEN":"${secret:gw#token}","KUBECONFIG":"${secretfile:kube#config}","LEGADO":"` + personaEnvClaro + `"}}`)
	f.Personas[0] = p
	return p
}

func TestPersonaEnvMostraReferenciaEMascaraValorEmClaro(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberAdmin
	p := personaComEnv(f)
	e, _ := newPersonaEnvEnv(t, f)
	rec := e.get(t, "/c/hanko/personas/"+p.Slug)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "Variáveis de ambiente", "API_TOKEN", "${secret:gw#token}", "KUBECONFIG", "${secretfile:kube#config}",
		"LEGADO", "***", "valor em claro")
	wantNotContains(t, rec, personaEnvClaro)
}

func TestPersonaEnvMembroNaoVeFormulario(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberMember
	p := personaComEnv(f)
	e, _ := newPersonaEnvEnv(t, f)
	rec := e.get(t, "/c/hanko/personas/"+p.Slug)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "API_TOKEN", "${secretfile:kube#config}")
	wantNotContains(t, rec, "/ambiente-variaveis", "/remover", personaEnvClaro)
}

func TestPersonaEnvPoeSoReferenciaPeloService(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberAdmin
	p := personaComEnv(f)
	e, pf := newPersonaEnvEnv(t, f)
	rec := e.post(t, "/c/hanko/personas/"+p.Slug+"/ambiente-variaveis",
		url.Values{"nome": {"KUBECONFIG"}, "referencia": {"${secretfile:kube#config}"}}, false)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/personas/"+p.Slug+"?ok=variavel" {
		t.Fatalf("Location = %q (status %d)", loc, rec.Code)
	}
	if len(pf.sets) != 1 || pf.sets[0] != (personaEnvSet{"KUBECONFIG", "${secretfile:kube#config}"}) {
		t.Fatalf("pedido ao service: %+v", pf.sets)
	}
}

func TestPersonaEnvTiraAVariavelPeloCaminho(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberAdmin
	p := personaComEnv(f)
	e, pf := newPersonaEnvEnv(t, f)
	rec := e.post(t, "/c/hanko/personas/"+p.Slug+"/ambiente-variaveis/LEGADO/remover", nil, false)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/personas/"+p.Slug+"?ok=variavel-removida" {
		t.Fatalf("Location = %q (status %d)", loc, rec.Code)
	}
	if len(pf.sets) != 1 || pf.sets[0] != (personaEnvSet{"LEGADO", ""}) {
		t.Fatalf("remover manda referência vazia: %+v", pf.sets)
	}
}

func TestPersonaEnvRecusaDoServiceNaoRedireciona(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberAdmin
	p := personaComEnv(f)
	e, pf := newPersonaEnvEnv(t, f)
	pf.err = domain.Invalid("reference", "use ${secret:NOME#campo} ou ${secretfile:NOME#campo}")
	rec := e.post(t, "/c/hanko/personas/"+p.Slug+"/ambiente-variaveis",
		url.Values{"nome": {"TOKEN"}, "referencia": {"texto em claro"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if rec.Header().Get("Location") != "" || len(pf.sets) != 1 {
		t.Fatalf("recusa não pode redirecionar: Location %q, pedidos %+v", rec.Header().Get("Location"), pf.sets)
	}
}

func TestPersonaEnvAvisoDeGravadaERemovida(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberAdmin
	p := personaComEnv(f)
	e, _ := newPersonaEnvEnv(t, f)
	wantContains(t, e.get(t, "/c/hanko/personas/"+p.Slug+"?ok=variavel"), "Variável de ambiente salva.")
	wantContains(t, e.get(t, "/c/hanko/personas/"+p.Slug+"?ok=variavel-removida"), "Variável de ambiente removida.")
}
