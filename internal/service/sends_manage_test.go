package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Gestão do envio (ADR-0069, fatia 4, #59): subir o conteúdo de um envio de arquivo, listar, ver e apagar. O servidor
// guarda o blob cifrado como veio; o teste confere o tamanho declarado, o alcance por papel e a limpeza do blob.

// criaArquivo cria um envio de arquivo de 10 bytes em claro, pelo dono logado (com reautenticação).
func criaArquivo(t *testing.T, f *reauthFixture, ctx context.Context, expira time.Duration) service.SendView {
	t.Helper()
	_, publicID := clienteChave(t)
	v, err := f.e.svc.CreateSend(ctx, f.owner.Actor(), f.co.ID, service.CreateSendInput{
		PublicID: publicID, Kind: domain.SendFile, SizeBytes: 10, Label: "arquivo", ExpiresIn: expira})
	if err != nil {
		t.Fatalf("criar envio de arquivo: %v", err)
	}
	return v
}

// cifraDeArquivo é o conteúdo cifrado de um arquivo de 10 bytes: os 10 bytes mais os 28 da cifra AES-GCM. O servidor
// não decifra, então o teste só precisa do tamanho.
func cifraDeArquivo() []byte { return make([]byte, 10+28) }

// O conteúdo do arquivo tem o tamanho declarado mais a cifra: outro tamanho é recusado sem gravar nada; o tamanho certo
// grava o blob, e subir de novo troca o blob sem criar outra linha. Cada subida fica no registro de atividade.
func TestEnvioArquivoSobeEConfereOTamanho(t *testing.T) {
	f := newReauthFixture(t)
	ctx := logadoComEnvio(t, f)
	v := criaArquivo(t, f, ctx, time.Hour)
	if v.Kind != domain.SendFile || v.SizeBytes != 10 || v.PurgedAt != nil {
		t.Fatalf("envio = %+v; quer arquivo de 10 bytes, não apagado", v.SecretSend)
	}

	if _, err := f.e.svc.UploadSendContent(ctx, f.owner.Actor(), f.co.ID, v.ID, cifraDeArquivo()[:20]); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("subir com tamanho errado = %v; quer inválido", err)
	}
	if n := f.e.count(t, `SELECT count(*) FROM secret_send_blobs WHERE send_id = $1`, v.ID); n != 0 {
		t.Fatalf("o tamanho errado gravou %d blob(s); quer 0", n)
	}

	if _, err := f.e.svc.UploadSendContent(ctx, f.owner.Actor(), f.co.ID, v.ID, cifraDeArquivo()); err != nil {
		t.Fatalf("subir o conteúdo: %v", err)
	}
	if n := f.e.count(t, `SELECT count(*) FROM secret_send_blobs WHERE send_id = $1 AND octet_length(content) = 38`, v.ID); n != 1 {
		t.Fatalf("blobs de 38 bytes = %d, quer 1", n)
	}
	if _, err := f.e.svc.UploadSendContent(ctx, f.owner.Actor(), f.co.ID, v.ID, cifraDeArquivo()); err != nil {
		t.Fatalf("subir de novo: %v", err)
	}
	if n := f.e.count(t, `SELECT count(*) FROM secret_send_blobs WHERE send_id = $1`, v.ID); n != 1 {
		t.Fatalf("subir de novo criou %d linhas de blob; quer 1 (troca)", n)
	}
	if n := f.e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'send.uploaded'`, f.co.ID); n != 2 {
		t.Fatalf("eventos send.uploaded = %d, quer 2 (uma por subida aceita)", n)
	}
}

// Um envio de arquivo não sai pelo link: a página não o abre, e a recusa vem antes da senha e da contagem de acesso.
func TestEnvioArquivoNaoAbrePeloLink(t *testing.T) {
	f := newReauthFixture(t)
	ctx := logadoComEnvio(t, f)
	v := criaArquivo(t, f, ctx, time.Hour)
	bg := context.Background()
	info, err := f.e.svc.PublicSendInfo(bg, v.PublicID)
	if err != nil || info.Openable {
		t.Fatalf("info do arquivo = %+v, %v; quer não abrível", info, err)
	}
	if _, err := f.e.svc.OpenPublicSend(bg, v.PublicID, ""); !errors.Is(err, service.ErrSendFileOpen) {
		t.Fatalf("abrir arquivo = %v; quer ErrSendFileOpen", err)
	}
	if n := f.e.count(t, `SELECT count(*) FROM secret_sends WHERE id = $1 AND access_count = 0`, v.ID); n != 1 {
		t.Fatal("a recusa contou acesso")
	}
}

// O alcance segue o papel: o admin vê e apaga todos os envios da empresa; o membro, só os que criou. O envio de fora
// do alcance é "não encontrado", e não se diz que ele existe.
func TestEnvioGestaoRespeitaOPapel(t *testing.T) {
	f := newReauthFixture(t)
	bg := context.Background()
	logado := logadoComEnvio(t, f)
	dono := criaArquivo(t, f, logado, time.Hour)

	bruno := mkUser(t, f.e, "bruno@exemplo.com", domain.RoleMember)
	join(t, f.e, bruno, domain.MemberMember)
	linkKey, publicID := clienteChave(t)
	cifra := clienteCifra(t, clienteAESKey(linkKey, nil, nil), publicID, sentinelEnvio)
	// sem identidade no contexto é o modo confiável: a criação não pede reautenticação, como nas outras ações do servidor
	proprio, err := f.e.svc.CreateSend(bg, bruno.Actor(), f.co.ID, service.CreateSendInput{
		PublicID: publicID, Kind: domain.SendText, Ciphertext: cifra, ExpiresIn: time.Hour})
	if err != nil {
		t.Fatalf("membro criar envio: %v", err)
	}

	comoBruno := as(bruno)
	lista, err := f.e.svc.ListSends(comoBruno, bruno.Actor(), f.co.ID, 50)
	if err != nil || len(lista) != 1 || lista[0].ID != proprio.ID {
		t.Fatalf("membro lista %d envios (%v); quer só o próprio", len(lista), err)
	}
	if _, err := f.e.svc.GetSend(comoBruno, bruno.Actor(), f.co.ID, dono.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("membro viu envio de outro = %v; quer não encontrado", err)
	}
	if _, err := f.e.svc.GetSend(comoBruno, bruno.Actor(), f.co.ID, proprio.ID); err != nil {
		t.Fatalf("membro ver o próprio: %v", err)
	}
	if err := f.e.svc.DeleteSend(comoBruno, bruno.Actor(), f.co.ID, dono.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("membro apagou envio de outro = %v; quer não encontrado", err)
	}

	lista, err = f.e.svc.ListSends(logado, f.owner.Actor(), f.co.ID, 50)
	if err != nil || len(lista) != 2 {
		t.Fatalf("admin lista %d envios (%v); quer 2", len(lista), err)
	}
	if _, err := f.e.svc.GetSend(logado, f.owner.Actor(), f.co.ID, proprio.ID); err != nil {
		t.Fatalf("admin ver envio de membro: %v", err)
	}
}

// Quem não criou o envio de arquivo não sobe o conteúdo dele, e a recusa não diz que o envio existe.
func TestEnvioArquivoSoCriadorSobe(t *testing.T) {
	f := newReauthFixture(t)
	bg := context.Background()
	v := criaArquivo(t, f, logadoComEnvio(t, f), time.Hour)
	bruno := mkUser(t, f.e, "bruno@exemplo.com", domain.RoleMember)
	join(t, f.e, bruno, domain.MemberMember)
	if _, err := f.e.svc.UploadSendContent(bg, bruno.Actor(), f.co.ID, v.ID, cifraDeArquivo()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("membro subiu conteúdo de envio alheio = %v; quer não encontrado", err)
	}
	if n := f.e.count(t, `SELECT count(*) FROM secret_send_blobs WHERE send_id = $1`, v.ID); n != 0 {
		t.Fatalf("a recusa gravou %d blob(s)", n)
	}
}

// O prazo apaga o blob junto com o conteúdo: depois do prazo, o job tira a linha e o blob.
func TestEnvioArquivoExpiraEApagaOBlob(t *testing.T) {
	f := newReauthFixture(t)
	ctx := logadoComEnvio(t, f)
	v := criaArquivo(t, f, ctx, 10*time.Minute)
	if _, err := f.e.svc.UploadSendContent(ctx, f.owner.Actor(), f.co.ID, v.ID, cifraDeArquivo()); err != nil {
		t.Fatalf("subir: %v", err)
	}
	f.advance(11 * time.Minute)
	n, err := f.e.svc.PurgeExpiredSends(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("limpeza = %d, %v; quer 1 envio apagado", n, err)
	}
	if k := f.e.count(t, `SELECT count(*) FROM secret_send_blobs WHERE send_id = $1`, v.ID); k != 0 {
		t.Fatalf("depois da limpeza ficaram %d blob(s); quer 0", k)
	}
	if p := f.e.count(t, `SELECT count(*) FROM secret_sends WHERE id = $1 AND purged_at IS NOT NULL AND blob_key = ''`, v.ID); p != 1 {
		t.Fatal("o envio não ficou marcado como apagado, sem a chave do blob")
	}
}

// Apagar pelo criador tira o conteúdo e o blob, e registra o fato. Apagar de novo não acha nada.
func TestEnvioApagadoPeloCriadorLimpaOBlob(t *testing.T) {
	f := newReauthFixture(t)
	bg := context.Background()
	v := criaArquivo(t, f, logadoComEnvio(t, f), time.Hour)
	if _, err := f.e.svc.UploadSendContent(bg, f.owner.Actor(), f.co.ID, v.ID, cifraDeArquivo()); err != nil {
		t.Fatalf("subir: %v", err)
	}
	if err := f.e.svc.DeleteSend(bg, f.owner.Actor(), f.co.ID, v.ID); err != nil {
		t.Fatalf("apagar: %v", err)
	}
	if k := f.e.count(t, `SELECT count(*) FROM secret_send_blobs WHERE send_id = $1`, v.ID); k != 0 {
		t.Fatalf("apagar deixou %d blob(s)", k)
	}
	if n := f.e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'send.deleted'`, f.co.ID); n != 1 {
		t.Fatalf("eventos send.deleted = %d, quer 1", n)
	}
	if err := f.e.svc.DeleteSend(bg, f.owner.Actor(), f.co.ID, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("apagar de novo = %v; quer não encontrado", err)
	}
}

