package service

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Assistente "Conectar ferramenta a uma persona" (ADR-0089). Um serviço de orquestração: nada de modelo novo — o
// passo 2 cria a integração pelo caminho comum (nasce fechada, ADR-0067: catálogo pendente, escrita em quarentena,
// sem vínculo com persona), a descoberta usa o refresh de catálogo, e a conclusão escreve perfil + regras + vínculos +
// ligação de personas + liberação de quarentena em UMA transação, cada escrita com seu activity_event.

// maxWizardPersonas limita quantas personas uma conclusão liga de uma vez.
const maxWizardPersonas = 50

// ConnectBeginInput é o passo 1+2: servidor e credencial. Header secreto ou OAuth (ambos desde a primeira versão).
type ConnectBeginInput struct {
	Name string
	URL  string
	// Header mode: cabeçalhos fixos (por exemplo Authorization) selados no cofre.
	Headers map[string]string
	// OAuth mode (ADR-0043): conexão da empresa com PKCE.
	OAuth    bool
	Scopes   []string
	ClientID string
}

// ConnectTool é uma tool do catálogo no estado do assistente.
type ConnectTool struct {
	domain.IntegrationTool
	Quarantined bool `json:"quarantined"`
}

// ConnectView é o estado do assistente para uma integração (a tela inteira deriva daqui).
type ConnectView struct {
	Integration domain.Integration `json:"integration"`
	// Transport http/stdio (o assistente só aceita http).
	Transport  string                  `json:"transport"`
	Catalog    domain.ToolCatalogState `json:"catalog"`
	CatalogErr string                  `json:"catalog_error,omitempty"`
	Tools      []ConnectTool           `json:"tools"`
	// Connection: preenchido quando o assistente começou por OAuth; AuthURL quando falta consentir.
	Connection *domain.Connection      `json:"connection,omitempty"`
	AuthURL    string                  `json:"auth_url,omitempty"`
	Templates  []domain.AccessTemplate `json:"templates"`
	// PersonasLinked: personas já ligadas à integração (antes da conclusão é zero; >0 é integração em uso).
	PersonasLinked int `json:"personas_linked"`
	// CanComplete: descoberta feita (catálogo sincronizado, sem erro, integração ligada).
	CanComplete bool `json:"can_complete"`
}

// ConnectPreviewTool é o veredito de uma tool para uma persona no passo 5.
type ConnectPreviewTool struct {
	Name  string          `json:"name"`
	Title string          `json:"title,omitempty"`
	Risk  domain.ToolRisk `json:"risk"`
	// Mode é o modo do modelo para o risco desta tool (desconhecido conta como destrutivo).
	Mode    domain.ToolPolicyMode `json:"mode"`
	Verdict domain.ToolVerdict    `json:"verdict"`
	// Effective: após concluir, a persona vê e pode chamar esta tool.
	Effective bool `json:"effective"`
	// Note explica o que não é óbvio (sai da quarentena, política existente mais restritiva, fora do catálogo).
	Note string `json:"note,omitempty"`
}

// ConnectPreviewPersona é a conferência do passo 5 para uma persona.
type ConnectPreviewPersona struct {
	PersonaID domain.ID            `json:"persona_id"`
	Name      string               `json:"name"`
	Tools     []ConnectPreviewTool `json:"tools"`
}

// ConnectPreviewView é o passo 5: tools efetivas por persona antes de concluir.
type ConnectPreviewView struct {
	Template domain.AccessTemplate   `json:"template"`
	Personas []ConnectPreviewPersona `json:"personas"`
	// Released: tools que saem da quarentena na conclusão.
	Released []string `json:"released,omitempty"`
}

// ConnectCompleteInput é a conclusão: modelo e personas escolhidos.
type ConnectCompleteInput struct {
	Template    string      // id ou slug do modelo de acesso; obrigatório ("Personalizado" é a UI abrindo o editor de perfil, não vem aqui)
	PersonaIDs  []domain.ID // pelo menos uma; nunca "todas"
	ProfileName string      // opcional; padrão "<integração> — <modelo>"
	ProfileSlug string      // opcional; derivado do nome
}

// ConnectResultView é o resultado da conclusão.
type ConnectResultView struct {
	Integration domain.Integration          `json:"integration"`
	Profile     ToolProfileView             `json:"profile"`
	Personas    []domain.IntegrationPersona `json:"personas"`
	Released    []string                    `json:"released,omitempty"`
}

