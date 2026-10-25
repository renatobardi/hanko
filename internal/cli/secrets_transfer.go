package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Import de .env e export cifrado do cofre (ADR-0068 §8, #62): hanko secret import-env | export | import.

const (
	maxEnvFileBytes   = 256 << 10
	maxVaultFileBytes = 96 << 20
	// vaultPasswordEnv é a variável que guarda a senha do arquivo quando não se usa --password-stdin.
	vaultPasswordEnv = "HANKO_VAULT_PASSWORD"
)

// vaultPassword lê a senha do arquivo .hkv: da primeira linha do stdin (--password-stdin) ou de HANKO_VAULT_PASSWORD.
// Nunca vem de argumento, para não ficar no histórico do shell nem na lista de processos.
func (e *env) vaultPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(io.LimitReader(e.in, 4<<10)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf(e.t("ler stdin: %w"), err)
		}
		pw := strings.TrimRight(line, "\r\n")
		if pw == "" {
			return "", usageError{errors.New(e.t("a senha do arquivo veio vazia no stdin"))}
		}
		return pw, nil
	}
	if pw := e.getenv(vaultPasswordEnv); pw != "" {
		return pw, nil
	}
	return "", usageError{fmt.Errorf(e.t("informe a senha do arquivo com --password-stdin ou %s"), vaultPasswordEnv)}
}

func (e *env) printEnvPlan(p api.EnvImportPlan) {
	fmt.Fprintf(e.out, e.t("Coleção de destino: %s\n"), p.Collection)
	for _, it := range p.Items {
		mark := ""
		if it.Exists {
			mark = e.t(" (já existe)")
		}
		fmt.Fprintf(e.out, "  %s%s: %s\n", it.Secret, mark, strings.Join(it.Fields, ", "))
	}
	for _, s := range p.Skipped {
		fmt.Fprintf(e.out, e.t("  ignorada, %s\n"), s)
	}
}

