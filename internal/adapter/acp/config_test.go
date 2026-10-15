package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	good, err := ParseConfig(json.RawMessage(`{"command":" my-agent ","args":["--stdio"],"env":{"MY_HOME":"/x"},"permission":"allow","model":" m ","init_timeout_sec":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if good.Command != "my-agent" || good.Model != "m" || good.Policy(false) != PermAllow || good.Policy(true) != PermReadOnly {
		t.Fatalf("good=%+v", good)
	}
	if c, _ := ParseConfig(json.RawMessage(`{"command":"a"}`)); c.Policy(false) != PermReadOnly {
		t.Fatal("default policy must be read_only")
	}

	bad := map[string]string{
		``:                                       `needs adapter_config`,
		`{}`:                                     `"command" is required`,
		`{"command":"  "}`:                       `"command" is required`,
		`{"command":"agent --stdio"}`:            `put the arguments in "args"`,
		`{"command":"a","permision":"x"}`:        `unknown field "permision"; acp accepts command, args, env, permission, model, init_timeout_sec`,
		`{"command":"a","permission":"yes"}`:     `"permission" must be allow, reject or read_only (got "yes")`,
		`{"command":"a","env":{"HANKO_X":"1"}}`:  `HANKO_* names are reserved`,
		`{"command":"a","env":{"1BAD":"1"}}`:     `invalid variable name`,
		`{"command":"a","init_timeout_sec":999}`: `"init_timeout_sec" must be between 0 and 300`,
		`{"command":"a","args":"x"}`:             `invalid acp adapter_config`,
		`[1]`:                                    `invalid acp adapter_config`,
	}
	for raw, want := range bad {
		if _, err := ParseConfig(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err=%v, want substring %q", raw, err, want)
		}
	}
	if _, err := ParseConfig(json.RawMessage(`{"command":"/opt/my tools/agent"}`)); err != nil {
		t.Errorf("absolute path with space must be accepted: %v", err)
	}
	many := `{"command":"a","args":[` + strings.Repeat(`"x",`, 64) + `"x"]}`
	if _, err := ParseConfig(json.RawMessage(many)); err == nil || !strings.Contains(err.Error(), `"args" accepts at most 64`) {
		t.Errorf("too many args: %v", err)
	}
}

func TestConvertMCP(t *testing.T) {
	raw := json.RawMessage(`{"mcpServers":{
		"z":{"type":"sse","url":"https://s/sse","headers":{"K":"v"}},
		"a":{"command":"run","env":{"B":"2","A":"1"}},
		"h":{"url":"https://h/mcp"},
		"weird":{"type":"ws"},
		"nocmd":{"type":"stdio"}}}`)
	got, warns := convertMCP(raw, mcpCaps{HTTP: true})
	b, _ := json.Marshal(got)
	want := `[{"args":[],"command":"run","env":[{"name":"A","value":"1"},{"name":"B","value":"2"}],"name":"a"},` +
		`{"headers":[],"name":"h","type":"http","url":"https://h/mcp"}]`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	if len(warns) != 3 {
		t.Fatalf("warns=%v", warns)
	}
	got, _ = convertMCP(raw, mcpCaps{SSE: true})
	if len(got) != 2 || got[1]["type"] != "sse" {
		t.Fatalf("sse: %v", got)
	}
	if g, w := convertMCP(nil, mcpCaps{}); len(g) != 0 || g == nil || w != nil {
		t.Fatalf("nil config: %v %v", g, w)
	}
	if g, w := convertMCP(json.RawMessage(`{bad`), mcpCaps{}); len(g) != 0 || len(w) != 1 {
		t.Fatalf("bad config: %v %v", g, w)
	}
}
