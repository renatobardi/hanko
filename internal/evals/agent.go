package evals

import (
	"context"
	"errors"
	"fmt"
)

// DefaultMaxSteps limita as voltas do laço do agente por tentativa (cada volta é uma chamada ao modelo).
const DefaultMaxSteps = 12

// injectedHarness é o que um harness com falhas injetadas expõe para a Invocation saber que o erro foi do chaos.
type injectedHarness interface {
	// WasInjected diz se a última chamada devolveu um erro injetado.
	WasInjected() bool
}

// RunAgent executa o laço persona: o modelo pede tools, o harness responde, até o modelo terminar ou o limite
// de passos. Devolve o registro (Run) mesmo quando termina por limite; o erro é só de infraestrutura.
func RunAgent(ctx context.Context, m Model, h Harness, system, prompt string, maxSteps int) (*Run, error) {
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	run := &Run{}
	req := Request{System: system, Tools: h.Tools(), Messages: []Message{{Role: "user", Text: prompt}}}
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return run, err
		}
		rep, err := m.Next(ctx, req)
		if err != nil {
			return run, fmt.Errorf("modelo: %w", err)
		}
		run.Steps++
		run.Usage.InputTokens += rep.Usage.InputTokens
		run.Usage.OutputTokens += rep.Usage.OutputTokens
		if rep.Text != "" {
			run.Final = rep.Text
		}
		req.Messages = append(req.Messages, Message{Role: "assistant", Text: rep.Text, Calls: rep.Calls})
		if len(rep.Calls) == 0 {
			return run, nil
		}
		var results []ToolResult
		for _, c := range rep.Calls {
			content, isErr, err := h.Call(ctx, c)
			if err != nil {
				return run, fmt.Errorf("harness: %w", err)
			}
			inj := false
			if ih, ok := h.(injectedHarness); ok {
				inj = ih.WasInjected()
			}
			run.Trace = append(run.Trace, Invocation{Seq: len(run.Trace) + 1, Name: c.Name, Args: c.Args, Result: content, IsError: isErr, Injected: inj})
			results = append(results, ToolResult{CallID: c.ID, Content: content, IsError: isErr})
		}
		req.Messages = append(req.Messages, Message{Role: "user", Results: results})
	}
	return run, ErrStepLimit
}

// ErrStepLimit: o modelo não terminou no limite de passos (falha de comportamento, não de infraestrutura).
var ErrStepLimit = errors.New("limite de passos atingido sem o modelo terminar")
