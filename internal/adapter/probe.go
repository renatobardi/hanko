package adapter

import "context"

// ProbeStep é um passo nomeado de um probe de readiness (binário, protocolo, autenticação...).
type ProbeStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// ProbeResult é o resultado de um probe: OK só se todos os passos passaram. Um probe não envia prompt nem gasta
// modelo; o que falha traz o motivo, sem segredo.
type ProbeResult struct {
	OK    bool        `json:"ok"`
	Steps []ProbeStep `json:"steps"`
}

// Add acrescenta um passo e mantém OK coerente.
func (r *ProbeResult) Add(name string, ok bool, detail string) {
	if len(r.Steps) == 0 {
		r.OK = true
	}
	r.Steps = append(r.Steps, ProbeStep{Name: name, OK: ok, Detail: detail})
	if !ok {
		r.OK = false
	}
}

// Prober é implementado pelos adapters que sabem checar se a persona consegue rodar (binário, autenticação,
// endpoint) sem executar trabalho.
type Prober interface {
	Probe(ctx context.Context, spec Spec) ProbeResult
}

// DiscardSink descarta os eventos (probes).
var DiscardSink Sink = SinkFunc(func(context.Context, Event) error { return nil })
