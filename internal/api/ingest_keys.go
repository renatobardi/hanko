package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
)

// Chaves de ingestão (ADR-0076 item 3): uma por host, presas à empresa; só gravam telemetria. Só owner e admin.

type listIngestKeysIn struct {
	CompanyPath
	Page
	IncludeRevoked bool `query:"include_revoked" default:"false" doc:"Inclui as revogadas."`
}

type createIngestKeyIn struct {
	CompanyPath
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100" doc:"Por convenção, o nome do host."`
	}
}

func ingestKeyOut(k domain.IngestKey) IngestKey {
	return IngestKey{ID: k.ID.String(), Name: k.Name, Prefix: k.Prefix, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt}
}

func (s *server) registerIngestKeys(api huma.API) {
	const tag = "external-source"
	b := "/companies/{company}/ingest-keys"

	huma.Register(api, op("list-ingest-keys", http.MethodGet, b, tag, "Lista as chaves de ingestão da empresa (owner ou admin)"),
		func(ctx context.Context, in *listIngestKeysIn) (*ListOut[IngestKey], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			keys, err := s.svc.ListIngestKeys(ctx, sc.Company.ID, in.IncludeRevoked, in.Limit, in.Offset)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			items := make([]IngestKey, len(keys))
			for i, k := range keys {
				items[i] = ingestKeyOut(k)
			}
			return &ListOut[IngestKey]{Body: ListBody[IngestKey]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
		})

	huma.Register(api, created(op("create-ingest-key", http.MethodPost, b, tag,
		"Cria uma chave de ingestão; o token aparece uma vez (owner ou admin)")),
		func(ctx context.Context, in *createIngestKeyIn) (*ItemOut[IngestKeyCreated], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			token, k, err := s.svc.CreateIngestKey(ctx, sc.Actor, sc.Company.ID, in.Body.Name)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[IngestKeyCreated]{Body: IngestKeyCreated{IngestKey: ingestKeyOut(k), Token: token}}, nil
		})

	del := op("revoke-ingest-key", http.MethodDelete, b+"/{key}", tag, "Revoga uma chave de ingestão (owner ou admin)")
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *IngestKeyPath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		id, perr := domain.ParseID(in.Key)
		if perr != nil {
			return nil, problem(http.StatusNotFound, "chave não encontrada")
		}
		if _, err := s.svc.RevokeIngestKey(ctx, sc.Actor, sc.Company.ID, id); err != nil {
			return nil, s.fail(ctx, err)
		}
		return nil, nil
	})
}