// resolveIntegrationRef resolve uma integração por id ou slug dentro da empresa.
func resolveIntegrationRef(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (store.Integration, error) {
	var r store.Integration
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		r, err = q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: id})
	} else {
		r, err = q.GetIntegrationBySlug(ctx, store.GetIntegrationBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return store.Integration{}, mapErr(err, "get integration")
	}
	return r, nil
}

// linkedPersonas conta as personas de fato ligadas à integração (a consulta traz todas as personas da empresa, com
// a ligação marcada em Enabled).
func linkedPersonas(rows []store.ListIntegrationPersonaLinksRow) int {
	n := 0
	for _, r := range rows {
		if r.Enabled {
			n++
		}
	}
	return n
}

// wizardIntegration carrega a integração do assistente com as guardas comuns aos passos (MCP; guardas de rascunho
// quem chamar decidir).
func (s *Service) wizardIntegration(ctx context.Context, companyID domain.ID, ref string) (store.Integration, error) {
	integ, err := resolveIntegrationRef(ctx, s.read(), companyID, ref)
	if err != nil {
		return store.Integration{}, err
	}
	if domain.IntegrationKind(integ.Kind) != domain.IntegrationMCP {
		return store.Integration{}, domain.Invalid("integration", "o assistente só orquestra servidores MCP")
	}
	return integ, nil
}

// ConnectBegin é o passo 1+2 do assistente (admin): cria a integração e descobre o catálogo. Header secreto: a
// integração nasce aqui e a descoberta já roda (falha de tools/list fica registrada no catálogo, sem erro). OAuth:
// nasce o rascunho da conexão (integração desligada) e a resposta traz a URL de autorização — sem consentimento
// não há descoberta.
func (s *Service) ConnectBegin(ctx context.Context, actor domain.Actor, companyID domain.ID, in ConnectBeginInput) (ConnectView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ConnectView{}, err
	}
	if in.OAuth {
		conn, err := s.CreateConnection(ctx, actor, companyID, CreateConnectionInput{
			Name: in.Name, URL: in.URL, Identity: domain.ConnIdentityCompany, Scopes: in.Scopes, ClientID: in.ClientID})
		if err != nil {
			return ConnectView{}, err
		}
		st, err := s.AuthorizeConnection(ctx, actor, companyID, conn.Slug)
		if err != nil {
			return ConnectView{}, err
		}
		if conn.IntegrationID == nil {
			return ConnectView{}, domain.Invalid("connection", "conexão sem integração da empresa")
		}
		v, err := s.ConnectState(ctx, actor, companyID, conn.IntegrationID.String())
		if err != nil {
			return ConnectView{}, err
		}
		v.AuthURL = st.URL
		return v, nil
	}
	integ, err := s.CreateIntegration(ctx, actor, companyID, CreateIntegrationInput{
		Kind: domain.IntegrationMCP, Name: in.Name, URL: in.URL, Headers: in.Headers, AllPersonas: false})
	if err != nil {
		return ConnectView{}, err
	}
	if _, err := s.RefreshIntegrationTools(ctx, actor, companyID, integ.ID); err != nil {
		return ConnectView{}, err // erro de validação (URL, header): a integração nem existiu
	}
	// falha de tools/list não é erro: o catálogo registra e o passo 2 oferece tentar de novo ou descartar
	return s.ConnectState(ctx, actor, companyID, integ.Slug)
}

