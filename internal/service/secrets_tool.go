package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/totp"
)

// hanko_secret_get (ADR-0068 §4): a persona lê um campo de um item das coleções que alcança. Antes de tudo, o alcance
// (um item fora delas nem abre gate). Depois, a política de tools (padrão ask, §4.2). Só a chamada liberada recebe o
// valor, e o TOTP sai como código do momento: a semente não sai nunca (§4.3).

// HankoSecretGetTool é o nome da tool MCP de leitura de campo de segredo.
const HankoSecretGetTool = "hanko_secret_get"

// SecretGetInput pede um campo a uma run: o item, o campo (vazio = value) e a run que chama.
type SecretGetInput struct {
	RunID domain.ID
	Item  string
	Field string
}

// SecretGetResult é a resposta. Decision diz o que a política decidiu: allow entrega o conteúdo; ask e deny vêm com
// Reason (e o gate, quando abriu). Só o campo do tipo certo preenche o resto: Value para texto e arquivo de texto, e
// Code (com ExpiresIn) para TOTP.
type SecretGetResult struct {
	Decision  string
	Reason    string
	Kind      domain.SecretFieldKind
	Value     string
	Code      string
	ExpiresIn int
	FileName  string
	SizeBytes int
}

// runSecretValues guarda, por run, os valores que a tool hanko_secret_get entregou: a redação do log da run os tira dos
// eventos e do resultado (ADR-0068 §4.4). Vive enquanto a run executa; o executor esquece os valores ao terminá-la.
type runSecretValues struct {
	mu    sync.Mutex
	byRun map[domain.ID][]string
}

func (r *runSecretValues) add(runID domain.ID, v string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byRun == nil {
		r.byRun = map[domain.ID][]string{}
	}
	r.byRun[runID] = append(r.byRun[runID], v)
}

func (r *runSecretValues) list(runID domain.ID) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.byRun[runID]...)
}

func (r *runSecretValues) forget(runID domain.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byRun, runID)
}

// RunSecretValues devolve os valores de segredo que a run recebeu pela tool, para a redação do log dela.
func (s *Service) RunSecretValues(runID domain.ID) []string { return s.runSecrets.list(runID) }

// ForgetRunSecrets esquece os valores de segredo da run quando ela termina.
func (s *Service) ForgetRunSecrets(runID domain.ID) { s.runSecrets.forget(runID) }

// SecretGet lê um campo para a tool hanko_secret_get. O erro nomeia o item e o campo, nunca o valor.
func (s *Service) SecretGet(ctx context.Context, companyID domain.ID, in SecretGetInput) (SecretGetResult, error) {
	item, field := strings.TrimSpace(in.Item), strings.TrimSpace(in.Field)
	if field == "" {
		field = secretref.DefaultField
	}
	if !secretref.ValidName(item) {
		return SecretGetResult{}, domain.Invalid("item", "nome de item inválido")
	}
	if !secretref.ValidFieldName(field) {
		return SecretGetResult{}, domain.Invalid("campo", "nome de campo inválido")
	}
	c, err := s.RunToolCaller(ctx, companyID, in.RunID)
	if err != nil {
		return SecretGetResult{}, err
	}
	if c.PersonaID == nil {
		return SecretGetResult{}, domain.Invalid("run", "hanko_secret_get só roda numa run de persona")
	}
	ref := secretref.Ref{Name: item, Field: field}
	if _, err := s.read().GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: item}); err != nil {
		return SecretGetResult{}, mapErr(err, "segredo "+item)
	}
	// alcance antes da política: um item que a persona não alcança nem abre gate (ADR-0068 §2.5)
	if err := s.requirePersonaUse(ctx, companyID, *c.PersonaID, ref); err != nil {
		return SecretGetResult{}, err
	}
	args, err := json.Marshal(map[string]string{"item": item, "campo": field})
	if err != nil {
		return SecretGetResult{}, fmt.Errorf("argumentos da tool: %w", err)
	}
	d, err := s.NativeToolCheck(ctx, c, NativeToolRequest{Name: HankoSecretGetTool, Title: "ler " + ref.String(), Input: args, Hanko: true})
	if err != nil {
		return SecretGetResult{}, err
	}
	if d.Decision != NativeAllow {
		return SecretGetResult{Decision: d.Decision, Reason: d.Reason}, nil
	}
	return s.readSecretForTool(ctx, c, companyID, ref)
}

