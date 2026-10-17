package views

// ---- chat (ADR-0027) ----

// ChatRow is one conversation in a list (Chat page and the persona page).
type ChatRow struct {
	Href        string
	Title       string
	Persona     string
	PersonaHref string
	Badge       Badge
	Last        string // when the last message arrived
	Cost        string
	Archived    bool
}

// ChatsVM is the Chat list with the "new conversation" form.
type ChatsVM struct {
	Subtitle string
	Tabs     []TabLink
	Rows     []ChatRow
	// new conversation form
	NewAction string
	Personas  []Option
	Message   string
	Error     string
	Empty     bool // the company has no persona to talk to
}

// ChatMsg is one message of a thread: Kind is human, persona or system.
type ChatMsg struct {
	Kind    string
	Author  string
	Body    HTML
	When    string
	RunHref string
	// Marker é "context_reset" no aviso que /new insere (ADR-0052); Attachments são os anexos da mensagem.
	Marker      string
	Attachments []AttachmentRow
}

// ChatVM is a conversation page; its Thread part is also the htmx fragment that polls while the persona answers.
type ChatVM struct {
	Title       string
	Persona     string
	PersonaHref string
	Badge       Badge
	Archived    bool
	Busy        bool // a run is answering
	Waiting     int  // messages waiting with no run to answer them (the next human message picks them up)
	Messages    []ChatMsg
	ThreadHref  string
	SendHref    string
	StatusHref  string // archive or reopen
	ListHref    string
	RunHref     string // run in progress, when there is one
	Cost        string
	Error       string
	Blocked     string       // why the persona cannot answer now ("" = it can); sending is disabled
	Followups   *FollowupsVM // fila de mensagens que esperam a resposta (ADR-0039)
	QueueHref   string       // fragmento da fila (polling)
	// DraftF liga o campo ao rascunho do servidor e UploadHint é a dica do campo de arquivos (ADR-0052).
	DraftF     DraftFields
	UploadHint string
}
