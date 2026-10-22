package docsite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
)

// FeatureMapPath é onde mora o feature-map, relativo à raiz do repositório.
const FeatureMapPath = "docs/feature-map.json"

// I18n é um texto nos dois idiomas da documentação.
type I18n struct {
	PT string `json:"pt"`
	EN string `json:"en"`
}

// Surfaces são as superfícies de uma funcionalidade; cada uma é verificável contra o código (ver Validate).
type Surfaces struct {
	// UI são padrões de rota do mux ("GET /c/{company}/tasks"), como escritos em internal/ui.
	UI []string `json:"ui,omitempty"`
	// CLI são caminhos de comando ("onboard", "context add").
	CLI []string `json:"cli,omitempty"`
	// API são operationId do OpenAPI.
	API []string `json:"api,omitempty"`
	// MCP são nomes de tool ("hanko_task_create").
	MCP []string `json:"mcp,omitempty"`
}

// Feature é uma funcionalidade com o roteiro de verificação dela.
type Feature struct {
	ID       string   `json:"id"`
	Title    I18n     `json:"title"`
	Surfaces Surfaces `json:"surfaces"`
	// Script é o roteiro em passos curtos: serve de base para o teste e2e e para o cenário de eval.
	Script I18n `json:"script"`
	// E2E são os arquivos de teste de jornada (relativos à raiz) que cobrem a funcionalidade.
	E2E []string `json:"e2e,omitempty"`
	// Evals são identificadores de cenários de eval de comportamento (texto livre; ainda não verificados contra um runner).
	Evals []string `json:"evals,omitempty"`
	// Status: covered (há jornada e2e), partial (só testes de unidade/integração) ou gap (sem teste automatizado de jornada).
	Status string `json:"status"`
}

// Domain agrupa funcionalidades de uma área do produto.
type Domain struct {
	ID       string    `json:"id"`
	Title    I18n      `json:"title"`
	ADRs     []string  `json:"adrs,omitempty"`
	Features []Feature `json:"features"`
}

// FeatureMap é o mapa de funcionalidades por domínio.
type FeatureMap struct {
	Version int      `json:"version"`
	Domains []Domain `json:"domains"`
}

// LoadFeatureMap lê e valida o formato (campos desconhecidos são recusados).
func LoadFeatureMap(root fs.FS) (FeatureMap, error) {
	raw, err := fs.ReadFile(root, FeatureMapPath)
	if err != nil {
		return FeatureMap{}, err
	}
	return ParseFeatureMap(raw)
}

// ParseFeatureMap decodifica o JSON do feature-map.
func ParseFeatureMap(raw []byte) (FeatureMap, error) {
	var fm FeatureMap
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fm); err != nil {
		return fm, fmt.Errorf("feature-map inválido: %w", err)
	}
	if fm.Version != 1 {
		return fm, fmt.Errorf("feature-map: versão %d não suportada", fm.Version)
	}
	return fm, nil
}

// Refs é o que existe de fato no código, contra o qual o feature-map é conferido.
type Refs struct {
	UIRoutes   map[string]bool
	CLIPaths   map[string]bool
	APIOps     map[string]bool
	MCPTools   map[string]bool
	ADRNumbers map[string]bool
	FileExists func(path string) bool
}

var idRe = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9-]+)*$`)

// Stats resume a cobertura.
type Stats struct {
	Features, Covered, Partial, Gap int
}

// Stats conta as funcionalidades por status.
func (fm FeatureMap) Stats() Stats {
	var s Stats
	for _, d := range fm.Domains {
		for _, f := range d.Features {
			s.Features++
			switch f.Status {
			case "covered":
				s.Covered++
			case "partial":
				s.Partial++
			default:
				s.Gap++
			}
		}
	}
	return s
}

// Validate confere o feature-map contra o código: ids únicos, textos nos dois idiomas, ADRs existentes, cada rota/comando/
// operação/tool citado existe, os arquivos de e2e existem e o status é coerente com eles. Devolve todos os problemas.
func (fm FeatureMap) Validate(r Refs) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	seenD, seenF := map[string]bool{}, map[string]bool{}
	for _, d := range fm.Domains {
		if !idRe.MatchString(d.ID) || strings.Contains(d.ID, ".") || seenD[d.ID] {
			bad("domínio %q: id inválido ou repetido", d.ID)
		}
		seenD[d.ID] = true
		if strings.TrimSpace(d.Title.PT) == "" || strings.TrimSpace(d.Title.EN) == "" {
			bad("domínio %s: título nos dois idiomas é obrigatório", d.ID)
		}
		for _, a := range d.ADRs {
			if !r.ADRNumbers[a] {
				bad("domínio %s: ADR-%s não existe em docs/adr", d.ID, a)
			}
		}
		if len(d.Features) == 0 {
			bad("domínio %s: sem funcionalidades", d.ID)
		}
		for _, f := range d.Features {
			if !idRe.MatchString(f.ID) || !strings.HasPrefix(f.ID, d.ID+".") || seenF[f.ID] {
				bad("funcionalidade %q: id inválido (use <domínio>.<nome>), fora do domínio %s ou repetido", f.ID, d.ID)
			}
			seenF[f.ID] = true
			if strings.TrimSpace(f.Title.PT) == "" || strings.TrimSpace(f.Title.EN) == "" {
				bad("%s: título nos dois idiomas é obrigatório", f.ID)
			}
			if strings.TrimSpace(f.Script.PT) == "" || strings.TrimSpace(f.Script.EN) == "" {
				bad("%s: roteiro nos dois idiomas é obrigatório", f.ID)
			}
			s := f.Surfaces
			if len(s.UI)+len(s.CLI)+len(s.API)+len(s.MCP) == 0 {
				bad("%s: sem nenhuma superfície", f.ID)
			}
			for _, x := range s.UI {
				if !r.UIRoutes[x] {
					bad("%s: rota de UI %q não existe", f.ID, x)
				}
			}
			for _, x := range s.CLI {
				if !r.CLIPaths[x] {
					bad("%s: comando %q não existe na CLI", f.ID, x)
				}
			}
			for _, x := range s.API {
				if !r.APIOps[x] {
					bad("%s: operação %q não existe no OpenAPI", f.ID, x)
				}
			}
			for _, x := range s.MCP {
				if !r.MCPTools[x] {
					bad("%s: tool MCP %q não existe", f.ID, x)
				}
			}
			for _, x := range f.E2E {
				if !r.FileExists(x) {
					bad("%s: arquivo e2e %q não existe", f.ID, x)
				}
			}
			switch f.Status {
			case "covered":
				if len(f.E2E) == 0 {
					bad("%s: status covered exige ao menos um arquivo e2e", f.ID)
				}
			case "partial", "gap":
				if f.Status == "gap" && len(f.E2E) > 0 {
					bad("%s: status gap com e2e listado: use partial ou covered", f.ID)
				}
			default:
				bad("%s: status %q desconhecido (covered, partial, gap)", f.ID, f.Status)
			}
			if slices.Contains(f.Evals, "") {
				bad("%s: eval vazio", f.ID)
			}
		}
	}
	return errors.Join(errs...)
}
