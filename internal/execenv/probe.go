package execenv

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Tempo máximo de um teste de conexão (o do sandbox inclui baixar a imagem).
const (
	ProbeSSHTimeout     = time.Minute
	ProbeSandboxTimeout = 5 * time.Minute
	ProbeCloudTimeout   = 6 * time.Minute
)

// probeBins são os binários procurados no alvo.
var probeBins = []string{"git", "claude", "codex"}

// probeScript é o script POSIX do teste: sistema, diretório gravável (só para SSH, com root) e binários.
func probeScript(root string) string { return probeScriptURL(root, "") }

// probeScriptURL acrescenta a medida que importa para um alvo remoto: ele alcança o Hanko pela URL pública (é por
// ela que o harness chega ao MCP)? Sem curl nem wget a medida fica "desconhecida".
func probeScriptURL(root, hankoURL string) string {
	var b strings.Builder
	b.WriteString(`echo "uname=$(uname -sm 2>/dev/null)"; `)
	if root != "" {
		r := Quote(root)
		b.WriteString("if mkdir -p " + r + " 2>/dev/null && [ -d " + r + " ] && [ -w " + r + " ]; then echo root=ok; else echo root=fail; fi; ")
	}
	b.WriteString(`for b in tar ` + strings.Join(probeBins, " ") + `; do p=$(command -v "$b" 2>/dev/null) || p=; echo "bin:$b=$p"; done`)
	if hankoURL != "" {
		u := Quote(strings.TrimRight(hankoURL, "/") + "/healthz")
		b.WriteString(`; if command -v curl >/dev/null 2>&1; then c=$(curl -sS -m 5 -o /dev/null -w '%{http_code}' ` + u + ` 2>/dev/null); echo "hanko_url=${c:-000}"; ` +
			`elif command -v wget >/dev/null 2>&1; then if wget -q -T 5 -O /dev/null ` + u + ` 2>/dev/null; then echo hanko_url=200; else echo hanko_url=000; fi; ` +
			`else echo hanko_url=unknown; fi`)
	}
	return b.String()
}

// parseProbe lê a saída do probeScript.
func parseProbe(out []byte, rep *domain.ProbeReport, root string) {
	bins := map[string]string{}
	hankoURL := ""
	rootOK := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "uname="):
			rep.System = strings.TrimSpace(strings.TrimPrefix(line, "uname="))
		case line == "root=ok":
			rootOK = true
		case strings.HasPrefix(line, "hanko_url="):
			hankoURL = strings.TrimSpace(strings.TrimPrefix(line, "hanko_url="))
		case strings.HasPrefix(line, "bin:"):
			k, v, _ := strings.Cut(strings.TrimPrefix(line, "bin:"), "=")
			bins[k] = strings.TrimSpace(v)
		}
	}
	rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "system", OK: rep.System != "", Detail: dashEmpty(rep.System)})
	if root != "" {
		d := root
		if !rootOK {
			d = root + ": sem permissão de escrita ou não foi possível criar"
		}
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "workspace_root", OK: rootOK, Detail: d})
	}
	rep.Binaries = map[string]string{}
	tarOK := bins["tar"] != ""
	rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "bin:tar", OK: tarOK, Detail: dashEmpty(bins["tar"])})
	anyHarness := false
	for _, b := range probeBins {
		rep.Binaries[b] = bins[b]
		ok := bins[b] != ""
		if b != "git" && ok {
			anyHarness = true
		}
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "bin:" + b, OK: ok, Detail: dashEmpty(bins[b])})
	}
	if !anyHarness {
		rep.Warnings = append(rep.Warnings, "nenhum harness (claude, codex) no PATH do alvo; personas fake e acp ainda podem rodar")
	}
	// tar é obrigatório para SSH (sincronização); git e os harnesses dependem da persona
	rep.OK = rep.System != "" && (root == "" || rootOK && tarOK)
	if tarOK {
		rep.Capabilities = append(rep.Capabilities, "sync")
	}
	for _, b := range probeBins {
		if bins[b] != "" {
			rep.Capabilities = append(rep.Capabilities, b)
		}
	}
	switch {
	case hankoURL == "":
	case hankoURL == "unknown":
		rep.Warnings = append(rep.Warnings, "sem curl nem wget no alvo: não foi possível medir se ele alcança o Hanko pela URL pública")
	case strings.HasPrefix(hankoURL, "2") || hankoURL == "401" || hankoURL == "403":
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "hanko_url", OK: true, Detail: "HTTP " + hankoURL})
		rep.Capabilities = append(rep.Capabilities, "hanko-reachable")
	default:
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "hanko_url", OK: false, Detail: "o alvo não alcançou o Hanko pela URL pública (HTTP " + hankoURL + ")"})
		rep.Warnings = append(rep.Warnings, "o alvo não alcança o Hanko pela URL pública: o harness remoto não terá MCP")
	}
}