// connectStateTx carrega o estado do assistente a partir da linha da integração.
func (s *Service) connectStateTx(ctx context.Context, q *store.Queries, companyID domain.ID, integ store.Integration) (ConnectView, error) {
	cfg := parseIntegrationConfig(integ.Config)
	v := ConnectView{Integration: integrationFromRow(integ, true), Transport: cfg.Transport,
		Tools: []ConnectTool{}, Templates: []domain.AccessTemplate{}}
	if cat, err := q.GetToolCatalog(ctx, store.GetToolCatalogParams{CompanyID: companyID, IntegrationID: integ.ID}); err == nil {
		v.Catalog = catalogFromRow(cat)
		v.CatalogErr = cat.LastError
	} else if !isNoRows(err) {
		return v, fmt.Errorf("get catalog: %w", err)
	}
	rows, err := q.ListIntegrationTools(ctx, store.ListIntegrationToolsParams{CompanyID: companyID, IntegrationID: integ.ID})
	if err != nil {
		return v, fmt.Errorf("list catalog tools: %w", err)
	}
	for _, r := range rows {
		tl := toolFromRow(r, integ.Slug)
		v.Tools = append(v.Tools, ConnectTool{IntegrationTool: tl, Quarantined: tl.Status == domain.ToolQuarantined})
	}
	if conn, err := q.GetConnectionByIntegration(ctx, store.GetConnectionByIntegrationParams{CompanyID: companyID, IntegrationID: &integ.ID}); err == nil {
		c := connectionFromRow(conn)
		v.Connection = &c
	} else if !isNoRows(err) {
		return v, fmt.Errorf("get connection: %w", err)
	}
	links, err := q.ListIntegrationPersonaLinks(ctx, store.ListIntegrationPersonaLinksParams{CompanyID: companyID, IntegrationID: integ.ID})
	if err != nil {
		return v, fmt.Errorf("list persona links: %w", err)
	}
	v.PersonasLinked = linkedPersonas(links)
	tpls, err := s.ListAccessTemplates(ctx, companyID)
	if err != nil {
		return v, err
	}
	v.Templates = tpls
	v.CanComplete = v.Catalog.SyncedAt != nil && v.CatalogErr == "" && integ.Enabled
	return v, nil
}

// ConnectState é o estado do assistente (admin): o que já existe, o que falta.
func (s *Service) ConnectState(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (ConnectView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ConnectView{}, err
	}
	integ, err := s.wizardIntegration(ctx, companyID, ref)
	if err != nil {
		return ConnectView{}, err
	}
	return s.connectStateTx(ctx, s.read(), companyID, integ)
}

// ConnectDiscover repete a descoberta do passo 2 ("testar de novo"). OAuth sem consentimento devolve a URL de
// autorização de novo em vez de falhar.
func (s *Service) ConnectDiscover(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (ConnectView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ConnectView{}, err
	}
	integ, err := s.wizardIntegration(ctx, companyID, ref)
	if err != nil {
		return ConnectView{}, err
	}
	if conn, err := s.read().GetConnectionByIntegration(ctx, store.GetConnectionByIntegrationParams{CompanyID: companyID, IntegrationID: &integ.ID}); err == nil &&
		domain.ConnectionStatus(conn.Status) != domain.ConnConnected {
		st, aerr := s.AuthorizeConnection(ctx, actor, companyID, conn.Slug)
		if aerr != nil {
			return ConnectView{}, aerr
		}
		v, serr := s.ConnectState(ctx, actor, companyID, ref)
		if serr != nil {
			return ConnectView{}, serr
		}
		v.AuthURL = st.URL
		return v, nil
	}
	if _, rerr := s.RefreshIntegrationTools(ctx, actor, companyID, integ.ID); rerr != nil {
		return ConnectView{}, rerr
	}
	return s.ConnectState(ctx, actor, companyID, ref)
}

// ConnectDiscard apaga o rascunho do assistente ("descartar" do passo 2). Guardas: só integração MCP que nasceu do
// assistente (sem "todas as personas" e sem persona ligada). OAuth apaga a conexão (leva a integração e os segredos).
func (s *Service) ConnectDiscard(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	q := s.read()
	integ, err := s.wizardIntegration(ctx, companyID, ref)
	if err != nil {
		return err
	}
	if integ.AllPersonas {
		return domain.Invalid("integration", "integração liberada para todas as personas não é rascunho do assistente")
	}
	links, err := q.ListIntegrationPersonaLinks(ctx, store.ListIntegrationPersonaLinksParams{CompanyID: companyID, IntegrationID: integ.ID})
	if err != nil {
		return fmt.Errorf("list persona links: %w", err)
	}
	if linkedPersonas(links) > 0 {
		return domain.Invalid("integration", "integração já ligada a personas não é rascunho do assistente")
	}
	if conn, err := q.GetConnectionByIntegration(ctx, store.GetConnectionByIntegrationParams{CompanyID: companyID, IntegrationID: &integ.ID}); err == nil {
		return s.DeleteConnection(ctx, actor, companyID, conn.Slug)
	} else if !isNoRows(err) {
		return fmt.Errorf("get connection: %w", err)
	}
	return s.DeleteIntegration(ctx, actor, companyID, integ.ID)
}

