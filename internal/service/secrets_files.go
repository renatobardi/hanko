package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// ${secretfile:ITEM#campo} (ADR-0068 §5.2). O valor de um campo arquivo (ou de um campo texto) vira um arquivo 0600 criado
// para esta run, e o caminho entra no adapter_config da persona. Os arquivos saem de um diretório 0700 próprio, fora da
// worktree: um `git add -A` do agente não pega o segredo. Quem chama guarda a função de limpeza e a roda ao fim da run,
// inclusive quando ela falha.

// configResolverFor é o resolvedor do adapter_config da persona no despacho: ${secret:} vira o valor agora, e
// ${secretfile:} fica como está até a run criar o arquivo (MaterializeSecretFiles).
func (s *Service) configResolverFor(ctx context.Context, companyID, personaID domain.ID) func(secretref.Ref) (string, error) {
	base := s.resolverFor(ctx, companyID, &personaID)
	return func(r secretref.Ref) (string, error) {
		if r.File {
			return r.String(), nil
		}
		return base(r)
	}
}

// MaterializeSecretFiles troca cada ${secretfile:} de cfg (o adapter_config da persona, já com os valores resolvidos)
// pelo caminho de um arquivo 0600 criado nesta run. Devolve a configuração nova, os valores escritos (para a redação do
// log da run) e a limpeza, que apaga o diretório dos arquivos. Sem referência de arquivo, devolve cfg e uma limpeza
// vazia. Num erro, apaga o que já tinha criado.
func (s *Service) MaterializeSecretFiles(ctx context.Context, companyID, personaID, runID domain.ID, cfg json.RawMessage) (json.RawMessage, []string, func(), error) {
	noop := func() {}
	if !secretref.HasFile(string(cfg)) {
		return cfg, nil, noop, nil
	}
	var doc any
	if err := json.Unmarshal(cfg, &doc); err != nil {
		return cfg, nil, noop, fmt.Errorf("adapter_config: %w", err)
	}
	dir, err := os.MkdirTemp("", "hanko-secret-*")
	if err != nil {
		return cfg, nil, noop, fmt.Errorf("diretório dos arquivos de segredo: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	var values []string
	out, _, err := secretref.ExpandJSON(doc, s.fileResolverFor(ctx, companyID, personaID, runID, dir, &values))
	if err != nil {
		cleanup()
		return cfg, nil, noop, err
	}
	raw, err := json.Marshal(out) //nolint:gosec // configuração com os caminhos dos arquivos: vai só para o adapter
	if err != nil {
		cleanup()
		return cfg, nil, noop, fmt.Errorf("adapter_config: %w", err)
	}
	return raw, values, cleanup, nil
}

// fileResolverFor devolve, para cada ${secretfile:}, o caminho do arquivo criado em dir; o conteúdo escrito vai para
// values, para a redação. A referência precisa estar nas coleções que a persona alcança (ADR-0068 §2.4); o erro nomeia a
// referência, nunca o valor.
func (s *Service) fileResolverFor(ctx context.Context, companyID, personaID, runID domain.ID, dir string, values *[]string) func(secretref.Ref) (string, error) {
	return func(r secretref.Ref) (string, error) {
		if !r.File {
			return "", errors.New("referência de valor sem resolver no despacho")
		}
		if r.User {
			return "", errors.New("${secretfile:} de segredo de usuário ainda não é aceito (ADR-0068 §6)")
		}
		if err := s.requirePersonaUse(ctx, companyID, personaID, r); err != nil {
			return "", err
		}
		plain, err := s.fileRefValue(ctx, companyID, personaID, runID, r)
		if err != nil {
			return "", err
		}
		f, err := os.CreateTemp(dir, r.FieldName()+"-*") // 0600
		if err != nil {
			return "", fmt.Errorf("criar arquivo de %s: %w", r.String(), err)
		}
		if _, err := f.Write(plain); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("gravar arquivo de %s: %w", r.String(), err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("fechar arquivo de %s: %w", r.String(), err)
		}
		*values = append(*values, string(plain))
		return f.Name(), nil
	}
}

// fileRefValue lê o conteúdo de um campo para virar arquivo e registra o uso (via file). O TOTP não vira arquivo: a
// semente não sai, e o código muda a cada 30 s.
func (s *Service) fileRefValue(ctx context.Context, companyID, personaID, runID domain.ID, r secretref.Ref) ([]byte, error) {
	q := s.read()
	sec, err := q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: r.Name})
	if err != nil {
		return nil, mapErr(err, "segredo "+r.Name)
	}
	f, err := q.GetSecretField(ctx, store.GetSecretFieldParams{CompanyID: companyID, SecretID: sec.ID, Name: r.FieldName()})
	if err != nil {
		return nil, mapErr(err, "campo "+r.String())
	}
	if err := refKindErr(r, domain.SecretFieldKind(f.Kind)); err != nil {
		return nil, err
	}
	plain, err := s.fieldValue(ctx, companyID, sec, f)
	if err != nil {
		return nil, fmt.Errorf("ler %s: %w", r.String(), err)
	}
	c := ToolCaller{CompanyID: companyID, RunID: &runID, PersonaID: &personaID}
	if err := s.recordSecretUse(ctx, c, companyID, sec.ID, f, domain.SecretAccessFile); err != nil {
		return nil, err
	}
	return plain, nil
}
