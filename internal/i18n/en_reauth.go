package i18n

// enReauth são os textos em inglês da seção de passkeys do perfil e do diálogo de reautenticação recente (ADR-0070).
// Os que já têm tradução em outro arquivo (Senha, Cancelar, Remover, Nome) não se repetem aqui.
var enReauth = map[string]string{
	"Passkeys": "Passkeys",
	"Confirme sua identidade com uma passkey. Ela libera as ações sensíveis do cofre por alguns minutos.": "Confirm your identity with a passkey. It unlocks the vault's sensitive actions for a few minutes.",
	"Nenhuma passkey cadastrada.":               "No passkey registered.",
	"Nome da passkey":                           "Passkey name",
	"Ex.: Notebook":                             "E.g.: Laptop",
	"Adicionar passkey":                         "Add passkey",
	"Não foi possível concluir. Tente de novo.": "Could not finish. Try again.",
	"A passkey não foi usada. Tente de novo.":   "The passkey was not used. Try again.",
	"Esta conta confirma pelo login do Google. Entre de novo para confirmar.": "This account confirms through the Google sign-in. Sign in again to confirm.",
	"Este navegador não tem suporte a passkey.":                               "This browser does not support passkeys.",
	"Sem nome":                "Unnamed",
	"Confirme sua identidade": "Confirm your identity",
	"Esta ação pede uma confirmação recente. Ela vale por": "This action needs a recent confirmation. It lasts for",
	"minutos.":             "minutes.",
	"Confirmar com senha":  "Confirm with password",
	"Usar passkey":         "Use passkey",
	"Confirmar com Google": "Confirm with Google",
	"Não foi possível confirmar pelo Google. Tente de novo.": "Could not confirm with Google. Try again.",
}

func init() {
	for k, v := range enReauth {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
