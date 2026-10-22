package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/provider"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Configurações › Decision model (ADR-0056). A chave é sempre uma referência ${secret:NOME}.

type decisionBackend interface {
	GetDecisionModel(ctx context.Context, companyID domain.ID) (service.DecisionView, error)
	SetDecisionModel(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.SetDecisionModelInput) (service.DecisionView, error)
	DeleteDecisionModel(ctx context.Context, actor domain.Actor, companyID domain.ID) error
	SetDecisionFeature(ctx context.Context, actor domain.Actor, companyID domain.ID, feature domain.DecisionFeature, enabled bool, model string) (service.DecisionView, error)
	DecisionCatalog(ctx context.Context, companyID domain.ID, baseURL, apiKeyRef string) ([]provider.CatalogModel, error)
	ListDecisionInvocations(ctx context.Context, companyID domain.ID, limit int) ([]domain.DecisionInvocation, error)
}

// featureTexts traduz título e descrição das features registradas (o texto pt-BR é o de domain.DecisionFeatures).
var featureTexts = map[domain.DecisionFeature][2]string{
	domain.FeatureTriage:          {i18n.N("Triagem"), i18n.N("Sugere a persona e a prioridade para uma task nova, como comentário do sistema.")},
	domain.FeaturePhaseClassifier: {i18n.N("Classificador de fase"), i18n.N("Sugere em qual etapa de um pipeline um texto se encaixa.")},
	domain.FeatureNameSummary:     {i18n.N("Nome e resumo"), i18n.N("Gera um nome curto e um resumo para um texto longo.")},
	domain.FeatureFastResponse:    {i18n.N("Resposta rápida"), i18n.N("Responde direto a perguntas simples que chegam por um canal externo, sem acordar a persona.")},
	domain.FeatureCardSummary:     {i18n.N("Resumo de card de status"), i18n.N("Escreve o resumo dos cards de status que usam o decision model como motor.")},
}

func (s *server) decisionBE() (decisionBackend, bool) {
	b, ok := s.be.(decisionBackend)
	return b, ok
}

var decisionNotices = map[string]string{
	"salvo":    i18n.N("Decision model salvo."),
	"removido": i18n.N("Decision model removido."),
	"feature":  i18n.N("Feature atualizada."),
}

func (s *server) decisionVM(r *http.Request, co domain.Company, be decisionBackend) (views.DecisionVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug + "/configuracoes/decisao"
	vm := views.DecisionVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "decisao"), Notice: i18n.T(ctx, decisionNotices[r.URL.Query().Get("ok")]),
		Form: views.DecisionFormVM{Action: base, FieldErr: map[string]string{}}, RemoveHref: base + "/remover", CostsHref: "/c/" + co.Slug + "/custos"}
	v, err := be.GetDecisionModel(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	if c := v.Config; c != nil {
		vm.Configured = true
		vm.Form.Configure = true
		vm.Form.BaseURL, vm.Form.KeyRef, vm.Form.Model = c.BaseURL, c.APIKeyRef, c.Model
		vm.Form.Timeout, vm.Form.Off = strconv.Itoa(c.TimeoutSec), !c.Enabled
		// catálogo do endpoint salvo, para escolher o modelo; indisponível não derruba a página
		if ms, cerr := be.DecisionCatalog(ctx, co.ID, "", ""); cerr != nil {
			vm.CatalogNote = i18n.T(ctx, "Catálogo indisponível agora; digite o id do modelo.")
		} else {
			for _, m := range ms {
				vm.Models = append(vm.Models, m.ID)
			}
		}
	}
	for _, f := range v.Features {
		vm.Features = append(vm.Features, views.DecisionFeatureVM{Key: string(f.Feature), Title: i18n.T(ctx, featureTexts[f.Feature][0]), Desc: i18n.T(ctx, featureTexts[f.Feature][1]),
			Enabled: f.Enabled, Model: f.Model, Action: base + "/features/" + string(f.Feature)})
	}
	if sb, ok := s.secretsBE(); ok {
		if list, lerr := sb.ListSecrets(ctx, co.ID, domain.SecretScopeCompany, false); lerr == nil {
			for _, sec := range list {
				vm.Secrets = append(vm.Secrets, "${secret:"+sec.Name+"}")
			}
		}
	}
	invs, err := be.ListDecisionInvocations(ctx, co.ID, 20)
	if err != nil {
		return vm, err
	}
	for _, i := range invs {
		row := views.DecisionInvRowVM{When: i.CreatedAt.Local().Format("02/01 15:04"), Feature: decisionFeatureTitle(ctx, i.Feature), Model: i.Model, OK: i.Status == "ok",
			Cost: fmt.Sprintf("US$ %.6f", i.CostUSD), Source: i.CostSource, Tokens: fmt.Sprintf("%d/%d", i.InputTokens, i.OutputTokens), ErrorMsg: i.Error}
		vm.Invocations = append(vm.Invocations, row)
	}
	return vm, nil
}

