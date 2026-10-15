// Package artifactsrv entrega o conteúdo de um artefato ao navegador com segurança (ADR-0028). O conteúdo é de
// terceiros (uma persona, um upload), então nunca é interpretado como página: nosniff, CSP que proíbe tudo e
// download (attachment) para qualquer tipo que não seja texto puro, markdown ou imagem raster.
package artifactsrv

import (
	"mime"
	"net/http"
	"strconv"

	"github.com/renatobardi/hanko/internal/domain"
)

// csp proíbe carregar ou executar qualquer coisa a partir do documento servido, e o isola numa sandbox.
const csp = "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox"

// Disposition devolve o valor de Content-Disposition: inline só para os tipos seguros, attachment para o resto.
// O nome do arquivo vai codificado por mime.FormatMediaType (sem injeção de cabeçalho).
func Disposition(a domain.Artifact) string {
	if domain.InlineSafe(a.Mime) {
		return disposition("inline", a.Name)
	}
	return disposition("attachment", a.Name)
}

func disposition(kind, name string) string {
	if v := mime.FormatMediaType(kind, map[string]string{"filename": name}); v != "" {
		return v
	}
	return kind // nome que o formato não aceita: sem filename
}

// ContentType é o Content-Type da resposta: o MIME do artefato, com charset para texto.
func ContentType(a domain.Artifact) string {
	if domain.IsText(a.Mime) {
		return a.Mime + "; charset=utf-8"
	}
	return a.Mime
}

// Headers chama set para cada cabeçalho de segurança e de conteúdo do artefato.
func Headers(set func(k, v string), a domain.Artifact) {
	set("Content-Type", ContentType(a))
	set("Content-Length", strconv.FormatInt(a.SizeBytes, 10))
	set("Content-Disposition", Disposition(a))
	set("X-Content-Type-Options", "nosniff")
	set("Content-Security-Policy", csp)
	set("Cross-Origin-Resource-Policy", "same-origin")
	set("Cache-Control", "private, no-cache")
	set("ETag", `"`+a.SHA256+`"`)
}

// Serve escreve o artefato na resposta.
func Serve(w http.ResponseWriter, a domain.Artifact, content []byte) { ServeAs(w, a, content, false) }

// ServeAs é Serve com a opção de forçar o download (attachment), mesmo para os tipos que abrem no navegador.
func ServeAs(w http.ResponseWriter, a domain.Artifact, content []byte, download bool) {
	Headers(w.Header().Set, a)
	if download {
		w.Header().Set("Content-Disposition", disposition("attachment", a.Name))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
