package i18n

// enM18 holds the English texts of M18 (ADR-0045): plugin tabs, capability grants, flow triggers, environment
// providers, skill sources and the plugin scaffold. Same rules as en.go: the key is the pt-BR text, and a key
// another catalog already has is not repeated here.
var enM18 = map[string]string{
	// CLI
	"Abas":                   "Tabs",
	"Fontes de skills":       "Skill sources",
	"Gatilhos":               "Triggers",
	"Providers de ambiente":  "Environment providers",
	"<id> <capacidade>":      "<id> <capability>",
	"<id> <gatilho> <fluxo>": "<id> <trigger> <flow>",
	"ABA":                    "TAB",
	"FLUXOS LIGADOS":         "LINKED FLOWS",
	"PLUGIN":                 "PLUGIN",
	"Concede ou revoga capacidades de um plugin (dono da instalação)":                                         "Grants or revokes the capabilities of a plugin (installation owner)",
	"Concede uma capacidade declarada no manifesto do plugin":                                                 "Grants a capability declared in the plugin manifest",
	"Revoga uma capacidade: o que depende dela deixa de funcionar na hora":                                    "Revokes a capability: whatever depends on it stops working right away",
	"Cria o esqueleto de um plugin em Go (manifesto, main.go com o SDK e README)":                             "Creates the skeleton of a Go plugin (manifest, main.go using the SDK and a README)",
	"Desliga o gatilho de um fluxo":                                                                           "Unlinks the trigger from a flow",
	"Gatilho %s do plugin %s desligado do fluxo %s.\n":                                                        "Trigger %s of plugin %s unlinked from flow %s.\n",
	"Gatilho %s do plugin %s ligado ao fluxo %s.\n":                                                           "Trigger %s of plugin %s linked to flow %s.\n",
	"Gatilhos de fluxo dos plugins (admin da empresa liga cada gatilho a fluxos)":                             "Flow triggers of plugins (a company admin links each trigger to flows)",
	"Liga o gatilho a um fluxo da empresa (o fluxo roda quando o plugin dispara o gatilho)":                   "Links the trigger to a company flow (the flow runs when the plugin fires the trigger)",
	"Lista as abas que os plugins acrescentam ao menu da empresa":                                             "Lists the tabs plugins add to the company menu",
	"Lista as fontes de skills oferecidas pelos plugins (conecte com: hanko skill source add <url>)":          "Lists the skill sources offered by plugins (connect with: hanko skill source add <url>)",
	"Mostra os gatilhos do plugin e os fluxos ligados a cada um":                                              "Shows the plugin triggers and the flows linked to each",
	"Nenhum plugin oferece fontes de skills nesta empresa.":                                                   "No plugin offers skill sources in this company.",
	"Nenhuma aba de plugin nesta empresa.":                                                                    "No plugin tab in this company.",
	"O plugin não declara gatilhos de fluxo.":                                                                 "The plugin declares no flow triggers.",
	"Plugin %s criado em %s:\n":                                                                               "Plugin %s created in %s:\n",
	"Próximo passo: cd %s && go mod tidy && go build -o %s . e copie o diretório para HANKO_PLUGINS_DIR/%s\n": "Next step: cd %s && go mod tidy && go build -o %s . and copy the directory to HANKO_PLUGINS_DIR/%s\n",
	"autor no manifesto": "author in the manifest",
	"diretório de destino (padrão: ./<id>; precisa estar vazio ou não existir)": "target directory (default: ./<id>; must be empty or not exist)",
	"id inválido: use ^[a-z][a-z0-9-]{1,62}$":                                   "invalid id: use ^[a-z][a-z0-9-]{1,62}$",
	"não conectada":                 "not connected",
	"o diretório %s não está vazio": "directory %s is not empty",

	// UI
	"Abas na barra lateral (a página do plugin roda isolada)": "Sidebar tabs (the plugin page runs isolated)",
	"Capacidade concedida.": "Capability granted.",
	"Capacidade revogada: o que dependia dela deixou de funcionar.":   "Capability revoked: whatever depended on it stopped working.",
	"Disparar fluxos ligados pelo admin":                              "Fire flows linked by the admin",
	"Gatilho desligado do fluxo.":                                     "Trigger unlinked from the flow.",
	"Gatilho ligado ao fluxo.":                                        "Trigger linked to the flow.",
	"Oferecer fontes de skills":                                       "Offer skill sources",
	"Oferecer sandboxes em nuvem (recebe a chave de API do ambiente)": "Offer cloud sandboxes (receives the environment API key)",
	"Chamada inválida.":                                               "Invalid call.",
	"O plugin não respondeu.":                                         "The plugin did not respond.",
	"Página indisponível.":                                            "Page unavailable.",
	"%d aba(s) · %d gatilho(s) de fluxo · %d provider(s) de ambiente · %d fonte(s) de skills": "%d tab(s) · %d flow trigger(s) · %d environment provider(s) · %d skill source(s)",
	"Capacidades concedidas": "Granted capabilities",
	"Conceder":               "Grant",
	"Revogar vale na hora: abas, tools, gatilhos, providers e fontes que dependem da capacidade deixam de funcionar.":     "Revoking takes effect right away: tabs, tools, triggers, providers and sources that depend on the capability stop working.",
	"Esta página é do plugin e roda isolada: sem acesso à sua sessão nem à rede; só fala com o plugin por meio do Hanko.": "This page belongs to the plugin and runs isolated: no access to your session or to the network; it only talks to the plugin through Hanko.",
	"Gatilhos de fluxo": "Flow triggers",
	"Ligar a um fluxo":  "Link to a flow",
	"Nenhum fluxo ligado: disparar o gatilho não faz nada.":                                                          "No flow linked: firing the trigger does nothing.",
	"O plugin dispara o gatilho; os fluxos ligados aqui rodam (cada um cria a sua task). Só o admin liga.":           "The plugin fires the trigger; the flows linked here run (each one creates its task). Only an admin links.",
	"Use a URL abaixo ao conectar uma fonte de skills (Skills › Fontes).":                                            "Use the URL below when connecting a skill source (Skills › Sources).",
	"Use o nome abaixo como provider ao criar um ambiente em nuvem. A chave de API do ambiente é enviada ao plugin.": "Use the name below as the provider when creating a cloud environment. The environment API key is sent to the plugin.",
}

func init() {
	for k, v := range enM18 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
