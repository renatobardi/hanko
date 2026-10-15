// Command perf-explain mede as consultas quentes do Hanko com EXPLAIN (ANALYZE, BUFFERS) num banco semeado por
// tools/seed-load. O SQL vem das constantes geradas pelo sqlc em internal/store (o mesmo texto que o app executa),
// com parâmetros reais ligados pelo protocolo estendido (plano customizado, como nas primeiras execuções do app).
//
//	go run ./tools/perf-explain -db URL -json antes.json -plans antes-planos.txt
//	go run ./tools/perf-explain -db URL -json depois.json
//	go run ./tools/perf-explain -merge antes.json,depois.json     # tabela markdown
//
// Cada medição roda numa transação que é sempre revertida (escritas não alteram o banco).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type result struct {
	Group   string  `json:"group"`
	Name    string  `json:"name"`
	Query   string  `json:"query"`
	ExecMS  float64 `json:"exec_ms"`
	PlanMS  float64 `json:"plan_ms"`
	Buffers int64   `json:"buffers"` // shared hit + read do nó raiz
	Rows    int64   `json:"rows"`
	TopNode string  `json:"top_node"`
	Plan    string  `json:"-"`
}

type tcase struct {
	group, name, query string
	args               []any
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "perf-explain:", err)
		os.Exit(1)
	}
}

func run() error {
	dbURL := flag.String("db", os.Getenv("HANKO_TEST_DATABASE_URL"), "URL do Postgres")
	storeDir := flag.String("store", "internal/store", "diretório com os *.sql.go do sqlc")
	jsonOut := flag.String("json", "", "grava os resultados em JSON")
	plansOut := flag.String("plans", "", "grava os planos completos (texto)")
	only := flag.String("only", "", "filtra casos por substring de grupo/nome")
	reps := flag.Int("reps", 3, "repetições medidas (mediana); a primeira, de aquecimento, é descartada")
	merge := flag.String("merge", "", "antes.json,depois.json: imprime a tabela markdown e sai")
	flag.Parse()
	if *merge != "" {
		return mergeTables(*merge)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	sqls, err := loadSQL(*storeDir)
	if err != nil {
		return err
	}
	cases, err := buildCases(ctx, conn)
	if err != nil {
		return err
	}
	var results []result
	var plans strings.Builder
	for _, c := range cases {
		if *only != "" && !strings.Contains(c.group+"/"+c.name, *only) {
			continue
		}
		sql, ok := sqls[c.query]
		if !ok {
			return fmt.Errorf("consulta %q não encontrada em %s", c.query, *storeDir)
		}
		r, err := measure(ctx, conn, c, sql, *reps)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", c.group, c.name, err)
		}
		results = append(results, r)
		fmt.Printf("%-10s %-34s %10.2f ms  plan %6.2f ms  buffers %9d  %s\n", c.group, c.name, r.ExecMS, r.PlanMS, r.Buffers, r.TopNode)
		fmt.Fprintf(&plans, "==== %s / %s (%s)\n%s\n", c.group, c.name, c.query, r.Plan)
	}
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		if err := os.WriteFile(*jsonOut, b, 0o600); err != nil {
			return err
		}
	}
	if *plansOut != "" {
		if err := os.WriteFile(*plansOut, []byte(plans.String()), 0o600); err != nil {
			return err
		}
	}
	return nil
}

var constRe = regexp.MustCompile("(?s)const ([a-zA-Z0-9]+) = `(.*?)`")

func loadSQL(dir string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql.go"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for _, m := range constRe.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = m[2]
		}
	}
	return out, nil
}

var (
	execRe = regexp.MustCompile(`Execution Time: ([0-9.]+) ms`)
	planRe = regexp.MustCompile(`Planning Time: ([0-9.]+) ms`)
	bufRe  = regexp.MustCompile(`Buffers: shared (.*)`)
	hitRe  = regexp.MustCompile(`(hit|read)=([0-9]+)`)
	rowsRe = regexp.MustCompile(`actual time=[0-9.]+\.\.[0-9.]+ rows=([0-9]+)`)
	nodeRe = regexp.MustCompile(`^\s*(?:->\s*)?([A-Za-z][A-Za-z ]+?)(?: using| on|\s+\(|$)`)
	tidyRe = regexp.MustCompile(`\s+`)
)

