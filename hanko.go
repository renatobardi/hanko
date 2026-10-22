// Package hanko guarda os arquivos da raiz do repositório que o binário carrega consigo (hoje, o CHANGELOG, de onde a
// UI lê o "O que há de novo" da versão instalada, sem rede; ADR-0060).
package hanko

import _ "embed"

// Changelog é o CHANGELOG.md da versão que foi compilada.
//
//go:embed CHANGELOG.md
var Changelog string
