package ui

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

var monthsLong = [...]string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}

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
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
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

func centsToUSD(c int64) float64 { return float64(c) / 100 }

// usdField formats cents for the budget input: "400", "400,50".
func usdField(c *int64) string {
	if c == nil {
		return ""
	}
	if *c%100 == 0 {
		return strconv.FormatInt(*c/100, 10)
	}
	return strings.Replace(fmt.Sprintf("%.2f", centsToUSD(*c)), ".", ",", 1)
}

// budgetBox turns a BudgetStatus into the view model of the meter.
func budgetBox(label string, st service.BudgetStatus, action, href string) views.BudgetBox {
	b := views.BudgetBox{Label: label, Spent: fmtUSD(st.SpentUSD), State: st.State, Value: usdField(st.BudgetCents), Action: action, Href: href}
	if st.BudgetCents != nil {
		b.Limit = fmtUSD(centsToUSD(*st.BudgetCents))
		b.Percent = int(math.Min(100, math.Round(st.Percent)))
		b.PercentTxt = fmt.Sprintf("%d%%", int(math.Round(st.Percent)))
		if st.State == service.BudgetExceeded && st.Percent == 0 {
			b.PercentTxt = "100%"
			b.Percent = 100
		}
	}
	switch st.State {
	case service.BudgetWarning:
		b.StateText = "Passou de 80% do limite."
	case service.BudgetExceeded:
		b.StateText = "Limite atingido: novas execuções estão bloqueadas."
	}
	return b
}

func monthLabel(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	return fmt.Sprintf("%s de %d", monthsLong[t.Month()-1], t.Year())
}

// costPeriod resolves the ?p= selector into a [from, to) window in the company time zone.
func costPeriod(p string, now time.Time, loc *time.Location) (from, to time.Time, key, label string) {
	n := now.In(loc)
	first := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
	switch p {
	case "anterior":
		from = first.AddDate(0, -1, 0)
		return from, first, "anterior", monthLabel(from, loc)
	case "30d":
		today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
		return today.AddDate(0, 0, -29), today.AddDate(0, 0, 1), "30d", "últimos 30 dias"
	}
	return first, first.AddDate(0, 1, 0), "mes", monthLabel(first, loc)
}

func dayName(key string) string {
	t, err := time.Parse("2006-01-02", key)
	if err != nil {
		return key
	}
	return fmt.Sprintf("%02d %s", t.Day(), monthsPT[t.Month()-1])
}

const chartW, chartH = 600, 160

func dayChart(sum service.CostSummary) views.DayChart {
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
	c.First, c.Last = dayName(sum.Rows[0].Key), dayName(sum.Rows[n-1].Key)
	c.Mid = dayName(sum.Rows[n/2].Key)
	if maxCost <= 0 {
		return c
	}
	c.HasData = true
	c.Max = fmtUSD(maxCost)
	c.Peak = fmt.Sprintf("pico %s %s", dayName(sum.Rows[peak].Key), fmtUSD(maxCost))
	slot := float64(chartW) / float64(n)
	bw := math.Max(2, math.Floor(slot*0.68))
	usable := float64(chartH - 6)
	for i, r := range sum.Rows {
		b := views.ChartBar{X: int(math.Round(float64(i)*slot + (slot-bw)/2)), W: int(bw), Peak: i == peak,
			Title: dayName(r.Key) + " · " + fmtUSD(r.CostUSD)}
		if r.EstimatedUSD > 0 {
			b.Title += " (estimado " + fmtUSD(r.EstimatedUSD) + ")"
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

func costTable(title string, sum service.CostSummary, href func(service.CostRow) string) views.CostTableVM {
	t := views.CostTableVM{Title: title}
	for _, r := range sum.Rows {
		if r.CostUSD == 0 && r.Runs == 0 {
			continue
		}
		share := 0
		if sum.TotalUSD > 0 {
			share = int(math.Round(r.CostUSD / sum.TotalUSD * 100))
		}
		row := views.CostRowVM{Label: r.Label, Cost: fmtUSD(r.CostUSD), Runs: fmtInt(r.Runs),
			Tokens: fmtTokens(r.InputTokens + r.OutputTokens + r.CacheTokens), Share: share, Estimated: r.EstimatedUSD > 0}
		if href != nil {
			row.Href = href(r)
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

var costNotices = map[string]string{
	"empresa": "Orçamento da empresa atualizado.",
	"persona": "Orçamento da persona atualizado.",
}

func (s *server) handleCosts(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	loc := loadLoc(co.Timezone)
	now := s.now()
	base := "/c/" + co.Slug
	from, to, key, label := costPeriod(r.URL.Query().Get("p"), now, loc)

	sums := map[string]service.CostSummary{}
	for _, g := range []string{service.GroupDay, service.GroupPersona, service.GroupProject, service.GroupModel, service.GroupHarness} {
		sum, err := s.be.CostSummary(ctx, co.ID, service.CostFilter{From: from, To: to, GroupBy: g})
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
		Month:    monthLabel(now, loc),
		Notice:   costNotices[r.URL.Query().Get("ok")],
		Subtitle: "Gasto registrado por execução, com a origem do valor. Ao chegar em 100% do orçamento, a persona ou a empresa para de abrir execuções.",
		Company:  budgetBox("Orçamento da empresa", cb, base+"/custos/orcamento", ""),
	}
	for _, p := range personas {
		slugByID[p.ID.String()] = p.Slug
		pb, err := s.be.PersonaBudget(ctx, co.ID, p.ID)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		b := budgetBox(p.Name, pb, base+"/personas/"+p.Slug+"/orcamento?next=custos", base+"/personas/"+p.Slug)
		vm.Personas = append(vm.Personas, b)
	}

	day := sums[service.GroupDay]
	vm.Chart = dayChart(day)
	vm.Total = fmtUSD(day.TotalUSD)
	vm.TotalNote = "Gasto · " + label
	vm.Runs = fmtInt(day.Runs)
	vm.Estimated = fmtUSD(day.EstimatedUSD)
	vm.EstNote = "Estimado, não informado pelo harness"
	vm.HasSpend = day.TotalUSD > 0
	if cb.BudgetCents != nil {
		vm.BudgetKpi = vm.Company.PercentTxt
		vm.BudgetNote = "Do orçamento de " + vm.Company.Limit
	} else {
		vm.BudgetKpi = "Sem limite"
		vm.BudgetNote = "Orçamento da empresa"
	}
	vm.Tables = []views.CostTableVM{
		costTable("Persona", sums[service.GroupPersona], func(r service.CostRow) string {
			if slug := slugByID[r.Key]; slug != "" {
				return base + "/personas/" + slug
			}
			return ""
		}),
		costTable("Projeto", sums[service.GroupProject], nil),
		costTable("Modelo", sums[service.GroupModel], nil),
		costTable("Harness", sums[service.GroupHarness], nil),
	}
	vm.Tabs = []views.TabLink{
		{Key: "mes", Label: "Este mês", Href: base + "/custos", On: key == "mes"},
		{Key: "anterior", Label: "Mês anterior", Href: base + "/custos?p=anterior", On: key == "anterior"},
		{Key: "30d", Label: "30 dias", Href: base + "/custos?p=30d", On: key == "30d"},
	}
	sh := s.shell(co, "custos", "Custos", views.Crumb{Label: "Custos"})
	s.render(w, r, http.StatusOK, views.CostsPage(sh, vm))
}

func (s *server) handleSetCompanyBudget(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cents, err := parseUSD(r.PostFormValue("budget"))
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
	cents, err := parseUSD(r.PostFormValue("budget"))
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
