package domain

import (
	"testing"
	"time"
)

func TestCardPolicyNormalize(t *testing.T) {
	p := CardPolicy{}
	if err := p.Normalize(); err == nil {
		t.Fatal("sem gatilhos deveria falhar")
	}
	p = CardPolicy{Triggers: CardTriggers{Status: true}}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	if p.Mode != CardModeReactive || p.IntervalMinutes != 60 || p.DebounceSeconds != 300 || p.MaxUpdatesPerHour != 6 || p.DailyTokenCap != 100000 {
		t.Fatalf("padrões: %+v", p)
	}
	bad := []CardPolicy{
		{Mode: "x", Triggers: CardTriggers{Status: true}},
		{IntervalMinutes: 10, Triggers: CardTriggers{Status: true}},
		{DebounceSeconds: 4000, Triggers: CardTriggers{Status: true}},
		{MaxUpdatesPerHour: 61, Triggers: CardTriggers{Status: true}},
		{DailyTokenCap: 10, Triggers: CardTriggers{Status: true}},
		{Triggers: CardTriggers{Status: true}, ActiveHours: &ActiveHours{Start: "25:00", End: "10:00"}},
		{Triggers: CardTriggers{Status: true}, ActiveHours: &ActiveHours{Start: "09:00", End: "09:00"}},
		{Triggers: CardTriggers{Status: true}, ActiveHours: &ActiveHours{Start: "09:00", End: "18:00", TZ: "Marte/Olimpo"}},
	}
	for i, b := range bad {
		if err := b.Normalize(); err == nil {
			t.Errorf("%d: deveria falhar: %+v", i, b)
		}
	}
	empty := CardPolicy{Triggers: CardTriggers{Status: true}, ActiveHours: &ActiveHours{}}
	if err := empty.Normalize(); err != nil || empty.ActiveHours != nil {
		t.Fatalf("horário vazio vira nil: %+v %v", empty, err)
	}
}

func TestActiveHours(t *testing.T) {
	day := ActiveHours{Start: "09:00", End: "18:00", TZ: "America/Sao_Paulo"}
	at := func(h, m int) time.Time { return time.Date(2026, 10, 16, h, m, 0, 0, time.UTC) }
	if !day.Contains(at(12, 0)) || day.Contains(at(11, 59)) || !day.Contains(at(20, 59)) || day.Contains(at(21, 0)) {
		t.Fatal("janela diurna em São Paulo (UTC-3)")
	}
	if n := day.NextStart(at(22, 0)); !n.Equal(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("próximo início: %v", n)
	}
	if n := day.NextStart(at(13, 0)); !n.Equal(at(13, 0)) {
		t.Fatalf("dentro da janela: %v", n)
	}
	night := ActiveHours{Start: "22:00", End: "06:00", TZ: "UTC"}
	if !night.Contains(at(23, 0)) || !night.Contains(at(5, 59)) || night.Contains(at(12, 0)) {
		t.Fatal("janela noturna")
	}
	if n := night.NextStart(at(12, 0)); !n.Equal(at(22, 0)) {
		t.Fatalf("próximo início noturno: %v", n)
	}
}