func measure(ctx context.Context, conn *pgx.Conn, c tcase, sql string, reps int) (result, error) {
	res := result{Group: c.group, Name: c.name, Query: c.query}
	var execs []float64
	var lastPlan string
	for i := 0; i <= reps; i++ {
		// Tudo roda numa transação revertida: UPDATE/INSERT medidos não alteram o banco semeado.
		tx, err := conn.Begin(ctx)
		if err != nil {
			return res, err
		}
		args := c.args
		if c.query == "appendRunEvent" { // a PK do evento não pode repetir entre as repetições
			args = append([]any{uuid.New()}, c.args[1:]...)
		}
		rows, err := tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+sql, args...)
		if err != nil {
			_ = tx.Rollback(ctx)
			return res, err
		}
		var lines []string
		for rows.Next() {
			var l string
			if err := rows.Scan(&l); err != nil {
				rows.Close()
				return res, err
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return res, err
		}
		_ = tx.Rollback(ctx)
		plan := strings.Join(lines, "\n")
		lastPlan = plan
		if i == 0 {
			continue // aquecimento
		}
		if m := execRe.FindStringSubmatch(plan); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			execs = append(execs, v)
		}
		if m := planRe.FindStringSubmatch(plan); m != nil {
			res.PlanMS, _ = strconv.ParseFloat(m[1], 64)
		}
	}
	sort.Float64s(execs)
	if len(execs) > 0 {
		res.ExecMS = execs[len(execs)/2]
	}
	lines := strings.Split(lastPlan, "\n")
	if len(lines) > 0 {
		if m := rowsRe.FindStringSubmatch(lines[0]); m != nil {
			res.Rows, _ = strconv.ParseInt(m[1], 10, 64)
		}
		if m := nodeRe.FindStringSubmatch(lines[0]); m != nil {
			res.TopNode = strings.TrimSpace(m[1])
		}
	}
	for _, l := range lines {
		if m := bufRe.FindStringSubmatch(l); m != nil {
			for _, h := range hitRe.FindAllStringSubmatch(m[1], -1) {
				n, _ := strconv.ParseInt(h[2], 10, 64)
				res.Buffers += n
			}
			break
		}
	}
	res.Plan = lastPlan
	return res, nil
}

