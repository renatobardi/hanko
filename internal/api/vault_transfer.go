package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/service"
)

// Import de .env e export cifrado do cofre (fatia 7, #62; ADR-0068 §8). Gravar e exportar exigem admin e reautenticação
// recente (ADR-0070): o service devolve reauth_required e a UI abre o pedido de prova. Nenhuma resposta leva valor.

// maxVaultImportBytes é o maior arquivo .hkv aceito no import: 500 segredos de até 32 campos não passam disso na
// prática, e o limite protege o processo de um corpo sem fim.
const maxVaultImportBytes = 96 << 20

func envInput(content, mode, itemName, collection string, overwrite bool) service.EnvImportInput {
	return service.EnvImportInput{Content: content, Mode: mode, ItemName: itemName, Collection: collection, Overwrite: overwrite}
}

func importResultOut(r service.VaultImportResult) VaultImportResult {
	return VaultImportResult{Secrets: r.Secrets, Fields: r.Fields, Updated: r.Updated, Existing: r.Existing,
		Collections: r.Collections, Grants: r.Grants, Skipped: r.Skipped}
}

func envPlanOut(p service.EnvImportPlan) EnvImportPlan {
	out := EnvImportPlan{Items: make([]EnvImportItem, 0, len(p.Items)), Conflicts: p.Conflicts, Collection: p.Collection}
	for _, it := range p.Items {
		out.Items = append(out.Items, EnvImportItem{Secret: it.Secret, Fields: it.Fields, Exists: it.Exists})
	}
	for _, sk := range p.Skipped {
		out.Skipped = append(out.Skipped, fmt.Sprintf("linha %d: %s", sk.Line, sk.Reason))
	}
	return out
}

func (s *server) registerPortability(api huma.API) {
	tag := "vault"
	huma.Register(api, op("preview-env-import", http.MethodPost, "/companies/{company}/vault/import-env/preview", tag,
		"Prévia do import de um .env (admin; não grava)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *envImportIn) (*ItemOut[EnvImportPlan], error) {
			b := in.Body
			p, err := vb.PreviewEnvImport(ctx, sc.Company.ID, envInput(b.Content, b.Mode, b.ItemName, b.Collection, b.Overwrite))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[EnvImportPlan]{Body: envPlanOut(p)}, nil
		}))
	huma.Register(api, op("apply-env-import", http.MethodPost, "/companies/{company}/vault/import-env", tag,
		"Importa um .env para o cofre (admin; exige reautenticação recente; tudo ou nada)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *envImportIn) (*ItemOut[VaultImportResult], error) {
			b := in.Body
			r, err := vb.ImportEnv(ctx, sc.Actor, sc.Company.ID, envInput(b.Content, b.Mode, b.ItemName, b.Collection, b.Overwrite))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[VaultImportResult]{Body: importResultOut(r)}, nil
		}))
	huma.Register(api, op("export-vault", http.MethodPost, "/companies/{company}/vault/export", tag,
		"Exporta o cofre cifrado com senha (admin; exige reautenticação recente)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *exportVaultIn) (*ItemOut[VaultExport], error) {
			r, err := vb.ExportVault(ctx, sc.Actor, sc.Company.ID, in.Body.Password)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			sum := sha256.Sum256(r.File)
			return &ItemOut[VaultExport]{Body: VaultExport{FileBase64: base64.StdEncoding.EncodeToString(r.File),
				SHA256: hex.EncodeToString(sum[:]), Secrets: r.Secrets}}, nil
		}))
	importVault := op("import-vault", http.MethodPost, "/companies/{company}/vault/import", tag,
		"Importa um cofre exportado por outra instância (admin; exige reautenticação recente; tudo ou nada)")
	importVault.MaxBodyBytes = maxVaultImportBytes
	huma.Register(api, importVault,
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *importVaultIn) (*ItemOut[VaultImportResult], error) {
			file, err := base64.StdEncoding.DecodeString(in.Body.FileBase64)
			if err != nil {
				return nil, problem(http.StatusUnprocessableEntity, "file_base64 não é base64 válido")
			}
			r, err := vb.ImportVault(ctx, sc.Actor, sc.Company.ID, file, in.Body.Password)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[VaultImportResult]{Body: importResultOut(r)}, nil
		}))
}
