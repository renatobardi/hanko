package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/dotenv"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// Import de .env (ADR-0068 §8.1, issue #62): a prévia mostra o mapeamento e os conflitos sem gravar; gravar exige admin
// e reautenticação recente (ADR-0070) e é tudo ou nada. O valor não passa por atividade, log nem mensagem de erro.

const (
	// EnvImportSingleItem faz um segredo com um campo por variável; a primeira vira o campo `value`, que todo segredo tem.
	EnvImportSingleItem = "single_item"
	// EnvImportItems faz um segredo por variável, com o valor no campo `value`.
	EnvImportItems = "items"

	maxEnvImportBytes = 256 << 10
)

// EnvImportInput é o pedido de prévia ou de import de um .env.
type EnvImportInput struct {
	Content    string
	Mode       string // EnvImportSingleItem ou EnvImportItems
	ItemName   string // só em single_item
	Collection string // nome ou id; vazio = a padrão
	Overwrite  bool   // rotaciona o que já existe em vez de pular
}

// EnvImportSkip é uma linha que não virou valor, com o motivo (nunca o conteúdo da linha).
type EnvImportSkip struct {
	Line   int
	Reason string
}

// EnvImportItem é um segredo que o import cria ou atualiza. Fields leva `campo` ou `campo ← VARIAVEL` quando o nome do
// campo não é o da variável (a primeira variável de single_item vira `value`).
type EnvImportItem struct {
	Secret string
	Fields []string
	Exists bool
}

// EnvImportPlan é a prévia: o que seria gravado, o que já existe e o que foi ignorado.
type EnvImportPlan struct {
	Items      []EnvImportItem
	Conflicts  []string
	Skipped    []EnvImportSkip
	Collection string
}

// VaultImportResult resume um import (de .env ou de arquivo exportado).
type VaultImportResult struct {
	Secrets     int // segredos criados
	Fields      int // campos criados ou trocados
	Updated     int // segredos que já existiam e foram rotacionados (só com Overwrite)
	Existing    int // segredos que já existiam e ficaram como estavam
	Collections int // coleções criadas
	Grants      int // concessões criadas
	Skipped     int // concessões não aplicadas: sujeito ausente no destino ou coleção que já existia
}

type envField struct{ name, value, from string }

type envItem struct {
	name   string
	exists bool
	fields []envField
}

type envPlan struct {
	plan  EnvImportPlan
	items []envItem
	col   *store.SecretCollection
}

// PreviewEnvImport monta a prévia do import (admin). Não grava nada e não pede reautenticação.
func (s *Service) PreviewEnvImport(ctx context.Context, companyID domain.ID, in EnvImportInput) (EnvImportPlan, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return EnvImportPlan{}, err
	}
	p, err := s.planEnv(ctx, s.read(), companyID, in)
	return p.plan, err
}

// requireVaultTransfer confere quem importa ou exporta o cofre: pessoa, admin da empresa e reautenticação recente.
// Sem identidade no contexto é o modo confiável (instalação sem login): RequireReauth passa (ADR-0070 §5).
func (s *Service) requireVaultTransfer(ctx context.Context, actor domain.Actor, companyID domain.ID) error {
	if actor.Type != domain.ActorHuman {
		return domain.ErrForbidden
	}
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	id, _ := auth.IdentityFrom(ctx)
	_, err := s.RequireReauth(id)
	return err
}

// ImportEnv grava o .env no cofre, tudo ou nada.
func (s *Service) ImportEnv(ctx context.Context, actor domain.Actor, companyID domain.ID, in EnvImportInput) (VaultImportResult, error) {
	if err := s.requireVaultTransfer(ctx, actor, companyID); err != nil {
		return VaultImportResult{}, err
	}
	var res VaultImportResult
	err := s.withTx(ctx, func(t *txc) error {
		p, err := s.planEnv(t.ctx, t.q, companyID, in)
		if err != nil {
			return err
		}
		for _, it := range p.items {
			if err := s.applyEnvItem(t, actor, companyID, in, p.col, it, &res); err != nil {
				return fmt.Errorf("segredo %s: %w", it.name, err)
			}
		}
		return t.activity(actor, companyID, "vault.env_imported", "company", companyID, map[string]any{
			"mode": in.Mode, "secrets": res.Secrets, "fields": res.Fields, "updated": res.Updated,
			"existing": res.Existing, "skipped_lines": len(p.plan.Skipped)}, "")
	})
	return res, err
}

