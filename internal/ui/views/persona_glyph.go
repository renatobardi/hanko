package views

import (
	"context"

	"github.com/renatobardi/hanko/internal/domain"
)

type personaIconsKey struct{}

// WithPersonaIcons anexa ao contexto de render o glifo de cada persona da empresa, por nome e por slug,
// para que avatares e chips desenhem a identidade da persona (Kubo DS) sem que cada view model a carregue.
func WithPersonaIcons(ctx context.Context, icons map[string]string) context.Context {
	return context.WithValue(ctx, personaIconsKey{}, icons)
}

// personaIcon é o glifo da persona chamada name (nome ou slug); desconhecida ou sem ícone, o padrão.
func personaIcon(ctx context.Context, name string) string {
	icons, _ := ctx.Value(personaIconsKey{}).(map[string]string)
	if i := icons[name]; domain.ValidPersonaIcon(i) {
		return i
	}
	return domain.DefaultPersonaIcon
}

// iconOrDefault normaliza um glifo escolhido (formulário, persona) para um valor do catálogo.
func iconOrDefault(i string) string {
	if domain.ValidPersonaIcon(i) {
		return i
	}
	return domain.DefaultPersonaIcon
}
