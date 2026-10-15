package views

// Seg é um pedaço de texto de um resultado de busca; Match marca o que casou. O texto nunca é HTML:
// a página o escapa (templ) e põe <mark> só em volta dos trechos que casaram.
type Seg struct {
	Text  string
	Match bool
}

// SearchHitVM é um resultado.
type SearchHitVM struct {
	Href    string // vazio = sem link (página da memória)
	Ref     string // chave da task, slug, nome da execução ou path da memória
	Title   []Seg
	Snippet []Seg
	Badge   *Badge
	Meta    string // contexto curto: task do comentário, persona da execução, autor
	When    string
	Exact   bool
}

// SearchGroupVM é um grupo de resultados de um tipo.
type SearchGroupVM struct {
	Icon        string
	Label       string
	Count       int
	More        bool
	Unavailable bool
	Hits        []SearchHitVM
}

// SearchVM alimenta a tela Buscar e o fragmento de resultados.
type SearchVM struct {
	Subtitle string
	Query    string
	Type     string // filtro de tipo ativo ("" = todos)
	Tabs     []TabLink
	Groups   []SearchGroupVM
	Total    int
	Error    string // consulta inválida (ex.: só pontuação)
	HasMemo  bool   // há provider de memória (o filtro "Memória" aparece)
}
