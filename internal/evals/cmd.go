package evals

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Códigos de saída do runner.
const (
	ExitOK     = 0
	ExitFailed = 1 // algum cenário abaixo do mínimo
	ExitUsage  = 2 // configuração inválida (inclui live sem chave)
)

// Main é o `hanko-evals`: parse de flags/env, execução, relatório. Separado do main para ser testável.
//
// Variáveis: HANKO_EVALS_LIVE=1 liga o modelo real (padrão: roteiro de mentira, sem custo); ANTHROPIC_API_KEY
// (só no live); HANKO_EVALS_MODEL; HANKO_EVALS_MAX_USD (teto estimado, padrão 1); HANKO_EVALS_TRIALS.
func Main(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hanko-evals", flag.ContinueOnError)
	fs.SetOutput(stderr)
	suite := fs.String("suite", orDefault(getenv("HANKO_EVALS_SUITE"), "all"), "behavior, chaos ou all")
	scenario := fs.String("scenario", "", "roda só este cenário")
	trials := fs.Int("trials", envInt(getenv("HANKO_EVALS_TRIALS")), "tentativas por cenário (padrão 1 no fake, 3 no live)")
	live := fs.Bool("live", getenv("HANKO_EVALS_LIVE") == "1", "usa o modelo real (CUSTA tokens; exige ANTHROPIC_API_KEY)")
	bad := fs.Bool("bad", false, "controle negativo: roda os roteiros errados; o relatório TEM de sair vermelho")
	maxUSD := fs.Float64("max-usd", envFloat(getenv("HANKO_EVALS_MAX_USD"), 1.0), "teto de gasto estimado no live (0 = sem teto)")
	report := fs.String("report", "", "arquivo do relatório JSON")
	summary := fs.String("summary", getenv("GITHUB_STEP_SUMMARY"), "arquivo do resumo em markdown (padrão $GITHUB_STEP_SUMMARY)")
	list := fs.Bool("list", false, "lista os cenários e sai")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *list {
		for _, s := range Scenarios() {
			fmt.Fprintf(stdout, "%s\t%s\n", s.Suite, s.Name)
		}
		return ExitOK
	}
	switch *suite {
	case "all", SuiteBehavior, SuiteChaos:
	default:
		fmt.Fprintf(stderr, "suite inválida: %q (use behavior, chaos ou all)\n", *suite)
		return ExitUsage
	}

	cfg := Config{Suite: *suite, Scenario: *scenario, Trials: *trials, Live: *live, Bad: *bad, MaxUSD: *maxUSD}
	modelName := ""
	if *live {
		// o live nunca roda em PR: custa token e o PR pode vir de fork
		if getenv("GITHUB_EVENT_NAME") == "pull_request" || getenv("GITHUB_EVENT_NAME") == "pull_request_target" {
			fmt.Fprintln(stderr, "recusado: o modelo real não roda em pull request")
			return ExitUsage
		}
		key := getenv("ANTHROPIC_API_KEY")
		if key == "" {
			fmt.Fprintln(stderr, "modo live sem ANTHROPIC_API_KEY: nada foi chamado")
			return ExitUsage
		}
		modelName = orDefault(getenv("HANKO_EVALS_MODEL"), DefaultLiveModel)
		cfg.NewModel = func() Model { return &Anthropic{APIKey: key, Model: modelName} }
		fmt.Fprintf(stderr, "AVISO: modo live (%s): chamadas pagas, teto estimado US$ %.2f; custo marcado como teste\n", modelName, *maxUSD)
	}

	rep, err := RunSuite(ctx, cfg, Scenarios())
	if err != nil {
		fmt.Fprintln(stderr, "erro:", err)
		return ExitUsage
	}
	rep.Model = modelName

	fmt.Fprint(stdout, rep.Markdown())
	if *report != "" {
		if err := writeFile(*report, rep.WriteJSON); err != nil {
			fmt.Fprintln(stderr, "relatório:", err)
			return ExitUsage
		}
	}
	if *summary != "" {
		if f, err := os.OpenFile(*summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = io.WriteString(f, rep.Markdown())
			_ = f.Close()
		}
	}
	if !rep.OK {
		return ExitFailed
	}
	return ExitOK
}

func writeFile(path string, fn func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func envInt(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

func envFloat(v string, d float64) float64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
		return f
	}
	return d
}
