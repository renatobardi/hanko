package i18n

// enFix1 holds the English texts added by the independent-review fixes (ACP operator gate). Same rules
// as en.go: the key is the pt-BR text, and a key that another catalog already has is not repeated here.
var enFix1 = map[string]string{
	"O harness ACP não está liberado neste servidor para este comando: quem opera a instalação define HANKO_ACP_ALLOW (e HANKO_ACP_COMMANDS, se houver lista).": "The ACP harness is not enabled on this server for this command: whoever runs the installation sets HANKO_ACP_ALLOW (and HANKO_ACP_COMMANDS, if there is a list).",
}

func init() {
	for k, v := range enFix1 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