// planEnv lê o .env e decide o que viraria cada segredo. Linha que não serve vai em Skipped; pedido sem nada gravável é erro.
func (s *Service) planEnv(ctx context.Context, q *store.Queries, companyID domain.ID, in EnvImportInput) (envPlan, error) {
	var p envPlan
	if in.Mode != EnvImportSingleItem && in.Mode != EnvImportItems {
		return p, domain.Invalid("mode", "use single_item ou items")
	}
	if len(in.Content) > maxEnvImportBytes {
		return p, domain.Invalid("content", fmt.Sprintf("o .env passa de %d bytes", maxEnvImportBytes))
	}
	if !s.secretBox().Enabled() {
		return p, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar segredos")
	}
	if name := strings.TrimSpace(in.Collection); name != "" {
		col, err := s.resolveCollection(ctx, q, companyID, name)
		if err != nil {
			return p, err
		}
		p.col, p.plan.Collection = &col, col.Name
	}
	parsed := dotenv.Parse(in.Content)
	for _, sk := range parsed.Skipped {
		p.plan.Skipped = append(p.plan.Skipped, EnvImportSkip{Line: sk.Line, Reason: sk.Reason})
	}
	usable := make([]dotenv.Entry, 0, len(parsed.Entries))
	for _, e := range parsed.Entries {
		if reason := envValueProblem(e.Value); reason != "" {
			p.plan.Skipped = append(p.plan.Skipped, EnvImportSkip{Line: e.Line, Reason: reason})
			continue
		}
		usable = append(usable, e)
	}
	var err error
	if in.Mode == EnvImportItems {
		err = s.planEnvItems(&p, usable)
	} else {
		err = s.planEnvSingle(&p, usable, in.ItemName)
	}
	if err != nil {
		return p, err
	}
	if len(p.items) == 0 {
		return p, domain.Invalid("content", "nenhuma variável com valor para importar")
	}
	sort.Slice(p.plan.Skipped, func(i, j int) bool { return p.plan.Skipped[i].Line < p.plan.Skipped[j].Line })
	for i := range p.items {
		it := &p.items[i]
		exists, err := secretExists(ctx, q, companyID, it.name)
		if err != nil {
			return p, err
		}
		it.exists = exists
		view := EnvImportItem{Secret: it.name, Exists: exists}
		for _, f := range it.fields {
			if f.from != "" {
				view.Fields = append(view.Fields, f.name+" ← "+f.from)
			} else {
				view.Fields = append(view.Fields, f.name)
			}
		}
		p.plan.Items = append(p.plan.Items, view)
		if exists {
			p.plan.Conflicts = append(p.plan.Conflicts, it.name)
		}
	}
	return p, nil
}

// envValueProblem diz por que um valor não vira segredo ("" = serve).
func envValueProblem(v string) string {
	switch {
	case v == "":
		return "valor vazio"
	case len(v) > maxSecretValueBytes:
		return fmt.Sprintf("valor acima de %d bytes", maxSecretValueBytes)
	case strings.ContainsRune(v, 0):
		return "valor com byte nulo"
	}
	return ""
}

func (s *Service) planEnvItems(p *envPlan, entries []dotenv.Entry) error {
	for _, e := range entries {
		if !secretref.ValidName(e.Key) {
			p.plan.Skipped = append(p.plan.Skipped, EnvImportSkip{Line: e.Line, Reason: "nome não serve de nome de segredo"})
			continue
		}
		p.items = append(p.items, envItem{name: e.Key, fields: []envField{{name: domain.DefaultSecretField, value: e.Value}}})
	}
	return nil
}

func (s *Service) planEnvSingle(p *envPlan, entries []dotenv.Entry, itemName string) error {
	name, err := s.validateSecretName(itemName)
	if err != nil {
		return err
	}
	it := envItem{name: name}
	for _, e := range entries {
		switch {
		case !domain.ValidSecretFieldName(e.Key):
			p.plan.Skipped = append(p.plan.Skipped, EnvImportSkip{Line: e.Line, Reason: "nome não serve de nome de campo"})
		case len(it.fields) > 0 && e.Key == domain.DefaultSecretField:
			p.plan.Skipped = append(p.plan.Skipped, EnvImportSkip{Line: e.Line, Reason: "colide com o campo value, que é o da primeira variável"})
		case len(it.fields) == 0:
			it.fields = append(it.fields, envField{name: domain.DefaultSecretField, value: e.Value, from: e.Key})
		default:
			it.fields = append(it.fields, envField{name: e.Key, value: e.Value})
		}
	}
	if len(it.fields) > domain.MaxSecretFields {
		return domain.Invalid("content", fmt.Sprintf("um segredo leva até %d campos; o .env tem %d variáveis", domain.MaxSecretFields, len(it.fields)))
	}
	if len(it.fields) > 0 {
		p.items = append(p.items, it)
	}
	return nil
}

func secretExists(ctx context.Context, q *store.Queries, companyID domain.ID, name string) (bool, error) {
	if _, err := q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: name}); err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, fmt.Errorf("get secret: %w", err)
	}
	return true, nil
}

