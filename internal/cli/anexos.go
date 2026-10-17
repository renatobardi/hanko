package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
)

// Anexos (ADR-0052): arquivos ligados a uma task (e a um comentário dela) ou a uma conversa do chat. `hanko attach`
// envia; `hanko attachment list|get|rm` consulta e apaga.

func (e *env) attachCmd() *cobra.Command {
	var task, conv, comment, message, name, mime string
	c := &cobra.Command{
		Use: "attach " + e.t("<arquivo|->..."), Short: e.t("Anexa arquivos a uma task (ou comentário) ou a uma conversa do chat"), Args: cobra.MinimumNArgs(1),
		Long: e.t("Anexa um ou mais arquivos à task (--task) ou à conversa (--conversation). Com --comment o anexo pertence a um comentário da task; com --message, a uma mensagem da conversa. Um nome repetido na mesma task ganha sufixo (nome (2).ext)."),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if (task == "") == (conv == "") {
				return usageError{errors.New(e.t("informe --task OU --conversation"))}
			}
			if (comment != "" && conv != "") || (message != "" && task != "") {
				return usageError{errors.New(e.t("--comment é da task e --message é da conversa"))}
			}
			if name != "" && len(args) > 1 {
				return usageError{errors.New(e.t("--name só vale com um arquivo"))}
			}
			var done []any
			for _, a := range args {
				var b []byte
				fname := name
				if a == "-" {
					if fname == "" {
						return usageError{errors.New(e.t("com entrada padrão (-), informe --name"))}
					}
					b, err = io.ReadAll(io.LimitReader(e.in, maxArtifactRead+1))
				} else {
					if fname == "" {
						fname = filepath.Base(a)
					}
					b, err = readLimited(a, maxArtifactRead)
				}
				if err != nil {
					return fmt.Errorf(e.t("ler %s: %w"), a, err)
				}
				if int64(len(b)) > maxArtifactRead {
					return fmt.Errorf(e.t("o arquivo %s passa de %d bytes"), a, maxArtifactRead)
				}
				at, err := e.client().PutAttachment(cmd.Context(), co, client.PutAttachmentReq{Name: fname, Mime: mime, Content: b,
					Task: task, Conversation: conv, CommentID: comment, MessageID: message})
				if err != nil {
					return err
				}
				done = append(done, at)
				if !e.asJSON {
					fmt.Fprintf(e.out, e.t("Anexo %s anexado (%s, %s)\n"), at.Name, at.Mime, humanBytes(at.SizeBytes))
					fmt.Fprintf(e.out, e.t("id: %s\n"), at.ID)
				}
			}
			if e.asJSON {
				return e.printJSON(done)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&task, "task", "", e.t("task a que o anexo pertence (id ou chave)"))
	f.StringVar(&conv, "conversation", "", e.t("conversa do chat a que o anexo pertence (id)"))
	f.StringVar(&comment, "comment", "", e.t("comentário da task a que o anexo pertence (id)"))
	f.StringVar(&message, "message", "", e.t("mensagem da conversa a que o anexo pertence (id)"))
	f.StringVar(&name, "name", "", e.t("nome do anexo (padrão: o nome do arquivo)"))
	f.StringVar(&mime, "mime", "", e.t("tipo do conteúdo (padrão: deduzido do nome e do conteúdo)"))
	return c
}

func (e *env) attachmentCmd() *cobra.Command {
	c := &cobra.Command{Use: "attachment", Aliases: []string{"attachments"}, Short: e.t("Consulta e apaga anexos")}

	var q client.AttachmentQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista anexos de uma task, comentário, conversa ou mensagem"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if q.Task == "" && q.Conversation == "" && q.CommentID == "" && q.MessageID == "" {
				return usageError{errors.New(e.t("informe --task, --conversation, --comment ou --message"))}
			}
			as, err := e.client().Attachments(cmd.Context(), co, q)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(as)
			}
			rows := make([][]string, 0, len(as))
			for _, a := range as {
				rows = append(rows, []string{a.ID, a.Name, a.Mime, humanBytes(a.SizeBytes), e.authorLabel(a.Author), e.fmtTime(a.CreatedAt)})
			}
			e.table([]string{"ID", e.t("NOME"), e.t("TIPO"), e.t("TAMANHO"), e.t("AUTOR"), e.t("CRIADO EM")}, rows)
			return nil
		},
	}
	lf := ls.Flags()
	lf.StringVar(&q.Task, "task", "", e.t("task (id ou chave)"))
	lf.StringVar(&q.Conversation, "conversation", "", e.t("conversa (id)"))
	lf.StringVar(&q.CommentID, "comment", "", e.t("comentário (id)"))
	lf.StringVar(&q.MessageID, "message", "", e.t("mensagem (id)"))
	lf.IntVar(&q.Limit, "limit", 0, e.t("máximo de itens"))

	var out string
	get := &cobra.Command{
		Use: "get " + e.t("<id>"), Short: e.t("Baixa o conteúdo de um anexo (saída padrão ou -o arquivo)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			b, err := e.client().ArtifactContent(cmd.Context(), co, args[0], maxArtifactRead)
			if err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err = e.out.Write(b)
				return err
			}
			if err := writeFileAtomic(out, b); err != nil {
				return fmt.Errorf(e.t("gravar %s: %w"), out, err)
			}
			fmt.Fprintf(e.errOut, e.t("gravado em %s (%s)\n"), out, humanBytes(int64(len(b))))
			return nil
		},
	}
	get.Flags().StringVarP(&out, "output", "o", "", e.t("grava neste arquivo (permissão 0600) em vez da saída padrão"))

	rm := &cobra.Command{
		Use: "rm " + e.t("<id>"), Aliases: []string{"delete"}, Short: e.t("Apaga um anexo (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteArtifact(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Anexo %s apagado.\n"), args[0])
			return nil
		},
	}
	c.AddCommand(ls, get, rm)
	return c
}