// readSecretForTool lê o campo liberado pela política, entrega o que o tipo permite e registra o uso (sem valor).
func (s *Service) readSecretForTool(ctx context.Context, c ToolCaller, companyID domain.ID, ref secretref.Ref) (SecretGetResult, error) {
	q := s.read()
	sec, err := q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: ref.Name})
	if err != nil {
		return SecretGetResult{}, mapErr(err, "segredo "+ref.Name)
	}
	f, err := q.GetSecretField(ctx, store.GetSecretFieldParams{CompanyID: companyID, SecretID: sec.ID, Name: ref.FieldName()})
	if err != nil {
		return SecretGetResult{}, mapErr(err, "campo "+ref.String())
	}
	plain, err := s.fieldValue(ctx, companyID, sec, f)
	if err != nil {
		return SecretGetResult{}, fmt.Errorf("ler %s: %w", ref.String(), err)
	}
	out := SecretGetResult{Decision: NativeAllow, Kind: domain.SecretFieldKind(f.Kind), FileName: f.FileName, SizeBytes: int(f.SizeBytes)}
	var shown string // o que a persona recebe, e portanto o que a redação da run tira do log
	switch out.Kind {
	case domain.SecretFieldTOTP:
		seed, err := totp.Parse(string(plain))
		if err != nil {
			return SecretGetResult{}, fmt.Errorf("semente de %s: %w", ref.String(), err)
		}
		out.Code, out.ExpiresIn = seed.At(s.Now())
		shown = out.Code
	case domain.SecretFieldFile:
		if !utf8.Valid(plain) {
			return SecretGetResult{}, domain.Invalid("arquivo", fmt.Sprintf("%s é binário (%d bytes): a tool entrega só texto; use ${secretfile:%s}", ref.String(), len(plain), ref.Name+"#"+ref.FieldName()))
		}
		out.Value = string(plain)
		shown = out.Value
	default:
		out.Value = string(plain)
		shown = out.Value
	}
	s.runSecrets.add(*c.RunID, shown)
	if err := s.recordSecretUse(ctx, c, companyID, sec.ID, f, domain.SecretAccessTool); err != nil {
		return SecretGetResult{}, err
	}
	return out, nil
}

// recordSecretUse registra o uso de um campo: a linha de secret_access_log (campo, via, run e persona), o instante de
// uso do campo e o fato secret.accessed. Nunca leva o valor (ADR-0068 §4.4).
func (s *Service) recordSecretUse(ctx context.Context, c ToolCaller, companyID, secretID domain.ID, f store.SecretField, via domain.SecretAccessVia) error {
	return s.withTx(ctx, func(t *txc) error {
		if err := t.q.LogSecretAccess(t.ctx, store.LogSecretAccessParams{ID: domain.NewID(), CompanyID: companyID, SecretID: secretID,
			FieldName: f.Name, Via: string(via), RunID: c.RunID, PersonaID: c.PersonaID, Now: t.now}); err != nil {
			return mapErr(err, "log secret access")
		}
		now := ptrTime(t.now)
		if err := t.q.TouchSecretUsed(t.ctx, store.TouchSecretUsedParams{ID: secretID, CompanyID: companyID, Now: now}); err != nil {
			return fmt.Errorf("touch secret: %w", err)
		}
		if err := t.q.TouchSecretFieldUsed(t.ctx, store.TouchSecretFieldUsedParams{ID: f.ID, CompanyID: companyID, Now: now}); err != nil {
			return fmt.Errorf("touch secret field: %w", err)
		}
		return t.activity(domain.PersonaActor(*c.PersonaID), companyID, "secret.accessed", "secret", secretID,
			map[string]any{"field": f.Name, "via": string(via), "run_id": c.RunID, "persona_id": c.PersonaID}, "")
	})
}
