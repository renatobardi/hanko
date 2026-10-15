package views

// InboxReview é uma revisão automática em andamento numa task em revisão (ainda sem gate).
type InboxReview struct {
	Key      string
	Href     string
	Title    string
	Persona  string // autora
	Reviewer string
	Waiting  bool // ainda não começou (espera a execução da autora terminar)
	When     string
}