func buildCases(ctx context.Context, conn *pgx.Conn) ([]tcase, error) {
	var env struct {
		coA, coB, coC            uuid.UUID
		personaHot, personaSub   uuid.UUID
		taskHot, taskWithRuns    uuid.UUID
		runLong, runMid, runName uuid.UUID
		sub                      uuid.UUID
		runNameTxt               string
		now, midActivity         time.Time
		nowTS                    time.Time
	}
	one := func(dst any, sql string, args ...any) error { return conn.QueryRow(ctx, sql, args...).Scan(dst) }
	for i, d := range []*uuid.UUID{&env.coA, &env.coB, &env.coC} {
		if err := one(d, `SELECT id FROM companies WHERE slug = $1`, "perf-"+string(rune('a'+i))); err != nil {
			return nil, fmt.Errorf("empresa perf-%c (rode tools/seed-load antes): %w", 'a'+i, err)
		}
	}
	qs := []struct {
		dst  any
		sql  string
		args []any
	}{
		{&env.personaHot, `SELECT persona_id FROM runs WHERE company_id = $1 GROUP BY persona_id ORDER BY count(*) DESC LIMIT 1`, []any{env.coA}},
		{&env.personaSub, `SELECT id FROM personas WHERE company_id = $1 AND subscription_id IS NOT NULL AND heartbeat_interval_sec > 0 ORDER BY slug LIMIT 1`, []any{env.coA}},
		{&env.taskHot, `SELECT task_id FROM runs WHERE company_id = $1 GROUP BY task_id ORDER BY count(*) DESC, task_id LIMIT 1`, []any{env.coA}},
		{&env.runLong, `SELECT id FROM runs WHERE company_id = $1 ORDER BY next_event_seq DESC, id LIMIT 1`, []any{env.coA}},
		{&env.runMid, `SELECT id FROM runs WHERE company_id = $1 AND status = 'done' ORDER BY id OFFSET (SELECT count(*) / 3 FROM runs WHERE company_id = $1 AND status = 'done') LIMIT 1`, []any{env.coA}},
		{&env.sub, `SELECT id FROM subscriptions WHERE company_id = $1`, []any{env.coA}},
		{&env.runNameTxt, `SELECT name FROM runs WHERE company_id = $1 ORDER BY id OFFSET (SELECT count(*) / 2 FROM runs WHERE company_id = $1) LIMIT 1`, []any{env.coA}},
		{&env.nowTS, `SELECT max(created_at) + interval '1 hour' FROM runs`, nil},
		{&env.midActivity, `SELECT occurred_at FROM activity_events WHERE company_id = $1 ORDER BY occurred_at DESC OFFSET (SELECT count(*) / 10 FROM activity_events WHERE company_id = $1) LIMIT 1`, []any{env.coA}},
	}
	for _, q := range qs {
		if err := one(q.dst, q.sql, q.args...); err != nil {
			return nil, fmt.Errorf("%s: %w", q.sql, err)
		}
	}
	now := env.nowTS.UTC()
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	ln := now.In(loc)
	monthFrom := time.Date(ln.Year(), ln.Month(), 1, 0, 0, 0, 0, loc).UTC()
	monthTo := monthFrom.AddDate(0, 1, 0)
	weekFrom := now.AddDate(0, 0, -7)
	ptr := func(u uuid.UUID) *uuid.UUID { return &u }
	str := func(s string) *string { return &s }
	co := env.coA
	var noS []string
	var noT *time.Time
	runCases := func(group, name, q string, args ...any) tcase {
		return tcase{group: group, name: name, query: q, args: args}
	}
	cs := []tcase{
		// Listagens paginadas
		runCases("lista", "tasks pág.1", "listTaskViews", co, noS, (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*string)(nil), int32(0), int32(100)),
		runCases("lista", "tasks status todo/in_progress", "listTaskViews", co, []string{"todo", "in_progress"}, (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*string)(nil), int32(0), int32(100)),
		runCases("lista", "tasks por persona", "listTaskViews", co, noS, ptr(env.personaHot), (*uuid.UUID)(nil), (*string)(nil), int32(0), int32(100)),
		runCases("lista", "tasks offset 20000", "listTaskViews", co, noS, (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*string)(nil), int32(20000), int32(100)),
		runCases("lista", "tasks busca q", "listTaskViews", co, noS, (*uuid.UUID)(nil), (*uuid.UUID)(nil), str("migrar módulo 42"), int32(0), int32(100)),
		runCases("lista", "runs pág.1", "listRunViews", co, (*uuid.UUID)(nil), (*uuid.UUID)(nil), noS, (*string)(nil), int32(50)),
		runCases("lista", "runs por persona", "listRunViews", co, (*uuid.UUID)(nil), ptr(env.personaHot), noS, (*string)(nil), int32(50)),
		runCases("lista", "runs por task", "listRunViews", co, ptr(env.taskHot), (*uuid.UUID)(nil), noS, (*string)(nil), int32(50)),
		runCases("lista", "runs status running", "listRunViews", co, (*uuid.UUID)(nil), (*uuid.UUID)(nil), []string{"running"}, (*string)(nil), int32(50)),
		runCases("lista", "runs status failed", "listRunViews", co, (*uuid.UUID)(nil), (*uuid.UUID)(nil), []string{"failed"}, (*string)(nil), int32(50)),
		runCases("lista", "runs por nome", "listRunViews", co, (*uuid.UUID)(nil), (*uuid.UUID)(nil), noS, str(env.runNameTxt), int32(50)),
		runCases("lista", "activity pág.1", "listActivity", co, (*string)(nil), (*uuid.UUID)(nil), noT, int32(100)),
		runCases("lista", "activity before (fundo)", "listActivity", co, (*string)(nil), (*uuid.UUID)(nil), &env.midActivity, int32(100)),
		runCases("lista", "activity por task", "listActivity", co, str("task"), ptr(env.taskHot), noT, int32(50)),
		runCases("lista", "activity painel (5)", "listActivity", co, (*string)(nil), (*uuid.UUID)(nil), noT, int32(5)),
		runCases("lista", "gates pendentes", "listGateViews", co, []string{"pending"}, noS, (*uuid.UUID)(nil), (*uuid.UUID)(nil), int32(0), int32(100)),
		runCases("lista", "gates todos pág.1", "listGateViews", co, noS, noS, (*uuid.UUID)(nil), (*uuid.UUID)(nil), int32(0), int32(100)),
		runCases("lista", "gates offset 3000", "listGateViews", co, noS, noS, (*uuid.UUID)(nil), (*uuid.UUID)(nil), int32(3000), int32(100)),
		runCases("lista", "gates por task", "listGateViews", co, noS, noS, ptr(env.taskHot), (*uuid.UUID)(nil), int32(0), int32(100)),
		// Caixa de entrada
		runCases("inbox", "gates pendentes (count)", "countPendingGates", co),
		runCases("inbox", "runs falhas", "listFailedRuns", co, now.Add(-7*24*time.Hour), int32(50)),
		runCases("inbox", "personas em erro", "listErrorPersonas", co),
		runCases("inbox", "revisões abertas", "listOpenReviewsForCompany", co),
		runCases("inbox", "alertas abertos", "listOpenAlertsForCompany", co, int32(100)),
		// Painel
		runCases("painel", "contagens", "dashboardCounts", co),
		runCases("painel", "custo do mês", "monthCost", co, monthFrom, monthTo),
		// Custos
		runCases("custos", "breakdown day", "costBreakdown", "day", "America/Sao_Paulo", co, monthFrom, monthTo),
		runCases("custos", "breakdown persona", "costBreakdown", "persona", "America/Sao_Paulo", co, monthFrom, monthTo),
		runCases("custos", "breakdown project", "costBreakdown", "project", "America/Sao_Paulo", co, monthFrom, monthTo),
		runCases("custos", "breakdown model", "costBreakdown", "model", "America/Sao_Paulo", co, monthFrom, monthTo),
		runCases("custos", "breakdown 90 dias", "costBreakdown", "day", "America/Sao_Paulo", co, now.AddDate(0, 0, -90), now),
		runCases("custos", "rateio do pago (runs)", "subscriptionRunsInPeriod", "America/Sao_Paulo", co, monthFrom, monthTo),
		runCases("custos", "períodos de assinatura", "listSubscriptionPeriods", co),
		runCases("custos", "gasto empresa no mês", "spendBetween", co, (*uuid.UUID)(nil), monthFrom, monthTo),
		runCases("custos", "gasto persona no mês", "spendBetween", co, ptr(env.personaHot), monthFrom, monthTo),
		runCases("custos", "trava semanal (uso)", "subscriptionWeekUsage", ptr(env.sub), weekFrom, now),
		runCases("custos", "assinatura tem runs", "subscriptionHasRuns", env.sub),
		runCases("custos", "reservas runs ativas", "activeRunReserves", 1.0, co),
		runCases("custos", "reserva de uma persona", "personaRunReserve", env.personaHot, 1.0),
		runCases("custos", "runs em execução", "listRunningRunIDs", co, (*uuid.UUID)(nil)),
		// Heartbeat
		runCases("heartbeat", "wake candidates", "listWakeCandidates", now.Add(-10*time.Minute), now, now.Add(-time.Minute), int32(50)),
		runCases("heartbeat", "tasks trabalháveis persona", "listWorkableTasksForPersona", ptr(env.personaSub)),
		runCases("heartbeat", "gate pendente na task", "hasPendingGateForTask", ptr(env.taskHot)),
		runCases("heartbeat", "persona após run", "setPersonaAfterRun", "ok", int32(3), "", now, env.personaHot),
		runCases("heartbeat", "persona aguarda gate", "setPersonaAwaitingGate", now, env.personaHot, env.taskHot),
		runCases("heartbeat", "persona sai do gate", "releasePersonaFromGate", now, env.personaHot),
		// alert_tick
		runCases("alert", "personas silenciosas", "listSilentPersonas", 3.0, now),
		runCases("alert", "runs travadas", "listStuckRuns", now.Add(-30*time.Minute)),
		runCases("alert", "personas com orçamento", "listBudgetedPersonas"),
		runCases("alert", "alertas abertos (todos)", "listOpenAlerts"),
		runCases("alert", "revisões obsoletas", "listStaleReviews", now.Add(-time.Hour), int32(50)),
		// SSE / eventos
		runCases("eventos", "catch-up seq>0 (200)", "listRunEvents", co, env.runLong, int64(0), int32(200)),
		runCases("eventos", "catch-up seq>20 (200)", "listRunEvents", co, env.runMid, int64(20), int32(200)),
		runCases("eventos", "append evento", "appendRunEvent", uuid.New(), "assistant", "teste", []byte(nil), now, now, co, env.runLong),
		// Export
		runCases("export", "página de eventos", "exportRunEventsPage", env.runLong, int64(10), int32(500)),
		runCases("export", "tasks da empresa", "exportTasks", co),
		runCases("export", "runs da empresa", "exportRuns", co),
		runCases("export", "comentários da empresa", "exportComments", co),
		// Boot
		runCases("boot", "runs sem nome", "listRunsWithoutName", int32(500)),
		runCases("boot", "nomes já usados", "takenRunNames", co, []string{"Run_1", "Run_2", "Run_3", "Zzz_Nenhum", env.runNameTxt}),
		runCases("boot", "interrompe runs ativas", "interruptActiveRuns", &now),
	}
	return cs, nil
}

