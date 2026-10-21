package views

// Modelos das telas do ADR-0057: ciclo de vida da empresa, import com prévia, login da CLI, pedidos de entrada e
// contas da instalação.

// CompanyAdminVM alimenta o cartão "Empresa" das Configurações (editar, arquivar, restaurar, excluir).
type CompanyAdminVM struct {
	Show        bool
	Name        string
	Slug        string
	Description string
	Timezone    string
	Prefix      string
	HasTasks    bool // o prefixo não muda com tasks
	Archived    bool
	ArchivedAt  string
	CanAdmin    bool // admin: editar, arquivar e restaurar
	CanOwner    bool // owner: excluir
	Error       string
	Notice      string
}

// JoinRequestRow é um pedido de entrada na tabela do admin.
type JoinRequestRow struct {
	ID      string
	Name    string
	Email   string
	Message string
	Status  string
	Role    string
	When    string
}

// JoinVM é a parte "link de pedido de entrada" da seção Membros.
type JoinVM struct {
	Show     bool
	Enabled  bool
	NewLink  string // só logo depois de gerar
	Requests []JoinRequestRow
	Error    string
}

// CLIAuthVM alimenta /cli-auth/<código>.
type CLIAuthVM struct {
	Version string
	Code    string
	Device  string
	Scope   string // rótulo
	Account string
	// State: "confirm" | "approved" | "denied" | "invalid".
	State string
	Error string
}

// JoinPageVM alimenta /pedir-acesso/<token>.
type JoinPageVM struct {
	Version string
	Token   string
	Company string
	Account string
	// State: "request" | "sent" | "invalid" | "member".
	State   string
	Message string
	Error   string
}

// UserRow é uma conta na tela de usuários da instância.
type UserRow struct {
	Name, Email, Role, Provider string
	Companies, Keys             int64
	LastLogin, Created          string
	Self                        bool
}

// UsersVM alimenta /admin/usuarios.
type UsersVM struct {
	Version string
	Users   []UserRow
	Home    string // link de volta
}

// ImportItemRow é um item do arquivo e o que acontece com ele.
type ImportItemRow struct {
	Kind, Key, Name string
	Action          string // create | skip | rename | replace
	ActionLabel     string
	Target          string
	Conflict        bool // já existe no destino: dá para escolher a ação
}

// ImportWarningRow é um aviso já traduzido.
type ImportWarningRow struct{ Text string }

// ImportCount é uma linha das contagens.
type ImportCount struct {
	Label string
	N     int
}

// ImportPreviewVM é a prévia de um import.
type ImportPreviewVM struct {
	Token    string
	Into     string // slug da empresa de destino; vazio = nova
	Slug     string
	Active   bool
	Default  string // ação padrão de conflito
	Title    string
	Counts   []ImportCount
	Items    []ImportItemRow
	Warnings []ImportWarningRow
	Conflict int
}

// ImportCompanyOption é uma empresa que pode receber o merge.
type ImportCompanyOption struct{ Slug, Name string }

// ImportVM alimenta /importar.
type ImportVM struct {
	Version   string
	Companies []ImportCompanyOption
	Error     string
	Preview   *ImportPreviewVM
	// valores do formulário mantidos
	Into, Slug, Default string
	Active              bool
	Done                string // link da empresa depois de importar
	DoneName            string
}
