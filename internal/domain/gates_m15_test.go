package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validForm() QuestionForm {
	return QuestionForm{Fields: []QuestionField{
		{Key: "ambiente", Label: "Onde publicar?", Type: QuestionSingle, Required: true,
			Options: []QuestionOption{{Value: "stg", Label: "Staging"}, {Value: "prd", Label: "Produção"}}},
		{Key: "extras", Label: "Extras", Type: QuestionMulti,
			Options: []QuestionOption{{Value: "docs"}, {Value: "tests"}, {Value: "perf"}}},
		{Key: "obs", Label: "Observações", Type: QuestionText},
	}}
}

func TestQuestionFormValidate(t *testing.T) {
	if err := validForm().Validate(); err != nil {
		t.Fatalf("form válido recusado: %v", err)
	}
	many := QuestionForm{}
	for i := 0; i <= MaxQuestionFields; i++ {
		many.Fields = append(many.Fields, QuestionField{Key: "k" + string(rune('a'+i)), Label: "x", Type: QuestionText})
	}
	manyOpts := QuestionField{Key: "o", Label: "x", Type: QuestionSingle}
	for i := 0; i <= MaxQuestionOptions; i++ {
		manyOpts.Options = append(manyOpts.Options, QuestionOption{Value: strings.Repeat("v", i+1)})
	}
	tests := []struct {
		name string
		form QuestionForm
	}{
		{"vazio", QuestionForm{}},
		{"campos demais", many},
		{"chave inválida", QuestionForm{Fields: []QuestionField{{Key: "Ab", Label: "x", Type: QuestionText}}}},
		{"chave repetida", QuestionForm{Fields: []QuestionField{{Key: "a", Label: "x", Type: QuestionText}, {Key: "a", Label: "y", Type: QuestionText}}}},
		{"rótulo vazio", QuestionForm{Fields: []QuestionField{{Key: "a", Label: " ", Type: QuestionText}}}},
		{"tipo desconhecido", QuestionForm{Fields: []QuestionField{{Key: "a", Label: "x", Type: "date"}}}},
		{"texto com opções", QuestionForm{Fields: []QuestionField{{Key: "a", Label: "x", Type: QuestionText, Options: []QuestionOption{{Value: "a"}}}}}},
		{"escolha sem opções", QuestionForm{Fields: []QuestionField{{Key: "a", Label: "x", Type: QuestionSingle}}}},
		{"opções demais", QuestionForm{Fields: []QuestionField{manyOpts}}},
		{"opção repetida", QuestionForm{Fields: []QuestionField{{Key: "a", Label: "x", Type: QuestionMulti, Options: []QuestionOption{{Value: "v"}, {Value: "v"}}}}}},
		{"opção sem valor", QuestionForm{Fields: []QuestionField{{Key: "a", Label: "x", Type: QuestionMulti, Options: []QuestionOption{{Value: " "}}}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.form.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("esperava ErrInvalid, veio %v", err)
			}
		})
	}
}

func TestQuestionCheckAnswer(t *testing.T) {
	f := validForm()
	ok, err := f.CheckAnswer(map[string][]string{"ambiente": {" stg "}, "extras": {"docs", "perf"}, "obs": {"  "}})
	if err != nil {
		t.Fatalf("resposta válida recusada: %v", err)
	}
	if got := ok.Values["ambiente"]; len(got) != 1 || got[0] != "stg" {
		t.Fatalf("ambiente = %v", got)
	}
	if _, has := ok.Values["obs"]; has {
		t.Fatalf("campo opcional vazio deveria ser omitido: %v", ok.Values)
	}
	bad := []struct {
		name string
		in   map[string][]string
	}{
		{"obrigatório vazio", map[string][]string{"extras": {"docs"}}},
		{"chave estranha", map[string][]string{"ambiente": {"stg"}, "zzz": {"x"}}},
		{"single com dois", map[string][]string{"ambiente": {"stg", "prd"}}},
		{"opção desconhecida", map[string][]string{"ambiente": {"dev"}}},
		{"multi repetida", map[string][]string{"ambiente": {"stg"}, "extras": {"docs", "docs"}}},
		{"multi desconhecida", map[string][]string{"ambiente": {"stg"}, "extras": {"nada"}}},
		{"texto com dois", map[string][]string{"ambiente": {"stg"}, "obs": {"a", "b"}}},
		{"texto longo", map[string][]string{"ambiente": {"stg"}, "obs": {strings.Repeat("a", MaxQuestionAnswer+1)}}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.CheckAnswer(tc.in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("esperava ErrInvalid, veio %v", err)
			}
		})
	}
}

