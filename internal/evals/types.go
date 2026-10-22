// Package evals é a suíte de evals do Hanko (#58): mede se uma persona, dado um modelo pequeno e as tools do
// Hanko, se comporta como o produto promete (pede gate antes de agir, delega ao especialista, ordena por
// dependência, não vaza segredo) e se aguenta falhas de infraestrutura (run morta, banco fora).
//
// Tudo que custa dinheiro ou depende de rede fica atrás de interfaces: Model (o modelo) e Harness (o mundo com
// as tools). O padrão é um modelo roteirizado contra um mundo em memória, determinístico e gratuito; o modelo
// real (Anthropic) só entra com a flag explícita. Os mesmos cenários e verificadores valem nos dois modos.
package evals

import "context"

// ToolSpec descreve uma tool para o modelo (nome, texto e JSON Schema dos argumentos).
type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ToolCall é uma chamada de tool pedida pelo modelo.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// ToolResult é a resposta de uma tool ao modelo.
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// Message é um turno da conversa. Role é "user" ou "assistant".
type Message struct {
	Role    string
	Text    string
	Calls   []ToolCall   // só assistant
	Results []ToolResult // só user
}

// Request é o que o modelo recebe a cada passo.
type Request struct {
	System   string
	Messages []Message
	Tools    []ToolSpec
}

// Usage é o consumo de tokens de uma resposta.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Reply é a resposta do modelo: texto, chamadas de tool ou os dois. Sem Calls, o modelo terminou.
type Reply struct {
	Text  string
	Calls []ToolCall
	Usage Usage
}

// Model é o modelo da persona. A implementação real chama a API; a roteirizada responde de um script.
type Model interface {
	Next(ctx context.Context, req Request) (Reply, error)
}

// Harness é o mundo onde a persona age: as tools do Hanko. A implementação em memória (World) é o padrão; a
// MCPHarness fala com um servidor Hanko de verdade. Call devolve o conteúdo e se foi um erro da tool (que o
// modelo vê e pode tratar); erro de Go é falha do próprio harness.
type Harness interface {
	Tools() []ToolSpec
	Call(ctx context.Context, c ToolCall) (content string, isError bool, err error)
}

// Invocation é uma chamada de tool já feita, para os verificadores.
type Invocation struct {
	Seq      int
	Name     string
	Args     map[string]any
	Result   string
	IsError  bool
	Injected bool // o erro veio do chaos, não da tool
}

// Run é o registro de uma tentativa de um cenário.
type Run struct {
	Trace []Invocation
	Final string // último texto do modelo
	Usage Usage
	Steps int
}

// Calls devolve as invocações com esse nome, em ordem.
func (r *Run) Calls(name string) []Invocation {
	var out []Invocation
	for _, i := range r.Trace {
		if i.Name == name {
			out = append(out, i)
		}
	}
	return out
}

// first devolve o Seq da primeira invocação com o nome, ou -1.
func (r *Run) first(name string) int {
	for _, i := range r.Trace {
		if i.Name == name {
			return i.Seq
		}
	}
	return -1
}
