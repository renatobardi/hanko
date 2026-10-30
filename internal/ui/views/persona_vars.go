package views

// PersonaVarsVM é a seção "Variáveis de ambiente" da persona (#117 fatia 3, ADR-0068 §5.2). Cada linha mostra o nome e
// a referência a segredo; valor em claro de versão antiga sai como "***" e nunca chega à página.
type PersonaVarsVM struct {
	Rows    []PersonaVarRow
	Admin   bool   // só o admin edita e remove
	SetHref string // POST: pôr ou trocar uma variável (campos nome e referencia)
}

// PersonaVarRow é uma variável do adapter_config.env.
type PersonaVarRow struct {
	Name       string
	Ref        string // a referência inteira (${secret:…} ou ${secretfile:…}); vazia quando Masked
	Masked     bool   // valor em claro: mostra "***" e pede a troca por referência
	RemoveHref string // POST: tirar a variável
}