func (e *env) importEnvCmd() *cobra.Command {
	var item, collection string
	var overwrite, dryRun bool
	c := &cobra.Command{
		Use:   "import-env " + e.t("<arquivo|->"),
		Short: e.t("Importa um .env para o cofre: mostra a prévia e grava (exige reautenticação; --dry-run só mostra)"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var r io.Reader = e.in
			if a[0] != "-" {
				f, err := os.Open(a[0])
				if err != nil {
					return fmt.Errorf(e.t("abrir %s: %w"), a[0], err)
				}
				defer f.Close()
				r = f
			}
			b, err := io.ReadAll(io.LimitReader(r, maxEnvFileBytes+1))
			if err != nil {
				return fmt.Errorf(e.t("ler o .env: %w"), err)
			}
			if len(b) > maxEnvFileBytes {
				return usageError{fmt.Errorf(e.t("o .env passa de %d bytes"), maxEnvFileBytes)}
			}
			req := client.EnvImportReq{Content: string(b), Mode: "items", ItemName: item, Collection: collection, Overwrite: overwrite}
			if item != "" {
				req.Mode = "single_item"
			}
			plan, err := e.client().PreviewEnvImport(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if !e.asJSON {
				e.printEnvPlan(plan)
			}
			if dryRun {
				if e.asJSON {
					return e.printJSON(plan)
				}
				fmt.Fprintln(e.out, e.t("Nada foi gravado (--dry-run)."))
				return nil
			}
			res, err := e.client().ImportEnv(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(res)
			}
			fmt.Fprintf(e.out, e.t("Importado: %d segredo(s) novo(s), %d campo(s), %d atualizado(s), %d já existente(s) mantido(s).\n"),
				res.Secrets, res.Fields, res.Updated, res.Existing)
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&item, "item", "", e.t("um segredo só, com um campo por variável (a primeira vira o campo value); sem isto, um segredo por variável"))
	f.StringVar(&collection, "collection", "", e.t("coleção de destino (nome ou id; padrão: a padrão)"))
	f.BoolVar(&overwrite, "overwrite", false, e.t("rotaciona o que já existe em vez de pular"))
	f.BoolVar(&dryRun, "dry-run", false, e.t("só mostra a prévia, sem gravar"))
	return c
}

func (e *env) exportVaultCmd() *cobra.Command {
	var out string
	var fromStdin, force bool
	c := &cobra.Command{
		Use:   "export",
		Short: e.t("Exporta o cofre cifrado com uma senha, para importar em outra instância (admin; exige reautenticação)"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if out == "" {
				return usageError{errors.New(e.t("informe o arquivo de saída com --out"))}
			}
			pw, err := e.vaultPassword(fromStdin)
			if err != nil {
				return err
			}
			res, err := e.client().ExportVault(cmd.Context(), co, pw)
			if err != nil {
				return err
			}
			data, err := base64.StdEncoding.DecodeString(res.FileBase64)
			if err != nil {
				return fmt.Errorf(e.t("resposta do servidor inválida: %w"), err)
			}
			if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != res.SHA256 {
				return errors.New(e.t("o arquivo recebido não bate com o SHA-256 informado pelo servidor"))
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
			if force {
				flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			}
			f, err := os.OpenFile(out, flags, 0o600)
			if err != nil {
				return fmt.Errorf(e.t("gravar %s (use --force para sobrescrever): %w"), out, err)
			}
			if _, err := f.Write(data); err != nil {
				_ = f.Close()
				return fmt.Errorf(e.t("gravar %s: %w"), out, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf(e.t("gravar %s: %w"), out, err)
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"file": out, "secrets": res.Secrets, "sha256": res.SHA256})
			}
			fmt.Fprintf(e.out, e.t("Cofre exportado em %s: %d segredo(s), SHA-256 %s.\n"), out, res.Secrets, res.SHA256)
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&out, "out", "", e.t("arquivo de saída (.hkv)"))
	f.BoolVar(&fromStdin, "password-stdin", false, e.t("lê a senha do arquivo da primeira linha do stdin (ou use HANKO_VAULT_PASSWORD)"))
	f.BoolVar(&force, "force", false, e.t("sobrescreve o arquivo de saída se ele já existir"))
	return c
}

func (e *env) importVaultCmd() *cobra.Command {
	var fromStdin bool
	c := &cobra.Command{
		Use:   "import " + e.t("<arquivo.hkv>"),
		Short: e.t("Importa um cofre exportado por outra instância (admin; exige reautenticação; tudo ou nada)"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f, err := os.Open(a[0])
			if err != nil {
				return fmt.Errorf(e.t("abrir %s: %w"), a[0], err)
			}
			defer f.Close()
			data, err := io.ReadAll(io.LimitReader(f, maxVaultFileBytes+1))
			if err != nil {
				return fmt.Errorf(e.t("ler %s: %w"), a[0], err)
			}
			if len(data) > maxVaultFileBytes {
				return usageError{fmt.Errorf(e.t("o arquivo passa de %d bytes"), maxVaultFileBytes)}
			}
			pw, err := e.vaultPassword(fromStdin)
			if err != nil {
				return err
			}
			res, err := e.client().ImportVault(cmd.Context(), co, base64.StdEncoding.EncodeToString(data), pw)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(res)
			}
			fmt.Fprintf(e.out, e.t("Importado: %d segredo(s) novo(s), %d campo(s), %d já existente(s) mantido(s), %d coleção(ões), %d concessão(ões), %d concessão(ões) não aplicada(s).\n"),
				res.Secrets, res.Fields, res.Existing, res.Collections, res.Grants, res.Skipped)
			return nil
		},
	}
	c.Flags().BoolVar(&fromStdin, "password-stdin", false, e.t("lê a senha do arquivo da primeira linha do stdin (ou use HANKO_VAULT_PASSWORD)"))
	return c
}