// policyChoice traduz o modo do modelo (off/ask/allow) para o par efeito/modo da regra de perfil.
func policyChoice(m domain.ToolPolicyMode) (domain.ToolRuleEffect, domain.ToolMode) {
	switch m {
	case domain.PolicyOff:
		return domain.ToolExclude, domain.ToolModeAllow
	case domain.PolicyAsk:
		return domain.ToolInclude, domain.ToolModeAsk
	}
	return domain.ToolInclude, domain.ToolModeAllow
}

// wizardRules monta as regras que o modelo viraria no perfil desta integração: uma regra de risco (escopo na
// integração) por risco declarado e uma regra de tool para cada tool de risco desconhecido — o modelo trata
// desconhecido como destrutivo, e o motor casa regra de risco só com risco declarado.
func wizardRules(integID domain.ID, tpl domain.AccessTemplate, unknownTools []string) []ToolRuleInput {
	rules := make([]ToolRuleInput, 0, 3+len(unknownTools))
	for _, r := range []struct {
		risk domain.ToolRisk
		mode domain.ToolPolicyMode
	}{
		{domain.ToolRiskRead, tpl.ReadMode},
		{domain.ToolRiskWrite, tpl.WriteMode},
		{domain.ToolRiskDestructive, tpl.DestructiveMode},
	} {
		eff, mode := policyChoice(r.mode)
		iid := integID
		rules = append(rules, ToolRuleInput{Target: domain.ToolTargetRisk, IntegrationID: &iid, Risk: r.risk, Effect: eff, Mode: mode})
	}
	iid := integID
	for _, n := range unknownTools {
		eff, mode := policyChoice(tpl.DestructiveMode)
		rules = append(rules, ToolRuleInput{Target: domain.ToolTargetTool, IntegrationID: &iid, ToolName: n, Effect: eff, Mode: mode})
	}
	return rules
}

// wizardProfile monta o perfil hipotético do modelo (mesmas regras da conclusão) para o veredito do passo 5.
func wizardProfile(integID domain.ID, tpl domain.AccessTemplate, unknownTools []string) *domain.ToolProfile {
	prof := &domain.ToolProfile{DefaultAction: domain.ToolDefaultDeny, Rules: []domain.ToolRule{}}
	for _, in := range wizardRules(integID, tpl, unknownTools) {
		prof.Rules = append(prof.Rules, domain.ToolRule{Target: in.Target, IntegrationID: in.IntegrationID,
			ToolName: in.ToolName, Risk: in.Risk, Effect: in.Effect, Mode: in.Mode})
	}
	return prof
}

// toolModeOf é o modo do modelo para o risco da tool (desconhecido conta como destrutivo).
func toolModeOf(tpl domain.AccessTemplate, risk domain.ToolRisk) domain.ToolPolicyMode {
	if risk == domain.ToolRiskUnknown {
		return tpl.DestructiveMode
	}
	return tpl.ModeForRisk(risk)
}

