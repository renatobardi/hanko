package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// OpenAPI do listener da ingestão (ADR-0076). Essas rotas ficam num listener próprio (`HANKO_INGEST_ADDR`), na tailnet,
// e só aceitam a chave de escopo `ingest`; o listener público não as tem, por isso não entram em `api/openapi.json`.
// Este documento só descreve o contrato (o OTLP/HTTP em protobuf, o replay do bucket e a prontidão); a ingestão em si
// vem na fatia 1 (#125).

// SpecIngest devolve o OpenAPI da ingestão, indentado e estável, sem precisar de banco.
func SpecIngest() ([]byte, error) {
	mux := http.NewServeMux()
	c := huma.DefaultConfig("Hanko Ingest API", SpecVersion)
	c.Info.Description = "Ingestão de telemetria de agente externo (ADR-0076): OTLP/HTTP em protobuf, replay do bucket e " +
		"prontidão. Listener próprio, só na tailnet, só com a chave de escopo `ingest`."
	c.Info.License = &huma.License{Name: "Apache-2.0", Identifier: "Apache-2.0"}
	c.CreateHooks = nil
	c.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"ingestKey": {Type: "http", Scheme: "bearer", BearerFormat: "hk_…",
			Description: "Chave de escopo `ingest`: uma por host, presa a uma empresa. Só grava telemetria; não lê nada."},
	}
	c.Security = []map[string][]string{{"ingestKey": {}}}
	api := humago.New(wrapMux{mux: mux, wrap: func(h http.Handler) http.Handler { return h }}, c)
	registerIngestDoc(api)
	b, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("openapi da ingestão: %w", err)
	}
	return append(b, '\n'), nil
}

func registerIngestDoc(api huma.API) {
	retry := map[string]*huma.Param{"Retry-After": {Description: "Segundos até tentar de novo.", Schema: &huma.Schema{Type: "string"}}}
	problem := func(d string) *huma.Response { return &huma.Response{Description: d} }
	busy := &huma.Response{Description: "Ingestão cheia ou gravação falhou. O collector retenta sem prazo.", Headers: retry}
	denied := map[string]*huma.Response{
		"401": problem("Sem chave, ou chave inválida ou revogada."),
		"403": problem("A chave não tem o escopo `ingest`."),
	}
	merge := func(extra map[string]*huma.Response) map[string]*huma.Response {
		out := map[string]*huma.Response{}
		for k, v := range denied {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	for _, sig := range []struct{ id, path, what string }{
		{"ingest-traces", "/v1/traces", "spans"},
		{"ingest-logs", "/v1/logs", "logs"},
		{"ingest-metrics", "/v1/metrics", "métricas"},
	} {
		api.OpenAPI().AddOperation(&huma.Operation{
			OperationID: sig.id,
			Method:      http.MethodPost,
			Path:        sig.path,
			Tags:        []string{"ingest"},
			Summary:     "Recebe " + sig.what + " em OTLP/HTTP (protobuf)",
			Description: "Aceita só `application/x-protobuf`, com gzip. Os fatos são gravados com a empresa da chave e a chave " +
				"que os entregou; o host é o declarado no fato. Repetir um fato não duplica nada. O 2xx só vem depois do commit. " +
				"Contrato da fonte externa (ADR-0076). Responde 501 até a fatia 1 (#125).",
			Parameters: []*huma.Param{{Name: "Content-Encoding", In: "header", Description: "`gzip` ou ausente.",
				Schema: &huma.Schema{Type: "string", Enum: []any{"gzip"}}}},
			RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
				"application/x-protobuf": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}},
			Responses: merge(map[string]*huma.Response{
				"200": {Description: "Aceito e gravado.", Content: map[string]*huma.MediaType{
					"application/x-protobuf": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}},
				"400": problem("Corpo inválido, ou acima de 64 MB descomprimido. O collector não retenta."),
				"501": problem("Ainda não implementada."),
				"503": busy,
			}),
		})
	}
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "ingest-replay",
		Method:      http.MethodPost,
		Path:        "/v1/replay/{signal}",
		Tags:        []string{"ingest"},
		Summary:     "Reenvia um objeto do bucket de telemetria",
		Description: "Aceita o objeto do bucket como ele é (`otlp_json`, com ou sem gzip). É a única entrada de JSON. Quem lê o " +
			"bucket é a fonte; o Hanko nunca guarda credencial do bucket. Contrato da fonte externa (ADR-0076). " +
			"Responde 501 até a fatia 1 (#125).",
		Parameters: []*huma.Param{
			{Name: "signal", In: "path", Required: true, Description: "O sinal do objeto.",
				Schema: &huma.Schema{Type: "string", Enum: []any{"traces", "logs", "metrics"}}},
			{Name: "Content-Encoding", In: "header", Description: "`gzip` ou ausente.", Schema: &huma.Schema{Type: "string", Enum: []any{"gzip"}}},
		},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
			"application/json": {Schema: &huma.Schema{Type: "object"}}}},
		Responses: merge(map[string]*huma.Response{
			"200": problem("Aceito e gravado."),
			"400": problem("Corpo inválido, ou acima de 64 MB descomprimido."),
			"501": problem("Ainda não implementada."),
			"503": busy,
		}),
	})
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "ingest-ready",
		Method:      http.MethodGet,
		Path:        "/v1/ready",
		Tags:        []string{"ingest"},
		Summary:     "Prontidão da ingestão",
		Description: "Substitui o `/readyz` do agent-studio. Contrato da fonte externa (ADR-0076). Responde 501 até a fatia 1 (#125).",
		Responses: merge(map[string]*huma.Response{
			"200": problem("Pronta para receber."),
			"501": problem("Ainda não implementada."),
			"503": problem("Não está pronta."),
		}),
	})
}
