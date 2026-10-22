// Command showcase é um plugin de exemplo do SDK Go do Hanko (sdk/plugin, docs/PLUGINS.md): uma tool para as
// personas, uma aba (página em iframe sandboxed) com ponte, um gatilho de fluxo e uma fonte de skills.
//
// Para instalar: go build -o showcase . dentro deste diretório e copie o diretório para HANKO_PLUGINS_DIR/showcase.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"strings"

	"github.com/renatobardi/hanko/sdk/plugin"
)

// painel é o HTML da aba. Roda num iframe sandboxed, sem rede: a única saída é window.hanko.call.
const painel = `<!doctype html>
<meta charset="utf-8">
<style>body{font:14px system-ui;margin:16px}button{padding:6px 12px}</style>
<h2>Showcase</h2>
<p>Olá, %s. Pedidos registrados nesta empresa: <b id="n">%d</b>.</p>
<button id="b">Registrar pedido</button>
<script>
document.getElementById("b").onclick = async () => {
  const r = await window.hanko.call("registrar", {});
  document.getElementById("n").textContent = r.total;
};
</script>`

const skillMD = `---
name: Checklist de pedido
description: Confere os dados de um pedido antes de aceitar.
---
# Checklist de pedido

1. Confirme o cliente.
2. Confirme os itens e as quantidades.
3. Só então registre o pedido.
`

func main() {
	p := plugin.New("showcase")

	p.Tool("ping", func(_ context.Context, _ *plugin.Host, c plugin.ToolCall) (plugin.ToolResult, error) {
		var in struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(c.Arguments, &in)
		return plugin.ToolResult{Text: "pong: " + strings.TrimSpace(in.Text)}, nil
	})

	p.Page("painel", func(ctx context.Context, h *plugin.Host, r plugin.PageRequest) (string, error) {
		var total int
		_, _ = h.StateGet(ctx, r.Company, "pedidos", &total)
		return fmt.Sprintf(painel, html.EscapeString(r.User.Name), total), nil
	})

	// A ponte: o Hanko entrega quem chamou (c.User) e o plugin decide o que permitir.
	p.Bridge(func(ctx context.Context, h *plugin.Host, c plugin.BridgeCall) (any, error) {
		if c.Method != "registrar" {
			return nil, plugin.Errorf(plugin.CodeInvalidParams, "método desconhecido: %s", c.Method)
		}
		if c.User.Role == "viewer" {
			return nil, plugin.Errorf(plugin.CodeDenied, "só membros registram pedidos")
		}
		var total int
		_, _ = h.StateGet(ctx, c.Company, "pedidos", &total)
		total++
		if err := h.StateSet(ctx, c.Company, "pedidos", total); err != nil {
			return nil, err
		}
		fired, err := h.FireTrigger(ctx, c.Company, "novo-pedido")
		if err != nil {
			return nil, err
		}
		return map[string]any{"total": total, "fluxos": fired}, nil
	})

	p.SkillSource("catalogo", func(context.Context, *plugin.Host, plugin.Company) (plugin.SkillSnapshot, error) {
		return plugin.SkillSnapshot{Revision: "1", Skills: []plugin.SkillFile{{Path: "checklist-de-pedido", SkillMD: skillMD}}}, nil
	})

	if err := p.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