// ConnectPreview é o passo 5 (admin): para cada persona escolhida, o veredito de cada tool com o modelo aplicado —
// a mesma conta da conclusão, sem escrever nada. Políticas já existentes entram por cima, como no gateway.
func (s *Service) ConnectPreview(ctx context.Context, actor domain.Actor, companyID domain.ID, ref, templateRef string, personaIDs []domain.ID) (ConnectPreviewView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ConnectPreviewView{}, err
	}
	personaIDs = dedupeIDs(personaIDs)
	if len(personaIDs) == 0 {
		return ConnectPreviewView{}, domain.Invalid("personas", "escolha pelo menos uma persona")
	}
	if len(personaIDs) > maxWizardPersonas {
		return ConnectPreviewView{}, domain.Invalid("personas", "personas demais para conferir de uma vez")
	}
	q := s.read()
	integ, err := s.wizardIntegration(ctx, companyID, ref)
	if err != nil {
		return ConnectPreviewView{}, err
	}
	tplRow, err := resolveTemplateRef(ctx, q, companyID, templateRef)
	if err != nil {
		return ConnectPreviewView{}, err
	}
	tpl := templateFromRow(tplRow)
	out := ConnectPreviewView{Template: tpl, Personas: []ConnectPreviewPersona{}}
	tools, err := q.ListIntegrationTools(ctx, store.ListIntegrationToolsParams{CompanyID: companyID, IntegrationID: integ.ID})
	if err != nil {
		return out, fmt.Errorf("list catalog tools: %w", err)
	}
	var unknown []string
	for _, r := range tools {
		tl := toolFromRow(r, integ.Slug)
		if tl.Risk == domain.ToolRiskUnknown {
			unknown = append(unknown, tl.Name)
		}
		if tl.Status == domain.ToolQuarantined && toolModeOf(tpl, tl.Risk) != domain.PolicyOff {
			out.Released = append(out.Released, tl.Name)
		}
	}
	prof := wizardProfile(integ.ID, tpl, unknown)
	for _, pid := range personaIDs {
		p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: pid})
		if err != nil {
			return out, mapErr(err, "get persona")
		}
		row := ConnectPreviewPersona{PersonaID: pid, Name: p.Name, Tools: []ConnectPreviewTool{}}
		pols, err := loadToolPolicies(ctx, q, companyID, &pid)
		if err != nil {
			return out, err
		}
		for _, r := range tools {
			tl := toolFromRow(r, integ.Slug)
			verdict := toolVerdict(gatewayIntegration{row: integ, profile: prof}, pols, ToolCaller{PersonaID: &pid}, tl.Name, tl.Risk)
			mode := toolModeOf(tpl, tl.Risk)
			et := ConnectPreviewTool{Name: tl.Name, Title: tl.Title, Risk: tl.Risk, Mode: mode, Verdict: verdict,
				Effective: verdict.Allowed && (tl.Status == domain.ToolActive || tl.Status == domain.ToolQuarantined && mode != domain.PolicyOff)}
			switch {
			case tl.Status == domain.ToolQuarantined && mode != domain.PolicyOff:
				et.Note = "sai da quarentena ao concluir"
			case tl.Status != domain.ToolActive && tl.Status != domain.ToolQuarantined:
				et.Note = "fora do catálogo ativo"
			case !verdict.Allowed && verdict.Source != "" && verdict.Source != "default":
				et.Note = "regra ou política existente desta persona é mais restritiva"
			}
			row.Tools = append(row.Tools, et)
		}
		out.Personas = append(out.Personas, row)
	}
	return out, nil
}