func TestQuestionRender(t *testing.T) {
	f := validForm()
	a, err := f.CheckAnswer(map[string][]string{"ambiente": {"prd"}, "extras": {"docs"}})
	if err != nil {
		t.Fatal(err)
	}
	got := f.Render(a)
	for _, want := range []string{"**Onde publicar?**: Produção (prd)", "**Extras**: docs", "**Observações**: _(sem resposta)_"} {
		if !strings.Contains(got, want) {
			t.Errorf("render sem %q:\n%s", want, got)
		}
	}
}

func TestGateAudience(t *testing.T) {
	u1, u2 := NewID(), NewID()
	tests := []struct {
		name string
		a    GateAudience
		user ID
		role MemberRole
		want bool
	}{
		{"vazia libera", GateAudience{}, u1, MemberMember, true},
		{"usuário nomeado", GateAudience{Users: []ID{u1}}, u1, MemberMember, true},
		{"outro usuário", GateAudience{Users: []ID{u1}}, u2, MemberAdmin, false},
		{"papel nomeado", GateAudience{Roles: []MemberRole{MemberAdmin}}, u2, MemberAdmin, true},
		{"papel diferente", GateAudience{Roles: []MemberRole{MemberAdmin}}, u2, MemberMember, false},
		{"owner sempre", GateAudience{Users: []ID{u1}}, u2, MemberOwner, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Allows(tc.user, tc.role); got != tc.want {
				t.Fatalf("Allows = %v, quer %v", got, tc.want)
			}
		})
	}
	if err := (GateAudience{Roles: []MemberRole{"chefe"}}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("papel desconhecido deveria ser inválido: %v", err)
	}
	if err := (GateAudience{Roles: []MemberRole{MemberAdmin, MemberAdmin}}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("papel repetido deveria ser inválido: %v", err)
	}
	big := GateAudience{}
	for i := 0; i <= MaxAudienceEntries; i++ {
		big.Users = append(big.Users, NewID())
	}
	if err := big.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("audiência grande deveria ser inválida: %v", err)
	}
	if err := (GateAudience{Users: []ID{u1}, Roles: []MemberRole{MemberAdmin}}).Validate(); err != nil {
		t.Fatalf("audiência válida recusada: %v", err)
	}
}

func TestHireSpecCheck(t *testing.T) {
	ok := HireSpec{Name: "Yuki", Agent: "claude", AdapterConfig: json.RawMessage(`{"permission_mode":"plan"}`)}
	if err := ok.Check(); err != nil {
		t.Fatalf("spec válida recusada: %v", err)
	}
	for name, s := range map[string]HireSpec{
		"sem nome":        {Agent: "claude"},
		"agente":          {Name: "x", Agent: "zzz"},
		"config inválida": {Name: "x", Agent: "claude", AdapterConfig: json.RawMessage(`[1]`)},
		"env":             {Name: "x", Agent: "claude", AdapterConfig: json.RawMessage(`{"env":{"K":"v"}}`)},
	} {
		if err := s.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: esperava ErrInvalid, veio %v", name, err)
		}
	}
}

func TestPlanPolicy(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name                   string
		task, project, persona *bool
		want                   PlanPolicy
	}{
		{"nada", nil, nil, nil, PlanPolicy{}},
		{"persona", nil, nil, &yes, PlanPolicy{Required: true, Source: PlanScopePersona}},
		{"projeto vence persona", nil, &no, &yes, PlanPolicy{Required: false, Source: PlanScopeProject}},
		{"task vence tudo", &yes, &no, &no, PlanPolicy{Required: true, Source: PlanScopeTask}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolvePlanPolicy(tc.task, tc.project, tc.persona); got != tc.want {
				t.Fatalf("got %+v, quer %+v", got, tc.want)
			}
		})
	}
	req := PlanPolicy{Required: true}
	if req.PhaseFor(false) != PlanPhasePlan || req.PhaseFor(true) != PlanPhaseExecute || (PlanPolicy{}).PhaseFor(true) != PlanPhaseNone {
		t.Fatal("fase errada")
	}
	if PlanScope("x").Check() == nil || PlanScopeTask.Check() != nil {
		t.Fatal("escopo")
	}
}

func TestGateKindsM15(t *testing.T) {
	for _, k := range []GateKind{GateQuestion, GatePlan} {
		if !k.Valid() || !k.Blocking() {
			t.Errorf("%s deveria ser válido e bloqueante", k)
		}
	}
	if !GateDismissed.Valid() || GateDismissed.Decision() {
		t.Error("dismissed é status válido, mas não é uma decisão de DecideGate")
	}
	if !ResolveRaiseCeiling.UserChoice() || ResolveAuto.UserChoice() {
		t.Error("UserChoice")
	}
	if !BudgetScopePersona.Valid() || BudgetScope("x").Valid() {
		t.Error("BudgetScope")
	}
}
