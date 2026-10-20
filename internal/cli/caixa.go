package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Caixa por pessoa, avisos e atividade por ator (ADR-0042).

func (e *env) inboxState(item api.InboxItem) string {
	switch {
	case item.Archived:
		return e.t("arquivado")
	case item.Read:
		return e.t("lido")
	}
	return e.t("novo")
}

func (e *env) inboxCmd() *cobra.Command {
	c := &cobra.Command{Use: "inbox", Aliases: []string{"caixa"}, Short: e.t("Caixa de entrada: o que espera por você (lido e arquivado são seus)")}

	var view string
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista a Caixa (padrão: tudo que não está arquivado)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			in, err := e.client().Inbox(cmd.Context(), co, view)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(in)
			}
			if len(in.Items) == 0 {
				fmt.Fprintln(e.out, e.t("A Caixa está vazia."))
				return nil
			}
			rows := make([][]string, 0, len(in.Items))
			for _, it := range in.Items {
				rows = append(rows, []string{it.Kind + ":" + it.ID, e.inboxState(it), dash(it.TaskKey), oneLine(it.Title, 60), e.fmtTime(it.At)})
			}
			e.table([]string{"ITEM", e.t("ESTADO"), e.t("TASK"), e.t("TÍTULO"), e.t("QUANDO")}, rows)
			fmt.Fprintf(e.out, e.t("\n%d aguardando, %d não lidos, %d arquivados\n"), in.Count, in.Unread, in.Archived)
			return nil
		},
	}
	ls.Flags().StringVar(&view, "view", "", e.t("all (padrão), unread ou archived"))

	act := func(use, short, action string) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<item...>"), Short: short, Args: cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				items := make([]api.InboxRef, 0, len(args))
				for _, a := range args {
					kind, id, ok := strings.Cut(a, ":")
					if !ok {
						return usageError{fmt.Errorf(e.t("item inválido %q: use tipo:id, como mostra `hanko inbox list`"), a)}
					}
					items = append(items, api.InboxRef{Kind: kind, ID: id})
				}
				r, err := e.client().InboxState(cmd.Context(), co, action, items)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(r)
				}
				fmt.Fprintf(e.out, e.t("%d item(ns) atualizado(s)\n"), r.Updated)
				return nil
			},
		}
	}
	readAll := &cobra.Command{
		Use: "read-all", Short: e.t("Marca como lidos todos os itens visíveis"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			r, err := e.client().InboxState(cmd.Context(), co, "read_all", nil)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("%d item(ns) atualizado(s)\n"), r.Updated)
			return nil
		},
	}
	c.AddCommand(ls, act("read", e.t("Marca itens como lidos"), "read"), act("unread", e.t("Marca itens como não lidos"), "unread"),
		act("archive", e.t("Arquiva itens: some da sua Caixa, a task e o gate seguem como estão"), "archive"),
		act("unarchive", e.t("Devolve itens arquivados à Caixa"), "unarchive"), readAll)
	return c
}

func (e *env) notifyCmd() *cobra.Command {
	c := &cobra.Command{Use: "notify", Short: e.t("Avisos: toast e web push por tipo")}

	prefs := &cobra.Command{
		Use: "prefs", Short: e.t("Mostra suas preferências de aviso nesta empresa"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ps, err := e.client().NotifyPrefs(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ps)
			}
			onOff := func(b bool) string {
				if b {
					return e.t("ligado")
				}
				return e.t("desligado")
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				rows = append(rows, []string{p.Kind, onOff(p.Toast), onOff(p.Push)})
			}
			e.table([]string{e.t("TIPO"), "TOAST", "PUSH"}, rows)
			return nil
		},
	}

	var toast, push bool
	set := &cobra.Command{
		Use: "set " + e.t("<tipo>"), Short: e.t("Define os canais de um tipo de aviso: mention, gate, run_failed ou alert"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cl := e.client()
			// o que não foi informado fica como está
			ps, err := cl.NotifyPrefs(cmd.Context(), co)
			if err != nil {
				return err
			}
			for _, p := range ps {
				if p.Kind == args[0] {
					if !cmd.Flags().Changed("toast") {
						toast = p.Toast
					}
					if !cmd.Flags().Changed("push") {
						push = p.Push
					}
				}
			}
			p, err := cl.SetNotifyPref(cmd.Context(), co, args[0], toast, push)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, "%s: toast=%v push=%v\n", p.Kind, p.Toast, p.Push)
			return nil
		},
	}
	set.Flags().BoolVar(&toast, "toast", true, e.t("aviso ao vivo na tela"))
	set.Flags().BoolVar(&push, "push", true, e.t("notificação do navegador/PWA"))

	test := &cobra.Command{
		Use: "test", Short: e.t("Envia um aviso de teste para você"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().TestNotification(cmd.Context(), co); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Aviso de teste enviado (conforme as preferências do tipo mention)."))
			return nil
		},
	}
	c.AddCommand(prefs, set, test)
	return c
}

func (e *env) activityCmd() *cobra.Command {
	var f client.ActivityQuery
	c := &cobra.Command{
		Use: "activity", Short: e.t("Atividade da empresa, filtrável por ator"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			as, err := e.client().Activity(cmd.Context(), co, f)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(as)
			}
			if len(as) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma atividade."))
				return nil
			}
			rows := make([][]string, 0, len(as))
			for _, a := range as {
				rows = append(rows, []string{e.fmtTime(a.OccurredAt), e.authorLabel(a.Actor), a.Verb, a.SubjectType + ":" + short(a.SubjectID)})
			}
			e.table([]string{e.t("QUANDO"), e.t("QUEM"), e.t("FATO"), e.t("SUJEITO")}, rows)
			return nil
		},
	}
	c.Flags().StringVar(&f.Actor, "actor", "", e.t("quem causou o fato: system, persona:<slug>, human:<e-mail> (ou persona/human para todos do tipo)"))
	c.Flags().StringVar(&f.SubjectType, "subject-type", "", e.t("tipo do sujeito (task, run, gate, goal...)"))
	c.Flags().IntVar(&f.Limit, "limit", 50, e.t("máximo de eventos"))
	return c
}
