// Comando docsite gera o site estático de documentação (docs/ e docs/en, mais o feature-map) numa pasta local. Não
// publica nada: quem hospeda o resultado decide como (ADR-0061).
//
//	go run ./tools/docsite -out /tmp/hanko-docs [-version v0.3.0]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/renatobardi/hanko/internal/docsite"
)

func main() {
	out := flag.String("out", "dist/docs", "pasta de saída")
	version := flag.String("version", "", "versão no rodapé")
	root := flag.String("root", ".", "raiz do repositório")
	flag.Parse()
	site, err := docsite.Build(os.DirFS(*root), docsite.Options{Version: *version})
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
	for _, w := range site.Warnings {
		fmt.Fprintln(os.Stderr, "aviso:", w)
	}
	if err := site.WriteTo(*out); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
	fmt.Printf("%d páginas em %s\n", len(site.Pages), *out)
}
