package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Canais externos (ADR-0046): Telegram e GitHub.

func (e *env) channelCmd() *cobra.Command {
	c := &cobra.Command{Use: "channel", Short: e.t("Canais externos: Telegram (conversar e decidir gates) e GitHub (comentário acorda a persona)")}

	var kind, persona, project, tokenRef, secretRef, mention, apiBase string
	var repos []string
	var enable bool
	add := &cobra.Command{
		Use: "add " + e.t("<nome>"), Short: e.t("Cadastra um canal (nasce desligado, a menos que --enable)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ch, err := e.client().CreateChannel(cmd.Context(), co, client.CreateChannelReq{Kind: kind, Name: args[0], Enabled: enable, Persona: persona, Project: project,
				TokenRef: tokenRef, WebhookSecretRef: secretRef, Config: api.ChannelConfig{APIBase: apiBase, Repos: repos, Mention: mention}})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ch)
			}
			fmt.Fprintf(e.out, e.t("Canal criado: %s (%s) — %s\n"), ch.Name, ch.Kind, e.onOff(ch.Enabled))
			fmt.Fprintf(e.out, e.t("Entrada do provedor: <url pública>%s\n"), ch.HookPath)
			if !ch.Enabled {
				fmt.Fprintln(e.out, e.t("Para ligar: informe --token e --webhook-secret (referências ${secret:NOME}) e rode `hanko channel enable`."))
			}
			return nil
		},
	}
	af := add.Flags()
	af.StringVar(&kind, "kind", "", e.t("telegram ou github (obrigatório)"))
	af.StringVarP(&persona, "persona", "p", "", e.t("persona que conversa (Telegram) ou é acordada (GitHub)"))
	af.StringVar(&project, "project", "", e.t("projeto das tasks criadas pelo GitHub"))
	af.StringVar(&tokenRef, "token", "", e.t("segredo com o token do bot/API: ${secret:NOME}"))
	af.StringVar(&secretRef, "webhook-secret", "", e.t("segredo que autentica a entrada: ${secret:NOME}"))
	af.StringArrayVar(&repos, "repo", nil, e.t("GitHub: repositório dono/repo ou dono/* (repetível)"))
	af.StringVar(&mention, "mention", "", e.t("GitHub: citação que acorda a persona (padrão @hanko)"))
	af.StringVar(&apiBase, "api-base", "", e.t("raiz da API do provedor (espelho, GitHub Enterprise)"))
	af.BoolVar(&enable, "enable", false, e.t("liga já na criação"))
	_ = add.MarkFlagRequired("kind")

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os canais"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cs, err := e.client().Channels(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if len(cs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum canal. Crie um com: hanko channel add <nome> --kind telegram --persona <persona>"))
				return nil
			}
			rows := make([][]string, 0, len(cs))
			for _, ch := range cs {
				rows = append(rows, []string{ch.Name, ch.Kind, e.onOff(ch.Enabled), dash(ch.PersonaName), fmt.Sprint(ch.Linked), fmt.Sprint(ch.FailedOut)})
			}
			e.table([]string{e.t("NOME"), e.t("TIPO"), e.t("ESTADO"), e.t("PERSONA"), e.t("VINCULADOS"), e.t("FALHAS")}, rows)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show " + e.t("<canal>"), Short: e.t("Mostra um canal, os vínculos e as últimas mensagens"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ch, err := e.client().Channel(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			ids, ierr := e.client().ChannelIdentities(cmd.Context(), co, args[0])
			msgs, merr := e.client().ChannelMessages(cmd.Context(), co, args[0], 15)
			if e.asJSON {
				return e.printJSON(map[string]any{"channel": ch, "identities": ids, "messages": msgs})
			}
			e.kv([2]string{e.t("Canal"), fmt.Sprintf("%s (%s)", ch.Name, ch.Kind)}, [2]string{e.t("ID"), ch.ID}, [2]string{e.t("Estado"), e.onOff(ch.Enabled)},
				[2]string{e.t("Persona"), dash(ch.PersonaName)}, [2]string{e.t("Token"), dash(ch.TokenRef)}, [2]string{e.t("Segredo do webhook"), dash(ch.WebhookSecretRef)},
				[2]string{e.t("Repositórios"), dash(strings.Join(ch.Config.Repos, ", "))}, [2]string{e.t("Entrada"), ch.HookPath})
			if ierr == nil && len(ids) > 0 {
				fmt.Fprintln(e.out, e.t("\nVínculos:"))
				rows := make([][]string, 0, len(ids))
				for _, i := range ids {
					rows = append(rows, []string{i.ID, i.Status, dash(i.UserEmail), dash(i.ExternalLabel)})
				}
				e.table([]string{e.t("ID"), e.t("ESTADO"), e.t("USUÁRIO"), e.t("CONTA NO CANAL")}, rows)
			}
			if merr == nil && len(msgs) > 0 {
				fmt.Fprintln(e.out, e.t("\nMensagens recentes:"))
				e.printChannelMessages(msgs)
			}
			return nil
		},
	}

	var editName, editPersona, editToken, editSecret, editMention string
	var editRepos []string
	var clearPersona bool
	edit := &cobra.Command{
		Use: "edit " + e.t("<canal>"), Short: e.t("Muda nome, persona, segredos ou configuração de um canal"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			var req client.UpdateChannelReq
			if fl.Changed("name") {
				req.Name = &editName
			}
			if fl.Changed("persona") {
				req.Persona = editPersona
			}
			req.ClearPersona = clearPersona
			if fl.Changed("token") {
				req.TokenRef = &editToken
			}
			if fl.Changed("webhook-secret") {
				req.WebhookSecretRef = &editSecret
			}
			if fl.Changed("repo") || fl.Changed("mention") {
				cur, err := e.client().Channel(cmd.Context(), co, args[0])
				if err != nil {
					return err
				}
				cfg := cur.Config
				if fl.Changed("repo") {
					cfg.Repos = editRepos
				}
				if fl.Changed("mention") {
					cfg.Mention = editMention
				}
				req.Config = &cfg
			}
			ch, err := e.client().UpdateChannel(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ch)
			}
			fmt.Fprintf(e.out, e.t("Canal %s atualizado.\n"), ch.Name)
			return nil
		},
	}
	ef := edit.Flags()
	ef.StringVar(&editName, "name", "", e.t("novo nome"))
	ef.StringVarP(&editPersona, "persona", "p", "", e.t("nova persona"))
	ef.BoolVar(&clearPersona, "clear-persona", false, e.t("tira a persona do canal"))
	ef.StringVar(&editToken, "token", "", e.t("segredo do token: ${secret:NOME}"))
	ef.StringVar(&editSecret, "webhook-secret", "", e.t("segredo da entrada: ${secret:NOME}"))
	ef.StringArrayVar(&editRepos, "repo", nil, e.t("GitHub: substitui a lista de repositórios (repetível)"))
	ef.StringVar(&editMention, "mention", "", e.t("GitHub: citação que acorda a persona"))

	toggle := func(use, short string, on bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<canal>"), Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				ch, err := e.client().UpdateChannel(cmd.Context(), co, args[0], client.UpdateChannelReq{Enabled: &on})
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(ch)
				}
				fmt.Fprintf(e.out, "%s: %s\n", ch.Name, e.onOff(ch.Enabled))
				return nil
			},
		}
	}

	rm := &cobra.Command{
		Use: "rm " + e.t("<canal>"), Short: e.t("Apaga um canal com vínculos e mensagens"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteChannel(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Canal apagado."))
			return nil
		},
	}

	connect := &cobra.Command{
		Use: "connect " + e.t("<canal>"), Short: e.t("Registra o webhook no provedor (Telegram) ou mostra a URL a configurar (GitHub)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			r, err := e.client().ConnectChannel(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("URL de entrada: %s\n"), r.URL)
			if r.Registered {
				fmt.Fprintln(e.out, e.t("Webhook registrado no provedor."))
			}
			if r.Note != "" {
				fmt.Fprintln(e.out, r.Note)
			}
			return nil
		},
	}

	link := &cobra.Command{
		Use: "link " + e.t("<canal>"), Short: e.t("Gera o seu código de vínculo (vale 15 minutos)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			l, err := e.client().LinkChannel(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			fmt.Fprintf(e.out, e.t("Código: %s (vale até %s)\n%s\n"), l.Code, e.fmtTime(l.ExpiresAt), l.Hint)
			return nil
		},
	}

	unlink := &cobra.Command{
		Use: "unlink " + e.t("<canal> <vínculo>"), Short: e.t("Revoga um vínculo (o seu, ou qualquer um se você é admin); ids em `channel show`"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().RevokeChannelIdentity(cmd.Context(), co, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Vínculo revogado."))
			return nil
		},
	}

	var limit int
	messages := &cobra.Command{
		Use: "messages " + e.t("<canal>"), Aliases: []string{"msgs"}, Short: e.t("Mensagens recentes com o estado de entrega"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ms, err := e.client().ChannelMessages(cmd.Context(), co, args[0], limit)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ms)
			}
			if len(ms) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma mensagem."))
				return nil
			}
			e.printChannelMessages(ms)
			return nil
		},
	}
	messages.Flags().IntVar(&limit, "limit", 30, e.t("quantas mensagens listar"))

	resend := &cobra.Command{
		Use: "resend " + e.t("<canal> <mensagem>"), Short: e.t("Reenvia uma mensagem de saída que falhou"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			m, err := e.client().ResendChannelMessage(cmd.Context(), co, args[0], args[1])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(m)
			}
			fmt.Fprintf(e.out, e.t("Mensagem %s de volta à fila.\n"), m.ID)
			return nil
		},
	}

	var artifacts []string
	send := &cobra.Command{
		Use: "send " + e.t("<canal> <destino> <texto>"), Short: e.t("Envia uma mensagem avulsa (destino: chat do Telegram ou dono/repo#N)"), Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			m, err := e.client().SendChannelMessage(cmd.Context(), co, args[0], args[1], args[2], artifacts)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(m)
			}
			fmt.Fprintf(e.out, e.t("Mensagem %s na fila (%s).\n"), m.ID, m.Status)
			return nil
		},
	}
	send.Flags().StringArrayVar(&artifacts, "artifact", nil, e.t("id de artefato enviado como documento (Telegram; repetível)"))

	var rvTask, rvPeer, rvEvent, rvBody string
	review := &cobra.Command{
		Use: "review " + e.t("<canal>"), Short: e.t("Publica uma review (ou comentário de review) num pull request do GitHub"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			m, err := e.client().PublishChannelReview(cmd.Context(), co, args[0], rvPeer, rvTask, strings.ToUpper(rvEvent), rvBody)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(m)
			}
			fmt.Fprintf(e.out, e.t("Review na fila: %s → %s (%s)\n"), strings.ToUpper(rvEvent), m.Peer, m.Status)
			return nil
		},
	}
	rf := review.Flags()
	rf.StringVar(&rvTask, "task", "", e.t("task nascida do comentário (resolve o pull request)"))
	rf.StringVar(&rvPeer, "peer", "", e.t("pull request dono/repo#N"))
	rf.StringVar(&rvEvent, "event", "comment", e.t("comment, request_changes ou approve (approve só por admin)"))
	rf.StringVar(&rvBody, "body", "", e.t("texto da review (obrigatório)"))
	_ = review.MarkFlagRequired("body")

	c.AddCommand(add, ls, show, edit, toggle("enable", e.t("Liga um canal"), true), toggle("disable", e.t("Desliga um canal"), false), rm, connect, link, unlink, messages, resend, send, review)
	return c
}

func (e *env) printChannelMessages(ms []api.ChannelMessage) {
	rows := make([][]string, 0, len(ms))
	for _, m := range ms {
		state := m.Status
		if m.Direction == "out" && m.Attempts > 0 {
			state = fmt.Sprintf("%s (%d)", m.Status, m.Attempts)
		}
		note := m.LastError
		if note == "" {
			note = m.StatusReason
		}
		rows = append(rows, []string{m.ID, e.fmtTime(m.CreatedAt), m.Direction, m.Kind, state, oneLine(dash(m.Body), 40), oneLine(dash(note), 40)})
	}
	e.table([]string{e.t("ID"), e.t("QUANDO"), e.t("SENTIDO"), e.t("TIPO"), e.t("ESTADO"), e.t("TEXTO"), e.t("OBSERVAÇÃO")}, rows)
}
