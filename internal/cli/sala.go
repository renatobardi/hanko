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

// Sala (ADR-0033): `hanko room` conversa com várias personas ao mesmo tempo.

// roomState resume o estado de uma sala para a lista e o show.
func (e *env) roomState(c api.Conversation) string {
	switch {
	case c.ActiveRunID != "":
		return e.t("respondendo")
	case c.PendingMessages > 0:
		return e.t("na fila")
	}
	return c.Status
}

// roomWho é o rótulo de quem escreveu uma mensagem da sala.
func (e *env) roomWho(m api.ConversationMessage) string {
	switch m.Author.Type {
	case "human":
		return e.t("você")
	case "system":
		return e.t("sistema")
	}
	return dash(m.PersonaName)
}

func (e *env) printRoomReplies(conv string, rs []api.ConversationMessage) error {
	if e.asJSON {
		return e.printJSON(map[string]any{"conversation_id": conv, "replies": rs})
	}
	for i, m := range rs {
		if i > 0 {
			fmt.Fprintln(e.out)
		}
		fmt.Fprintf(e.out, "[%s]\n%s\n", e.roomWho(m), strings.TrimSpace(m.Body))
	}
	fmt.Fprintln(e.out, "\n"+e.tf("-- sala %s", conv))
	return nil
}