// dedupeIDs remove repetições preservando a ordem.
func dedupeIDs(ids []domain.ID) []domain.ID {
	seen := make(map[domain.ID]bool, len(ids))
	out := ids[:0]
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// wizardProfileName monta o nome do perfil ("<integração> — <modelo>") dentro do limite de 120 caracteres.
func wizardProfileName(integName, tplName, custom string) string {
	n := custom
	if n == "" {
		n = integName + " — " + tplName
	}
	if utf8.RuneCountInString(n) > 120 {
		n = string([]rune(n)[:120])
	}
	return trimSpace(n)
}

// uniqueProfileSlug deriva um slug livre dentro da empresa (sufixo -2, -3… quando o nome já existe).
func (t *txc) uniqueProfileSlug(companyID domain.ID, base string) (string, error) {
	slug, err := resolveSlug(base, base)
	if err != nil {
		return "", err
	}
	for _, bad := range append([]string{"novo", "efetivas"}, reservedUISlugs...) {
		if slug == bad {
			return "", domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
		}
	}
	for n := 2; ; n++ {
		if _, err := t.q.GetToolProfileBySlug(t.ctx, store.GetToolProfileBySlugParams{CompanyID: companyID, Slug: slug}); isNoRows(err) {
			return slug, nil
		} else if err != nil {
			return "", fmt.Errorf("check profile slug: %w", err)
		}
		if n > 50 {
			return "", domain.Invalid("slug", "não deu para derivar um slug livre")
		}
		slug, err = resolveSlug(fmt.Sprintf("%s-%d", base, n), "")
		if err != nil {
			return "", err
		}
	}
}

// ConnectComplete conclui o assistente (admin) numa transação única: perfil (padrão negar) com as regras do modelo,
// vínculo por persona, ligação da integração a cada persona escolhida e liberação das tools cujo modo do modelo não
// é "off" (risco desconhecido conta como destrutivo). Cada escrita com seu activity_event, todos no mesmo commit.
// Nunca liga "todas as personas": só as escolhidas.
func (s *Service) ConnectComplete(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in ConnectCompleteInput) (ConnectResultView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ConnectResultView{}, err
	}
	if in.Template == "" {
		return ConnectResultView{}, domain.Invalid("template", "escolha um modelo de acesso")
	}
	in.PersonaIDs = dedupeIDs(in.PersonaIDs)
	if len(in.PersonaIDs) == 0 {
		return ConnectResultView{}, domain.Invalid("personas", "escolha pelo menos uma persona")
	}
	if len(in.PersonaIDs) > maxWizardPersonas {
		return ConnectResultView{}, domain.Invalid("personas", fmt.Sprintf("no máximo %d personas por conclusão", maxWizardPersonas))
	}
	var out ConnectResultView
	err := s.withTx(ctx, func(t *txc) error {
		integ, err := resolveIntegrationRef(ctx, t.q, companyID, ref)
		if err != nil {
			return err
		}
		if domain.IntegrationKind(integ.Kind) != domain.IntegrationMCP {
			return domain.Invalid("integration", "o assistente só orquestra servidores MCP")
		}
		// trava a linha para a conclusão não correr com outra escrita embaixo
		integ, err = t.q.GetIntegrationByIDForUpdate(ctx, store.GetIntegrationByIDForUpdateParams{CompanyID: companyID, ID: integ.ID})
		if err != nil {
			return mapErr(err, "get integration")
		}
		if integ.AllPersonas {
			return domain.Invalid("integration", "integração liberada para todas as personas não passa pelo assistente")
		}
		links, err := t.q.ListIntegrationPersonaLinks(ctx, store.ListIntegrationPersonaLinksParams{CompanyID: companyID, IntegrationID: integ.ID})
		if err != nil {
			return fmt.Errorf("list persona links: %w", err)
		}
		if linkedPersonas(links) > 0 {
			return domain.Invalid("integration", "integração já ligada a personas: ajuste pelo editor de perfis")
		}
		if !integ.Enabled {
			return domain.Invalid("integration", "conclua a autorização OAuth antes de conectar personas")
		}
		cat, err := t.q.GetToolCatalog(ctx, store.GetToolCatalogParams{CompanyID: companyID, IntegrationID: integ.ID})
		if err != nil {
			if isNoRows(err) {
				return domain.Invalid("integration", "descoberta pendente: rode o passo 2 antes de concluir")
			}
			return fmt.Errorf("get catalog: %w", err)
		}
		if cat.SyncedAt == nil {
			return domain.Invalid("integration", "descoberta pendente: rode o passo 2 antes de concluir")
		}
		tplRow, err := resolveTemplateRef(ctx, t.q, companyID, in.Template)
		if err != nil {
			return err
		}
		tpl := templateFromRow(tplRow)
		if err := t.checkPersonasInCompany(companyID, in.PersonaIDs); err != nil {
			return err
		}
		tools, err := t.q.ListIntegrationTools(ctx, store.ListIntegrationToolsParams{CompanyID: companyID, IntegrationID: integ.ID})
		if err != nil {
			return fmt.Errorf("list catalog tools: %w", err)
		}
		var unknown, quarantined []string
		for _, r := range tools {
			tl := toolFromRow(r, integ.Slug)
			if tl.Risk == domain.ToolRiskUnknown {
				unknown = append(unknown, tl.Name)
			}
			if tl.Status == domain.ToolQuarantined {
				quarantined = append(quarantined, tl.Name)
			}
		}

		// perfil: padrão negar, nome "<integração> — <modelo>"
		n, err := t.q.CountToolProfiles(ctx, companyID)
		if err != nil {
			return fmt.Errorf("count profiles: %w", err)
		}
		if n >= domain.MaxToolProfiles {
			return domain.Invalid("profiles", fmt.Sprintf("no máximo %d perfis por empresa", domain.MaxToolProfiles))
		}
		name := wizardProfileName(integ.Name, tpl.Name, in.ProfileName)
		slug := in.ProfileSlug
		if slug == "" {
			if slug, err = t.uniqueProfileSlug(companyID, domain.Slugify(name)); err != nil {
				return err
			}
		} else if slug, err = resolveSlug(slug, slug); err != nil {
			return err
		}
		profRow, err := t.q.CreateToolProfile(ctx, store.CreateToolProfileParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug,
			Name: name, Description: "Criado pelo assistente Conectar a partir do modelo " + tpl.Name + ".",
			DefaultAction: string(domain.ToolDefaultDeny), RateLimitPerMin: i32(domain.DefaultToolRateLimit),
			ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create tool profile")
		}
		if err := t.activity(actor, companyID, "tool_profile.created", "tool_profile", profRow.ID,
			map[string]any{"slug": profRow.Slug, "name": profRow.Name, "default_action": profRow.DefaultAction,
				"wizard": true, "integration": integ.Slug, "template": tpl.Slug}, ""); err != nil {
			return err
		}

		// regras do modelo (risco por risco e tool por tool para as desconhecidas)
		rules := wizardRules(integ.ID, tpl, unknown)
		if err := t.checkRuleCount(profRow.ID, len(rules)); err != nil {
			return err
		}
		for _, r := range rules {
			rr := r
			if _, err := t.validateRule(companyID, &rr); err != nil {
				return err
			}
			if _, err := t.insertRule(companyID, profRow.ID, rr); err != nil {
				return err
			}
		}

		// vínculo e ligação por persona
		for _, pid := range in.PersonaIDs {
			p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: pid})
			if err != nil {
				return mapErr(err, "get persona")
			}
			if _, err := t.q.UpsertToolBinding(ctx, store.UpsertToolBindingParams{ID: domain.NewID(), CompanyID: companyID,
				ProfileID: profRow.ID, Scope: string(domain.ToolScopePersona), PersonaID: &pid, Priority: 0, Now: t.now}); err != nil {
				return mapErr(err, "bind tool profile")
			}
			if err := t.activity(actor, companyID, "tool_profile.bound", "tool_profile", profRow.ID,
				map[string]any{"slug": profRow.Slug, "scope": string(domain.ToolScopePersona), "persona_id": pid,
					"persona": p.Slug, "priority": 0, "wizard": true}, ""); err != nil {
				return err
			}
			if err := t.q.SetIntegrationPersona(ctx, store.SetIntegrationPersonaParams{CompanyID: companyID,
				IntegrationID: integ.ID, PersonaID: pid}); err != nil {
				return fmt.Errorf("link integration persona: %w", err)
			}
			if err := t.activity(actor, companyID, "persona.integration_enabled", "persona", p.ID,
				map[string]any{"integration": integ.Slug, "persona": p.Slug, "wizard": true}, ""); err != nil {
				return err
			}
			out.Personas = append(out.Personas, domain.IntegrationPersona{PersonaID: pid, Slug: p.Slug, Name: p.Name, Enabled: true})
		}

		// liberação da quarentena: só o que o modelo não desliga
		var released []string
		for _, r := range tools {
			tl := toolFromRow(r, integ.Slug)
			if tl.Status != domain.ToolQuarantined || toolModeOf(tpl, tl.Risk) == domain.PolicyOff {
				continue
			}
			if _, err := t.q.SetIntegrationToolStatus(ctx, store.SetIntegrationToolStatusParams{CompanyID: companyID, ID: tl.ID,
				Status: string(domain.ToolActive), ReviewedByType: ptr(string(actor.Type)), ReviewedByID: actor.ID, Now: &t.now}); err != nil {
				return mapErr(err, "review tool")
			}
			released = append(released, tl.Name)
		}
		if len(released) > 0 {
			if err := t.activity(actor, companyID, "integration.tools_reviewed", "integration", integ.ID,
				map[string]any{"slug": integ.Slug, "tools": released, "wizard": true}, ""); err != nil {
				return err
			}
		}

		// resumo da conexão feita
		if err := t.activity(actor, companyID, "integration.connected", "integration", integ.ID,
			map[string]any{"slug": integ.Slug, "name": integ.Name, "template": tpl.Slug, "profile": profRow.Slug,
				"personas": len(in.PersonaIDs), "released": len(released)}, ""); err != nil {
			return err
		}
		prof, err := loadProfile(ctx, t.q, profRow)
		if err != nil {
			return err
		}
		out.Integration = integrationFromRow(integ, true)
		out.Profile = prof
		out.Released = released
		return nil
	})
	if err != nil {
		return ConnectResultView{}, err
	}
	return out, nil
}
