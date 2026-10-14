package procrun

import (
	"strings"
	"testing"
)

func TestHarnessEnv(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "HOME=/home/x", "LC_ALL=C", "https_proxy=http://p", "ANTHROPIC_API_KEY=ak", "XDG_CONFIG_HOME=/c",
		"HANKO_SECRET=s", "HANKO_DATABASE_URL=postgres://u:p@h/d", "HANKO_MEMORY_TOKEN=m",
		"AWS_SECRET_ACCESS_KEY=aws", "DATABASE_URL=x", "MY_TOOL_HOME=/t", "MY_TOOL_KEY=k", "BROKEN",
	}
	got := HarnessEnv(environ, []string{"MY_TOOL_HOME", "MY_TOOL_*", "HANKO_SECRET"}, map[string]string{"HANKO_RUN_TOKEN": "rt", "PATH": "/override"})
	m := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	for k, want := range map[string]string{"PATH": "/override", "HOME": "/home/x", "LC_ALL": "C", "https_proxy": "http://p",
		"ANTHROPIC_API_KEY": "ak", "XDG_CONFIG_HOME": "/c", "HANKO_RUN_TOKEN": "rt", "MY_TOOL_HOME": "/t", "MY_TOOL_KEY": "k"} {
		if m[k] != want {
			t.Errorf("%s = %q, quer %q", k, m[k], want)
		}
	}
	for _, k := range []string{"HANKO_SECRET", "HANKO_DATABASE_URL", "HANKO_MEMORY_TOKEN", "AWS_SECRET_ACCESS_KEY", "DATABASE_URL", "BROKEN"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s não deveria passar", k)
		}
	}
}
