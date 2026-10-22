// Package teams guarda o catálogo embutido de pacotes de time (hanko.team.v1, ADR-0044): times prontos que se instalam
// numa empresa existente, sempre pausados. O catálogo vem no binário; nada é baixado.
package teams

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"sync"

	"github.com/renatobardi/hanko/internal/export"
)

//go:embed catalog/*.json
var catalogFS embed.FS

var (
	once  sync.Once
	items []export.TeamPackage
	err   error
)

func load() {
	files, e := fs.Glob(catalogFS, "catalog/*.json")
	if e != nil {
		err = e
		return
	}
	for _, f := range files {
		raw, e := catalogFS.ReadFile(f)
		if e != nil {
			err = e
			return
		}
		p, e := Parse(raw)
		if e != nil {
			err = fmt.Errorf("%s: %w", f, e)
			return
		}
		items = append(items, p)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Package.Slug < items[j].Package.Slug })
}

// Parse lê e valida um pacote de time (campos desconhecidos são recusados: um erro de digitação não vira campo ignorado).
func Parse(raw []byte) (export.TeamPackage, error) {
	var p export.TeamPackage
	if len(raw) > export.MaxTeamBytes {
		return p, fmt.Errorf("pacote maior que %d MiB", export.MaxTeamBytes>>20)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&p); e != nil {
		return p, fmt.Errorf("pacote inválido: %w", e)
	}
	if dec.More() {
		return p, fmt.Errorf("pacote inválido: conteúdo depois do JSON")
	}
	if e := p.Validate(); e != nil {
		return p, e
	}
	p.Requires = p.ComputeRequires()
	return p, nil
}

// List devolve os pacotes do catálogo, por slug.
func List() ([]export.TeamPackage, error) {
	once.Do(load)
	return items, err
}

// Get devolve um pacote do catálogo pelo slug.
func Get(slug string) (export.TeamPackage, bool, error) {
	l, e := List()
	if e != nil {
		return export.TeamPackage{}, false, e
	}
	for _, p := range l {
		if p.Package.Slug == slug {
			return p, true, nil
		}
	}
	return export.TeamPackage{}, false, nil
}
