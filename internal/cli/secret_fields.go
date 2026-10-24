package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Campos e coleções do cofre (ADR-0068, fatia 1). Reveal de campo, histórico e concessões chegam na fatia 2.

// inCompany resolve a empresa antes do comando: sem empresa, o erro sai antes de qualquer chamada ao servidor.
func (e *env) inCompany(run func(cmd *cobra.Command, co string, a []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, a []string) error {
		co, err := e.needCompany()
		if err != nil {
			return err
		}
		return run(cmd, co, a)
	}
}

// valueFlags registra --value, --value-stdin e --generate; cada comando passa o próprio texto de ajuda.
func (e *env) valueFlags(cmd *cobra.Command, value *string, stdin, gen *bool, valueHelp, stdinHelp, genHelp string) {
	f := cmd.Flags()
	f.StringVar(value, "value", "", valueHelp)
	f.BoolVar(stdin, "value-stdin", false, stdinHelp)
	f.BoolVar(gen, "generate", false, genHelp)
}

// showRevealedField mostra o valor que o servidor devolveu (gerado), uma vez; nunca ecoa o que o usuário enviou.
func (e *env) showRevealedField(f api.SecretField) {
	if f.Value == "" {
		return
	}
	fmt.Fprintf(e.out, e.t("Valor (mostrado só agora; guarde-o): %s\n"), f.Value)
}

func (e *env) fieldLine(f api.SecretField) []string {
	vis := e.t("secreto")
	val := "-"
	if !f.Sensitive {
		vis, val = e.t("visível"), f.VisibleText
	}
	revealed := e.t("não")
	if f.Revealed {
		revealed = e.t("sim")
	}
	return []string{f.Name, f.Kind, vis, strconv.Itoa(f.Version), revealed, val}
}

var fieldHeader = func(e *env) []string {
	return []string{e.t("NOME"), e.t("TIPO"), e.t("VISIBILIDADE"), e.t("VERSÃO"), e.t("REVELADO"), e.t("VALOR")}
}

