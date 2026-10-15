package ui

import (
	"context"
	"errors"
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

// Metas (ADR-0028). Ler é de qualquer membro; registrar progresso e ligar tasks é de member; criar, alterar,
// abandonar e reabrir é de admin. A barreira é o service; a UI só esconde os controles.

var goalNotices = map[string]string{
	"criada":      i18n.N("Meta criada."),
	"salva":       i18n.N("Meta atualizada."),
	"progresso":   i18n.N("Progresso registrado."),
	"atingida":    i18n.N("Meta atingida. Parabéns!"),
	"abandonada":  i18n.N("Meta abandonada: não recebe progresso nem tasks novas."),
	"reaberta":    i18n.N("Meta reaberta."),
	"task-ligada": i18n.N("Task ligada à meta."),
}

type goalTab struct{ key, label string }

var goalTabs = []goalTab{
	{"ativas", i18n.N("Ativas")},
	{"atingidas", i18n.N("Atingidas")},
	{"abandonadas", i18n.N("Abandonadas")},
	{"todas", i18n.N("Todas")},
}

// fmtNum é um número de meta: até duas casas, sem zeros à direita, no separador decimal do idioma.
func fmtNum(ctx context.Context, v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return decimalComma(ctx, s)
}

// parseNum lê um número digitado: "25,5", "25.5", "1.234,5" (pt) ou "1,234.5" (en). O último separador é o decimal.
func parseNum(raw string) (float64, bool) {
	s := strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
	if s == "" {
		return 0, false
	}
	dot, comma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	switch {
	case dot >= 0 && comma >= 0 && comma > dot:
		s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
	case dot >= 0 && comma >= 0:
		s = strings.ReplaceAll(s, ",", "")
	case comma >= 0:
		s = strings.ReplaceAll(s, ",", ".")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func goalBadge(ctx context.Context, st domain.GoalStatus) views.Badge {
	switch st {
	case domain.GoalAchieved:
		return views.Badge{Label: i18n.T(ctx, "Atingida"), Class: "done"}
	case domain.GoalAbandoned:
		return views.Badge{Label: i18n.T(ctx, "Abandonada"), Class: "soft"}
	}
	return views.Badge{Label: i18n.T(ctx, "Ativa"), Class: "run"}
}

// goalValues é "65 de 100 seg.".
func goalValues(ctx context.Context, g domain.Goal) string {
	unit := ""
	if g.Unit != "" {
		unit = " " + g.Unit
	}
	return i18n.Tf(ctx, "%s de %s%s", fmtNum(ctx, g.CurrentValue), fmtNum(ctx, g.TargetValue), unit)
}

func withUnit(ctx context.Context, v float64, unit string) string {
	if unit == "" {
		return fmtNum(ctx, v)
	}
	return fmtNum(ctx, v) + " " + unit
}

// dueText é o prazo (uma data, sem hora, em UTC): "30 nov 2026".
func dueText(ctx context.Context, d time.Time) string {
	return fmt.Sprintf("%s %d", shortDate(ctx, d), d.Year())
}

// goalOverdue diz se o prazo de uma meta ativa já passou (hoje, no fuso da empresa, ainda é o prazo).
func goalOverdue(g domain.Goal, now time.Time, loc *time.Location) bool {
	if g.Status != domain.GoalActive || g.DueDate == nil {
		return false
	}
	y, m, d := now.In(loc).Date()
	return g.DueDate.Before(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

func goalProgressTxt(g service.GoalView) string {
	return fmt.Sprintf("%d%%", int(math.Round(g.ProgressPercent)))
}

func goalMini(ctx context.Context, g service.GoalView, base string, now time.Time, loc *time.Location) views.GoalMini {
	m := views.GoalMini{Title: g.Title, Href: base + "/metas/" + g.Slug, Badge: goalBadge(ctx, g.Status), Progress: percentInt(g.ProgressPercent),
		ProgressTxt: goalProgressTxt(g), Overdue: goalOverdue(g.Goal, now, loc)}
	if g.DueDate != nil {
		m.Due = dueText(ctx, *g.DueDate)
	}
	return m
}

// ---------- lista ----------

func (s *server) handleGoals(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	q := r.URL.Query()
	cur := goalTabs[0].key
	for _, t := range goalTabs {
		if t.key == q.Get("aba") {
			cur = t.key
		}
	}
	var filter service.GoalFilter
	projRef, projName := strings.TrimSpace(q.Get("projeto")), ""
	if projRef != "" {
		pv, err := s.be.GetProjectView(ctx, co.ID, projRef)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		filter.ProjectID, projRef, projName = &pv.ID, pv.Slug, pv.Name
	}
	list, err := s.be.ListGoalViews(ctx, co.ID, filter)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc, now := loadLoc(co.Timezone), s.now()
	vm := views.GoalsVM{Subtitle: i18n.T(ctx, "Metas mensuráveis ligadas a projetos."), Notice: i18n.T(ctx, goalNotices[q.Get("ok")]),
		NewHref: base + "/metas/nova", Empty: len(list) == 0 && projRef == "", Project: projName, ClearHref: base + "/metas?aba=" + cur}
	counts := map[string]int{"todas": len(list)}
	for _, g := range list {
		switch g.Status {
		case domain.GoalActive:
			counts["ativas"]++
			vm.Active++
			if goalOverdue(g.Goal, now, loc) {
				vm.Overdue++
			}
		case domain.GoalAchieved:
			counts["atingidas"]++
			vm.Achieved++
		case domain.GoalAbandoned:
			counts["abandonadas"]++
			vm.Abandoned++
		}
		if (cur == "ativas" && g.Status != domain.GoalActive) || (cur == "atingidas" && g.Status != domain.GoalAchieved) ||
			(cur == "abandonadas" && g.Status != domain.GoalAbandoned) {
			continue
		}
		row := views.GoalRow{Title: g.Title, Href: base + "/metas/" + g.Slug, Badge: goalBadge(ctx, g.Status), Progress: percentInt(g.ProgressPercent),
			ProgressTxt: goalProgressTxt(g), Values: goalValues(ctx, g.Goal), Metric: g.Metric, Overdue: goalOverdue(g.Goal, now, loc)}
		if g.DueDate != nil {
			row.Due = dueText(ctx, *g.DueDate)
		}
		if g.ProjectSlug != "" {
			row.Project, row.ProjectHref = g.ProjectName, base+"/projetos/"+g.ProjectSlug
		}
		if g.TasksTotal > 0 {
			row.Tasks = i18n.Tf(ctx, "%d de %d concluídas", g.Tasks[domain.TaskDone], g.TasksTotal)
		} else {
			row.Tasks = "—"
		}
		vm.Rows = append(vm.Rows, row)
	}
	for _, t := range goalTabs {
		href := base + "/metas?aba=" + t.key
		if projRef != "" {
			href += "&projeto=" + url.QueryEscape(projRef)
		}
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: href, Count: counts[t.key], On: t.key == cur})
	}
	if projRef != "" {
		vm.ClearHref = base + "/metas?aba=" + cur
	}
	sh := s.shell(r, co, "metas", i18n.T(ctx, "Metas"), views.Crumb{Label: i18n.T(ctx, "Metas")})
	s.render(w, r, http.StatusOK, views.GoalsPage(sh, vm))
}

// ---------- formulário ----------

func (s *server) goalOwnerOptions(r *http.Request, co domain.Company, sel *domain.ID) []views.Option {
	personas, _ := s.be.ListPersonas(r.Context(), co.ID) // opcional: a meta funciona sem dona
	return personaOptions(personas, sel, i18n.T(r.Context(), "O time (humano)"))
}

func (s *server) blankGoalForm(r *http.Request, co domain.Company) views.GoalFormVM {
	base := "/c/" + co.Slug
	return views.GoalFormVM{Action: base + "/metas/nova", CancelHref: base + "/metas", FieldErr: map[string]string{},
		ProjectOptions: s.taskProjectOptions(r, co, nil), OwnerOptions: s.goalOwnerOptions(r, co, nil)}
}

func goalFormValues(r *http.Request, vm *views.GoalFormVM) {
	vm.Title, vm.Description = trimForm(r, "title"), strings.TrimSpace(r.PostFormValue("description"))
	vm.Metric, vm.Unit = trimForm(r, "metric"), trimForm(r, "unit")
	vm.Start, vm.Target, vm.Current, vm.Due = trimForm(r, "start"), trimForm(r, "target"), trimForm(r, "current"), trimForm(r, "due")
	selectOption(vm.ProjectOptions, trimForm(r, "project"))
	selectOption(vm.OwnerOptions, trimForm(r, "owner"))
}

func goalFormError(ctx context.Context, vm *views.GoalFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		field, msg := ve.Field, ""
		switch field {
		case "title":
			msg = i18n.T(ctx, "Dê um título à meta (até 200 caracteres).")
		case "metric":
			msg = i18n.T(ctx, "Descreva o que se mede em até 120 caracteres.")
		case "unit":
			msg = i18n.T(ctx, "Use até 24 caracteres na unidade.")
		case "start", "start_value":
			field, msg = "start", i18n.T(ctx, "Informe um número, como 0 ou 25,5.")
		case "target", "target_value":
			field, msg = "target", i18n.T(ctx, "Informe um alvo numérico diferente do valor inicial.")
		case "current", "current_value":
			field, msg = "current", i18n.T(ctx, "Informe um número que ainda não alcance o alvo.")
		case "due", "due_date":
			field, msg = "due", i18n.T(ctx, "Informe uma data válida.")
		case "project_id":
			msg = i18n.T(ctx, "Projeto não encontrado nesta empresa.")
		case "owner":
			msg = i18n.T(ctx, "Persona não encontrada nesta empresa.")
		default:
			msg = ve.Field + ": " + ve.Reason + "."
		}
		vm.FieldErr[field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

// goalFormInput lê os números e a data do formulário; o erro já vem como ValidationError do campo.
type goalFormInput struct {
	start, target float64
	current       *float64
	due           *time.Time
	project       *domain.ID
	owner         *domain.ID
}

func parseGoalForm(vm views.GoalFormVM, projectVal, ownerVal string) (goalFormInput, error) {
	var in goalFormInput
	var ok bool
	if in.start, ok = parseNum(vm.Start); !ok {
		return in, domain.Invalid("start", "invalid")
	}
	if in.target, ok = parseNum(vm.Target); !ok {
		return in, domain.Invalid("target", "invalid")
	}
	if vm.Current != "" {
		v, ok := parseNum(vm.Current)
		if !ok {
			return in, domain.Invalid("current", "invalid")
		}
		in.current = &v
	}
	if vm.Due != "" {
		d, err := time.Parse("2006-01-02", vm.Due)
		if err != nil {
			return in, domain.Invalid("due", "invalid")
		}
		in.due = &d
	}
	var good bool
	if in.project, good = parseID(projectVal); !good {
		return in, domain.Invalid("project_id", "invalid")
	}
	if in.owner, good = parseID(ownerVal); !good {
		return in, domain.Invalid("owner", "invalid")
	}
	return in, nil
}

func (s *server) renderNewGoal(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.GoalFormVM) {
	base := "/c/" + co.Slug
	ctx := r.Context()
	sh := s.shell(r, co, "metas", i18n.T(ctx, "Nova meta"), views.Crumb{Label: i18n.T(ctx, "Metas"), Href: base + "/metas"}, views.Crumb{Label: i18n.T(ctx, "Nova")})
	s.render(w, r, status, views.NewGoalPage(sh, vm))
}

func (s *server) handleNewGoal(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm := s.blankGoalForm(r, co)
	if ref := strings.TrimSpace(r.URL.Query().Get("projeto")); ref != "" {
		for i := range vm.ProjectOptions {
			vm.ProjectOptions[i].Selected = vm.ProjectOptions[i].Slug == ref
		}
	}
	s.renderNewGoal(w, r, co, http.StatusOK, vm)
}

func (s *server) handleCreateGoal(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	vm := s.blankGoalForm(r, co)
	goalFormValues(r, &vm)
	bad := func(err error) {
		goalFormError(ctx, &vm, err)
		s.renderNewGoal(w, r, co, statusFor(err), vm)
	}
	in, err := parseGoalForm(vm, trimForm(r, "project"), trimForm(r, "owner"))
	if err != nil {
		bad(err)
		return
	}
	g, err := s.be.CreateGoal(ctx, actorFrom(ctx), co.ID, service.CreateGoalInput{Title: vm.Title, Description: vm.Description, Metric: vm.Metric, Unit: vm.Unit,
		ProjectID: in.project, Start: in.start, Target: in.target, Current: in.current, DueDate: in.due, OwnerPersonaID: in.owner})
	if err != nil {
		bad(err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/metas/"+g.Slug+"?ok=criada")
}

// ---------- detalhe ----------

func goalChart(ctx context.Context, g service.GoalView, loc *time.Location) views.ChartVM {
	type pt struct {
		at time.Time
		v  float64
	}
	var series []pt
	if len(g.Updates) > 0 {
		series = append(series, pt{g.CreatedAt, g.StartValue})
		for _, u := range g.Updates {
			series = append(series, pt{u.CreatedAt, u.Value})
		}
	}
	c := views.ChartVM{Width: 640, Height: 240, Empty: len(series) == 0}
	if c.Empty {
		return c
	}
	const left, right, top, bottom = 12.0, 628.0, 16.0, 212.0
	lo, hi := math.Min(g.StartValue, g.TargetValue), math.Max(g.StartValue, g.TargetValue)
	for _, p := range series {
		lo, hi = math.Min(lo, p.v), math.Max(hi, p.v)
	}
	pad := (hi - lo) * 0.08
	if pad == 0 {
		pad = 1
	}
	lo, hi = lo-pad, hi+pad
	t0, t1 := series[0].at, series[len(series)-1].at
	span := t1.Sub(t0).Seconds()
	xOf := func(t time.Time) float64 {
		if span <= 0 {
			return (left + right) / 2
		}
		return left + (right-left)*t.Sub(t0).Seconds()/span
	}
	yOf := func(v float64) float64 { return top + (bottom-top)*(hi-v)/(hi-lo) }
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) } // ponto decimal: é coordenada, não texto
	var line []string
	for _, p := range series {
		x, y := xOf(p.at), yOf(p.v)
		line = append(line, f(x)+","+f(y))
		c.Points = append(c.Points, views.ChartPoint{X: f(x), Y: f(y), Title: shortDate(ctx, p.at.In(loc)) + ": " + withUnit(ctx, p.v, g.Unit)})
	}
	c.Polyline = strings.Join(line, " ")
	if len(series) > 1 {
		c.Area = c.Polyline + " " + f(xOf(t1)) + "," + f(bottom) + " " + f(xOf(t0)) + "," + f(bottom)
	}
	c.PlotLeft, c.PlotRight, c.PlotTop, c.PlotBottom = f(left), f(right), f(top), f(bottom)
	c.TargetY, c.StartY = f(yOf(g.TargetValue)), f(yOf(g.StartValue))
	c.TargetLabel = i18n.T(ctx, "alvo") + " " + withUnit(ctx, g.TargetValue, g.Unit)
	c.StartLabel = i18n.T(ctx, "início") + " " + withUnit(ctx, g.StartValue, g.Unit)
	c.XFirst, c.XLast = shortDate(ctx, t0.In(loc)), shortDate(ctx, t1.In(loc))
	c.Summary = i18n.Tf(ctx, "Histórico de progresso: de %s a %s em %d registros; alvo %s.", withUnit(ctx, series[0].v, g.Unit), withUnit(ctx, series[len(series)-1].v, g.Unit), len(g.Updates), withUnit(ctx, g.TargetValue, g.Unit))
	return c
}

func (s *server) buildGoalVM(r *http.Request, co domain.Company, g service.GoalView) (views.GoalVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	loc, now := loadLoc(co.Timezone), s.now()
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return views.GoalVM{}, err
	}
	names := s.lookup(ctx, personas, nil)
	self := base + "/metas/" + g.Slug
	vm := views.GoalVM{Title: g.Title, Description: renderMD(g.Description), Badge: goalBadge(ctx, g.Status), Status: string(g.Status),
		Progress: percentInt(g.ProgressPercent), ProgressTxt: goalProgressTxt(g), Metric: g.Metric,
		Start: withUnit(ctx, g.StartValue, g.Unit), Current: withUnit(ctx, g.CurrentValue, g.Unit), Target: withUnit(ctx, g.TargetValue, g.Unit),
		Overdue: goalOverdue(g.Goal, now, loc), TasksTotal: g.TasksTotal, TasksHref: base + "/tasks?aba=todas&meta=" + url.QueryEscape(g.Slug),
		Chart: goalChart(ctx, g, loc), CanProgress: g.Status == domain.GoalActive, ProgressURL: self + "/progresso", StatusURL: self + "/status",
		LinkURL: self + "/tasks", ListHref: base + "/metas", Notice: i18n.T(ctx, goalNotices[r.URL.Query().Get("ok")])}
	if g.Description == "" {
		vm.Description = ""
	}
	if g.DueDate != nil {
		vm.Due = dueText(ctx, *g.DueDate)
	}
	if g.ProjectSlug != "" {
		vm.Project, vm.ProjectHref = g.ProjectName, base+"/projetos/"+g.ProjectSlug
	}
	if g.OwnerSlug != "" {
		vm.Owner, vm.OwnerHref = g.OwnerName, base+"/personas/"+g.OwnerSlug
	}
	if g.AchievedAt != nil {
		vm.Achieved = fmtDateTime(ctx, *g.AchievedAt, loc)
	}
	if g.Status == domain.GoalActive {
		vm.StatusTo, vm.StatusLabel = string(domain.GoalAbandoned), i18n.T(ctx, "Abandonar meta")
	} else {
		vm.StatusTo, vm.StatusLabel = string(domain.GoalActive), i18n.T(ctx, "Reabrir meta")
	}
	for _, st := range domain.TaskStatuses {
		tab := statusTab[st]
		if tab == "" {
			tab = "ativas"
		}
		vm.Statuses = append(vm.Statuses, views.StatusCount{Label: taskStatusLabel(ctx, st), Count: g.Tasks[st],
			Href: base + "/tasks?aba=" + tab + "&meta=" + url.QueryEscape(g.Slug)})
	}
	tasks, err := s.be.ListTasks(ctx, co.ID, service.TaskFilter{GoalID: &g.ID, Limit: 12})
	if err != nil {
		return views.GoalVM{}, err
	}
	for _, tv := range tasks {
		vm.Tasks = append(vm.Tasks, taskRow(ctx, tv, base, now, loc))
	}
	for i := len(g.Updates) - 1; i >= 0; i-- { // do mais novo para o mais antigo
		u := g.Updates[i]
		vm.Updates = append(vm.Updates, views.GoalUpdateRow{Value: withUnit(ctx, u.Value, g.Unit), Note: u.Note, Author: names.actorName(u.Author),
			Pct: fmt.Sprintf("%d%%", int(math.Round(domain.GoalProgress(g.StartValue, g.TargetValue, u.Value)*100))), When: fmtDateTime(ctx, u.CreatedAt, loc)})
	}
	form := views.GoalFormVM{Action: self, CancelHref: base + "/metas", Edit: true, FieldErr: map[string]string{}, Title: g.Title, Description: g.Description,
		Metric: g.Metric, Unit: g.Unit, Start: fmtNum(ctx, g.StartValue), Target: fmtNum(ctx, g.TargetValue),
		ProjectOptions: s.taskProjectOptions(r, co, g.ProjectID), OwnerOptions: s.goalOwnerOptions(r, co, g.OwnerPersonaID)}
	if g.DueDate != nil {
		form.Due = g.DueDate.Format("2006-01-02")
	}
	vm.Form = form
	return vm, nil
}

func (s *server) renderGoal(w http.ResponseWriter, r *http.Request, co domain.Company, status int, g service.GoalView, vm views.GoalVM) {
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "metas", g.Title, views.Crumb{Label: i18n.T(r.Context(), "Metas"), Href: base + "/metas"}, views.Crumb{Label: g.Title})
	s.render(w, r, status, views.GoalPage(sh, vm))
}

func (s *server) handleGoal(w http.ResponseWriter, r *http.Request, co domain.Company) {
	g, err := s.be.GetGoalView(r.Context(), co.ID, r.PathValue("goal"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.buildGoalVM(r, co, g)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderGoal(w, r, co, http.StatusOK, g, vm)
}

func (s *server) handleUpdateGoal(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetGoalView(ctx, co.ID, r.PathValue("goal"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.buildGoalVM(r, co, g)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	goalFormValues(r, &vm.Form)
	vm.Notice = ""
	bad := func(err error) {
		goalFormError(ctx, &vm.Form, err)
		vm.Error, vm.Form.Error = vm.Form.Error, "" // o aviso vai no topo: quem não edita não vê o formulário
		s.renderGoal(w, r, co, statusFor(err), g, vm)
	}
	f := vm.Form
	in, err := parseGoalForm(f, trimForm(r, "project"), trimForm(r, "owner"))
	if err != nil {
		bad(err)
		return
	}
	up := service.UpdateGoalInput{Title: &f.Title, Description: &f.Description, Metric: &f.Metric, Unit: &f.Unit, Start: &in.start, Target: &in.target,
		ProjectID: in.project, ClearProject: in.project == nil, DueDate: in.due, ClearDue: in.due == nil, OwnerPersonaID: in.owner, ClearOwner: in.owner == nil}
	if _, err := s.be.UpdateGoal(ctx, actorFrom(ctx), co.ID, g.ID, up); err != nil {
		bad(err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/metas/"+g.Slug+"?ok=salva")
}

// handleGoalProgress registra o valor atual; ao cruzar o alvo a meta é atingida e a página avisa.
func (s *server) handleGoalProgress(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetGoalView(ctx, co.ID, r.PathValue("goal"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	v, ok := parseNum(r.PostFormValue("value"))
	if !ok {
		s.fail(w, r, &co, domain.Invalid("value", i18n.T(ctx, "informe um número, como 25 ou 25,5")))
		return
	}
	res, err := s.be.RecordGoalProgress(ctx, actorFrom(ctx), co.ID, g.ID, v, trimForm(r, "note"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	ok2 := "progresso"
	if res.Status == domain.GoalAchieved {
		ok2 = "atingida"
	}
	redirect(w, r, "/c/"+co.Slug+"/metas/"+g.Slug+"?ok="+ok2)
}

// handleGoalStatus abandona ou reabre (admin). "Atingida" não se escolhe: acontece ao cruzar o alvo.
func (s *server) handleGoalStatus(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetGoalView(ctx, co.ID, r.PathValue("goal"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var st domain.GoalStatus
	var ok string
	switch r.PostFormValue("to") {
	case string(domain.GoalAbandoned):
		st, ok = domain.GoalAbandoned, "abandonada"
	case string(domain.GoalActive):
		st, ok = domain.GoalActive, "reaberta"
	default:
		s.fail(w, r, &co, domain.Invalid("status", i18n.T(ctx, "valor desconhecido")))
		return
	}
	if _, err := s.be.UpdateGoal(ctx, actorFrom(ctx), co.ID, g.ID, service.UpdateGoalInput{Status: &st}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/metas/"+g.Slug+"?ok="+ok)
}

// handleGoalLinkTask liga uma task (pela chave) à meta.
func (s *server) handleGoalLinkTask(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetGoalView(ctx, co.ID, r.PathValue("goal"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	t, err := s.be.GetTask(ctx, co.ID, trimForm(r, "task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := s.be.UpdateTask(ctx, actorFrom(ctx), co.ID, t.ID, service.UpdateTaskInput{GoalID: &g.ID}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/metas/"+g.Slug+"?ok=task-ligada")
}

// taskGoalOptions são as opções do campo meta de uma task: "Nenhuma", as metas ativas e a da task, mesmo que já
// atingida ou abandonada (para o formulário não desligá-la sem querer).
func (s *server) taskGoalOptions(r *http.Request, co domain.Company, sel *domain.ID) []views.Option {
	ctx := r.Context()
	goals, _ := s.be.ListGoalViews(ctx, co.ID, service.GoalFilter{}) // opcional: a task funciona sem metas
	out := []views.Option{{Value: "", Label: i18n.T(ctx, "Nenhuma"), Selected: sel == nil}}
	for _, g := range goals {
		cur := sel != nil && *sel == g.ID
		if g.Status != domain.GoalActive && !cur {
			continue
		}
		label := g.Title
		switch g.Status {
		case domain.GoalAchieved:
			label += " (" + i18n.T(ctx, "atingida") + ")"
		case domain.GoalAbandoned:
			label += " (" + i18n.T(ctx, "abandonada") + ")"
		}
		out = append(out, views.Option{Value: g.ID.String(), Label: label, Selected: cur, Slug: g.Slug})
	}
	return out
}
