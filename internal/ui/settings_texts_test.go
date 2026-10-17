package ui

import (
	"testing"

	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/settings"
)

// Os textos traduzíveis das chaves de instance_settings ficam no pacote ui (para o catálogo ver o uso); o pt-BR tem
// de coincidir com o do registro e todo texto precisa de versão em inglês.
func TestSettingTextsMatchRegistry(t *testing.T) {
	en := i18n.English()
	for _, d := range settings.Defs {
		tx, ok := settingTexts[d.Key]
		if !ok {
			t.Errorf("%s: sem entrada em settingTexts", d.Key)
			continue
		}
		if tx[0] != d.Label || tx[1] != d.Help {
			t.Errorf("%s: texto da UI difere do registro (%q/%q vs %q/%q)", d.Key, tx[0], tx[1], d.Label, d.Help)
		}
		for _, s := range tx {
			if _, ok := en[s]; s != "" && !ok {
				t.Errorf("%s: sem tradução para %q", d.Key, s)
			}
		}
	}
	for k := range settingTexts {
		if _, ok := settings.Lookup(k); !ok {
			t.Errorf("settingTexts tem a chave %q fora do registro", k)
		}
	}
}
