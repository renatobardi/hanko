package service

import "testing"

func TestRunAndChainStart(t *testing.T) {
	ts := []int64{1, 2, 3, 4}
	bad := []bool{true, false, true, true}
	if got := runStart(ts, func(i int) bool { return bad[i] }); got != 3 {
		t.Errorf("runStart = %d, quer 3 (a sequência final começa no 3)", got)
	}
	if got := runStart(ts, func(int) bool { return false }); got != 0 {
		t.Errorf("runStart sem ponto ruim = %d, quer 0", got)
	}
	if got := chainStart([]int64{10, 100, 105, 110}, 10); got != 100 {
		t.Errorf("chainStart = %d, quer 100 (o intervalo de 90 quebra a sequência)", got)
	}
}

func TestRisesOfCounter(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	s := func(v int64) *int64 { return &v }
	pts := []mpoint{
		{t: 10, start: s(5), value: f(3)},  // primeiro ponto: o contador começou dentro da janela (start >= lo), sobe 3
		{t: 20, start: s(5), value: f(3)},  // igual: não sobe
		{t: 30, start: s(5), value: f(7)},  // sobe 4
		{t: 40, start: s(5), value: f(2)},  // menor: reinício, sobe o valor inteiro (2)
		{t: 50, start: s(45), value: f(2)}, // start diferente: reinício, sobe 2
	}
	got := risesOf(pts, 0)
	want := []rise{{10, 3}, {30, 4}, {40, 2}, {50, 2}}
	if len(got) != len(want) {
		t.Fatalf("subidas = %v, quer %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("subida %d = %v, quer %v", i, got[i], want[i])
		}
	}
	if r := risesOf(pts[:1], 100); len(r) != 0 {
		t.Errorf("primeiro ponto com o contador começando antes da janela não conta, veio %v", r)
	}
}

func TestQuotaWindowOf(t *testing.T) {
	if got := quotaWindowOf(`{"oute.quota.window": "5h", "x": "y"}`); got != "5h" {
		t.Errorf("janela = %q, quer 5h", got)
	}
	if got := quotaWindowOf(`{"x": "y"}`); got != "" {
		t.Errorf("sem a chave = %q, quer vazio", got)
	}
}
