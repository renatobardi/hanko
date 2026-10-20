package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Ciclo de vida da empresa (ADR-0057): editar, arquivar, restaurar e excluir.

type restoreCompanyIn struct {
	CompanyPath
	Body struct {
		Resume bool `json:"resume,omitempty" doc:"Reativa as personas e os fluxos que o arquivamento pausou/desligou. Padrão: tudo continua pausado."`
	}
}

type deleteCompanyIn struct {
	CompanyPath
	Body struct {
		Confirm string `json:"confirm" minLength:"1" doc:"Nome exato da empresa."`
	}
}

// CompanyDeletion é o registro de uma exclusão; o export automático feito antes fica disponível para baixar.
type CompanyDeletion struct {
	ID          string    `json:"id" format:"uuid"`
	CompanyID   string    `json:"company_id" format:"uuid"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	DeletedAt   time.Time `json:"deleted_at"`
	ExportBytes int64     `json:"export_bytes" doc:"Tamanho do hanko.company.v1 guardado (sem compressão)."`
	SHA256      string    `json:"sha256" doc:"SHA-256 do arquivo sem compressão."`
	ExportPath  string    `json:"export_path" doc:"Caminho para baixar o arquivo (GET)."`
}

func deletionOut(d service.CompanyDeletion) CompanyDeletion {
	return CompanyDeletion{ID: idStr(d.ID), CompanyID: idStr(d.CompanyID), Slug: d.Slug, Name: d.Name, DeletedAt: d.DeletedAt,
		ExportBytes: d.ExportBytes, SHA256: d.SHA256, ExportPath: Prefix + "/company-deletions/" + idStr(d.ID) + "/export"}
}

type deletionPath struct {
	ID string `path:"id" format:"uuid" doc:"Id da exclusão."`
}

func (s *server) registerLifecycle(api huma.API) {
	huma.Register(api, op("archive-company", http.MethodPost, "/companies/{company}/archive", "companies",
		"Arquiva a empresa: pausa as personas, cancela as execuções, desliga os fluxos e passa a aceitar só leitura (admin)"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[Company], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			c, err := s.svc.ArchiveCompany(ctx, sc.Actor, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Company]{Body: companyOut(c)}, nil
		})

	huma.Register(api, op("restore-company", http.MethodPost, "/companies/{company}/restore", "companies",
		"Restaura uma empresa arquivada; personas e fluxos continuam pausados, a menos que resume=true (admin)"),
		func(ctx context.Context, in *restoreCompanyIn) (*ItemOut[Company], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			c, err := s.svc.RestoreCompany(ctx, sc.Actor, sc.Company.ID, in.Body.Resume)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Company]{Body: companyOut(c)}, nil
		})

	huma.Register(api, op("delete-company", http.MethodDelete, "/companies/{company}", "companies",
		"Exclui uma empresa arquivada, depois de guardar o export automático (só owner; exige o nome da empresa)"),
		func(ctx context.Context, in *deleteCompanyIn) (*ItemOut[CompanyDeletion], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			d, err := s.svc.DeleteCompany(ctx, sc.Actor, sc.Company.ID, in.Body.Confirm)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[CompanyDeletion]{Body: deletionOut(d)}, nil
		})

	huma.Register(api, op("list-company-deletions", http.MethodGet, "/company-deletions", "companies",
		"Lista as empresas excluídas e os exports guardados (dono da instalação)"),
		func(ctx context.Context, in *struct{ Page }) (*ListOut[CompanyDeletion], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ds, err := s.svc.ListCompanyDeletions(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(ds, in.Page, deletionOut), nil
		})

	exp := op("get-company-deletion-export", http.MethodGet, "/company-deletions/{id}/export", "companies",
		"Baixa o export (hanko.company.v1) guardado na exclusão (dono da instalação ou quem excluiu)")
	exp.Responses = map[string]*huma.Response{"200": {Description: "Arquivo hanko.company.v1.", Content: map[string]*huma.MediaType{
		"application/json": {Schema: exportDoc{}.Schema(nil)}}}}
	huma.Register(api, exp, func(ctx context.Context, in *deletionPath) (*huma.StreamResponse, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		id, err := domain.ParseID(in.ID)
		if err != nil {
			return nil, s.fail(ctx, domain.ErrNotFound)
		}
		d, gz, err := s.svc.CompanyDeletionExport(ctx, id)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			zr, err := gzip.NewReader(bytes.NewReader(gz))
			if err != nil {
				hctx.SetStatus(http.StatusInternalServerError)
				return
			}
			_, w := humago.Unwrap(hctx)
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(s.xfer.ExportTimeout))
			hctx.SetHeader("Content-Type", "application/json")
			hctx.SetHeader("Content-Disposition", `attachment; filename="`+d.Slug+`.hanko.json"`)
			hctx.SetStatus(http.StatusOK)
			if _, err := io.Copy(hctx.BodyWriter(), zr); err != nil { //nolint:gosec // arquivo gravado pelo próprio servidor na exclusão
				panic(http.ErrAbortHandler) // 200 já enviado: romper a conexão em vez de entregar um arquivo truncado
			}
		}}, nil
	})
}