// decisionCostVM é o bloco "Decision model" do Custos; nil quando não houve nenhuma invocação no período.
func decisionCostVM(ctx context.Context, d service.DecisionCost, href string) *views.DecisionCostVM {
	if d.Calls == 0 {
		return nil
	}
	vm := &views.DecisionCostVM{Total: fmtUSDCtx(ctx, d.CostUSD), Calls: fmtInt(d.Calls), Href: href}
	for _, r := range d.Rows {
		title := decisionFeatureTitle(ctx, domain.DecisionFeature(r.Feature))
		vm.Rows = append(vm.Rows, views.DecisionCostRowVM{Feature: title, Model: r.Model, Calls: fmtInt(r.Calls), Errors: fmtInt(r.Errors),
			Cost: fmtUSDCtx(ctx, r.CostUSD), Tokens: fmt.Sprintf("%d/%d", r.InputTokens, r.OutputTokens)})
	}
	return vm
}

func decisionFeatureTitle(ctx context.Context, f domain.DecisionFeature) string {
	if t, ok := featureTexts[f]; ok {
		return i18n.T(ctx, t[0])
	}
	return string(f)
}

func (s *server) renderDecision(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.DecisionVM) {
	sh := s.settingsShell(r, co, i18n.T(r.Context(), "Decision model"))
	s.render(w, r, status, views.DecisionPage(sh, vm))
}

func (s *server) handleDecision(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.decisionBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	vm, err := s.decisionVM(r, co, be)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderDecision(w, r, co, http.StatusOK, vm)
}

// decisionErrorMsg traduz o erro do service em texto para a página (nunca com o valor da chave).
func decisionErrorMsg(ctx context.Context, err error) (field, msg string) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		switch ve.Field {
		case "api_key_ref":
			return ve.Field, i18n.T(ctx, "Use uma referência ${secret:NOME} a um segredo que exista na empresa.")
		case "model":
			return ve.Field, i18n.T(ctx, "Escolha um modelo que esteja no catálogo do endpoint.")
		case "base_url":
			return ve.Field, i18n.T(ctx, "Endpoint inválido: use uma URL http(s) de uma API compatível com chat completions.")
		case "timeout_sec":
			return ve.Field, i18n.T(ctx, "O prazo deve ficar entre 1 e 300 segundos.")
		}
		return ve.Field, ve.Field + ": " + ve.Reason + "."
	}
	_, title, m := friendly(ctx, err)
	return "", title + ". " + m
}

func (s *server) handleSaveDecision(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.decisionBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	form := views.DecisionFormVM{Action: "/c/" + co.Slug + "/configuracoes/decisao", BaseURL: trimForm(r, "base_url"), KeyRef: trimForm(r, "api_key_ref"),
		Model: trimForm(r, "model"), Timeout: trimForm(r, "timeout_sec"), Off: r.PostFormValue("off") != "", FieldErr: map[string]string{}}
	in := service.SetDecisionModelInput{BaseURL: form.BaseURL, APIKeyRef: form.KeyRef, Model: form.Model}
	if form.Timeout != "" {
		n, err := strconv.Atoi(form.Timeout)
		if err != nil {
			form.FieldErr["timeout_sec"] = i18n.T(ctx, "O prazo deve ficar entre 1 e 300 segundos.")
			s.decisionFail(w, r, co, be, nil, form)
			return
		}
		in.TimeoutSec = n
	}
	enabled := !form.Off
	in.Enabled = &enabled
	v, err := be.SetDecisionModel(ctx, actorFrom(ctx), co.ID, in)
	if err != nil {
		s.decisionFail(w, r, co, be, err, form)
		return
	}
	if v.Warning != "" {
		vm, verr := s.decisionVM(r, co, be)
		if verr != nil {
			s.fail(w, r, &co, verr)
			return
		}
		vm.Warning = v.Warning
		s.renderDecision(w, r, co, http.StatusOK, vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/decisao?ok=salvo")
}

func (s *server) decisionFail(w http.ResponseWriter, r *http.Request, co domain.Company, be decisionBackend, err error, form views.DecisionFormVM) {
	vm, lerr := s.decisionVM(r, co, be)
	if lerr != nil {
		s.fail(w, r, &co, lerr)
		return
	}
	status := http.StatusUnprocessableEntity
	if err != nil {
		field, msg := decisionErrorMsg(r.Context(), err)
		vm.Error = msg
		if field != "" {
			form.FieldErr[field] = msg
		}
		status = statusFor(err)
	}
	form.Configure = vm.Configured
	vm.Form = form
	s.renderDecision(w, r, co, status, vm)
}

func (s *server) handleDeleteDecision(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.decisionBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	if err := be.DeleteDecisionModel(r.Context(), actorFrom(r.Context()), co.ID); err != nil {
		s.decisionFail(w, r, co, be, err, views.DecisionFormVM{Action: "/c/" + co.Slug + "/configuracoes/decisao", FieldErr: map[string]string{}})
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/decisao?ok=removido")
}

func (s *server) handleDecisionFeature(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.decisionBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	enabled := strings.TrimSpace(r.PostFormValue("enabled")) == "1"
	if _, err := be.SetDecisionFeature(r.Context(), actorFrom(r.Context()), co.ID, domain.DecisionFeature(r.PathValue("feature")), enabled, trimForm(r, "model")); err != nil {
		s.decisionFail(w, r, co, be, err, views.DecisionFormVM{Action: "/c/" + co.Slug + "/configuracoes/decisao", FieldErr: map[string]string{}})
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/decisao?ok=feature")
}