// secretFieldCmd é o grupo `hanko secret field`; o --user vem do `secret` (flag persistente).
func (e *env) secretFieldCmd(user *bool) *cobra.Command {
	c := &cobra.Command{Use: "field", Short: e.t("Campos de um segredo (valor só em campo visível; os secretos nunca saem na listagem)")}

	ls := &cobra.Command{
		Use: "ls " + e.t("<segredo>"), Aliases: []string{"list"}, Short: e.t("Lista os campos de um segredo"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			l, err := e.client().ListSecretFields(cmd.Context(), co, *user, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			if len(l) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum campo."))
				return nil
			}
			rows := make([][]string, len(l))
			for i, f := range l {
				rows[i] = e.fieldLine(f)
			}
			e.table(fieldHeader(e), rows)
			return nil
		}),
	}

	var (
		aValue, aKind      string
		aStdin, aGen, aVis bool
		aPos               int
	)
	add := &cobra.Command{
		Use: "add " + e.t("<segredo> <campo>"), Short: e.t("Acrescenta um campo (--value-stdin ou --generate; padrão secreto)"), Args: cobra.ExactArgs(2),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			v, err := e.secretValue(aValue, aStdin)
			if err != nil {
				return err
			}
			req := client.CreateFieldReq{Name: a[1], Kind: aKind, Position: aPos, Value: v, Generate: aGen}
			if aVis {
				req.Sensitive = new(bool)
			}
			f, err := e.client().CreateSecretField(cmd.Context(), co, *user, a[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			fmt.Fprintf(e.out, e.t("Campo criado: %s (use %s)\n"), f.Name, f.Reference)
			e.showRevealedField(f)
			return nil
		}),
	}
	af := add.Flags()
	e.valueFlags(add, &aValue, &aStdin, &aGen,
		e.t("valor (prefira --value-stdin: o valor não fica no histórico do shell)"), e.t("lê o valor do stdin"),
		e.t("o servidor gera o valor e o mostra uma vez"))
	af.BoolVar(&aVis, "visible", false, e.t("campo visível: o valor aparece na listagem (padrão: secreto)"))
	af.StringVar(&aKind, "kind", "text", e.t("tipo do campo: text ou totp"))
	af.IntVar(&aPos, "position", 0, e.t("posição do campo na lista"))

	var (
		rValue       string
		rStdin, rGen bool
	)
	rotate := &cobra.Command{
		Use: "rotate " + e.t("<segredo> <campo>"), Short: e.t("Troca o valor de um campo (a versão sobe)"), Args: cobra.ExactArgs(2),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			v, err := e.secretValue(rValue, rStdin)
			if err != nil {
				return err
			}
			f, err := e.client().RotateSecretField(cmd.Context(), co, *user, a[0], a[1], client.RotateFieldReq{Value: v, Generate: rGen})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			fmt.Fprintf(e.out, e.t("Campo %s rotacionado (versão %d).\n"), f.Name, f.Version)
			e.showRevealedField(f)
			return nil
		}),
	}
	e.valueFlags(rotate, &rValue, &rStdin, &rGen,
		e.t("novo valor (prefira --value-stdin)"), e.t("lê o novo valor do stdin"),
		e.t("o servidor gera o novo valor e o mostra uma vez"))

	var sVis, sSecret bool
	var sPos int
	set := &cobra.Command{
		Use: "set " + e.t("<segredo> <campo>"), Short: e.t("Muda a marca (--visible ou --secret) e a posição de um campo"), Args: cobra.ExactArgs(2),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			if sVis && sSecret {
				return usageError{fmt.Errorf("%s", e.t("use --visible ou --secret, não os dois"))}
			}
			var req client.UpdateFieldReq
			if sVis || sSecret {
				req.Sensitive = &sSecret
			}
			if cmd.Flags().Changed("position") {
				req.Position = &sPos
			}
			if req.Sensitive == nil && req.Position == nil {
				return usageError{fmt.Errorf("%s", e.t("nada para mudar: use --visible, --secret ou --position"))}
			}
			f, err := e.client().UpdateSecretField(cmd.Context(), co, *user, a[0], a[1], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			fmt.Fprintf(e.out, e.t("Campo %s atualizado.\n"), f.Name)
			e.table(fieldHeader(e), [][]string{e.fieldLine(f)})
			return nil
		}),
	}
	sf := set.Flags()
	sf.BoolVar(&sVis, "visible", false, e.t("torna o campo visível (o valor passa a aparecer na listagem)"))
	sf.BoolVar(&sSecret, "secret", false, e.t("torna o campo secreto"))
	sf.IntVar(&sPos, "position", 0, e.t("posição do campo na lista"))

	c.AddCommand(ls, add, rotate, set)
	return c
}

func (e *env) collectionLine(c api.SecretCollection) []string {
	def := e.t("não")
	if c.IsDefault {
		def = e.t("sim")
	}
	rot := "-"
	if c.RotateAfterDays != nil {
		rot = strconv.Itoa(*c.RotateAfterDays)
	}
	return []string{c.Name, def, strconv.Itoa(c.Secrets), rot, c.Description}
}

func (e *env) collectionHeader() []string {
	return []string{e.t("NOME"), e.t("PADRÃO"), e.t("SEGREDOS"), e.t("ROTAÇÃO (DIAS)"), e.t("DESCRIÇÃO")}
}

