package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Variáveis, gatilhos, revisões e arquivar de fluxos (ADR-0047) e canais externos (ADR-0046) na UI.

func TestParseVarLines(t *testing.T) {
	got := parseVarLines("repo!\n\n ambiente = staging \nregiao!=sa\nlivre\n")
	want := []domain.FlowVariable{{Name: "repo", Required: true}, {Name: "ambiente", Default: "staging"}, {Name: "regiao", Required: true, Default: "sa"}, {Name: "livre"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v want %+v", i, got[i], want[i])
		}
	}
	if len(parseVarLines("  \n")) != 0 || parseVarLines("") == nil {
		t.Error("vazio deve ser lista vazia não nula")
	}
	if formatVarLines(want) != "repo!\nambiente=staging\nregiao!=sa\nlivre" {
		t.Errorf("format = %q", formatVarLines(want))
	}
	if _, err := parseKVLines("a=1\nsem-igual"); err == nil {
		t.Error("linha sem = aceita")
	}
	m, _ := parseKVLines(" a = 1 \n\nb=2=3")
	if m["a"] != "1" || m["b"] != "2=3" {
		t.Errorf("kv = %v", m)
	}
	if formatKVLines(map[string]string{"b": "2", "a": "1"}) != "a=1\nb=2" {
		t.Error("formatKVLines")
	}
	if fireValuesText(map[string]string{"b": "2", "a": strings.Repeat("x", 80)}) != "a="+strings.Repeat("x", 60)+"…, b=2" {
		t.Errorf("fireValuesText = %q", fireValuesText(map[string]string{"b": "2", "a": strings.Repeat("x", 80)}))
	}
}

