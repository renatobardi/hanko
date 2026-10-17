package service

import (
	"math/rand"
	"testing"
	"testing/quick"
	"time"
)

// Testes de propriedade do motor de orçamento (#56): funções puras (budgetStatus, monthWindow, fillDays,
// sortRowsByCost) exercitadas com entradas aleatórias de semente fixa, para a falha ser reproduzível.

const propertySeed = 20261017

func quickCfg() *quick.Config {
	return &quick.Config{MaxCount: 2000, Rand: rand.New(rand.NewSource(propertySeed))}
}

func stateRank(s string) int {
	switch s {
	case BudgetNone:
		return 0
	case BudgetOK:
		return 1
	case BudgetWarning:
		return 2
	case BudgetExceeded:
		return 3
	}
	return -1
}

// cents/gasto limitados a faixas realistas (centavos até ~10 mil dólares; gasto até ~20 mil).
func bound(b uint32, mod uint32) uint32 { return b % mod }

func TestBudgetStatusNoCeilingIsNone(t *testing.T) {
	f := func(spent uint32) bool {
		st := budgetStatus(nil, float64(spent)/100)
		return st.State == BudgetNone && st.Percent == 0
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// Sem teto positivo, qualquer gasto (inclusive zero) já está estourado: orçamento 0 = bloqueio.
func TestBudgetStatusNonPositiveCeilingExceeded(t *testing.T) {
	f := func(neg int32, spent uint32) bool {
		b := -int64(bound(uint32(neg), 1<<20))
		return budgetStatus(&b, float64(spent)/100).State == BudgetExceeded
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// Os limiares são exatos: estourado em gasto >= teto; aviso em [80%, 100%); ok abaixo; percent = gasto/teto.
func TestBudgetStatusThresholds(t *testing.T) {
	f := func(rawBudget, rawSpent uint32) bool {
		budget := int64(bound(rawBudget, 1_000_000)) + 1 // 1 cent..10k USD
		spent := float64(bound(rawSpent, 2_000_000)) / 100
		st := budgetStatus(&budget, spent)
		cents := spent * 100
		var want string
		switch {
		case cents >= float64(budget):
			want = BudgetExceeded
		case cents >= budgetWarnRatio*float64(budget):
			want = BudgetWarning
		default:
			want = BudgetOK
		}
		wantPct := cents / float64(budget) * 100
		return st.State == want && st.Percent == wantPct && st.BudgetCents == &budget && st.SpentUSD == spent
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// Gastar mais nunca melhora o estado; um teto maior nunca piora.
func TestBudgetStatusMonotonic(t *testing.T) {
	f := func(rawBudget, rawSpent, rawExtra, rawRaise uint32) bool {
		budget := int64(bound(rawBudget, 1_000_000)) + 1
		spent := float64(bound(rawSpent, 2_000_000)) / 100
		more := spent + float64(bound(rawExtra, 500_000))/100
		bigger := budget + int64(bound(rawRaise, 1_000_000))
		base := stateRank(budgetStatus(&budget, spent).State)
		if stateRank(budgetStatus(&budget, more).State) < base {
			return false
		}
		return stateRank(budgetStatus(&bigger, spent).State) <= base
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// BudgetFor (usado por prévias e testes) é exatamente o motor do service.
func TestBudgetForMirrorsEngine(t *testing.T) {
	r := rand.New(rand.NewSource(propertySeed))
	for i := 0; i < 500; i++ {
		b := int64(r.Intn(100_000))
		s := r.Float64() * 1500
		if BudgetFor(&b, s) != budgetStatus(&b, s) {
			t.Fatalf("BudgetFor difere para budget=%d spent=%v", b, s)
		}
	}
}

var propZones = []string{"UTC", "America/Sao_Paulo", "Asia/Tokyo", "Pacific/Auckland", "America/Los_Angeles", "Europe/Lisbon", "Nao/Existe"}

// A janela do mês contém o instante, é contígua ao mês seguinte e dura 28 a 31 dias no fuso (em horário de
// parede), qualquer que seja o fuso — inclusive nome inválido, que cai em UTC.
func TestMonthWindowContainsNow(t *testing.T) {
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	f := func(sec uint32, zi uint8) bool {
		tz := propZones[int(zi)%len(propZones)]
		now := base.Add(time.Duration(sec%100_000_000) * 10 * time.Second) // até ~31 anos
		from, to, key := monthWindow(tz, now)
		if now.Before(from) || !now.Before(to) {
			return false
		}
		loc := loadLocation(tz)
		lf, lt := from.In(loc), to.In(loc)
		if lf.Day() != 1 || lt.Day() != 1 || lf.Hour() != 0 || lt.Hour() != 0 {
			return false
		}
		days := int(lt.Sub(lf).Round(24*time.Hour) / (24 * time.Hour))
		return days >= 28 && days <= 31 && key == lf.Format("2006-01") && key == now.In(loc).Format("2006-01")
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}

// fillDays devolve um dia por data local de [from, to), em ordem, sem duplicar nem perder as linhas dadas.
func TestFillDaysCoversEveryDay(t *testing.T) {
	r := rand.New(rand.NewSource(propertySeed))
	loc := loadLocation("America/Sao_Paulo")
	for i := 0; i < 300; i++ {
		from := time.Date(2026, time.Month(1+r.Intn(12)), 1+r.Intn(28), 0, 0, 0, 0, loc)
		n := 1 + r.Intn(40)
		to := from.AddDate(0, 0, n)
		var rows []CostRow
		have := map[string]float64{}
		for d := 0; d < n; d++ {
			if r.Intn(3) == 0 {
				k := from.AddDate(0, 0, d).Format("2006-01-02")
				c := float64(1 + r.Intn(500))
				rows = append(rows, CostRow{Key: k, Label: k, CostUSD: c})
				have[k] = c
			}
		}
		out := fillDays(rows, from, to, loc)
		if len(out) != n {
			t.Fatalf("len = %d, quer %d (from=%v to=%v)", len(out), n, from, to)
		}
		for d, row := range out {
			k := from.AddDate(0, 0, d).Format("2006-01-02")
			if row.Key != k || row.CostUSD != have[k] {
				t.Fatalf("dia %d: %+v, quer chave %s custo %v", d, row, k, have[k])
			}
		}
	}
}

// sortRowsByCost ordena por custo decrescente e preserva o multiconjunto.
func TestSortRowsByCost(t *testing.T) {
	f := func(costs []uint16) bool {
		rows := make([]CostRow, len(costs))
		sum := 0.0
		for i, c := range costs {
			rows[i] = CostRow{Key: string(rune('a' + i%26)), CostUSD: float64(c)}
			sum += float64(c)
		}
		sortRowsByCost(rows)
		got := 0.0
		for i, row := range rows {
			got += row.CostUSD
			if i > 0 && rows[i-1].CostUSD < row.CostUSD {
				return false
			}
		}
		return got == sum
	}
	if err := quick.Check(f, quickCfg()); err != nil {
		t.Fatal(err)
	}
}
