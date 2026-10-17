package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// chatDefaultTimeout é quanto `hanko chat` espera a resposta da persona.
const chatDefaultTimeout = 5 * time.Minute

// chatPollEvery é o intervalo entre consultas à conversa enquanto espera a resposta.
const chatPollEvery = 2 * time.Second

// resolveConversation aceita o id completo ou um prefixo único (como mostrado em `chat list`).
func (e *env) resolveConversation(ctx context.Context, company, ref string) (string, error) {
	if len(ref) >= 36 {
		return ref, nil
	}
	cs, err := e.client().Conversations(ctx, company, client.ConversationQuery{Limit: 200})
	if err != nil {
		return "", err
	}
	var found []string
	for _, c := range cs {
		if strings.HasPrefix(c.ID, ref) {
			found = append(found, c.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf(e.t("conversa %q não encontrada"), ref)
	case 1:
		return found[0], nil
	}
	return "", usageError{fmt.Errorf(e.t("prefixo %q é ambíguo (%d conversas)"), ref, len(found))}
}

// pause espera d sem ignorar o cancelamento.
func (e *env) pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// repliesAfter são as mensagens da persona (ou avisos do sistema) depois da mensagem since.
func repliesAfter(ms []api.ConversationMessage, since string) []api.ConversationMessage {
	seen := since == ""
	var out []api.ConversationMessage
	for _, m := range ms {
		if !seen {
			seen = m.ID == since
			continue
		}
		if m.Author.Type != "human" {
			out = append(out, m)
		}
	}
	return out
}

// waitReply espera a conversa ficar ociosa (nenhuma execução ativa, nenhuma mensagem na fila) e devolve as
// respostas à mensagem since. O timeout é do chamador (ctx).
func (e *env) waitReply(ctx context.Context, company, conv, since string) ([]api.ConversationMessage, error) {
	cl := e.client()
	for {
		c, err := cl.Conversation(ctx, company, conv)
		if err != nil {
			return nil, err
		}
		if c.ActiveRunID == "" && c.PendingMessages == 0 {
			ms, err := cl.ConversationMessages(ctx, company, conv, 0)
			if err != nil {
				return nil, err
			}
			return repliesAfter(ms, since), nil
		}
		if err := e.pause(ctx, e.chatPoll); err != nil {
			return nil, err
		}
	}
}

func (e *env) printReplies(conv string, rs []api.ConversationMessage) error {
	if e.asJSON {
		return e.printJSON(map[string]any{"conversation_id": conv, "replies": rs})
	}
	for i, m := range rs {
		if i > 0 {
			fmt.Fprintln(e.out)
		}
		fmt.Fprintln(e.out, strings.TrimSpace(m.Body))
	}
	foot := e.tf("-- conversa %s", conv)
	for _, m := range rs {
		if m.RunID != "" {
			foot += e.tf(" · execução %s", m.RunID)
			break
		}
	}
	fmt.Fprintln(e.out, "\n"+foot)
	return nil
}

func (e *env) chatCmd() *cobra.Command {
	var (
		conv    string
		noWait  bool
		timeout time.Duration
		title   string
	)
	c := &cobra.Command{
		Use:     "chat " + e.t("<persona> <mensagem>"),
		Short:   e.t("Conversa direta com uma persona, fora de uma task"),
		Long:    e.t("Abre uma conversa com a persona (id ou slug) e envia a mensagem; a persona responde por uma execução com o gatilho chat, que entra no custo e no orçamento dela. O comando espera a resposta e a imprime (--no-wait volta já, com o id da conversa). Use --conversation para continuar uma conversa existente."),
		Example: e.t("  hanko chat cto \"Qual o risco da migração de sexta?\"\n  hanko chat cto \"E o plano B?\" --conversation 01a13f51\n  hanko chat cto \"Resuma o dia\" --no-wait"),
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ctx, message := cmd.Context(), strings.TrimSpace(args[1])
			if message == "" {
				return usageError{errors.New(e.t("a mensagem está vazia"))}
			}
			cl := e.client()
			var (
				convID string
				since  string
			)
			if conv != "" {
				if convID, err = e.resolveConversation(ctx, co, conv); err != nil {
					return err
				}
				m, err := cl.SendMessage(ctx, co, convID, message)
				if err != nil {
					return err
				}
				since = m.ID
			} else {
				cv, err := cl.CreateConversation(ctx, co, client.CreateConversationReq{Persona: args[0], Title: title, Message: message})
				if err != nil {
					return err
				}
				convID = cv.ID
				ms, err := cl.ConversationMessages(ctx, co, convID, 0)
				if err != nil {
					return err
				}
				for _, m := range ms {
					if m.Author.Type == "human" {
						since = m.ID
					}
				}
			}
			if noWait {
				if e.asJSON {
					return e.printJSON(map[string]string{"conversation_id": convID, "message_id": since})
				}
				fmt.Fprintln(e.out, e.tf("Mensagem enviada. Conversa %s; acompanhe com: hanko chat show %s", convID, convID))
				return nil
			}
			wctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			rs, err := e.waitReply(wctx, co, convID, since)
			if err != nil {
				if wctx.Err() != nil && ctx.Err() == nil {
					return fmt.Errorf(e.t("tempo esgotado esperando a resposta (a persona segue trabalhando); acompanhe com: hanko chat show %s"), convID)
				}
				return err
			}
			if len(rs) == 0 {
				return fmt.Errorf(e.t("a conversa ficou ociosa sem resposta; veja: hanko chat show %s"), convID)
			}
			if err := e.printReplies(convID, rs); err != nil {
				return err
			}
			// a failed run leaves a notice from the system instead of a reply: that is a failure of the command
			for _, m := range rs {
				if m.Author.Type == "persona" {
					return nil
				}
			}
			return errors.New(e.t("a persona não respondeu (veja o aviso acima)"))
		},
	}
	f := c.Flags()
	f.StringVar(&conv, "conversation", "", e.t("continua esta conversa (id ou prefixo) em vez de abrir uma nova"))
	f.BoolVar(&noWait, "no-wait", false, e.t("não espera a resposta: imprime o id da conversa e sai"))
	f.DurationVar(&timeout, "timeout", chatDefaultTimeout, e.t("quanto esperar pela resposta"))
	f.StringVar(&title, "title", "", e.t("título da nova conversa (padrão: derivado da mensagem)"))

	var q client.ConversationQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista conversas (mais recentes primeiro)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			q.Kind = "direct" // as salas têm o seu comando (hanko room list)
			cs, err := e.client().Conversations(cmd.Context(), co, q)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if len(cs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma conversa."))
				return nil
			}
			rows := make([][]string, 0, len(cs))
			for _, c := range cs {
				state := c.Status
				switch {
				case c.ActiveRunID != "":
					state = e.t("respondendo")
				case c.PendingMessages > 0:
					state = e.t("na fila")
				}
				rows = append(rows, []string{shortID(c.ID), state, dash(c.PersonaSlug), e.fmtTime(c.LastMessageAt), oneLine(c.Title, 60)})
			}
			e.table([]string{e.t("ID"), e.t("STATUS"), e.t("PERSONA"), e.t("ÚLTIMA MENSAGEM"), e.t("TÍTULO")}, rows)
			return nil
		},
	}
	ls.Flags().StringVar(&q.Persona, "persona", "", e.t("persona (id ou slug)"))
	ls.Flags().StringVar(&q.Status, "status", "", e.t("open ou archived"))
	ls.Flags().IntVar(&q.Limit, "limit", 0, e.t("limite"))

	var last int
	show := &cobra.Command{
		Use: "show " + e.t("<conversa>"), Short: e.t("Mostra uma conversa e as últimas mensagens"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveConversation(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			cv, err := e.client().Conversation(cmd.Context(), co, id)
			if err != nil {
				return err
			}
			ms, err := e.client().ConversationMessages(cmd.Context(), co, id, last)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"conversation": cv, "messages": ms})
			}
			state := cv.Status
			switch {
			case cv.ActiveRunID != "":
				state = e.t("respondendo")
			case cv.PendingMessages > 0:
				state = e.t("na fila")
			}
			e.kv([2]string{e.t("Conversa"), cv.ID}, [2]string{"Persona", cv.PersonaName}, [2]string{e.t("Título"), cv.Title},
				[2]string{"Status", state}, [2]string{e.t("Custo"), fmt.Sprintf("US$ %.4f", cv.CostUSD)})
			for _, m := range ms {
				who := e.t("você")
				switch m.Author.Type {
				case "persona":
					who = cv.PersonaName
				case "system":
					who = e.t("sistema")
				}
				fmt.Fprintf(e.out, "\n[%s] %s\n%s\n", e.fmtTime(m.CreatedAt), who, strings.TrimSpace(m.Body))
			}
			return nil
		},
	}
	show.Flags().IntVar(&last, "last", 20, e.t("quantas mensagens mostrar (as mais recentes)"))

	c.AddCommand(ls, show)
	return c
}
