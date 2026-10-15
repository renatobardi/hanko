package ui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func TestHireACPPersona(t *testing.T) {
	t.Run("form offers the ACP harness", func(t *testing.T) {
		rec := newEnv(t, uitest.Sample()).get(t, "/c/hanko/personas/nova")
		wantContains(t, rec, `value="acp"`, `name="acp_command"`, `name="acp_args"`, `name="acp_permission"`,
			`value="read_only" selected`, "Somente leitura (padrão)", "Permitir tudo", "Recusar tudo")
	})
	t.Run("hire stores the adapter_config", func(t *testing.T) {
		f := uitest.Sample()
		e := newEnv(t, f)
		rec := e.post(t, "/c/hanko/personas/nova", url.Values{"name": {"Agente Externo"}, "agent": {"acp"},
			"acp_command": {" meu-agente "}, "acp_args": {"--stdio\r\n\r\n --verbose \n"}, "acp_permission": {"allow"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		p := personaOf(f, "agente-externo")
		if p == nil || p.Agent != "acp" {
			t.Fatalf("persona = %+v", p)
		}
		var cfg map[string]any
		if err := json.Unmarshal(p.AdapterConfig, &cfg); err != nil {
			t.Fatal(err)
		}
		want := `{"args":["--stdio","--verbose"],"command":"meu-agente","permission":"allow"}`
		if got, _ := json.Marshal(cfg); string(got) != want {
			t.Errorf("adapter_config = %s, want %s", got, want)
		}
	})
	t.Run("missing command is refused with a friendly message", func(t *testing.T) {
		f := uitest.Sample()
		rec := newEnv(t, f).post(t, "/c/hanko/personas/nova", url.Values{"name": {"Sem Comando"}, "agent": {"acp"}, "acp_permission": {"reject"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "Informe o comando do harness ACP.", `value="reject" selected`)
		if f.Called("HirePersona") {
			t.Error("HirePersona called with an invalid ACP config")
		}
	})
	t.Run("command with arguments is refused", func(t *testing.T) {
		rec := newEnv(t, uitest.Sample()).post(t, "/c/hanko/personas/nova", url.Values{"name": {"Espaço"}, "agent": {"acp"}, "acp_command": {"agente --stdio"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "O comando deve ser só o executável")
	})
	t.Run("ACP is not an alternate reviewer harness", func(t *testing.T) {
		f := uitest.Sample()
		rec := newEnv(t, f).post(t, "/c/hanko/personas/kenji/revisor", url.Values{"mode": {"harness"}, "reviewer_agent": {"acp"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "O ACP não pode ser o harness alternativo do revisor")
	})
}
