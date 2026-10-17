package views

// ---- sala (ADR-0033) ----

// RoomRow é uma sala numa lista (página Sala e página da persona).
type RoomRow struct {
	Href     string
	Title    string
	Personas []string // nomes das personas ativas, na ordem em que entraram
	Badge    Badge
	Last     string
	Cost     string
}

// RoomPick é uma persona no formulário de nova sala (multi-seleção com glifo).
type RoomPick struct {
	ID       string
	Name     string
	Title    string
	Checked  bool
	Disabled string // motivo de a persona não poder responder agora ("" = pode)
}

// RoomSuggestion é um atalho que preenche a primeira mensagem.
type RoomSuggestion struct {
	Label string
	Href  string
}

// RoomsVM é a lista de salas com o formulário de nova sala.
type RoomsVM struct {
	Subtitle    string
	Tabs        []TabLink
	Rows        []RoomRow
	NewAction   string
	Picks       []RoomPick
	Moderators  []Option
	Mode        string
	RoomTitle   string
	Message     string
	Error       string
	Empty       bool // a empresa tem menos de duas personas
	Suggestions []RoomSuggestion
}

// RoomMember é uma persona no cabeçalho da sala.
type RoomMember struct {
	ID        string
	Name      string
	Slug      string
	Href      string
	Moderator bool
	Thinking  bool   // tem run ativa nesta sala
	RunHref   string // a run ativa
	Pending   int
	RemoveURL string // "" = não pode remover (papel ou mínimo de personas)
	Blocked   string // por que não pode responder agora
}

// RoomMsg é uma mensagem da sala: Kind human, persona ou system.
type RoomMsg struct {
	Kind    string
	Author  string
	Body    HTML
	When    string
	RunHref string
	// Waiting são as personas que ainda vão receber a mensagem (estão terminando outra resposta).
	Waiting []string
}

// RoomFeedItem é uma linha do Feed do time.
type RoomFeedItem = ActivityItem

// RoomVM é a página de uma sala; RoomThread e RoomFeed são também os fragmentos htmx que fazem polling.
type RoomVM struct {
	Title      string
	Badge      Badge
	Mode       string // mentions | all
	ModeLabel  string
	Archived   bool
	Busy       bool // ao menos uma persona está respondendo
	Members    []RoomMember
	Messages   []RoomMsg
	Waiting    int // mensagens esperando sem run (canceladas ou interrompidas)
	ThreadHref string
	SendHref   string
	FeedHref   string
	StatusHref string
	ListHref   string
	AddHref    string
	ConfigHref string
	AddOptions []Option // personas fora da sala
	ModOptions []Option // moderadora
	MaxHops    int
	MaxRuns    int
	Cost       string
	Draft      string
	DraftKey   string
	Feed       []RoomFeedItem
	CanAdd     bool
}
