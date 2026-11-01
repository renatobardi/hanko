package domain

import "time"

// Modelo de acesso (ADR-0089): um valor nomeado de modo por risco (leitura / escrita / destrutiva) que o
// assistente Conectar copia para o perfil de ferramentas na conclusão. É dado por empresa, cópia no uso:
// editar um modelo depois não muda conexão já criada.

// MaxAccessTemplates limita quantos modelos uma empresa guarda.
const MaxAccessTemplates = 50

// AccessTemplate é um modelo de acesso da empresa.
type AccessTemplate struct {
	ID              ID
	CompanyID       ID
	Slug            string
	Name            string
	Description     string
	ReadMode        ToolPolicyMode
	WriteMode       ToolPolicyMode
	DestructiveMode ToolPolicyMode
	Suggested       bool
	CreatedBy       Actor
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ModeForRisk devolve o modo do modelo para o risco dado. Risco desconhecido cai no modo da escrita
// destrutiva (o mais restritivo entre escrita e destruição): tool sem anotação não é liberada por acaso.
func (a AccessTemplate) ModeForRisk(r ToolRisk) ToolPolicyMode {
	switch r {
	case ToolRiskRead:
		return a.ReadMode
	case ToolRiskWrite:
		return a.WriteMode
	default: // destructive e unknown
		return a.DestructiveMode
	}
}
