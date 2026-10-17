package views

import (
	"strconv"

	"github.com/a-h/templ"
)

// Configurações › Notificações e selo da Caixa (ADR-0042).

// NotifyPrefRow é um tipo de aviso com os canais da pessoa.
type NotifyPrefRow struct {
	Kind  string
	Label string
	Help  string
	Toast bool
	Push  bool
}

// NotifyDevice é um aparelho com web push.
type NotifyDevice struct {
	ID    string
	Name  string
	Since string
	Last  string
}

type NotifyVM struct {
	Sections     []TabLink
	Notice       string
	Personal     bool // há pessoa autenticada
	Available    bool // o servidor pode enviar push (tem HANKO_SECRET_KEY)
	Prefs        []NotifyPrefRow
	Devices      []NotifyDevice
	VapidKey     string
	Action       string
	TestURL      string
	SubscribeURL string
	RemoveURL    string
}

// notifyAttrs liga o stream de avisos da pessoa (hanko.js abre o EventSource); sem pessoa autenticada, nada.
func notifyAttrs(sh Shell) templ.Attributes {
	if !sh.Personal {
		return nil
	}
	return templ.Attributes{"data-notify": true, "data-notify-url": sh.Base() + "/caixa/eventos"}
}

// badgeNum é o número do selo: 99+ acima de 99.
func badgeNum(n int) string {
	if n > 99 {
		return "99+"
	}
	return strconv.Itoa(n)
}