type pair struct {
	Before, After *result
}

func mergeTables(spec string) error {
	parts := strings.Split(spec, ",")
	if len(parts) != 2 {
		return fmt.Errorf("-merge espera antes.json,depois.json")
	}
	load := func(p string) ([]result, error) {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r []result
		return r, json.Unmarshal(b, &r)
	}
	b, err := load(parts[0])
	if err != nil {
		return err
	}
	a, err := load(parts[1])
	if err != nil {
		return err
	}
	idx := map[string]*pair{}
	var order []string
	for i := range b {
		k := b[i].Group + "/" + b[i].Name
		idx[k] = &pair{Before: &b[i]}
		order = append(order, k)
	}
	for i := range a {
		k := a[i].Group + "/" + a[i].Name
		if p, ok := idx[k]; ok {
			p.After = &a[i]
		}
	}
	fmt.Println("| Grupo | Consulta | Antes (ms) | Depois (ms) | Ganho | Buffers antes → depois | Nó raiz antes → depois |")
	fmt.Println("|---|---|---:|---:|---:|---|---|")
	for _, k := range order {
		p := idx[k]
		if p.After == nil {
			continue
		}
		gain := "—"
		if p.After.ExecMS > 0 && p.Before.ExecMS/p.After.ExecMS >= 1.2 {
			gain = fmt.Sprintf("%.0f×", p.Before.ExecMS/p.After.ExecMS)
			if p.Before.ExecMS/p.After.ExecMS < 10 {
				gain = fmt.Sprintf("%.1f×", p.Before.ExecMS/p.After.ExecMS)
			}
		}
		fmt.Printf("| %s | %s | %.2f | %.2f | %s | %d → %d | %s → %s |\n", p.Before.Group, p.Before.Name, p.Before.ExecMS, p.After.ExecMS, gain,
			p.Before.Buffers, p.After.Buffers, tidyRe.ReplaceAllString(p.Before.TopNode, " "), tidyRe.ReplaceAllString(p.After.TopNode, " "))
	}
	return nil
}
