package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/mcpupstream"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/store"
)

// Catálogo de tools de uma integração MCP http (ADR-0036). O servidor descobre as tools (tools/list) passando
// pela proteção SSRF; a primeira descoberta é a linha de base (tudo entra ativo: o admin já confiou na
// integração ao criá-la); depois, tool nova que escreve ou que não declara que só lê, e tool cuja definição
// mudou e escreve, entram em quarentena até um admin revisar. Tool que sumiu vira removed.

const (
	maxToolNameLen       = 128
	maxToolTitleRunes    = 200
	maxToolDescBytes     = 8 << 10
	maxToolSchemaBytes   = 128 << 10
	toolUpstreamTimeout  = 60 * time.Second
	toolCatalogMaxAge    = time.Hour // o gateway redescobre um catálogo mais velho que isso (melhor esforço)
	toolCatalogRetryWait = time.Minute
)

var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,128}$`)

// toolHTTPClient é o cliente de saída do gateway: o de testes, se houver, senão o de netguard (SSRF na conexão).
func (s *Service) toolHTTPClient() *http.Client {
	if s.Integrations.HTTP != nil {
		return s.Integrations.HTTP
	}
	return netguard.NewClient(s.Integrations.policy(), toolUpstreamTimeout)
}

// toolEndpoint monta o destino de uma integração MCP http, com os headers guardados. Destino que a política
// do operador recusa (interno sem HANKO_INTEGRATIONS_ALLOW_PRIVATE) é erro antes de qualquer conexão.
func (s *Service) toolEndpoint(ctx context.Context, integ store.Integration) (mcpupstream.Endpoint, error) {
	cfg := parseIntegrationConfig(integ.Config)
	if domain.IntegrationKind(integ.Kind) != domain.IntegrationMCP || cfg.Transport != domain.TransportHTTP {
		return mcpupstream.Endpoint{}, domain.Invalid("integration", "só servidores MCP http têm catálogo de tools")
	}
	u, err := netguard.ValidateURL(cfg.URL, true, s.Integrations.policy())
	if err != nil {
		return mcpupstream.Endpoint{}, fmt.Errorf("destino não permitido: %w", err)
	}
	if err := s.ensureConnectionFresh(ctx, integ); err != nil { // integração de uma conexão OAuth (ADR-0043)
		return mcpupstream.Endpoint{}, err
	}
	sec, err := s.openResolved(ctx, integ.CompanyID, integ.ID, integ.SecretEnc)
	if err != nil {
		return mcpupstream.Endpoint{}, fmt.Errorf("segredos ilegíveis ou referência indisponível: %w", err)
	}
	return mcpupstream.Endpoint{URL: u.String(), Headers: sec.Headers, Client: s.toolHTTPClient(), Timeout: toolUpstreamTimeout}, nil
}

// ToolCatalogResult é o resultado de uma descoberta.
type ToolCatalogResult struct {
	OK          bool     `json:"ok"`
	Error       string   `json:"error,omitempty"`
	Tools       int      `json:"tools"`
	New         int      `json:"new"`
	Changed     int      `json:"changed"`
	Quarantined int      `json:"quarantined"`
	Removed     int      `json:"removed"`
	Skipped     []string `json:"skipped,omitempty"` // nomes inválidos ou acima do teto
	Baseline    bool     `json:"baseline"`          // foi a primeira descoberta
}

// IntegrationToolsView é o catálogo de uma integração com o estado da descoberta.
type IntegrationToolsView struct {
	Integration IntegrationView
	Catalog     domain.ToolCatalogState
	Tools       []domain.IntegrationTool
	// Stdio: a integração não passa pelo gateway; o perfil só a inclui ou exclui inteira.
	Stdio bool
}

func toolFromRow(r store.IntegrationTool, slug string) domain.IntegrationTool {
	t := domain.IntegrationTool{ID: r.ID, CompanyID: r.CompanyID, IntegrationID: r.IntegrationID, IntegrationSlug: slug,
		Name: r.Name, Title: r.Title, Description: r.Description, InputSchema: json.RawMessage(r.InputSchema),
		SchemaSHA256: r.SchemaSha256, Risk: domain.ToolRisk(r.Risk), Status: domain.ToolStatus(r.Status),
		FirstSeenAt: r.FirstSeenAt, LastSeenAt: r.LastSeenAt, ReviewedAt: r.ReviewedAt}
	_ = json.Unmarshal(r.Annotations, &t.Annotations)
	if r.ReviewedByType != nil {
		a := actorOf(*r.ReviewedByType, r.ReviewedByID)
		t.ReviewedBy = &a
	}
	return t
}

func catalogFromRow(r store.IntegrationToolCatalog) domain.ToolCatalogState {
	return domain.ToolCatalogState{IntegrationID: r.IntegrationID, SyncedAt: r.SyncedAt, LastError: r.LastError, ToolsCount: int(r.ToolsCount)}
}

// sanitizedTool é uma tool pronta para gravar.
type sanitizedTool struct {
	name, title, desc string
	schema, annots    []byte
	sha               string
	risk              domain.ToolRisk
}

// canonicalJSON reescreve um JSON com as chaves em ordem (o encoding/json ordena mapas), para hashes estáveis.
func canonicalJSON(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("null"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// objectSchema devolve o schema como objeto JSON com type "object" (o que o protocolo exige de um inputSchema);
// qualquer outra coisa, ou um schema grande demais, vira o schema genérico.
func objectSchema(raw json.RawMessage) ([]byte, bool) {
	generic := []byte(`{"type":"object"}`)
	if len(raw) == 0 || len(raw) > maxToolSchemaBytes {
		return generic, len(raw) > maxToolSchemaBytes
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m["type"] != "object" {
		return generic, false
	}
	out, err := canonicalJSON(raw)
	if err != nil {
		return generic, false
	}
	return out, false
}

func toolClipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

func sanitizeTool(t mcpupstream.Tool) (sanitizedTool, bool) {
	if !toolNameRe.MatchString(t.Name) {
		return sanitizedTool{}, false
	}
	st := sanitizedTool{name: t.Name, title: truncateRunes(strings.TrimSpace(t.Title), maxToolTitleRunes),
		desc: toolClipBytes(t.Description, maxToolDescBytes)}
	var big bool
	st.schema, big = objectSchema(t.InputSchema)
	if big {
		st.desc = toolClipBytes(st.desc+"\n\n(schema de entrada omitido pelo Hanko: grande demais)", maxToolDescBytes+80)
	}
	a := t.Annotations
	if !t.HasAnnotations {
		a = domain.ToolAnnotations{}
	}
	st.annots, _ = json.Marshal(a)
	st.risk = domain.DeriveToolRisk(a)
	h := sha256.New()
	for _, part := range [][]byte{[]byte(st.name), []byte(st.title), []byte(st.desc), st.schema, st.annots} {
		h.Write(part)
		h.Write([]byte{0})
	}
	st.sha = hex.EncodeToString(h.Sum(nil))
	return st, true
}

// RefreshIntegrationTools redescobre o catálogo de uma integração MCP http (admin). Falha de conexão ou recusa do
// servidor volta no resultado (Error), não como erro: o catálogo anterior segue valendo.
func (s *Service) RefreshIntegrationTools(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID) (ToolCatalogResult, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ToolCatalogResult{}, err
	}
	integ, err := s.read().GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: integrationID})
	if err != nil {
		return ToolCatalogResult{}, mapErr(err, "get integration")
	}
	return s.refreshCatalog(ctx, actor, integ)
}

func (s *Service) refreshCatalog(ctx context.Context, actor domain.Actor, integ store.Integration) (ToolCatalogResult, error) {
	ep, err := s.toolEndpoint(ctx, integ)
	if err != nil {
		if errors.Is(err, domain.ErrInvalid) {
			return ToolCatalogResult{}, err
		}
		return s.catalogFailed(ctx, integ, err.Error())
	}
	tools, err := mcpupstream.ListTools(ctx, ep)
	if err != nil {
		return s.catalogFailed(ctx, integ, err.Error())
	}
	var res ToolCatalogResult
	err = s.withTx(ctx, func(t *txc) error {
		res = ToolCatalogResult{OK: true}
		legacy := false
		if err := t.q.LockToolCatalog(ctx, integ.ID.String()); err != nil {
			return fmt.Errorf("lock catalog: %w", err)
		}
		// Linha de base (ADR-0036) só para integração anterior à regra, que não tem linha de catálogo: nada muda para quem
		// já a usa. Integração criada depois nasce com o catálogo pendente, e a primeira descoberta já põe em quarentena o
		// que escreve (ADR-0067).
		cat, err := t.q.GetToolCatalog(ctx, store.GetToolCatalogParams{CompanyID: integ.CompanyID, IntegrationID: integ.ID})
		switch {
		case err == nil:
			res.Baseline = cat.SyncedAt == nil
		case isNoRows(err):
			res.Baseline, legacy = true, true
		default:
			return fmt.Errorf("get catalog: %w", err)
		}
		seen := map[string]bool{}
		names := []string{}
		for _, up := range tools {
			st, ok := sanitizeTool(up)
			if !ok || seen[up.Name] || len(names) >= domain.MaxToolsPerCatalog {
				if len(res.Skipped) < 20 {
					res.Skipped = append(res.Skipped, truncateRunes(up.Name, 64))
				}
				continue
			}
			seen[st.name] = true
			names = append(names, st.name)
			if err := t.upsertCatalogTool(integ, st, res.Baseline && legacy, &res); err != nil {
				return err
			}
		}
		removed, err := t.q.MarkMissingToolsRemoved(ctx, store.MarkMissingToolsRemovedParams{Now: t.now, IntegrationID: integ.ID, Names: names})
		if err != nil {
			return fmt.Errorf("mark removed tools: %w", err)
		}
		res.Removed = len(removed)
		res.Tools = len(names)
		if err := t.q.RecordToolCatalogSync(ctx, store.RecordToolCatalogSyncParams{IntegrationID: integ.ID, CompanyID: integ.CompanyID,
			Now: &t.now, ToolsCount: i32(len(names))}); err != nil {
			return fmt.Errorf("record catalog sync: %w", err)
		}
		return t.activity(actor, integ.CompanyID, "integration.tools_refreshed", "integration", integ.ID, map[string]any{
			"slug": integ.Slug, "tools": res.Tools, "new": res.New, "changed": res.Changed, "quarantined": res.Quarantined,
			"removed": res.Removed, "baseline": res.Baseline}, "")
	})
	if err != nil {
		return ToolCatalogResult{}, err
	}
	return res, nil
}

// catalogFailed registra o erro da descoberta (sem segredos) e o devolve no resultado.
func (s *Service) catalogFailed(ctx context.Context, integ store.Integration, msg string) (ToolCatalogResult, error) {
	if sec, ok := s.secretsForRedaction(ctx, integ.CompanyID, integ.ID, integ.SecretEnc); ok {
		msg = redactSecrets(msg, sec)
	}
	msg = truncateRunes(msg, 500)
	if err := store.New(s.Pool).RecordToolCatalogError(ctx, store.RecordToolCatalogErrorParams{IntegrationID: integ.ID,
		CompanyID: integ.CompanyID, LastError: msg, Now: s.Now().UTC()}); err != nil {
		return ToolCatalogResult{}, fmt.Errorf("record catalog error: %w", err)
	}
	return ToolCatalogResult{Error: msg}, nil
}

// upsertCatalogTool grava uma tool descoberta e decide o estado dela.
func (t *txc) upsertCatalogTool(integ store.Integration, st sanitizedTool, baseline bool, res *ToolCatalogResult) error {
	cur, err := t.q.GetIntegrationToolByNameForUpdate(t.ctx, store.GetIntegrationToolByNameForUpdateParams{
		CompanyID: integ.CompanyID, IntegrationID: integ.ID, Name: st.name})
	if err != nil && !isNoRows(err) {
		return fmt.Errorf("get catalog tool: %w", err)
	}
	if isNoRows(err) {
		status := domain.ToolActive
		if !baseline && st.risk.Writes() {
			status = domain.ToolQuarantined
		}
		row, err := t.q.InsertIntegrationTool(t.ctx, store.InsertIntegrationToolParams{ID: domain.NewID(), CompanyID: integ.CompanyID,
			IntegrationID: integ.ID, Name: st.name, Title: st.title, Description: st.desc, InputSchema: st.schema, Annotations: st.annots,
			SchemaSha256: st.sha, Risk: string(st.risk), Status: string(status), Now: t.now})
		if err != nil {
			return mapErr(err, "insert catalog tool")
		}
		if !baseline {
			res.New++
		}
		if status == domain.ToolQuarantined {
			res.Quarantined++
			return t.activity(domain.SystemActor, integ.CompanyID, "integration.tool_quarantined", "integration", integ.ID,
				map[string]any{"slug": integ.Slug, "tool": row.Name, "risk": row.Risk, "reason": "new"}, "")
		}
		return nil
	}
	status := domain.ToolStatus(cur.Status)
	changed := cur.SchemaSha256 != st.sha
	reason := ""
	switch {
	case status == domain.ToolDisabled:
		// desligada por um admin continua desligada
	case status == domain.ToolRemoved:
		status, reason = domain.ToolActive, "returned"
		if st.risk.Writes() {
			status = domain.ToolQuarantined
		}
	case changed && st.risk.Writes():
		if status != domain.ToolQuarantined {
			reason = "changed"
		}
		status = domain.ToolQuarantined
	}
	if changed {
		res.Changed++
	}
	if !changed && status == domain.ToolStatus(cur.Status) {
		if _, err := t.q.UpdateIntegrationToolSeen(t.ctx, store.UpdateIntegrationToolSeenParams{ID: cur.ID, Title: cur.Title,
			Description: cur.Description, InputSchema: cur.InputSchema, Annotations: cur.Annotations, SchemaSha256: cur.SchemaSha256,
			Risk: cur.Risk, Status: cur.Status, Now: t.now}); err != nil {
			return fmt.Errorf("touch catalog tool: %w", err)
		}
		return nil
	}
	row, err := t.q.UpdateIntegrationToolSeen(t.ctx, store.UpdateIntegrationToolSeenParams{ID: cur.ID, Title: st.title, Description: st.desc,
		InputSchema: st.schema, Annotations: st.annots, SchemaSha256: st.sha, Risk: string(st.risk), Status: string(status), Now: t.now})
	if err != nil {
		return fmt.Errorf("update catalog tool: %w", err)
	}
	if status == domain.ToolQuarantined && reason != "" {
		res.Quarantined++
		return t.activity(domain.SystemActor, integ.CompanyID, "integration.tool_quarantined", "integration", integ.ID,
			map[string]any{"slug": integ.Slug, "tool": row.Name, "risk": row.Risk, "reason": reason}, "")
	}
	return nil
}

// ListIntegrationTools devolve o catálogo de uma integração MCP (qualquer membro lê).
func (s *Service) ListIntegrationTools(ctx context.Context, companyID, integrationID domain.ID) (IntegrationToolsView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return IntegrationToolsView{}, err
	}
	q := s.read()
	integ, err := q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: integrationID})
	if err != nil {
		return IntegrationToolsView{}, mapErr(err, "get integration")
	}
	if domain.IntegrationKind(integ.Kind) != domain.IntegrationMCP {
		return IntegrationToolsView{}, domain.Invalid("integration", "só servidores MCP têm tools")
	}
	out := IntegrationToolsView{Integration: IntegrationView{Integration: integrationFromRow(integ, s.canSeeURL(ctx, companyID))},
		Catalog: domain.ToolCatalogState{IntegrationID: integ.ID}, Tools: []domain.IntegrationTool{}}
	out.Stdio = parseIntegrationConfig(integ.Config).Transport == domain.TransportStdio
	if cat, err := q.GetToolCatalog(ctx, store.GetToolCatalogParams{CompanyID: companyID, IntegrationID: integ.ID}); err == nil {
		out.Catalog = catalogFromRow(cat)
	} else if !isNoRows(err) {
		return IntegrationToolsView{}, fmt.Errorf("get catalog: %w", err)
	}
	rows, err := q.ListIntegrationTools(ctx, store.ListIntegrationToolsParams{CompanyID: companyID, IntegrationID: integ.ID})
	if err != nil {
		return IntegrationToolsView{}, fmt.Errorf("list catalog: %w", err)
	}
	for _, r := range rows {
		out.Tools = append(out.Tools, toolFromRow(r, integ.Slug))
	}
	return out, nil
}

// ToolIntegration é um servidor MCP da empresa com o resumo do catálogo (para as telas de tools e perfis).
type ToolIntegration struct {
	ID          domain.ID
	Slug, Name  string
	Transport   string
	Enabled     bool
	Catalog     domain.ToolCatalogState
	Tools       int // ativas, em quarentena e desligadas (as removidas não contam)
	Quarantined int
}

// ToolIntegrations lista os servidores MCP da empresa com o resumo do catálogo (qualquer membro lê).
func (s *Service) ToolIntegrations(ctx context.Context, companyID domain.ID) ([]ToolIntegration, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListIntegrations(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	cats, err := q.ListToolCatalogs(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list catalogs: %w", err)
	}
	byID := map[domain.ID]domain.ToolCatalogState{}
	for _, c := range cats {
		byID[c.IntegrationID] = catalogFromRow(c)
	}
	tools, err := q.ListCompanyTools(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list company tools: %w", err)
	}
	out := []ToolIntegration{}
	for _, r := range rows {
		if domain.IntegrationKind(r.Kind) != domain.IntegrationMCP {
			continue
		}
		ti := ToolIntegration{ID: r.ID, Slug: r.Slug, Name: r.Name, Transport: parseIntegrationConfig(r.Config).Transport, Enabled: r.Enabled,
			Catalog: domain.ToolCatalogState{IntegrationID: r.ID}}
		if c, ok := byID[r.ID]; ok {
			ti.Catalog = c
		}
		for _, t := range tools {
			if t.IntegrationID != r.ID || domain.ToolStatus(t.Status) == domain.ToolRemoved {
				continue
			}
			ti.Tools++
			if domain.ToolStatus(t.Status) == domain.ToolQuarantined {
				ti.Quarantined++
			}
		}
		out = append(out, ti)
	}
	return out, nil
}

// ListCompanyTools devolve todas as tools catalogadas da empresa (para os perfis e a revisão).
func (s *Service) ListCompanyTools(ctx context.Context, companyID domain.ID) ([]domain.IntegrationTool, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListCompanyTools(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list company tools: %w", err)
	}
	out := make([]domain.IntegrationTool, len(rows))
	for i, r := range rows {
		out[i] = toolFromRow(store.IntegrationTool{ID: r.ID, CompanyID: r.CompanyID, IntegrationID: r.IntegrationID, Name: r.Name, Title: r.Title,
			Description: r.Description, InputSchema: r.InputSchema, Annotations: r.Annotations, SchemaSha256: r.SchemaSha256, Risk: r.Risk,
			Status: r.Status, FirstSeenAt: r.FirstSeenAt, LastSeenAt: r.LastSeenAt, ReviewedByType: r.ReviewedByType,
			ReviewedByID: r.ReviewedByID, ReviewedAt: r.ReviewedAt, UpdatedAt: r.UpdatedAt}, r.IntegrationSlug)
	}
	return out, nil
}

// SetToolStatus muda o estado de uma tool do catálogo (admin): active (libera, inclusive da quarentena) ou
// disabled. A revisão fica registrada (quem e quando).
func (s *Service) SetToolStatus(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID, tool string, status domain.ToolStatus) (domain.IntegrationTool, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.IntegrationTool{}, err
	}
	if status != domain.ToolActive && status != domain.ToolDisabled {
		return domain.IntegrationTool{}, domain.Invalid("status", "use active ou disabled")
	}
	var out domain.IntegrationTool
	err := s.withTx(ctx, func(t *txc) error {
		integ, err := t.q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: integrationID})
		if err != nil {
			return mapErr(err, "get integration")
		}
		cur, err := t.q.GetIntegrationToolByNameForUpdate(ctx, store.GetIntegrationToolByNameForUpdateParams{CompanyID: companyID,
			IntegrationID: integrationID, Name: tool})
		if err != nil {
			return mapErr(err, "get tool")
		}
		if domain.ToolStatus(cur.Status) == domain.ToolRemoved {
			return domain.Invalid("status", "a tool não é mais exposta pelo servidor; atualize o catálogo")
		}
		from := cur.Status
		row, err := t.q.SetIntegrationToolStatus(ctx, store.SetIntegrationToolStatusParams{CompanyID: companyID, ID: cur.ID,
			Status: string(status), ReviewedByType: ptr(string(actor.Type)), ReviewedByID: actor.ID, Now: &t.now})
		if err != nil {
			return mapErr(err, "set tool status")
		}
		out = toolFromRow(row, integ.Slug)
		verb := "integration.tool_enabled"
		if status == domain.ToolDisabled {
			verb = "integration.tool_disabled"
		} else if from == string(domain.ToolQuarantined) {
			verb = "integration.tool_reviewed"
		}
		return t.activity(actor, companyID, verb, "integration", integ.ID, map[string]any{"slug": integ.Slug, "tool": row.Name,
			"from": from, "to": row.Status, "risk": row.Risk}, "")
	})
	return out, err
}

// ReviewQuarantinedTools libera de uma vez as tools em quarentena de uma integração (admin), ou só as listadas.
// Devolve quantas liberou.
func (s *Service) ReviewQuarantinedTools(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID, tools []string) (int, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return 0, err
	}
	n := 0
	err := s.withTx(ctx, func(t *txc) error {
		integ, err := t.q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: integrationID})
		if err != nil {
			return mapErr(err, "get integration")
		}
		rows, err := t.q.ListIntegrationTools(ctx, store.ListIntegrationToolsParams{CompanyID: companyID, IntegrationID: integrationID})
		if err != nil {
			return fmt.Errorf("list catalog: %w", err)
		}
		want := map[string]bool{}
		for _, x := range tools {
			want[x] = true
		}
		var released []string
		for _, r := range rows {
			if domain.ToolStatus(r.Status) != domain.ToolQuarantined || len(want) > 0 && !want[r.Name] {
				continue
			}
			if _, err := t.q.SetIntegrationToolStatus(ctx, store.SetIntegrationToolStatusParams{CompanyID: companyID, ID: r.ID,
				Status: string(domain.ToolActive), ReviewedByType: ptr(string(actor.Type)), ReviewedByID: actor.ID, Now: &t.now}); err != nil {
				return mapErr(err, "review tool")
			}
			released = append(released, r.Name)
		}
		n = len(released)
		if n == 0 {
			return nil
		}
		return t.activity(actor, companyID, "integration.tools_reviewed", "integration", integ.ID,
			map[string]any{"slug": integ.Slug, "tools": released}, "")
	})
	return n, err
}

// createPendingCatalog registra o catálogo de uma integração MCP recém-criada como pendente (ADR-0067): a primeira
// descoberta dela não é linha de base, e o que escreve entra em quarentena até um admin revisar.
func (t *txc) createPendingCatalog(companyID, integrationID domain.ID, kind string) error {
	if kind != string(domain.IntegrationMCP) {
		return nil
	}
	if err := t.q.CreatePendingToolCatalog(t.ctx, store.CreatePendingToolCatalogParams{IntegrationID: integrationID,
		CompanyID: companyID, Now: t.now}); err != nil {
		return fmt.Errorf("create pending catalog: %w", err)
	}
	return nil
}

// ensureCatalog descobre o catálogo quando ele nunca foi descoberto ou está velho (melhor esforço: com um
// catálogo anterior, uma falha mantém o antigo). Usado pelo gateway, sem identidade de usuário.
func (s *Service) ensureCatalog(ctx context.Context, integ store.Integration) {
	cat, err := s.read().GetToolCatalog(ctx, store.GetToolCatalogParams{CompanyID: integ.CompanyID, IntegrationID: integ.ID})
	now := s.Now().UTC()
	switch {
	case err == nil && cat.SyncedAt != nil && now.Sub(*cat.SyncedAt) < toolCatalogMaxAge:
		return
	case err == nil && now.Sub(cat.UpdatedAt) < toolCatalogRetryWait && cat.LastError != "":
		return // falhou há pouco: não martela o servidor
	case err != nil && !isNoRows(err):
		return
	}
	_, _ = s.refreshCatalog(ctx, domain.SystemActor, integ)
}
