package ui

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

var monthsLong = [...]string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}
var monthsLongEN = [...]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

// parseUSD reads a dollar amount typed by a person ("400", "400,50", "1.200,50",
// "US$ 80") and returns USD cents. Empty means "no ceiling" (nil).
func parseUSD(raw string) (*int64, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "us$"), "$"))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return nil, nil
	}
	switch {
	case strings.Contains(s, ",") && strings.Contains(s, "."):
		// the last separator is the decimal one: "1.200,50" (pt-BR) or "1,200.50" (en)
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case strings.Contains(s, ","):
		s = strings.Replace(s, ",", ".", 1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e9 {
		return nil, domain.Invalid("budget", "informe um valor em dólares, como 400 ou 400,50")
	}
	c := int64(math.Round(v * 100))
	return &c, nil
}

// parseUSDCtx is parseUSD with the error in the language of ctx.
func parseUSDCtx(ctx context.Context, raw string) (*int64, error) {
	c, err := parseUSD(raw)
	if err != nil {
		return nil, domain.Invalid("budget", i18n.T(ctx, "informe um valor em dólares, como 400 ou 400,50"))
	}
	return c, nil
}

func centsToUSD(c int64) float64 { return float64(c) / 100 }

// usdField formats cents for the budget input: "400", "400,50" ("400.50" in English).
func usdField(ctx context.Context, c *int64) string {
	if c == nil {
		return ""
	}
	if *c%100 == 0 {
		return strconv.FormatInt(*c/100, 10)
	}
	return decimalComma(ctx, fmt.Sprintf("%.2f", centsToUSD(*c)))
}

// decimalComma turns the "." of a Go-formatted number into the decimal comma of pt-BR.
func decimalComma(ctx context.Context, s string) string {
	if isEN(ctx) {
		return s
	}
	return strings.Replace(s, ".", ",", 1)
}

// budgetBox turns a BudgetStatus into the view model of the meter.
func budgetBox(ctx context.Context, label string, st service.BudgetStatus, action, href string) views.BudgetBox {
	b := views.BudgetBox{Label: label, Spent: fmtUSDCtx(ctx, st.SpentUSD), State: st.State, Value: usdField(ctx, st.BudgetCents), Action: action, Href: href}
	if st.BudgetCents != nil {
		b.Limit = fmtUSDCtx(ctx, centsToUSD(*st.BudgetCents))
		b.Percent = int(math.Min(100, math.Round(st.Percent)))
		b.PercentTxt = fmt.Sprintf("%d%%", int(math.Round(st.Percent)))
		if st.State == service.BudgetExceeded && st.Percent == 0 {
			b.PercentTxt = "100%"
			b.Percent = 100
		}
	}
	switch st.State {
	case service.BudgetWarning:
		b.StateText = i18n.T(ctx, "Passou de 80% do limite.")
	case service.BudgetExceeded:
		b.StateText = i18n.T(ctx, "Limite atingido: novas execuções estão bloqueadas.")
	}
	return b
}

func monthLabel(ctx context.Context, t time.Time, loc *time.Location) string {
	t = t.In(loc)
	if isEN(ctx) {
		return fmt.Sprintf("%s %d", monthsLongEN[t.Month()-1], t.Year())
	}
	return fmt.Sprintf("%s de %d", monthsLong[t.Month()-1], t.Year())
}

// costPeriod resolves the ?p= selector into a [from, to) window in the company time zone.
func costPeriod(ctx context.Context, p string, now time.Time, loc *time.Location) (from, to time.Time, key, label string) {
	n := now.In(loc)
	first := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
	switch p {
	case "anterior":
		from = first.AddDate(0, -1, 0)
		return from, first, "anterior", monthLabel(ctx, from, loc)
	case "30d":
		today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
		return today.AddDate(0, 0, -29), today.AddDate(0, 0, 1), "30d", i18n.T(ctx, "últimos 30 dias")
	}
	return first, first.AddDate(0, 1, 0), "mes", monthLabel(ctx, first, loc)
}

func dayName(ctx context.Context, key string) string {
	t, err := time.Parse("2006-01-02", key)
	if err != nil {
		return key
	}
	if isEN(ctx) {
		return shortDate(ctx, t)
	}
	return fmt.Sprintf("%02d %s", t.Day(), monthsPT[t.Month()-1])
}

const chartW, chartH = 600, 160

func dayChart(ctx context.Context, sum service.CostSummary) views.DayChart {
	c := views.DayChart{W: chartW, H: chartH}
	n := len(sum.Rows)
	if n == 0 {
		return c
	}
	maxCost, peak := 0.0, -1
	for i, r := range sum.Rows {
		if r.CostUSD > maxCost {
			maxCost, peak = r.CostUSD, i
		}
	}
	c.First, c.Last = dayName(ctx, sum.Rows[0].Key), dayName(ctx, sum.Rows[n-1].Key)
	c.Mid = dayName(ctx, sum.Rows[n/2].Key)
	if maxCost <= 0 {
		return c
	}
	c.HasData = true
	c.Max = fmtUSDCtx(ctx, maxCost)
	c.Peak = i18n.Tf(ctx, "pico %s %s", dayName(ctx, sum.Rows[peak].Key), fmtUSDCtx(ctx, maxCost))
	slot := float64(chartW) / float64(n)
	bw := math.Max(2, math.Floor(slot*0.68))
	usable := float64(chartH - 6)
	for i, r := range sum.Rows {
		b := views.ChartBar{X: int(math.Round(float64(i)*slot + (slot-bw)/2)), W: int(bw), Peak: i == peak,
			Title: dayName(ctx, r.Key) + " · " + fmtUSDCtx(ctx, r.CostUSD)}
		if r.EstimatedUSD > 0 {
			b.Title += " (" + i18n.Tf(ctx, "estimado %s", fmtUSDCtx(ctx, r.EstimatedUSD)) + ")"
		}
		total := r.CostUSD / maxCost * usable
		est := r.EstimatedUSD / maxCost * usable
		if r.CostUSD > 0 {
			total = math.Max(total, 2)
		}
		rep := total - est
		b.H = int(math.Round(rep))
		b.Y = chartH - 1 - int(math.Round(total))
		b.EH = int(math.Round(est))
		b.EY = b.Y + b.H
		if r.EstimatedUSD > 0 && b.EH == 0 {
			b.EH = 1
		}
		c.Bars = append(c.Bars, b)
	}
	return c
}

func costTable(ctx context.Context, title string, sum service.CostSummary, href func(service.CostRow) string) views.CostTableVM {
	t := views.CostTableVM{Title: title}
	for _, r := range sum.Rows {
		if r.CostUSD == 0 && r.Runs == 0 {
			continue
		}
		share := 0
		if sum.TotalUSD > 0 {
			share = int(math.Round(r.CostUSD / sum.TotalUSD * 100))
		}
		row := views.CostRowVM{Label: r.Label, Cost: fmtUSDCtx(ctx, r.CostUSD), List: fmtUSDCtx(ctx, r.ListUSD), Paid: fmtUSDCtx(ctx, r.PaidUSD), Runs: fmtInt(r.Runs),
			Tokens: fmtTokensCtx(ctx, r.InputTokens+r.OutputTokens+r.CacheTokens), Share: share, Estimated: r.EstimatedUSD > 0}
		if href != nil {
			row.Href = href(r)
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

var costNotices = map[string]string{
	"empresa":         i18n.N("Orçamento da empresa atualizado."),
	"persona":         i18n.N("Orçamento da persona atualizado."),
	"projeto":         i18n.N("Orçamento do projeto atualizado."),
	"assinatura":      i18n.N("Assinatura salva."),
	"assinatura-nova": i18n.N("Assinatura criada."),
	"assinatura-saiu": i18n.N("Assinatura removida."),
}

func (s *server) handleCosts(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	loc := loadLoc(co.Timezone)
	now := s.now()
	base := "/c/" + co.Slug
	from, to, key, label := costPeriod(ctx, r.URL.Query().Get("p"), now, loc)

	projects, err := s.be.ListProjectViews(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var filter *service.ProjectView
	if ref := strings.TrimSpace(r.URL.Query().Get("projeto")); ref != "" {
		for i := range projects {
			if projects[i].Slug == ref || projects[i].ID.String() == ref {
				filter = &projects[i]
			}
		}
		if filter == nil {
			s.fail(w, r, &co, domain.ErrNotFound)
			return
		}
	}
	var filterID *domain.ID
	projQS := ""
	if filter != nil {
		filterID = &filter.ID
		projQS = "projeto=" + url.QueryEscape(filter.Slug)
	}

	sums := map[string]service.CostSummary{}
	for _, g := range []string{service.GroupDay, service.GroupPersona, service.GroupProject, service.GroupModel, service.GroupHarness} {
		sum, err := s.be.CostSummary(ctx, co.ID, service.CostFilter{From: from, To: to, GroupBy: g, ProjectID: filterID})
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		sums[g] = sum
	}
	cb, err := s.be.CompanyBudget(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	slugByID := map[string]string{}
	vm := views.CostsVM{
		Month:    monthLabel(ctx, now, loc),
		Notice:   i18n.T(ctx, costNotices[r.URL.Query().Get("ok")]),
		Subtitle: i18n.T(ctx, "Gasto registrado por execução, com a origem do valor. Ao chegar em 100% do orçamento, a persona ou a empresa para de abrir execuções."),
		Company:  budgetBox(ctx, i18n.T(ctx, "Orçamento da empresa"), cb, base+"/custos/orcamento", ""),
	}
	for _, p := range personas {
		slugByID[p.ID.String()] = p.Slug
		pb, err := s.be.PersonaBudget(ctx, co.ID, p.ID)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		b := budgetBox(ctx, p.Name, pb, base+"/personas/"+p.Slug+"/orcamento?next=custos", base+"/personas/"+p.Slug)
		vm.Personas = append(vm.Personas, b)
	}

	day := sums[service.GroupDay]
	vm.Chart = dayChart(ctx, day)
	vm.Total = fmtUSDCtx(ctx, day.TotalUSD)
	vm.TotalNote = i18n.T(ctx, "Gasto") + " · " + label
	vm.Runs = fmtInt(day.Runs)
	vm.Estimated = fmtUSDCtx(ctx, day.EstimatedUSD)
	vm.EstNote = i18n.T(ctx, "Estimado, não informado pelo harness")
	vm.HasSpend = day.TotalUSD > 0
	vm.Decision = decisionCostVM(ctx, day.Decision, base+"/configuracoes/decisao")
	if cb.BudgetCents != nil {
		vm.BudgetKpi = vm.Company.PercentTxt
		vm.BudgetNote = i18n.Tf(ctx, "Do orçamento de %s", vm.Company.Limit)
	} else {
		vm.BudgetKpi = i18n.T(ctx, "Sem limite")
		vm.BudgetNote = i18n.T(ctx, "Orçamento da empresa")
	}
	vm.Paid = fmtUSDCtx(ctx, day.PaidUSD)
	if filter != nil {
		vm.PaidNote = i18n.T(ctx, "Pago: mensalidades não são rateadas por projeto")
	} else if len(day.Subscriptions) == 0 {
		vm.PaidNote = i18n.T(ctx, "Pago: sem assinaturas cadastradas")
	} else {
		vm.PaidNote = i18n.Tf(ctx, "Pago (mensalidades rateadas) · sem uso %s", fmtUSDCtx(ctx, day.IdleUSD))
	}
	for _, d := range day.Rows {
		if d.CostUSD == 0 && d.PaidUSD == 0 && !d.Unused && d.ListUSD == 0 {
			continue
		}
		vm.Days = append(vm.Days, views.DayCostVM{Label: dayName(ctx, d.Key), Real: fmtUSDCtx(ctx, d.ReportedUSD), Estimated: fmtUSDCtx(ctx, d.EstimatedUSD),
			List: fmtUSDCtx(ctx, d.ListUSD), Paid: fmtUSDCtx(ctx, d.PaidUSD), Unused: d.Unused, Idle: fmtUSDCtx(ctx, d.IdleUSD)})
	}
	subs, err := s.be.ListSubscriptions(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	periodCost := map[domain.ID]service.SubscriptionCost{}
	for _, c := range day.Subscriptions {
		periodCost[c.ID] = c
	}
	vm.HasSubs = len(subs) > 0
	vm.SubCreate = base + "/custos/assinaturas"
	vm.AgentOpts = []views.Option{{Value: "claude", Label: "Claude Code", Selected: true}, {Value: "codex", Label: "Codex"}, {Value: "acp", Label: "ACP"}, {Value: "fake", Label: i18n.T(ctx, "Simulado")}}
	for _, sub := range subs {
		pc := periodCost[sub.ID]
		row := views.SubRowVM{Name: sub.Name, Agent: sub.Agent, Monthly: fmtUSDCtx(ctx, sub.MonthlyUSD), Models: modelsLabel(ctx, sub.ModelPrefixes),
			Active: sub.Active, Blocked: sub.Blocked, WeekUsage: fmtUSDCtx(ctx, sub.WeekUsageUSD), GuardPct: sub.WeeklyGuardPct,
			Paid: fmtUSDCtx(ctx, pc.PaidUSD), Idle: fmtUSDCtx(ctx, pc.IdleUSD), Runs: fmtInt(pc.Runs),
			MonthlyValue: usdFloatField(ctx, sub.MonthlyUSD), ModelsValue: strings.Join(sub.ModelPrefixes, ", "),
			UpdateAction: base + "/custos/assinaturas/" + sub.ID.String(), DeleteAction: base + "/custos/assinaturas/" + sub.ID.String() + "/remover",
			CanDelete: pc.Runs == 0}
		if sub.WeeklyLimitUSD != nil {
			row.WeekLimit = fmtUSDCtx(ctx, *sub.WeeklyLimitUSD)
			row.LimitValue = usdFloatField(ctx, *sub.WeeklyLimitUSD)
			row.WeekPercent = int(math.Min(100, math.Round(sub.WeekPercent)))
			row.WeekPctTxt = fmt.Sprintf("%d%%", int(math.Round(sub.WeekPercent)))
		}
		vm.Subs = append(vm.Subs, row)
	}
	vm.Tables = []views.CostTableVM{
		costTable(ctx, i18n.T(ctx, "Persona"), sums[service.GroupPersona], func(r service.CostRow) string {
			if slug := slugByID[r.Key]; slug != "" {
				return base + "/personas/" + slug
			}
			return ""
		}),
		costTable(ctx, i18n.T(ctx, "Projeto"), sums[service.GroupProject], func(r service.CostRow) string {
			for _, p := range projects {
				if p.ID.String() == r.Key || p.Name == r.Key {
					return base + "/custos?projeto=" + url.QueryEscape(p.Slug)
				}
			}
			return ""
		}),
		costTable(ctx, i18n.T(ctx, "Modelo"), sums[service.GroupModel], nil),
		costTable(ctx, "Harness", sums[service.GroupHarness], nil),
	}
	for _, p := range projects {
		if p.BudgetMonthlyCents == nil || p.Status == domain.ProjectArchived {
			continue
		}
		vm.Projects = append(vm.Projects, budgetBox(ctx, p.Name, p.Budget, base+"/projetos/"+p.Slug+"/orcamento?next=custos", base+"/projetos/"+p.Slug))
	}
	tabHref := func(p string) string {
		var qs []string
		if p != "" {
			qs = append(qs, "p="+p)
		}
		if projQS != "" {
			qs = append(qs, projQS)
		}
		if len(qs) == 0 {
			return base + "/custos"
		}
		return base + "/custos?" + strings.Join(qs, "&")
	}
	vm.Tabs = []views.TabLink{
		{Key: "mes", Label: i18n.T(ctx, "Este mês"), Href: tabHref(""), On: key == "mes"},
		{Key: "anterior", Label: i18n.T(ctx, "Mês anterior"), Href: tabHref("anterior"), On: key == "anterior"},
		{Key: "30d", Label: i18n.T(ctx, "30 dias"), Href: tabHref("30d"), On: key == "30d"},
	}
	if filter != nil {
		vm.FilterProject, vm.FilterHref = filter.Name, base+"/projetos/"+filter.Slug
		vm.ClearHref = base + "/custos"
		if key != "mes" {
			vm.ClearHref += "?p=" + key
		}
	}
	sh := s.shell(r, co, "custos", i18n.T(ctx, "Custos"), views.Crumb{Label: i18n.T(ctx, "Custos")})
	s.render(w, r, http.StatusOK, views.CostsPage(sh, vm))
}

func (s *server) handleSetCompanyBudget(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cents, err := parseUSDCtx(r.Context(), r.PostFormValue("budget"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := s.be.SetCompanyBudget(r.Context(), actorFrom(r.Context()), co.ID, cents); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/custos?ok=empresa")
}

func (s *server) handleSetPersonaBudget(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	cents, err := parseUSDCtx(r.Context(), r.PostFormValue("budget"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	in := service.UpdatePersonaInput{BudgetMonthlyCents: cents, ClearBudget: cents == nil}
	if _, err := s.be.UpdatePersona(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if r.URL.Query().Get("next") == "custos" {
		redirect(w, r, "/c/"+co.Slug+"/custos?ok=persona")
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=orcamento")
}

func modelsLabel(ctx context.Context, prefixes []string) string {
	if len(prefixes) == 0 {
		return i18n.T(ctx, "todos os modelos do harness")
	}
	return strings.Join(prefixes, ", ")
}

// usdFloatField formats dollars for an input: "200", "12,50".
func usdFloatField(ctx context.Context, v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return decimalComma(ctx, fmt.Sprintf("%.2f", v))
}

// dollars reads a typed dollar amount ("200", "12,50") as float dollars; empty means nil.
func dollars(ctx context.Context, raw string) (*float64, error) {
	c, err := parseUSDCtx(ctx, raw)
	if err != nil || c == nil {
		return nil, err
	}
	v := centsToUSD(*c)
	return &v, nil
}

func splitModels(raw string) []string {
	out := []string{}
	for _, m := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func guardPct(ctx context.Context, raw string) (int, error) {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, domain.Invalid(i18n.T(ctx, "trava"), i18n.T(ctx, "informe o percentual do teto semanal, como 90"))
	}
	return n, nil
}

func (s *server) handleCreateSubscription(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	monthly, err := dollars(ctx, r.PostFormValue("monthly"))
	if err == nil && monthly == nil {
		err = domain.Invalid(i18n.T(ctx, "mensalidade"), i18n.T(ctx, "informe a mensalidade em dólares"))
	}
	var limit *float64
	if err == nil {
		limit, err = dollars(ctx, r.PostFormValue("weekly_limit"))
	}
	pct := 0
	if err == nil {
		pct, err = guardPct(ctx, r.PostFormValue("guard_pct"))
	}
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	in := service.CreateSubscriptionInput{Name: trimForm(r, "name"), Agent: trimForm(r, "agent"), MonthlyUSD: *monthly,
		ModelPrefixes: splitModels(r.PostFormValue("models")), WeeklyLimitUSD: limit, WeeklyGuardPct: pct}
	if _, err := s.be.CreateSubscription(r.Context(), actorFrom(r.Context()), co.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/custos?ok=assinatura-nova#assinaturas")
}

func (s *server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("sub"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	monthly, err := dollars(ctx, r.PostFormValue("monthly"))
	if err == nil && monthly == nil {
		err = domain.Invalid(i18n.T(ctx, "mensalidade"), i18n.T(ctx, "informe a mensalidade em dólares"))
	}
	var limit *float64
	if err == nil {
		limit, err = dollars(ctx, r.PostFormValue("weekly_limit"))
	}
	pct := 0
	if err == nil {
		pct, err = guardPct(ctx, r.PostFormValue("guard_pct"))
	}
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	prefixes := splitModels(r.PostFormValue("models"))
	active := r.PostFormValue("active") != ""
	in := service.UpdateSubscriptionInput{MonthlyUSD: monthly, WeeklyLimitUSD: limit, ClearWeeklyLimit: limit == nil,
		ModelPrefixes: &prefixes, Active: &active}
	if pct > 0 {
		in.WeeklyGuardPct = &pct
	}
	if _, err := s.be.UpdateSubscription(r.Context(), actorFrom(r.Context()), co.ID, id, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/custos?ok=assinatura#assinaturas")
}

func (s *server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("sub"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.be.DeleteSubscription(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/custos?ok=assinatura-saiu#assinaturas")
}