func TestFlowVariablesAndRunUI(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	pid := uitest.ID("persona:ada").String()
	co := e.f.Companies[0].ID

	// cria um fluxo sem agenda, com variáveis
	rec := e.post(t, "/c/hanko/fluxos/novo", url.Values{"name": {"Revisar PR"}, "persona": {pid}, "cron": {""}, "timezone": {"America/Sao_Paulo"},
		"priority": {"medium"}, "task_title": {"Revisar {{repo}}"}, "variables": {"repo!\nambiente=staging"}, "enabled": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	page := e.get(t, "/c/hanko/fluxos/revisar-pr")
	wantContains(t, page, "Rodar agora", `name="var_repo"`, `name="var_ambiente"`, "Gatilhos · 0", "Revisões · 1", "Sem agenda")
	if g, _ := e.f.GetFlow(context.Background(), co, "revisar-pr"); len(g.Variables) != 2 || !g.Variables[0].Required || g.Variables[1].Default != "staging" {
		t.Fatalf("variáveis: %+v", g.Variables)
	}
	// na lista, o "Rodar agora" de um fluxo com variáveis leva à tela do fluxo
	wantContains(t, e.get(t, "/c/hanko/fluxos"), `href="/c/hanko/fluxos/revisar-pr#rodar"`)

	// obrigatória faltando: volta ao formulário com o erro e o que foi digitado
	rec = e.post(t, "/c/hanko/fluxos/revisar-pr/executar", url.Values{"var_repo": {""}, "var_ambiente": {"prod"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Preencha as variáveis obrigatórias", `value="prod"`)
	// com valores: dispara
	rec = e.post(t, "/c/hanko/fluxos/revisar-pr/executar", url.Values{"var_repo": {"acme/api"}, "var_ambiente": {"prod"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !e.f.Called("RunFlowNowWith") {
		t.Error("RunFlowNowWith não foi chamado")
	}
	wantContains(t, e.get(t, "/c/hanko/fluxos/revisar-pr"), "Histórico de disparos · 1")

	// o modelo que cita variável não declarada volta como erro no campo das variáveis
	rec = e.post(t, "/c/hanko/fluxos/revisar-pr", url.Values{"name": {"Revisar PR"}, "persona": {pid}, "cron": {""}, "timezone": {"UTC"}, "priority": {"high"},
		"variables": {"repo!"}, "task_title": {"Revisar {{repo}}"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if g, _ := e.f.GetFlow(context.Background(), co, "revisar-pr"); len(g.Variables) != 1 {
		t.Errorf("variáveis depois da edição: %+v", g.Variables)
	}
}

func TestFlowTriggersAndRevisionsUI(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	co := e.f.Companies[0].ID
	base := "/c/hanko/fluxos/digest-semanal"

	// webhook: o segredo gerado aparece uma vez, na resposta da criação
	rec := e.post(t, base+"/gatilhos", url.Values{"kind": {"webhook"}, "name": {"github"}, "mapping": {"repo=repository.full_name"}}, false)
	wantStatus(t, rec, http.StatusCreated)
	wantContains(t, rec, "hk_segredo-de-teste", "X-Hanko-Signature", "/hooks/flows/", "github")
	page := e.get(t, base)
	wantContains(t, page, "Gatilhos · 1", "repo=repository.full_name", "${secret:hook-digest-semanal-")
	wantNotContains(t, page, "hk_segredo-de-teste")

	// evento
	rec = e.post(t, base+"/gatilhos", url.Values{"kind": {"event"}, "name": {"ao decidir"}, "event_verb": {"gate.*"}, "filter": {"data.decision=approved"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, base), "Gatilhos · 2", "gate.*", "data.decision=approved")
	// erro de validação volta ao formulário
	rec = e.post(t, base+"/gatilhos", url.Values{"kind": {"webhook"}, "name": {""}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	rec = e.post(t, base+"/gatilhos", url.Values{"kind": {"event"}, "name": {"x"}, "filter": {"sem-igual"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	rec = e.post(t, base+"/gatilhos", url.Values{"kind": {"event"}, "name": {"x"}, "rate": {"abc"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)

	g, _ := e.f.GetFlow(context.Background(), co, "digest-semanal")
	tid := g.Triggers[0].ID.String()
	rec = e.post(t, base+"/gatilhos/"+tid+"/desligar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if g, _ = e.f.GetFlow(context.Background(), co, "digest-semanal"); g.Triggers[0].Enabled {
		t.Error("gatilho deveria estar desligado")
	}
	rec = e.post(t, base+"/gatilhos/"+tid+"/ligar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	rec = e.post(t, base+"/gatilhos/"+tid+"/remover", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, base), "Gatilhos · 1")
	wantStatus(t, e.post(t, base+"/gatilhos/nao-e-id/remover", nil, false), http.StatusNotFound)

	// revisões: restaurar
	wantContains(t, e.get(t, base), "Revisões ·", "vigente")
	rec = e.post(t, base+"/revisoes/1/restaurar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !e.f.Called("RestoreFlowRevision") {
		t.Error("RestoreFlowRevision não foi chamado")
	}
	wantStatus(t, e.post(t, base+"/revisoes/zero/restaurar", nil, false), http.StatusNotFound)
}

func TestFlowArchiveUI(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	base := "/c/hanko/fluxos/digest-semanal"
	wantContains(t, e.get(t, base), "Arquivar fluxo")
	rec := e.post(t, base+"/arquivo", url.Values{"acao": {"arquivar"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/fluxos?ok=arquivado" {
		t.Fatalf("Location = %q", loc)
	}
	wantNotContains(t, e.get(t, "/c/hanko/fluxos"), "Digest semanal de fontes")
	arch := e.get(t, "/c/hanko/fluxos?arquivados=1")
	wantContains(t, arch, "Digest semanal de fontes", "Arquivado", "Ativos")
	page := e.get(t, base)
	wantContains(t, page, "está arquivado desde", "Desarquivar")
	wantNotContains(t, page, "Arquivar fluxo")
	rec = e.post(t, base+"/arquivo", url.Values{"acao": {"desarquivar"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, "/c/hanko/fluxos"), "Digest semanal de fontes")
}

func TestFlowM19VocabularyAndRoles(t *testing.T) {
	for name, f := range map[string]*uitest.Fake{"vazio": uitest.Empty(), "amostra": uitest.Sample()} {
		e := newEnv(t, f)
		for _, path := range []string{"/c/hanko/fluxos", "/c/hanko/fluxos?arquivados=1", "/c/hanko/fluxos/digest-semanal", "/c/hanko/configuracoes/canais"} {
			rec := e.get(t, path)
			if rec.Code == http.StatusNotFound {
				continue
			}
			text := strings.ToLower(tagRE.ReplaceAllString(rec.Body.String(), " "))
			for _, bad := range []string{"agente", "agent", "heartbeat"} {
				if strings.Contains(text, bad) {
					i := strings.Index(text, bad)
					t.Errorf("%s %s mostra %q: …%s…", name, path, bad, text[max(0, i-40):min(len(text), i+40)])
				}
			}
		}
	}
	// papel menor que admin não vê os formulários de gatilho
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberMember
	e := newEnv(t, f)
	page := e.get(t, "/c/hanko/fluxos/digest-semanal")
	wantNotContains(t, page, "Novo gatilho", "Arquivar fluxo")
	f.SetErr("CreateFlowTrigger", domain.ErrForbidden)
	f2 := uitest.Sample()
	f2.SetErr("CreateFlowTrigger", domain.ErrForbidden)
	rec := newEnv(t, f2).post(t, "/c/hanko/fluxos/digest-semanal/gatilhos", url.Values{"kind": {"event"}, "name": {"x"}, "event_verb": {"task.created"}}, false)
	wantStatus(t, rec, http.StatusForbidden)
}

func TestChannelsUI(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	pid := uitest.ID("persona:ada").String()
	base := "/c/hanko/configuracoes/canais"

	empty := e.get(t, base)
	wantStatus(t, empty, http.StatusOK)
	wantContains(t, empty, "Nenhum canal ainda.", "Novo canal", "Criar canal", "Canais")

	// erro de validação: GitHub sem repositórios
	rec := e.post(t, base, url.Values{"kind": {"github"}, "name": {"GH"}, "persona": {pid}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "repos")

	rec = e.post(t, base, url.Values{"kind": {"telegram"}, "name": {"Bot do time"}, "persona": {pid}, "token_ref": {"${secret:tg}"}, "webhook_secret_ref": {"${secret:wh}"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, base+"/") || !strings.HasSuffix(loc, "?ok=criado") {
		t.Fatalf("Location = %q", loc)
	}
	chURL := strings.TrimSuffix(loc, "?ok=criado")
	list := e.get(t, base)
	wantContains(t, list, "Bot do time", "Telegram", "Desligado")

	page := e.get(t, chURL+"?ok=criado")
	wantContains(t, page, "Canal criado.", "/hooks/channels/", "Conectar ao provedor", "Gerar código de vínculo", "Configuração", "Apagar canal")
	// ligar, conectar, vincular
	rec = e.post(t, chURL+"/ligar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	rec = e.post(t, chURL+"/conectar", nil, false)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Webhook registrado")
	rec = e.post(t, chURL+"/vincular", nil, false)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "K7QM-2XPA", "/vincular")
	// editar
	rec = e.post(t, chURL, url.Values{"name": {"Bot renomeado"}, "persona": {pid}, "token_ref": {"${secret:tg}"}, "webhook_secret_ref": {"${secret:wh}"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, base), "Bot renomeado")
	rec = e.post(t, chURL+"/desligar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	// ligar sem segredo mostra o erro na tela do canal
	rec = e.post(t, chURL, url.Values{"name": {"Bot renomeado"}, "persona": {pid}, "token_ref": {""}, "webhook_secret_ref": {""}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	rec = e.post(t, chURL+"/ligar", nil, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "token_ref")
	// mensagem que falhou pode ser reenviada
	chs, _ := e.f.ListChannels(context.Background(), e.f.Companies[0].ID)
	mid := domain.NewID()
	e.f.ChannelMsgs = append(e.f.ChannelMsgs, domain.ChannelMessage{ID: mid, ChannelID: chs[0].ID, Direction: domain.MessageOut, Kind: "gate", Body: "Gate para decidir",
		Status: domain.MsgFailed, Attempts: 6, LastError: "o provedor respondeu 403"})
	e.f.ChannelIdents = append(e.f.ChannelIdents, domain.ChannelIdentity{ID: domain.NewID(), ChannelID: chs[0].ID, UserEmail: "ana@x.com", ExternalLabel: "@ana", Status: domain.IdentityLinked})
	page = e.get(t, chURL)
	wantContains(t, page, "o provedor respondeu 403", "Reenviar", "ana@x.com", "@ana", "Seu vínculo", "Desvincular minha conta")
	rec = e.post(t, chURL+"/mensagens/"+mid.String()+"/reenviar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !e.f.Called("ResendChannelMessage") {
		t.Error("reenvio não chamado")
	}
	rec = e.post(t, chURL+"/vinculos/"+e.f.ChannelIdents[0].ID.String()+"/revogar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	rec = e.post(t, chURL+"/apagar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantContains(t, e.get(t, base), "Nenhum canal ainda.")
	wantStatus(t, e.get(t, base+"/nao-existe"), http.StatusNotFound)
	wantStatus(t, e.post(t, base+"/"+chs[0].ID.String()+"/vinculos/zzz/revogar", nil, false), http.StatusNotFound)
}

func TestChannelsRoles(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberMember
	e := newEnv(t, f)
	wantNotContains(t, e.get(t, "/c/hanko/configuracoes/canais"), "Criar canal")
	f.SetErr("CreateChannel", domain.ErrForbidden)
	rec := e.post(t, "/c/hanko/configuracoes/canais", url.Values{"kind": {"telegram"}, "name": {"x"}}, false)
	wantStatus(t, rec, http.StatusForbidden)
}
