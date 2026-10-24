package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
)

// Gerador e saúde na UI (fatia 6, #61): o bloco do gerador chega aos formulários, o que o formulário manda chega ao
// backend, e a página de saúde mostra os achados sem valor.

func TestSecretsHealthPageShowsFindingsWithoutValue(t *testing.T) {
	e, _ := newCofreEnv(t, domain.MemberAdmin)
	rec := e.get(t, "/c/hanko/configuracoes/segredos/saude")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "solto", "velho", "Órfão", "sem rotação há 100 dias (prazo 30)")
	wantNotContains(t, rec, cofreSecretText)
}

func TestFieldFormSendsGeneratorOptions(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	form := url.Values{"name": {"pin"}, "generate": {"1"}, "gen_length": {"16"}, "gen_class": {"digits", "symbols"}}
	rec := e.post(t, cofreFieldsPage, form, false)
	wantStatus(t, rec, http.StatusSeeOther)
	rec = e.post(t, cofreFieldsPage+"/senha/valor", url.Values{"generate": {"1"}, "gen_words": {"4"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)

	want := []domain.GenOptions{
		{Length: 16, Classes: []domain.GenClass{domain.GenDigits, domain.GenSymbols}},
		{PassphraseWords: 4},
	}
	if !reflect.DeepEqual(cf.gen, want) {
		t.Fatalf("opções que chegaram ao backend = %+v, quer %+v", cf.gen, want)
	}
}

func TestFieldFormWithoutGeneratorFieldsKeepsTheDefault(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := e.post(t, cofreFieldsPage, url.Values{"name": {"pin"}, "generate": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(cf.gen) != 1 || !reflect.DeepEqual(cf.gen[0], domain.GenOptions{}) {
		t.Fatalf("formulário sem o bloco do gerador deveria mandar o padrão, veio %+v", cf.gen)
	}
}

func TestFieldFormRejectsUnreadableGeneratorNumber(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := e.post(t, cofreFieldsPage, url.Values{"name": {"pin"}, "generate": {"1"}, "gen_length": {"muito"}}, false)
	if len(cf.gen) != 0 {
		t.Fatalf("número ilegível chegou ao backend: %+v", cf.gen)
	}
	wantNotContains(t, rec, cofreSecretText)
}

func TestGenOptsFromFormParsesAndRefusesGarbage(t *testing.T) {
	cases := []struct {
		name    string
		form    url.Values
		want    domain.GenOptions
		wantErr string
	}{
		{name: "vazio é o padrão", form: url.Values{}, want: domain.GenOptions{}},
		{name: "tamanho e classes", form: url.Values{"gen_length": {" 24 "}, "gen_class": {"upper"}},
			want: domain.GenOptions{Length: 24, Classes: []domain.GenClass{domain.GenUpper}}},
		{name: "frase-senha", form: url.Values{"gen_words": {"5"}}, want: domain.GenOptions{PassphraseWords: 5}},
		{name: "tamanho que não é número", form: url.Values{"gen_length": {"x"}}, wantErr: "length"},
		{name: "palavras que não é número", form: url.Values{"gen_words": {"três"}}, wantErr: "passphrase_words"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			got, err := genOptsFromForm(r)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("erro = %v, quer um erro no campo %q", err, c.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("opções = %+v %v, quer %+v", got, err, c.want)
			}
		})
	}
}

func TestAlertRowForSecretRotationLinksToHealth(t *testing.T) {
	ctx := i18n.WithLang(t.Context(), i18n.PT)
	row := alertRow(ctx, domain.Alert{Kind: domain.AlertSecretRotationDue, SubjectType: "secret", Message: "Segredo gh: value com rotação vencida",
		OpenedAt: time.Now()}, "/c/hanko", time.Now(), time.UTC)
	if row.KindText != "Rotação de segredo" {
		t.Fatalf("rótulo do alerta = %q", row.KindText)
	}
	if row.Href != "/c/hanko/configuracoes/segredos/saude" {
		t.Fatalf("link do alerta = %q", row.Href)
	}
}