func TestDiffFingerprints(t *testing.T) {
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	old := CardFingerprint{
		"a": {Key: "HNK-1", Status: TaskBlocked, PersonaID: "p1", PersonaName: "Ana", UpdatedAt: t0},
		"b": {Key: "HNK-2", Status: TaskTodo, UpdatedAt: t0},
		"c": {Key: "HNK-3", Status: TaskTodo, UpdatedAt: t0, Title: "x"},
		"d": {Key: "HNK-4", Status: TaskTodo, UpdatedAt: t0},
	}
	cur := CardFingerprint{
		"a": {Key: "HNK-1", Status: TaskInProgress, PersonaID: "p2", PersonaName: "Bia", UpdatedAt: t0.Add(time.Minute)},
		"c": {Key: "HNK-3", Status: TaskTodo, UpdatedAt: t0.Add(time.Minute), Title: "y"},
		"d": {Key: "HNK-4", Status: TaskTodo, UpdatedAt: t0, HumanCommentID: "c1"},
		"e": {Key: "HNK-5", Status: TaskTodo, UpdatedAt: t0},
	}
	all := DiffFingerprints(old, cur, CardTriggers{AnyUpdate: true})
	kinds := map[string]string{}
	for _, c := range all {
		kinds[c.Key+"/"+c.ChangeKind] = c.From + ">" + c.To
	}
	want := []string{"HNK-1/status", "HNK-1/assignee", "HNK-2/left", "HNK-3/update", "HNK-4/human_comment", "HNK-5/entered"}
	if len(all) != len(want) {
		t.Fatalf("mudanças: %+v", all)
	}
	for _, w := range want {
		if _, ok := kinds[w]; !ok {
			t.Errorf("falta %s em %+v", w, kinds)
		}
	}
	if kinds["HNK-1/status"] != "blocked>in_progress" || kinds["HNK-1/assignee"] != "Ana>Bia" {
		t.Fatalf("de/para: %+v", kinds)
	}
	only := DiffFingerprints(old, cur, CardTriggers{Status: true})
	if len(only) != 1 || only[0].ChangeKind != ChangeStatus {
		t.Fatalf("só status: %+v", only)
	}
	if len(DiffFingerprints(old, cur, CardTriggers{Membership: true})) != 2 {
		t.Fatal("entrada e saída")
	}
	if HashChanges(nil) != "" || HashChanges(all) == HashChanges(only) || HashChanges(all) != HashChanges(DiffFingerprints(old, cur, CardTriggers{AnyUpdate: true})) {
		t.Fatal("hash")
	}
}

func TestChooseCardUpdateKind(t *testing.T) {
	inc := CardKindInput{HasSummary: true, Changes: 3, IncrementalsSinceFull: 2}
	if ChooseCardUpdateKind(inc) != CardUpdateIncremental {
		t.Fatal("incremental")
	}
	for name, in := range map[string]CardKindInput{
		"explícito":     {ExplicitFull: true, HasSummary: true},
		"sem resumo":    {Changes: 1},
		"muitas":        {HasSummary: true, Changes: 11},
		"filtros novos": {HasSummary: true, QueryChanged: true},
		"9 seguidos":    {HasSummary: true, IncrementalsSinceFull: 9},
		"restauração":   {HasSummary: true, Restore: true},
	} {
		if ChooseCardUpdateKind(in) != CardUpdateFull {
			t.Errorf("%s deveria ser full", name)
		}
	}
}

func TestValidateCardQueries(t *testing.T) {
	if ValidateCardQueries(nil) == nil {
		t.Fatal("vazio")
	}
	if ValidateCardQueries(make([]StatusCardQuery, 11)) == nil {
		t.Fatal("11 filtros")
	}
	if ValidateCardQueries([]StatusCardQuery{{}}) == nil {
		t.Fatal("filtro sem critério")
	}
	qs := []StatusCardQuery{{Statuses: []TaskStatus{TaskBlocked, TaskBlocked}, Priorities: []Priority{PriorityCritical}}}
	if err := ValidateCardQueries(qs); err != nil || len(qs[0].Statuses) != 1 {
		t.Fatalf("dedup: %+v %v", qs, err)
	}
	if ValidateCardQueries([]StatusCardQuery{{Priorities: []Priority{"urgente"}}}) == nil {
		t.Fatal("prioridade inválida")
	}
}

func TestStatusCardView(t *testing.T) {
	now := time.Now()
	id := NewID()
	cases := []struct {
		c    StatusCard
		want StatusCardView
	}{
		{StatusCard{ArchivedAt: &now, State: CardError}, CardViewArchived},
		{StatusCard{State: CardCompiling, GeneratingTaskID: &id}, CardViewCompiling},
		{StatusCard{State: CardError}, CardViewError},
		{StatusCard{State: CardPausedBudget}, CardViewPausedBudget},
		{StatusCard{State: CardPausedHours}, CardViewPausedHours},
		{StatusCard{State: CardActive, GeneratingTaskID: &id, PendingChangeCount: 3}, CardViewUpdating},
		{StatusCard{State: CardActive, PendingChangeCount: 3}, CardViewStale},
		{StatusCard{State: CardActive}, CardViewFresh},
	}
	for i, c := range cases {
		if got := c.c.View(); got != c.want {
			t.Errorf("%d: %s, quer %s", i, got, c.want)
		}
	}
}
