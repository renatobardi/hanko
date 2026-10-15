package acp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Policy is the operator's decision about running ACP agents (ADR-0023, addendum). An ACP persona
// executes a command on the server machine, and who writes adapter_config is a company admin, not
// the operator: so it is off by default (HANKO_ACP_ALLOW) and can be narrowed to a list of
// executables (HANKO_ACP_COMMANDS).
type Policy struct {
	// Allow turns the acp harness on. The zero value refuses every command.
	Allow bool
	// Commands, when not empty, restricts Allow to these executables: a bare name matches only a
	// bare command of the same name (resolved through PATH); an absolute path matches only that path.
	Commands []string
}

// Check is nil when command may run under the policy. The text is meant to be shown to people.
func (p Policy) Check(command string) error {
	if !p.Allow {
		return errors.New("o harness acp está desligado neste servidor (o operador precisa definir HANKO_ACP_ALLOW=true)")
	}
	if len(p.Commands) == 0 {
		return nil
	}
	command = strings.TrimSpace(command)
	for _, c := range p.Commands {
		if c = strings.TrimSpace(c); c != "" && sameCommand(c, command) {
			return nil
		}
	}
	return fmt.Errorf("o comando %q não está na lista HANKO_ACP_COMMANDS deste servidor", command)
}

func sameCommand(allowed, command string) bool {
	if filepath.IsAbs(allowed) {
		return filepath.IsAbs(command) && filepath.Clean(allowed) == filepath.Clean(command)
	}
	// A bare name; anything with a separator (./x, sub/x) is a path relative to the working directory.
	return !strings.ContainsRune(command, filepath.Separator) && allowed == command
}
