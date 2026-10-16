package views

import "context"

// UserRef is the signed-in user shown in the sidebar (authenticated mode).
type UserRef struct {
	Name  string
	Email string
}

type userKey struct{}

// WithUser attaches the signed-in user to the render context.
func WithUser(ctx context.Context, u UserRef) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

func userFrom(ctx context.Context) (UserRef, bool) {
	u, ok := ctx.Value(userKey{}).(UserRef)
	return u, ok
}

type companiesKey struct{}

// Switcher is the data of the company selector: the companies the person can move to and the
// company-relative section kept on the move ("tasks", "personas"…; "" = the company home).
type Switcher struct {
	Companies []CompanyLink
	Section   string
}

// WithSwitcher attaches the company switcher data to the render context.
func WithSwitcher(ctx context.Context, sw Switcher) context.Context {
	return context.WithValue(ctx, companiesKey{}, sw)
}

func switcherFrom(ctx context.Context) Switcher {
	sw, _ := ctx.Value(companiesKey{}).(Switcher)
	return sw
}

// SwitchSections are the company sections that exist in every company and are safe to keep when
// switching (list pages; detail pages of one company do not exist in another).
var SwitchSections = map[string]bool{
	"tasks": true, "personas": true, "organograma": true, "gates": true, "execucoes": true, "custos": true,
	"atividade": true, "caixa": true, "decisoes": true, "fluxos": true,
}

type themeKey struct{}

// WithTheme attaches the explicit theme ("light", "dark"; "" = follow the system) to the render context.
func WithTheme(ctx context.Context, theme string) context.Context {
	return context.WithValue(ctx, themeKey{}, theme)
}

func themeFrom(ctx context.Context) string {
	t, _ := ctx.Value(themeKey{}).(string)
	return t
}
