package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Cost é o custo estimado de uma execução. Kind é sempre "test": o gasto dos evals nunca se mistura ao custo
// de uso real da empresa (relatório e contabilidade distinguem).
type Cost struct {
	Kind         string  `json:"kind"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	USD          float64 `json:"usd"`
}

// Preços por milhão de tokens do modelo pequeno (estimativa para o teto de gasto; ajuste por env no cmd).
type Pricing struct{ InPerM, OutPerM float64 }

// DefaultPricing é a estimativa para o Haiku.
var DefaultPricing = Pricing{InPerM: 1.0, OutPerM: 5.0}

func (p Pricing) usd(u Usage) float64 {
	return float64(u.InputTokens)/1e6*p.InPerM + float64(u.OutputTokens)/1e6*p.OutPerM
}

// Config é a configuração de uma execução do runner.
type Config struct {
	Suite    string // behavior, chaos ou all
	Scenario string // opcional: um cenário só
	Trials   int    // tentativas por cenário (padrão 1 no fake, 3 no live)
	// Live liga o modelo real. Sem ele o runner usa o roteiro de mentira (Scenario.Fake): determinístico e grátis.
	Live bool
	// NewModel monta o modelo de uma tentativa live. Obrigatório com Live.
	NewModel func() Model
	// MaxUSD interrompe o live quando o gasto estimado passa disso (0 = sem teto; o cmd define um padrão).
	MaxUSD  float64
	Pricing Pricing
	// Bad roda o controle negativo (Scenario.Bad) em vez do roteiro certo: o relatório tem de sair vermelho.
	Bad      bool
	MaxSteps int
	Now      func() time.Time
}

// ScenarioResult é o resultado agregado de um cenário.
type ScenarioResult struct {
	Name     string   `json:"name"`
	Suite    string   `json:"suite"`
	Trials   int      `json:"trials"`
	Passed   int      `json:"passed"`
	MinPass  float64  `json:"min_pass"`
	OK       bool     `json:"ok"`
	Failures []string `json:"failures,omitempty"` // motivos distintos das tentativas reprovadas
	Cost     Cost     `json:"cost"`
}

// Report é o relatório da execução inteira.
type Report struct {
	Mode      string           `json:"mode"` // fake ou live
	Model     string           `json:"model,omitempty"`
	StartedAt time.Time        `json:"started_at"`
	Scenarios []ScenarioResult `json:"scenarios"`
	Cost      Cost             `json:"cost"`
	OK        bool             `json:"ok"`
	// Aborted explica uma interrupção (teto de gasto, erro do modelo).
	Aborted string `json:"aborted,omitempty"`
}

// Run executa os cenários escolhidos e devolve o relatório. Erro de Go só para configuração inválida; falha de
// comportamento vira Report.OK=false.
func RunSuite(ctx context.Context, cfg Config, scenarios []Scenario) (*Report, error) {
	if cfg.Live && cfg.NewModel == nil {
		return nil, errors.New("modo live sem modelo configurado")
	}
	if cfg.Live && cfg.Bad {
		return nil, errors.New("controle negativo só roda no modo fake")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	pr := cfg.Pricing
	if pr == (Pricing{}) {
		pr = DefaultPricing
	}
	trials := cfg.Trials
	if trials <= 0 {
		trials = 1
		if cfg.Live {
			trials = 3
		}
	}
	rep := &Report{Mode: "fake", StartedAt: now().UTC(), OK: true, Cost: Cost{Kind: "test"}}
	if cfg.Live {
		rep.Mode = "live"
	}
	picked := Select(scenarios, cfg.Suite, cfg.Scenario)
	if len(picked) == 0 {
		return nil, fmt.Errorf("nenhum cenário para suite=%q cenário=%q", cfg.Suite, cfg.Scenario)
	}
	for _, sc := range picked {
		res := ScenarioResult{Name: sc.Name, Suite: sc.Suite, Trials: trials, MinPass: sc.MinPass, Cost: Cost{Kind: "test"}}
		seen := map[string]bool{}
		for t := 0; t < trials; t++ {
			if cfg.MaxUSD > 0 && rep.Cost.USD+res.Cost.USD >= cfg.MaxUSD {
				rep.Aborted = fmt.Sprintf("teto de gasto dos evals atingido (US$ %.2f)", cfg.MaxUSD)
				res.Trials = t
				break
			}
			var m Model
			switch {
			case cfg.Live:
				m = cfg.NewModel()
			case cfg.Bad:
				m = ScriptedModel{Policy: sc.Bad}
			default:
				m = ScriptedModel{Policy: sc.Fake}
			}
			w := sc.Setup()
			var h Harness = w
			if sc.Wrap != nil {
				h = sc.Wrap(h)
			}
			run, err := RunAgent(ctx, m, h, sc.System, sc.Prompt, cfg.MaxSteps)
			var reasons []string
			switch {
			case errors.Is(err, ErrStepLimit):
				reasons = append(reasons, err.Error())
			case err != nil:
				if ctx.Err() != nil {
					return rep, ctx.Err()
				}
				// erro do modelo/rede: não é falha de comportamento; interrompe e reporta
				rep.Aborted = fmt.Sprintf("%s: %v", sc.Name, err)
				reasons = append(reasons, "erro de infraestrutura do runner: "+err.Error())
			}
			if err == nil || errors.Is(err, ErrStepLimit) {
				reasons = append(reasons, sc.Check(run, w)...)
			}
			if run != nil {
				res.Cost.InputTokens += run.Usage.InputTokens
				res.Cost.OutputTokens += run.Usage.OutputTokens
				res.Cost.USD += pr.usd(run.Usage)
			}
			if len(reasons) == 0 {
				res.Passed++
			}
			for _, r := range reasons {
				if !seen[r] {
					seen[r] = true
					res.Failures = append(res.Failures, r)
				}
			}
			if rep.Aborted != "" && err != nil && !errors.Is(err, ErrStepLimit) {
				res.Trials = t + 1
				break
			}
		}
		res.OK = res.Trials > 0 && float64(res.Passed)/float64(res.Trials) >= sc.MinPass
		rep.Cost.InputTokens += res.Cost.InputTokens
		rep.Cost.OutputTokens += res.Cost.OutputTokens
		rep.Cost.USD += res.Cost.USD
		if !res.OK {
			rep.OK = false
		}
		rep.Scenarios = append(rep.Scenarios, res)
		if rep.Aborted != "" {
			rep.OK = false
			break
		}
	}
	return rep, nil
}

// WriteJSON escreve o relatório em JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Markdown é o resumo para o $GITHUB_STEP_SUMMARY.
func (r *Report) Markdown() string {
	var b strings.Builder
	status := "verde"
	if !r.OK {
		status = "VERMELHO"
	}
	fmt.Fprintf(&b, "## evals (%s): %s\n\n", r.Mode, status)
	if r.Model != "" {
		fmt.Fprintf(&b, "Modelo: `%s`. ", r.Model)
	}
	fmt.Fprintf(&b, "Custo (marcado como teste): %d tokens de entrada, %d de saída, ~US$ %.4f.\n\n", r.Cost.InputTokens, r.Cost.OutputTokens, r.Cost.USD)
	b.WriteString("| cenário | suíte | aprovadas | mínimo | resultado |\n|---|---|---|---|---|\n")
	for _, s := range r.Scenarios {
		res := "ok"
		if !s.OK {
			res = "FALHOU"
		}
		fmt.Fprintf(&b, "| %s | %s | %d/%d | %.0f%% | %s |\n", s.Name, s.Suite, s.Passed, s.Trials, s.MinPass*100, res)
	}
	for _, s := range r.Scenarios {
		if len(s.Failures) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n**%s**\n", s.Name)
		for _, f := range s.Failures {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	if r.Aborted != "" {
		fmt.Fprintf(&b, "\nInterrompido: %s\n", r.Aborted)
	}
	return b.String()
}

// ScriptedModel é o modelo de mentira: responde do roteiro (Policy) do cenário, sem rede e sem custo.
type ScriptedModel struct {
	Policy func(Request) Reply
}

// Next implementa Model.
func (s ScriptedModel) Next(_ context.Context, req Request) (Reply, error) {
	if s.Policy == nil {
		return Reply{}, errors.New("cenário sem roteiro")
	}
	return s.Policy(req), nil
}