// Pedido de envio de arquivo inválido: texto junto, sem tamanho ou acima de 25 MiB. Recusa antes de gravar.
func TestEnvioArquivoRecusaEntradaInvalida(t *testing.T) {
	f := newReauthFixture(t)
	ctx := logadoComEnvio(t, f)
	casos := []struct {
		nome string
		in   service.CreateSendInput
	}{
		{"com texto cifrado", service.CreateSendInput{Kind: domain.SendFile, SizeBytes: 10, Ciphertext: "YWJj"}},
		{"sem tamanho", service.CreateSendInput{Kind: domain.SendFile, SizeBytes: 0}},
		{"acima de 25 MiB", service.CreateSendInput{Kind: domain.SendFile, SizeBytes: domain.MaxSendFileBytes + 1}},
	}
	for _, c := range casos {
		_, publicID := clienteChave(t)
		c.in.PublicID = publicID
		c.in.ExpiresIn = time.Hour
		if _, err := f.e.svc.CreateSend(ctx, f.owner.Actor(), f.co.ID, c.in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: criar = %v; quer inválido", c.nome, err)
		}
	}
	if n := f.e.count(t, `SELECT count(*) FROM secret_sends WHERE company_id = $1`, f.co.ID); n != 0 {
		t.Fatalf("pedidos inválidos gravaram %d envio(s)", n)
	}
}
