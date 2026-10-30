package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Variáveis de ambiente da persona pelo banco (#117 fatia 3): grava só a referência com `persona.updated` na mesma
// transação, sem o valor no evento; recusa segredo inexistente sem gravar nada; espera a trava da persona (FOR UPDATE).
// Pulam sem HANKO_TEST_DATABASE_URL, como os demais testes de banco.

// adapterConfigNoBanco lê o adapter_config da persona direto da tabela.
func adapterConfigNoBanco(t *testing.T, e *secEnv, id domain.ID) string {
	t.Helper()
	var raw string
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT adapter_config::text FROM personas WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// eventosPersonaEnv conta os eventos persona.updated da variável `name` na empresa.
func eventosPersonaEnv(t *testing.T, e *secEnv, name string) int {
	t.Helper()
	return e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'persona.updated' AND (data::jsonb)->>'variable' = $2`, e.co.ID, name)
}

func TestPersonaEnvRefGravaSoReferenciaComEvento(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "gw", sentinel)
	p := e.persona(t, "Ana", func(in *service.HirePersonaInput) { in.AdapterConfig = []byte(`{"command":"agente"}`) })

	got, err := e.svc.SetPersonaEnvRef(bg, domain.HumanActor, e.co.ID, p.ID, "API_TOKEN", "${secret:gw}")
	if err != nil {
		t.Fatalf("pôr a referência: %v", err)
	}
	if !strings.Contains(string(got.AdapterConfig), "${secret:gw}") {
		t.Fatalf("resposta sem a referência: %s", got.AdapterConfig)
	}
	raw := adapterConfigNoBanco(t, e, p.ID)
	if !strings.Contains(raw, `"API_TOKEN": "${secret:gw}"`) && !strings.Contains(raw, `"API_TOKEN":"${secret:gw}"`) {
		t.Fatalf("banco sem a referência: %s", raw)
	}
	if !strings.Contains(raw, `"command"`) {
		t.Fatalf("chave fora do env sumiu: %s", raw)
	}
	if strings.Contains(raw, sentinel) {
		t.Fatalf("valor em claro no adapter_config: %s", raw)
	}
	if n := eventosPersonaEnv(t, e, "API_TOKEN"); n != 1 {
		t.Fatalf("eventos persona.updated para API_TOKEN = %d, quer 1", n)
	}
	// o evento diz o campo e o nome da variável, nunca o valor
	var data string
	if err := e.svc.Pool.QueryRow(bg, `SELECT data::text FROM activity_events WHERE company_id = $1 AND verb = 'persona.updated' AND (data::jsonb)->>'variable' = 'API_TOKEN'`, e.co.ID).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, sentinel) || strings.Contains(data, "${secret:gw}") || !strings.Contains(data, "env") {
		t.Fatalf("evento persona.updated: %s", data)
	}

	// tirar a variável grava o evento com removed=true e apaga a chave env (era a única variável)
	if _, err := e.svc.SetPersonaEnvRef(bg, domain.HumanActor, e.co.ID, p.ID, "API_TOKEN", ""); err != nil {
		t.Fatalf("tirar a variável: %v", err)
	}
	if raw := adapterConfigNoBanco(t, e, p.ID); strings.Contains(raw, `"env"`) {
		t.Fatalf("env sobrou depois de tirar a única variável: %s", raw)
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'persona.updated' AND (data::jsonb)->>'variable' = 'API_TOKEN' AND (data::jsonb)->>'removed' = 'true'`, e.co.ID); n != 1 {
		t.Fatalf("evento de remoção = %d, quer 1", n)
	}
}

func TestPersonaEnvRefRecusaSegredoInexistenteSemGravar(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	p := e.persona(t, "Beto", func(in *service.HirePersonaInput) { in.AdapterConfig = []byte(`{"command":"agente"}`) })
	before := adapterConfigNoBanco(t, e, p.ID)

	_, err := e.svc.SetPersonaEnvRef(bg, domain.HumanActor, e.co.ID, p.ID, "API_TOKEN", "${secret:nao-existe}")
	if err == nil {
		t.Fatal("referência a segredo inexistente foi aceita")
	}
	if after := adapterConfigNoBanco(t, e, p.ID); after != before {
		t.Fatalf("recusa mexeu no banco: antes %s, depois %s", before, after)
	}
	if n := eventosPersonaEnv(t, e, "API_TOKEN"); n != 0 {
		t.Fatalf("recusa deixou %d evento(s), quer 0 (a transação devia voltar)", n)
	}
}

func TestPersonaEnvRefEsperaATravaDaPersona(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "gw", sentinel)
	p := e.persona(t, "Cida", func(in *service.HirePersonaInput) { in.AdapterConfig = []byte(`{"command":"agente"}`) })

	// outra transação segura a linha da persona; a troca de variável tem de esperar por ela
	tx, err := e.svc.Pool.Begin(bg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(bg) }()
	if _, err := tx.Exec(bg, `SELECT id FROM personas WHERE id = $1 FOR UPDATE`, p.ID); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := e.svc.SetPersonaEnvRef(bg, domain.HumanActor, e.co.ID, p.ID, "API_TOKEN", "${secret:gw}")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("gravou com a persona travada por outra transação: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(bg); err != nil {
		t.Fatal(err)
	}
	if err := esperaFechar(t, done); err != nil {
		t.Fatalf("depois de soltar a trava: %v", err)
	}
	if n := eventosPersonaEnv(t, e, "API_TOKEN"); n != 1 {
		t.Fatalf("eventos depois da trava = %d, quer 1", n)
	}
}

func esperaFechar(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("a chamada não voltou em 10 s")
		return nil
	}
}