// secretCollectionCmd é o grupo `hanko secret collection`. Concessões chegam na fatia 2.
func (e *env) secretCollectionCmd() *cobra.Command {
	c := &cobra.Command{Use: "collection", Aliases: []string{"collections"}, Short: e.t("Coleções de segredos (agrupam segredos; a coleção padrão não se apaga)")}

	ls := &cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: e.t("Lista as coleções"), Args: cobra.NoArgs,
		RunE: e.inCompany(func(cmd *cobra.Command, co string, _ []string) error {
			l, err := e.client().ListSecretCollections(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			if len(l) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma coleção."))
				return nil
			}
			rows := make([][]string, len(l))
			for i, x := range l {
				rows[i] = e.collectionLine(x)
			}
			e.table(e.collectionHeader(), rows)
			return nil
		}),
	}

	var cDesc string
	var cRot int
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria uma coleção (admin)"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			req := client.CollectionReq{Name: a[0], Description: cDesc}
			if cmd.Flags().Changed("rotate-days") {
				req.RotateAfterDays = &cRot
			}
			x, err := e.client().CreateSecretCollection(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(x)
			}
			fmt.Fprintf(e.out, e.t("Coleção criada: %s\n"), x.Name)
			return nil
		}),
	}
	create.Flags().StringVar(&cDesc, "description", "", e.t("descrição"))
	create.Flags().IntVar(&cRot, "rotate-days", 0, e.t("prazo de rotação, em dias, dos itens sem prazo próprio"))

	show := &cobra.Command{
		Use: "show " + e.t("<coleção>"), Short: e.t("Mostra uma coleção (id ou nome)"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			x, err := e.client().GetSecretCollection(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(x)
			}
			e.table(e.collectionHeader(), [][]string{e.collectionLine(x)})
			return nil
		}),
	}

	secrets := &cobra.Command{
		Use: "secrets " + e.t("<coleção>"), Short: e.t("Lista os segredos de uma coleção"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			l, err := e.client().ListCollectionSecrets(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			e.printSecretList(l)
			return nil
		}),
	}

	var uName, uDesc string
	var uRot int
	update := &cobra.Command{
		Use: "update " + e.t("<coleção>"), Short: e.t("Muda nome, descrição ou prazo de rotação (admin)"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			fl := cmd.Flags()
			if !fl.Changed("name") && !fl.Changed("description") && !fl.Changed("rotate-days") {
				return usageError{fmt.Errorf("%s", e.t("nada para mudar: use --name, --description ou --rotate-days"))}
			}
			// o servidor troca os três campos de uma vez: parte da coleção atual.
			cur, err := e.client().GetSecretCollection(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			req := client.CollectionReq{Name: cur.Name, Description: cur.Description, RotateAfterDays: cur.RotateAfterDays}
			if fl.Changed("name") {
				req.Name = uName
			}
			if fl.Changed("description") {
				req.Description = uDesc
			}
			if fl.Changed("rotate-days") {
				req.RotateAfterDays = &uRot
			}
			x, err := e.client().UpdateSecretCollection(cmd.Context(), co, a[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(x)
			}
			fmt.Fprintf(e.out, e.t("Coleção %s atualizada.\n"), x.Name)
			return nil
		}),
	}
	uf := update.Flags()
	uf.StringVar(&uName, "name", "", e.t("novo nome"))
	uf.StringVar(&uDesc, "description", "", e.t("nova descrição"))
	uf.IntVar(&uRot, "rotate-days", 0, e.t("prazo de rotação, em dias, dos itens sem prazo próprio"))

	rm := &cobra.Command{
		Use: "rm " + e.t("<coleção>"), Aliases: []string{"delete"}, Short: e.t("Apaga uma coleção (os segredos ficam; a padrão não se apaga)"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			if err := e.client().DeleteSecretCollection(cmd.Context(), co, a[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Coleção %s apagada.\n"), a[0])
			return nil
		}),
	}

	add := &cobra.Command{
		Use: "add " + e.t("<coleção> <segredo>"), Short: e.t("Põe um segredo na coleção"), Args: cobra.ExactArgs(2),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			if err := e.client().AddSecretToCollection(cmd.Context(), co, a[0], a[1]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Segredo %s na coleção %s.\n"), a[1], a[0])
			return nil
		}),
	}
	remove := &cobra.Command{
		Use: "remove " + e.t("<coleção> <segredo>"), Short: e.t("Tira um segredo da coleção"), Args: cobra.ExactArgs(2),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			if err := e.client().RemoveSecretFromCollection(cmd.Context(), co, a[0], a[1]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Segredo %s fora da coleção %s.\n"), a[1], a[0])
			return nil
		}),
	}

	c.AddCommand(ls, create, show, secrets, update, rm, add, remove)
	return c
}
