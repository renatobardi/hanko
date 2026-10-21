package domain

import (
	"fmt"
	"slices"
)

// MaxAudienceEntries é o teto de pessoas e de papéis de uma audiência.
const MaxAudienceEntries = 50

// GateAudience restringe quem decide um gate e quem o vê na Caixa (ADR-0041). Vazia = qualquer decisor que o tipo do
// gate já exige (papel). Preenchida: decide quem está em Users ou tem um papel de Roles; o owner da empresa sempre
// pode (uma audiência mal escolhida nunca deixa um gate órfão).
type GateAudience struct {
	Users []ID         `json:"users,omitempty"`
	Roles []MemberRole `json:"roles,omitempty"`
}

// Empty diz se a audiência não restringe nada.
func (a GateAudience) Empty() bool { return len(a.Users) == 0 && len(a.Roles) == 0 }

// Validate confere papéis conhecidos, sem repetição e dentro do teto.
func (a GateAudience) Validate() error {
	if len(a.Users) > MaxAudienceEntries || len(a.Roles) > MaxAudienceEntries {
		return Invalid("audience", fmt.Sprintf("no máximo %d entradas", MaxAudienceEntries))
	}
	seen := map[MemberRole]bool{}
	for _, r := range a.Roles {
		if !r.Valid() {
			return Invalid("audience.roles", fmt.Sprintf("papel desconhecido %q", r))
		}
		if seen[r] {
			return Invalid("audience.roles", fmt.Sprintf("papel repetido %q", r))
		}
		seen[r] = true
	}
	return nil
}

// Allows diz se o usuário, com o papel dado na empresa, está na audiência.
func (a GateAudience) Allows(user ID, role MemberRole) bool {
	if a.Empty() || role == MemberOwner {
		return true
	}
	return slices.Contains(a.Users, user) || slices.Contains(a.Roles, role)
}
