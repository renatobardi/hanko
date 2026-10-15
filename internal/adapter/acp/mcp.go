package acp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type mcpCaps struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

// mcpEntry is one entry of Spec.MCPConfig ({"mcpServers":{name:{...}}}): a stdio server
// (command/args/env) or a remote one (type http|sse, url, headers).
type mcpEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type nameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// convertMCP turns Spec.MCPConfig into the ACP mcpServers array. Remote servers are passed only
// when the agent announced the matching mcpCapabilities; the others are skipped and reported in
// warnings (never with the URL or headers, which may carry credentials).
func convertMCP(raw json.RawMessage, caps mcpCaps) (servers []map[string]any, warnings []string) {
	servers = []map[string]any{} // ACP requires an array, never null
	if len(strings.TrimSpace(string(raw))) == 0 {
		return servers, nil
	}
	var cfg struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return servers, []string{"configuração MCP ilegível; nenhum servidor MCP repassado ao agente"}
	}
	names := make([]string, 0, len(cfg.Servers))
	for n := range cfg.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		var e mcpEntry
		if err := json.Unmarshal(cfg.Servers[name], &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("servidor MCP %q ignorado: configuração inválida", name))
			continue
		}
		typ := strings.ToLower(e.Type)
		if typ == "" {
			switch {
			case e.Command != "":
				typ = "stdio"
			case e.URL != "":
				typ = "http"
			}
		}
		switch typ {
		case "stdio":
			if e.Command == "" {
				warnings = append(warnings, fmt.Sprintf("servidor MCP %q ignorado: sem command", name))
				continue
			}
			args := e.Args
			if args == nil {
				args = []string{}
			}
			servers = append(servers, map[string]any{"name": name, "command": e.Command, "args": args, "env": sortedPairs(e.Env)})
		case "http", "sse":
			if (typ == "http" && !caps.HTTP) || (typ == "sse" && !caps.SSE) {
				warnings = append(warnings, fmt.Sprintf("servidor MCP %q ignorado: o agente não anuncia suporte a MCP %s", name, typ))
				continue
			}
			if e.URL == "" {
				warnings = append(warnings, fmt.Sprintf("servidor MCP %q ignorado: sem url", name))
				continue
			}
			servers = append(servers, map[string]any{"type": typ, "name": name, "url": e.URL, "headers": sortedPairs(e.Headers)})
		default:
			warnings = append(warnings, fmt.Sprintf("servidor MCP %q ignorado: tipo %q não suportado", name, e.Type))
		}
	}
	return servers, warnings
}

func sortedPairs(m map[string]string) []nameValue {
	out := make([]nameValue, 0, len(m))
	for k, v := range m {
		out = append(out, nameValue{Name: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
