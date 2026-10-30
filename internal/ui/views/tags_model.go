package views

// Tags de persona (ADR-0087): chips, o cartão da ficha e a aba Tags em Personas.

// MaxChips é quantos chips aparecem no card e no nó; o resto vira "+N".
const MaxChips = 3

// TagChip é uma tag como aparece num chip. Color é um nome da paleta (domain.PersonaTagColors).
type TagChip struct {
	Name        string
	Color       string
	Description string
	Archived    bool
}

// TagChipsVM são os chips visíveis e quantos ficaram de fora.
type TagChipsVM struct {
	Shown []TagChip
	More  int
}

// ChipsOf corta a lista em MaxChips chips e conta o resto.
func ChipsOf(tags []TagChip) TagChipsVM {
	if len(tags) <= MaxChips {
		return TagChipsVM{Shown: tags}
	}
	return TagChipsVM{Shown: tags[:MaxChips], More: len(tags) - MaxChips}
}

// PersonaTagChip é um chip da ficha, com o POST que tira a tag.
type PersonaTagChip struct {
	TagChip
	RemoveHref string
}

// PersonaTagsVM é o cartão de tags da ficha da persona.
type PersonaTagsVM struct {
	Chips     []PersonaTagChip
	ApplyHref string   // POST: aplica pelo nome e cria a tag se ela não existe
	Options   []string // nomes das tags ativas, para o seletor
	TagsHref  string   // aba Tags (cadastro)
	CanWrite  bool
}

// TagRow é uma linha da aba Tags.
type TagRow struct {
	TagChip
	ID            string
	Personas      int
	EditHref      string
	ArchiveHref   string // vazio quando já arquivada
	UnarchiveHref string // vazio quando ativa
	DeleteHref    string
	Colors        []Option
}

// TagsPageVM é a aba Tags em Personas: o cadastro da empresa.
type TagsPageVM struct {
	Subtitle     string
	Rows         []TagRow
	ShowArchived bool
	ToggleHref   string
	CreateHref   string
	Colors       []Option
	Error        string
}
