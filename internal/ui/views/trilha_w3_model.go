package views

import "context"

// Trilha contínua, onda 3 (ADR-0061): onboarding, favoritos e recentes, perfil, "O que há de novo".

// ---- sidebar: favoritos e recentes ----

// NavLink é um favorito ou recente da sidebar.
type NavLink struct {
	Href  string
	Title string
}

// NavPrefs são os atalhos pessoais da sidebar na página atual.
type NavPrefs struct {
	Favorites []NavLink
	Recents   []NavLink
	// Current é o caminho da página atual relativo à empresa ("/tasks/HNK-1"); vazio quando a página não pode ser favoritada.
	Current string
	// IsFavorite diz se a página atual já é favorita.
	IsFavorite bool
}

type navPrefsKey struct{}

// WithNavPrefs põe os atalhos da sidebar no contexto de renderização.
func WithNavPrefs(ctx context.Context, p NavPrefs) context.Context {
	return context.WithValue(ctx, navPrefsKey{}, p)
}

func navPrefsFrom(ctx context.Context) NavPrefs {
	p, _ := ctx.Value(navPrefsKey{}).(NavPrefs)
	return p
}

// ---- onboarding ----

// WizardStepVM é um passo do assistente de primeiros passos.
type WizardStepVM struct {
	N       int
	Key     string
	Label   string
	Done    bool
	Skipped bool
	Current bool
}

// WizardProbeStep é um passo do teste do harness.
type WizardProbeStep struct {
	Name   string
	Status string
	Detail string
}

// WizardProbeVM é o último teste de harness.
type WizardProbeVM struct {
	Agent  string
	Model  string
	Ready  bool
	Reason string
	Steps  []WizardProbeStep
}

// WizardTeamVM é um pacote de time do catálogo oferecido no passo da persona.
type WizardTeamVM struct {
	Slug     string
	Name     string
	Desc     string
	Personas int
	Agents   string
}

// WizardVM é a página /primeiros-passos.
type WizardVM struct {
	Steps     []WizardStepVM
	Current   string
	Done      int
	Total     int
	Complete  bool
	Dismissed bool
	// Harness escolhido no último teste (vazio = nenhum) e o modelo.
	Harness string
	Model   string
	Probe   *WizardProbeVM
	Teams   []WizardTeamVM
	// HarnessOptions são os harnesses que o assistente sabe testar (o do último teste vem marcado).
	HarnessOptions []Option
	// Personas são as personas que podem receber a primeira task.
	Personas []Option
	// Pronto: link da task criada pelo assistente, quando há.
	Notice string
	Error  string
	// Form guarda o que a pessoa digitou, para não perder no erro.
	PersonaName, PersonaTitle, PersonaInstr, TaskTitle, TaskDesc string
	CanAdmin, CanWrite                                           bool
}

// WizardCardVM é o convite no Painel para continuar o assistente.
type WizardCardVM struct {
	Done, Total int
	Next        string
}

// ---- perfil ----

// ProfileVM é a página de perfil próprio.
type ProfileVM struct {
	Email       string
	Name        string
	AvatarGlyph string
	AvatarTone  string
	Timezone    string
	Locale      string
	Tones       []Option
	Locales     []Option
	// Trusted: sem conta (modo trusted), o perfil não existe.
	Trusted bool
	Error   string
	Notice  string
}

// ---- o que há de novo ----

// WhatsNewVM é a página (e o cartão do Painel) de novidades da versão instalada.
type WhatsNewVM struct {
	Installed string
	Version   string
	Date      string
	BodyHTML  HTML
	Key       string
	Dismissed bool
}