// addDriverCapabilities completa o relatório com o que o driver garante por construção.
func addDriverCapabilities(rep *domain.ProbeReport, t Target) {
	caps := map[string]bool{}
	for _, c := range rep.Capabilities {
		caps[c] = true
	}
	switch t.Driver {
	case domain.EnvLocal, domain.EnvSSH:
		caps["persistent"], caps["network"] = true, true
	case domain.EnvSandbox:
		caps["isolated"] = true
		if t.Sandbox.Network != domain.SandboxNetworkNone {
			caps["network"] = true
		}
	case domain.EnvCloud:
		caps["isolated"] = true
		if t.Cloud.Network != "none" {
			caps["network"] = true
		}
	}
	rep.Capabilities = rep.Capabilities[:0]
	for c := range caps {
		rep.Capabilities = append(rep.Capabilities, c)
	}
	sort.Strings(rep.Capabilities)
}

func dashEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// Probe testa a conexão com o alvo e devolve o relatório. Nunca devolve erro: falhas são passos com ok=false.
func Probe(ctx context.Context, cfg Config, t Target) domain.ProbeReport {
	start := time.Now()
	var rep domain.ProbeReport
	switch t.Driver {
	case domain.EnvSSH:
		rep = probeSSH(ctx, cfg, t)
	case domain.EnvSandbox:
		rep = probeSandbox(ctx, cfg, t)
	case domain.EnvCloud:
		rep = probeCloud(ctx, cfg, t)
	case domain.EnvLocal:
		rep = probeLocal(ctx, cfg, t)
	default:
		rep.Steps = []domain.ProbeStep{{Name: "driver", OK: false, Detail: fmt.Sprintf("driver %q não tem teste remoto", t.Driver)}}
	}
	if t.Driver.Remote() {
		if err := cfg.RemoteURLStatus(); err != nil {
			rep.Warnings = append(rep.Warnings, err.Error())
		}
	}
	addDriverCapabilities(&rep, t)
	if rep.Steps == nil {
		rep.Steps = []domain.ProbeStep{}
	}
	rep.DurationMs = time.Since(start).Milliseconds()
	return rep
}

func probeSSH(ctx context.Context, cfg Config, t Target) domain.ProbeReport {
	var rep domain.ProbeReport
	s, err := openSSH(cfg, t, RunInfo{RunID: "probe", Company: "probe"})
	if err != nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "config", OK: false, Detail: err.Error()})
		return rep
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(ctx, ProbeSSHTimeout)
	defer cancel()
	var out bytes.Buffer
	if err := s.exec(ctx, probeScriptURL(s.t.SSH.Root, urlFor(cfg, true)), nil, &limitedBuffer{b: &out, max: 16 << 10}); err != nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "connect", OK: false, Detail: err.Error()})
		return rep
	}
	rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "connect", OK: true, Detail: s.Describe()})
	parseProbe(out.Bytes(), &rep, s.t.SSH.Root)
	return rep
}

func probeSandbox(ctx context.Context, cfg Config, t Target) domain.ProbeReport {
	var rep domain.ProbeReport
	if err := cfg.SandboxStatus(); err != nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "config", OK: false, Detail: err.Error()})
		return rep
	}
	c := t.Sandbox
	if err := c.Normalize(); err != nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "config", OK: false, Detail: err.Error()})
		return rep
	}
	if !c.Pinned() {
		rep.Warnings = append(rep.Warnings, "imagem sem digest (@sha256:…): o conteúdo pode mudar entre execuções")
	}
	ctx, cancel := context.WithTimeout(ctx, ProbeSandboxTimeout)
	defer cancel()
	s := &sandboxSession{cfg: cfg, t: Target{Name: t.Name, Sandbox: c}}
	var ver bytes.Buffer
	if err := s.docker(ctx, dockerOpTime, &limitedBuffer{b: &ver, max: 1 << 10}, "version", "--format", "{{.Server.Version}}"); err != nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "docker", OK: false, Detail: err.Error()})
		return rep
	}
	rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "docker", OK: true, Detail: "docker " + strings.TrimSpace(ver.String())})
	var out bytes.Buffer
	err := s.docker(ctx, ProbeSandboxTimeout, &limitedBuffer{b: &out, max: 16 << 10}, "run", "--rm", "--network", "none", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--read-only", "--entrypoint", "sh", c.Image, "-c", probeScriptURL("", urlFor(cfg, c.Network != domain.SandboxNetworkNone)))
	if err != nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "image", OK: false, Detail: err.Error()})
		return rep
	}
	rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "image", OK: true, Detail: c.Image})
	parseProbe(out.Bytes(), &rep, "")
	return rep
}

// urlFor devolve a URL pública a medir num alvo remoto ("" quando o alvo não tem rede ou não há URL utilizável).
func urlFor(cfg Config, network bool) string {
	if !network || cfg.RemoteURLStatus() != nil {
		return ""
	}
	return strings.TrimSpace(cfg.RemoteURL)
}