// applyEnvItem grava um item do plano: cria o segredo novo, rotaciona o que existe (com Overwrite) ou deixa como está.
func (s *Service) applyEnvItem(t *txc, actor domain.Actor, companyID domain.ID, in EnvImportInput, col *store.SecretCollection, it envItem, res *VaultImportResult) error {
	if it.exists && !in.Overwrite {
		res.Existing++
		return nil
	}
	if it.exists {
		return s.overwriteEnvItem(t, actor, companyID, it, res)
	}
	id := domain.NewID()
	first := it.fields[0]
	m, err := s.prepareMaterial(id, domain.SecretProviderLocal, first.value, false, domain.GenOptions{}, "")
	if err != nil {
		return err
	}
	sec, err := s.createSecretRowTx(t, actor, companyID, domain.SecretScopeCompany, nil, id, it.name, "Importado de .env", m)
	if err != nil {
		return err
	}
	if col == nil {
		err = s.addToDefaultCollectionTx(t, actor, companyID, sec.ID, sec.Name)
	} else {
		err = s.addToCollectionTx(t, actor, companyID, *col, sec.ID, sec.Name)
	}
	if err != nil {
		return err
	}
	if err := t.activity(actor, companyID, "secret.created", "secret", sec.ID, secretEventData(sec), ""); err != nil {
		return err
	}
	res.Secrets++
	res.Fields++
	for i, f := range it.fields[1:] {
		if _, err := s.createFieldTx(t, actor, companyID, sec, CreateSecretFieldInput{Name: f.name, Position: i + 1,
			SecretFieldValueInput: SecretFieldValueInput{Value: f.value}}); err != nil {
			return err
		}
		res.Fields++
	}
	return nil
}

// overwriteEnvItem rotaciona os campos de um segredo que já existe e cria os que faltam.
func (s *Service) overwriteEnvItem(t *txc, actor domain.Actor, companyID domain.ID, it envItem, res *VaultImportResult) error {
	sec, err := getSecretRow(t.ctx, t.q, companyID, domain.SecretScopeCompany, nil, it.name, true)
	if err != nil {
		return err
	}
	for _, f := range it.fields {
		cur, err := t.q.GetSecretFieldForUpdate(t.ctx, store.GetSecretFieldForUpdateParams{CompanyID: companyID, SecretID: sec.ID, Name: f.name})
		switch {
		case isNoRows(err):
			if _, err := s.createFieldTx(t, actor, companyID, sec, CreateSecretFieldInput{Name: f.name,
				SecretFieldValueInput: SecretFieldValueInput{Value: f.value}}); err != nil {
				return err
			}
		case err != nil:
			return mapErr(err, "get secret field")
		case cur.Kind != string(domain.SecretFieldText):
			return domain.Invalid("field", fmt.Sprintf("o campo %s é %s, não texto; o import não troca esse tipo", f.name, cur.Kind))
		case f.name == domain.DefaultSecretField:
			m, err := s.prepareMaterial(sec.ID, domain.SecretProviderLocal, f.value, false, domain.GenOptions{}, "")
			if err != nil {
				return err
			}
			if _, err := s.rotateSecretTx(t, actor, companyID, sec, m, sec.Description); err != nil {
				return err
			}
			if err := t.activity(actor, companyID, "secret.rotated", "secret", sec.ID, secretEventData(sec), ""); err != nil {
				return err
			}
		default:
			if err := s.rotateTextFieldTx(t, actor, companyID, sec, cur, f.value); err != nil {
				return err
			}
		}
		res.Fields++
	}
	res.Updated++
	return nil
}

// rotateTextFieldTx troca o valor de um campo texto que não é o `value`, com o anterior no histórico.
func (s *Service) rotateTextFieldTx(t *txc, actor domain.Actor, companyID domain.ID, sec store.Secret, f store.SecretField, value string) error {
	m, err := s.prepareFieldMaterial(sec.ID, f.ID, SecretFieldValueInput{Value: value}, domain.SecretFieldText)
	if err != nil {
		return err
	}
	if m.enc, err = s.sealMaterialFor(m, fieldAAD(sec.ID, f.ID)); err != nil {
		return err
	}
	row, err := s.rotateFieldTx(t, actor, companyID, f, m)
	if err != nil {
		return err
	}
	return t.activity(actor, companyID, "secret.field_updated", "secret", sec.ID, fieldEventData(row), "")
}

// addToCollectionTx põe o segredo numa coleção escolhida e registra o fato.
func (s *Service) addToCollectionTx(t *txc, actor domain.Actor, companyID domain.ID, col store.SecretCollection, secretID domain.ID, name string) error {
	if err := t.q.AddSecretToCollection(t.ctx, store.AddSecretToCollectionParams{CompanyID: companyID,
		CollectionID: col.ID, SecretID: secretID, Now: t.now}); err != nil {
		return mapErr(err, "add secret to collection")
	}
	return t.activity(actor, companyID, "collection.item_added", "collection", col.ID, map[string]any{"secret": name}, "")
}
