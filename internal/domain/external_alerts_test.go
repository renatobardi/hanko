package domain

import "testing"

func TestExternalAlertKinds(t *testing.T) {
	if len(ExternalAlertKinds) != 17 {
		t.Fatalf("tipos externos = %d, quer 17", len(ExternalAlertKinds))
	}
	crit := 0
	for _, k := range ExternalAlertKinds {
		if !k.Valid() || !k.IsExternal() {
			t.Errorf("%s deveria ser válido e externo", k)
		}
		if ExternalAlertSeverity(k) == AlertCritical {
			crit++
		}
	}
	if crit != 4 {
		t.Errorf("tipos critical = %d, quer 4 (host_no_data, destination_refusing, spool, quota)", crit)
	}
	for _, k := range AlertKinds {
		if k.IsExternal() {
			t.Errorf("%s é do Hanko, não externo", k)
		}
	}
	if AlertKind("price_fixed_differs").Valid() {
		t.Error("price_fixed_differs virou price_locked_differs (ADR-0083 item 1)")
	}
}

func TestAlertSubjectID(t *testing.T) {
	co, other := NewID(), NewID()
	a := AlertSubjectID(co, AlertPriceChanged, "gpt-6-luna")
	if a != AlertSubjectID(co, AlertPriceChanged, "gpt-6-luna") {
		t.Error("a mesma entrada deve dar o mesmo id")
	}
	for name, b := range map[string]ID{
		"modelo":           AlertSubjectID(co, AlertPriceChanged, "gpt-6-sol"),
		"tipo":             AlertSubjectID(co, AlertPriceModelUnpriced, "gpt-6-luna"),
		"empresa":          AlertSubjectID(other, AlertPriceChanged, "gpt-6-luna"),
		"dimensões juntas": AlertSubjectID(co, AlertPriceChanged, "gpt-6", "-luna"),
	} {
		if a == b {
			t.Errorf("%s diferente deveria dar outro id", name)
		}
	}
}

func TestAlertSettings(t *testing.T) {
	if len(AlertLimitSpecs) != 14 {
		t.Errorf("limites = %d, quer 14", len(AlertLimitSpecs))
	}
	seen := map[string]bool{}
	for _, s := range append(append([]AlertSettingSpec{}, AlertLimitSpecs...), AlertDueSpecs()...) {
		if seen[s.Key] {
			t.Errorf("chave repetida: %s", s.Key)
		}
		seen[s.Key] = true
		if s.Default < s.Min || s.Default > s.Max {
			t.Errorf("%s: o padrão %g está fora da faixa %g a %g", s.Key, s.Default, s.Min, s.Max)
		}
	}
	// 22 tipos (5 do Hanko e 17 externos) mais a pergunta da rodada.
	if n := len(AlertDueSpecs()); n != 23 {
		t.Errorf("prazos = %d, quer 23", n)
	}
	if AlertDueDefaultHours(AlertQueue) != 24 || AlertDueDefaultHours(AlertRunStuck) != 0 {
		t.Error("prazo padrão: 24 h para externo, sem prazo para os do Hanko")
	}
	for _, c := range []struct {
		key string
		v   float64
		ok  bool
	}{
		{"limit.host_no_data_minutes", 45, true},
		{"limit.host_no_data_minutes", 0, false},
		{"limit.queue_max_ratio", 1.5, false},
		{"due.queue", 0, true},
		{"due.queue", 720, true},
		{"due.queue", 721, false},
		{"due.price_fixed_differs", 1, false},
		{"limit.unknown", 1, false},
	} {
		if err := ValidateAlertSetting(c.key, c.v); (err == nil) != c.ok {
			t.Errorf("ValidateAlertSetting(%q, %g) = %v, quer ok=%v", c.key, c.v, err, c.ok)
		}
	}
}

func TestRunCostClass(t *testing.T) {
	for _, c := range []struct {
		src  CostSource
		subs bool
		want CallCostClass
	}{
		{CostReported, false, CallCostReal}, {CostReported, true, CallCostReal},
		{CostEstimated, true, CallCostListed}, {CostEstimated, false, CallCostEstimated},
		{CostNone, true, CallCostUnpriced}, {CostNone, false, CallCostUnpriced}, {"", true, CallCostUnpriced},
	} {
		if got := RunCostClass(c.src, c.subs); got != c.want {
			t.Errorf("RunCostClass(%q, %v) = %q, quer %q", c.src, c.subs, got, c.want)
		}
	}
}