// waitRoom espera a sala ficar ociosa (nenhuma persona rodando, nada na fila) e imprime o que veio depois de since.
// Falha (código 1) quando nenhuma persona respondeu.
func (e *env) waitRoom(ctx context.Context, co, convID, since string, timeout time.Duration) error {
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rs, err := e.waitReply(wctx, co, convID, since)
	if err != nil {
		if wctx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf(e.t("tempo esgotado esperando as respostas (as personas seguem trabalhando); acompanhe com: hanko room show %s"), convID)
		}
		return err
	}
	if len(rs) == 0 {
		return fmt.Errorf(e.t("a sala ficou ociosa sem resposta; veja: hanko room show %s"), convID)
	}
	if err := e.printRoomReplies(convID, rs); err != nil {
		return err
	}
	for _, m := range rs {
		if m.Author.Type == "persona" {
			return nil
		}
	}
	return errors.New(e.t("nenhuma persona respondeu (veja os avisos acima)"))
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (e *env) roomCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "room",
		Short: e.t("Sala: conversa com várias personas ao mesmo tempo"),
		Long:  e.t("Uma sala junta várias personas numa conversa. Cite uma persona com @slug para ela responder; sem citação responde a moderadora (modo mentions) ou todas (modo all). Uma persona pode citar outra na resposta e acordá-la, até o limite de saltos da sala. Cada resposta é uma execução que entra no custo e no orçamento da persona."),
	}

	var (
		personas, moderator, mode, message string
		maxHops, maxRuns                   int
		noWait                             bool
		timeout                            time.Duration
	)
	create := &cobra.Command{
		Use: "create " + e.t("<título>"), Short: e.t("Abre uma sala"), Args: cobra.ExactArgs(1),
		Example: e.t("  hanko room create --persona cto,pm,design \"Planejar a semana\"\n  hanko room create --persona cto,pm --mode all --message \"Bom dia, time\" \"Daily\""),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ps := splitList(personas)
			if len(ps) < 2 {
				return usageError{errors.New(e.t("informe ao menos duas personas em --persona (separadas por vírgula)"))}
			}
			ctx, cl := cmd.Context(), e.client()
			cv, err := cl.CreateRoom(ctx, co, client.CreateRoomReq{Title: strings.TrimSpace(args[0]), Message: strings.TrimSpace(message), Personas: ps,
				Moderator: moderator, Mode: mode, MaxHops: maxHops, MaxRunsPerMessage: maxRuns})
			if err != nil {
				return err
			}
			if strings.TrimSpace(message) == "" || noWait {
				if e.asJSON {
					return e.printJSON(cv)
				}
				fmt.Fprintln(e.out, e.tf("Sala %s aberta; fale com: hanko room say %s \"@persona mensagem\"", cv.ID, cv.ID))
				return nil
			}
			ms, err := cl.ConversationMessages(ctx, co, cv.ID, 0)
			if err != nil {
				return err
			}
			since := ""
			for _, m := range ms {
				if m.Author.Type == "human" {
					since = m.ID
				}
			}
			return e.waitRoom(ctx, co, cv.ID, since, timeout)
		},
	}
	cf := create.Flags()
	cf.StringVar(&personas, "persona", "", e.t("personas da sala (ids ou slugs, separados por vírgula; 2 a 12)"))
	cf.StringVar(&moderator, "moderator", "", e.t("moderadora (padrão: a primeira persona)"))
	cf.StringVar(&mode, "mode", "", e.t("mentions (padrão) ou all"))
	cf.IntVar(&maxHops, "max-hops", 0, e.t("saltos entre personas a partir de uma mensagem humana (1 a 5; padrão 2)"))
	cf.IntVar(&maxRuns, "max-runs", 0, e.t("teto de execuções por mensagem humana (padrão: número de personas)"))
	cf.StringVar(&message, "message", "", e.t("primeira mensagem (espera as respostas, como room say)"))
	cf.BoolVar(&noWait, "no-wait", false, e.t("não espera as respostas"))
	cf.DurationVar(&timeout, "timeout", chatDefaultTimeout, e.t("quanto esperar pelas respostas"))

	var (
		sayNoWait  bool
		sayTimeout time.Duration
	)
	say := &cobra.Command{
		Use: "say " + e.t("<sala> <mensagem>"), Short: e.t("Envia uma mensagem à sala e espera as respostas"), Args: cobra.ExactArgs(2),
		Example: e.t("  hanko room say 01a13f51 \"@cto qual o risco da migração?\""),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ctx, message := cmd.Context(), strings.TrimSpace(args[1])
			if message == "" {
				return usageError{errors.New(e.t("a mensagem está vazia"))}
			}
			id, err := e.resolveConversation(ctx, co, args[0])
			if err != nil {
				return err
			}
			m, err := e.client().SendMessage(ctx, co, id, message)
			if err != nil {
				return err
			}
			if sayNoWait {
				if e.asJSON {
					return e.printJSON(m)
				}
				fmt.Fprintln(e.out, e.tf("Mensagem enviada. Acompanhe com: hanko room show %s", id))
				return nil
			}
			return e.waitRoom(ctx, co, id, m.ID, sayTimeout)
		},
	}
	say.Flags().BoolVar(&sayNoWait, "no-wait", false, e.t("não espera as respostas"))
	say.Flags().DurationVar(&sayTimeout, "timeout", chatDefaultTimeout, e.t("quanto esperar pelas respostas"))

	var q client.ConversationQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista salas (mais recentes primeiro)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			q.Kind = "room"
			cs, err := e.client().Conversations(cmd.Context(), co, q)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if len(cs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma sala."))
				return nil
			}
			rows := make([][]string, 0, len(cs))
			for _, c := range cs {
				var who []string
				if c.Room != nil {
					for _, p := range c.Room.Participants {
						if p.Active {
							who = append(who, p.Slug)
						}
					}
				}
				rows = append(rows, []string{shortID(c.ID), e.roomState(c), oneLine(strings.Join(who, ","), 40), e.fmtTime(c.LastMessageAt), oneLine(c.Title, 60)})
			}
			e.table([]string{e.t("ID"), e.t("STATUS"), e.t("PERSONAS"), e.t("ÚLTIMA MENSAGEM"), e.t("TÍTULO")}, rows)
			return nil
		},
	}
	ls.Flags().StringVar(&q.Persona, "persona", "", e.t("só salas com esta persona (id ou slug)"))
	ls.Flags().StringVar(&q.Status, "status", "", e.t("open ou archived"))
	ls.Flags().IntVar(&q.Limit, "limit", 0, e.t("limite"))

	var last int
	show := &cobra.Command{
		Use: "show " + e.t("<sala>"), Short: e.t("Mostra uma sala, as personas e as últimas mensagens"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			id, err := e.resolveConversation(ctx, co, args[0])
			if err != nil {
				return err
			}
			cv, err := e.client().Conversation(ctx, co, id)
			if err != nil {
				return err
			}
			ms, err := e.client().ConversationMessages(ctx, co, id, last)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"room": cv, "messages": ms})
			}
			if cv.Room == nil {
				return fmt.Errorf(e.t("%s é um chat direto; use: hanko chat show %s"), id, id)
			}
			var who []string
			for _, p := range cv.Room.Participants {
				if !p.Active {
					continue
				}
				label := p.Name + " (@" + p.Slug + ")"
				if p.PersonaID == cv.Room.ModeratorPersonaID {
					label += " " + e.t("moderadora")
				}
				if p.ActiveRunID != "" {
					label += " · " + e.t("respondendo")
				}
				who = append(who, label)
			}
			e.kv([2]string{e.t("Sala"), cv.ID}, [2]string{e.t("Título"), cv.Title}, [2]string{"Status", e.roomState(cv)},
				[2]string{e.t("Modo"), cv.Room.Mode}, [2]string{e.t("Personas"), strings.Join(who, "; ")},
				[2]string{e.t("Limites"), e.tf("%d saltos · %d execuções por mensagem", cv.Room.MaxHops, cv.Room.MaxRunsPerMessage)},
				[2]string{e.t("Custo"), fmt.Sprintf("US$ %.4f", cv.CostUSD)})
			for _, m := range ms {
				fmt.Fprintf(e.out, "\n[%s] %s\n%s\n", e.fmtTime(m.CreatedAt), e.roomWho(m), strings.TrimSpace(m.Body))
			}
			return nil
		},
	}
	show.Flags().IntVar(&last, "last", 20, e.t("quantas mensagens mostrar (as mais recentes)"))

	member := func(use, short string, add bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<sala> <persona>"), Short: short, Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				ctx := cmd.Context()
				id, err := e.resolveConversation(ctx, co, args[0])
				if err != nil {
					return err
				}
				var cv api.Conversation
				if add {
					cv, err = e.client().AddRoomParticipant(ctx, co, id, args[1])
				} else {
					cv, err = e.client().RemoveRoomParticipant(ctx, co, id, args[1])
				}
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(cv)
				}
				n := 0
				if cv.Room != nil {
					for _, p := range cv.Room.Participants {
						if p.Active {
							n++
						}
					}
				}
				fmt.Fprintln(e.out, e.tf("Sala %s: %d personas.", shortID(cv.ID), n))
				return nil
			},
		}
	}

	var setMode, setModerator string
	var setHops, setRuns int
	set := &cobra.Command{
		Use: "set " + e.t("<sala>"), Short: e.t("Muda o modo, a moderadora ou os limites da sala"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ctx, fl := cmd.Context(), cmd.Flags()
			var p client.RoomPatch
			if fl.Changed("mode") {
				p.Mode = &setMode
			}
			if fl.Changed("moderator") {
				p.Moderator = &setModerator
			}
			if fl.Changed("max-hops") {
				p.MaxHops = &setHops
			}
			if fl.Changed("max-runs") {
				p.MaxRunsPerMessage = &setRuns
			}
			if p == (client.RoomPatch{}) {
				return usageError{errors.New(e.t("nada a mudar: use --mode, --moderator, --max-hops ou --max-runs"))}
			}
			id, err := e.resolveConversation(ctx, co, args[0])
			if err != nil {
				return err
			}
			cv, err := e.client().UpdateRoom(ctx, co, id, p)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cv)
			}
			fmt.Fprintln(e.out, e.tf("Sala %s atualizada.", shortID(cv.ID)))
			return nil
		},
	}
	sf := set.Flags()
	sf.StringVar(&setMode, "mode", "", e.t("mentions ou all"))
	sf.StringVar(&setModerator, "moderator", "", e.t("moderadora (id ou slug; precisa estar na sala)"))
	sf.IntVar(&setHops, "max-hops", 0, e.t("saltos entre personas (1 a 5)"))
	sf.IntVar(&setRuns, "max-runs", 0, e.t("teto de execuções por mensagem humana (0 = número de personas)"))

	c.AddCommand(create, say, ls, show, member("add", e.t("Põe uma persona na sala"), true), member("remove", e.t("Tira uma persona da sala"), false), set)
	return c
}
