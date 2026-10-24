package service_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Envio por link (ADR-0069, #59). As funções abaixo são o lado do navegador escrito em Go, com a biblioteca padrão:
// o teste fala o formato do ADR, não o código do servidor, e o teste de interoperabilidade confere o mesmo formato
// no WebCrypto (internal/ui/static/send-crypto.js).

const sentinelEnvio = "SENTINELA-envio-texto-claro-5f6a7b"

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64u(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64url %q: %v", s, err)
	}
	return b
}

// clienteChave é a chave do link e o id público, gerados como o navegador gera.
func clienteChave(t *testing.T) ([]byte, string) {
	t.Helper()
	link := make([]byte, 32)
	id := make([]byte, 16)
	if _, err := rand.Read(link); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return link, b64u(id)
}

// clienteSenha deriva a chave da senha (PBKDF2-SHA256, 600 000 iterações) e a prova que o servidor confere.
func clienteSenha(password string, salt []byte) (pwd, prova []byte) {
	pwd = pbkdf2.Key([]byte(password), salt, 600000, 32, sha256.New)
	prova = make([]byte, 32)
	_, _ = hkdf.New(sha256.New, pwd, nil, []byte("hanko-send-auth")).Read(prova)
	return pwd, prova
}

// clienteAESKey deriva a chave AES-GCM: ikm = chave do link (+ senha derivada), sal = o do envio.
func clienteAESKey(linkKey, pwd, salt []byte) []byte {
	ikm := linkKey
	if pwd != nil {
		ikm = append(append([]byte{}, linkKey...), pwd...)
	}
	k := make([]byte, 32)
	_, _ = hkdf.New(sha256.New, ikm, salt, []byte("hanko-send-v1")).Read(k)
	return k
}

// clienteCifra cifra o texto como o navegador: nonce de 12 bytes na frente, AAD "send/<id público>".
func clienteCifra(t *testing.T, key []byte, publicID, plain string) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return b64u(gcm.Seal(nonce, nonce, []byte(plain), []byte("send/"+publicID)))
}

// clienteDecifra decifra como o navegador; chave errada ou texto alterado devolvem erro.
func clienteDecifra(key []byte, publicID, cifra string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cifra)
	if err != nil || len(raw) < 12 {
		return "", errors.New("cifra inválida")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, raw[:12], raw[12:], []byte("send/"+publicID))
	return string(plain), err
}

// captura liga o log do service num buffer, para conferir que a chave e o texto não aparecem nele.
func captura(svc *service.Service) *bytes.Buffer {
	buf := &bytes.Buffer{}
	svc.Log = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return buf
}

// vazamentos devolve, para cada lugar que o servidor guarda ou registra, se o valor aparece nele.
func vazamentos(t *testing.T, e *env, companyID domain.ID, logs *bytes.Buffer, valores ...string) []string {
	t.Helper()
	ctx := context.Background()
	var banco, eventos string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT COALESCE(string_agg(row_to_json(s)::text, ' '), '') FROM secret_sends s WHERE company_id = $1`,
		companyID).Scan(&banco); err != nil {
		t.Fatalf("ler envios: %v", err)
	}
	if err := e.svc.Pool.QueryRow(ctx, `SELECT COALESCE(string_agg(data::text, ' '), '') FROM activity_events WHERE company_id = $1`,
		companyID).Scan(&eventos); err != nil {
		t.Fatalf("ler eventos: %v", err)
	}
	var achados []string
	for _, v := range valores {
		for nome, texto := range map[string]string{"banco": banco, "evento": eventos, "log": logs.String()} {
			if strings.Contains(texto, v) {
				achados = append(achados, nome)
			}
		}
	}
	return achados
}

// logadoComEnvio faz um envio criado por humano, com a reautenticação recente que o service exige.
func logadoComEnvio(t *testing.T, f *reauthFixture) context.Context {
	t.Helper()
	_, id := f.provedSession(t)
	return auth.WithIdentity(context.Background(), id)
}

// O servidor nunca recebe a chave do link: um envio criado por humano não deixa a chave, nem o texto claro, no banco,
// nos eventos nem no log.
func TestEnvioHumanoNaoLevaAChaveAoServidor(t *testing.T) {
	f := newReauthFixture(t)
	logs := captura(f.e.svc)
	ctx := logadoComEnvio(t, f)
	linkKey, publicID := clienteChave(t)
	cifra := clienteCifra(t, clienteAESKey(linkKey, nil, nil), publicID, sentinelEnvio)

	v, err := f.e.svc.CreateSend(ctx, f.owner.Actor(), f.co.ID, service.CreateSendInput{
		PublicID: publicID, Kind: domain.SendText, Ciphertext: cifra, Label: "rótulo", ExpiresIn: time.Hour})
	if err != nil {
		t.Fatalf("criar envio: %v", err)
	}
	if v.PublicID != publicID || v.Link != "" {
		t.Fatalf("envio = %+v; o servidor não monta link de envio humano", v.SecretSend)
	}
	if achados := vazamentos(t, f.e, f.co.ID, logs, b64u(linkKey), sentinelEnvio); len(achados) > 0 {
		t.Fatalf("a chave ou o texto claro apareceu em: %v", achados)
	}
	if n := f.e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'send.created'`, f.co.ID); n != 1 {
		t.Fatalf("eventos send.created = %d, quer 1", n)
	}
}

// Envio por persona: o servidor cifra o campo com uma chave nova e devolve o link. A chave vive só no link: não
// fica no banco, nos eventos nem no log, e o link abre o envio.
func TestEnvioPersonaNaoGuardaAChave(t *testing.T) {
	e := newSecEnv(t)
	e.svc.PublicURL = "https://hanko.exemplo.com"
	e.create(t, "api-token", sentinelEnvio)
	ana := e.persona(t, "Ana", nil)
	ator := domain.Actor{Type: domain.ActorPersona, ID: &ana.ID}

	v, err := e.svc.CreatePersonaSend(context.Background(), ator, e.co.ID, service.PersonaSendInput{
		Item: "api-token", ExpiresIn: time.Hour})
	if err != nil {
		t.Fatalf("envio da persona: %v", err)
	}
	prefixo := "https://hanko.exemplo.com/s/" + v.PublicID + "#"
	if !strings.HasPrefix(v.Link, prefixo) {
		t.Fatalf("link = %q, quer prefixo %q", v.Link, prefixo)
	}
	chave := unb64u(t, strings.TrimPrefix(v.Link, prefixo))
	if len(chave) != 32 {
		t.Fatalf("chave no fragmento tem %d bytes, quer 32", len(chave))
	}
	if achados := vazamentos(t, e.env, e.co.ID, e.logs, b64u(chave), sentinelEnvio); len(achados) > 0 {
		t.Fatalf("a chave ou o texto claro apareceu em: %v", achados)
	}
	var cifra string
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT ciphertext FROM secret_sends WHERE id = $1`, v.ID).Scan(&cifra); err != nil {
		t.Fatalf("ler cifra: %v", err)
	}
	if plain, err := clienteDecifra(clienteAESKey(chave, nil, nil), v.PublicID, cifra); err != nil || plain != sentinelEnvio {
		t.Fatalf("o link não abre o envio: %q, %v", plain, err)
	}
	if v.SourceField != domain.DefaultSecretField {
		t.Fatalf("campo de origem = %q, quer %q", v.SourceField, domain.DefaultSecretField)
	}
}

// Expira por prazo: depois do prazo ninguém abre, e a limpeza do job apaga o conteúdo.
func TestEnvioExpiraPorPrazoEApagaOConteudo(t *testing.T) {
	f := newReauthFixture(t)
	ctx := logadoComEnvio(t, f)
	linkKey, publicID := clienteChave(t)
	cifra := clienteCifra(t, clienteAESKey(linkKey, nil, nil), publicID, sentinelEnvio)
	if _, err := f.e.svc.CreateSend(ctx, f.owner.Actor(), f.co.ID, service.CreateSendInput{
		PublicID: publicID, Kind: domain.SendText, Ciphertext: cifra, ExpiresIn: 10 * time.Minute}); err != nil {
		t.Fatalf("criar envio: %v", err)
	}
	bg := context.Background()
	if _, err := f.e.svc.OpenPublicSend(bg, publicID, ""); err != nil {
		t.Fatalf("antes do prazo, abrir: %v", err)
	}

	f.advance(11 * time.Minute)
	if _, err := f.e.svc.OpenPublicSend(bg, publicID, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("depois do prazo, abrir = %v; quer não encontrado", err)
	}
	if info, err := f.e.svc.PublicSendInfo(bg, publicID); err != nil || info.Openable {
		t.Fatalf("info depois do prazo = %+v, %v; quer não abrível", info, err)
	}
	n, err := f.e.svc.PurgeExpiredSends(bg)
	if err != nil || n != 1 {
		t.Fatalf("limpeza = %d, %v; quer 1 envio apagado", n, err)
	}
	var cifraDepois string
	var apagadoEm *time.Time
	if err := f.e.svc.Pool.QueryRow(bg, `SELECT ciphertext, purged_at FROM secret_sends WHERE public_id = $1`, publicID).Scan(&cifraDepois, &apagadoEm); err != nil {
		t.Fatalf("ler envio: %v", err)
	}
	if cifraDepois != "" || apagadoEm == nil {
		t.Fatalf("depois da limpeza: cifra %q, apagado em %v; quer conteúdo vazio e data", cifraDepois, apagadoEm)
	}
}

// Expira por acessos: o último acesso permitido entrega o conteúdo e já o apaga; o seguinte não encontra nada.
func TestEnvioExpiraPorAcessosEApagaNoUltimo(t *testing.T) {
	f := newReauthFixture(t)
	ctx := logadoComEnvio(t, f)
	linkKey, publicID := clienteChave(t)
	cifra := clienteCifra(t, clienteAESKey(linkKey, nil, nil), publicID, sentinelEnvio)
	limite := 2
	if _, err := f.e.svc.CreateSend(ctx, f.owner.Actor(), f.co.ID, service.CreateSendInput{
		PublicID: publicID, Kind: domain.SendText, Ciphertext: cifra, ExpiresIn: time.Hour, MaxAccesses: &limite}); err != nil {
		t.Fatalf("criar envio: %v", err)
	}
	bg := context.Background()
	for i := 1; i <= limite; i++ {
		v, err := f.e.svc.OpenPublicSend(bg, publicID, "")
		if err != nil {
			t.Fatalf("acesso %d: %v", i, err)
		}
		if i == limite && v.Ciphertext == "" {
			t.Fatal("o último acesso permitido devia entregar o conteúdo")
		}
	}
	if _, err := f.e.svc.OpenPublicSend(bg, publicID, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("acesso além do limite = %v; quer não encontrado", err)
	}
	var cifraDepois string
	if err := f.e.svc.Pool.QueryRow(bg, `SELECT ciphertext FROM secret_sends WHERE public_id = $1`, publicID).Scan(&cifraDepois); err != nil {
		t.Fatalf("ler envio: %v", err)
	}
	if cifraDepois != "" {
		t.Fatal("o conteúdo ficou no banco depois do último acesso")
	}
	if n := f.e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'send.expired' AND data->>'reason' = 'limit'`, f.co.ID); n != 1 {
		t.Fatalf("eventos send.expired por limite = %d, quer 1", n)
	}
}

// Senha errada não decifra: a prova errada não devolve o texto, não conta acesso e, depois de cinco erros, bloqueia.
// A senha certa abre, e a chave derivada de outra senha não decifra a cifra (AES-GCM confere a tag).
func TestEnvioSenhaErradaNaoDecifra(t *testing.T) {
	f := newReauthFixture(t)
	ctx := logadoComEnvio(t, f)
	bg := context.Background()
	linkKey, publicID := clienteChave(t)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	pwd, prova := clienteSenha("correta-cavalo", salt)
	verificador := sha256.Sum256(prova)
	cifra := clienteCifra(t, clienteAESKey(linkKey, pwd, salt), publicID, sentinelEnvio)
	if _, err := f.e.svc.CreateSend(ctx, f.owner.Actor(), f.co.ID, service.CreateSendInput{
		PublicID: publicID, Kind: domain.SendText, Ciphertext: cifra, ExpiresIn: time.Hour,
		KdfSalt: b64u(salt), PasswordVerifier: b64u(verificador[:])}); err != nil {
		t.Fatalf("criar envio com senha: %v", err)
	}
	_, provaErrada := clienteSenha("errada", salt)
	provaErradaB64 := b64u(provaErrada)

	for i := 1; i <= domain.SendLockAfterFails; i++ {
		v, err := f.e.svc.OpenPublicSend(bg, publicID, provaErradaB64)
		if !errors.Is(err, service.ErrSendPassword) || v.Ciphertext != "" {
			t.Fatalf("senha errada %d: erro %v, cifra %q; quer ErrSendPassword sem texto", i, err, v.Ciphertext)
		}
	}
	if _, err := f.e.svc.OpenPublicSend(bg, publicID, b64u(prova)); !errors.Is(err, service.ErrSendLocked) {
		t.Fatalf("com a senha certa depois de %d erros = %v; quer bloqueado", domain.SendLockAfterFails, err)
	}

	f.advance(domain.SendLockFor + time.Minute)
	v, err := f.e.svc.OpenPublicSend(bg, publicID, b64u(prova))
	if err != nil || v.Ciphertext == "" {
		t.Fatalf("depois do bloqueio, senha certa: %v", err)
	}
	if plain, err := clienteDecifra(clienteAESKey(linkKey, pwd, salt), publicID, v.Ciphertext); err != nil || plain != sentinelEnvio {
		t.Fatalf("senha certa não decifrou: %q, %v", plain, err)
	}
	outra, _ := clienteSenha("errada", salt)
	if _, err := clienteDecifra(clienteAESKey(linkKey, outra, salt), publicID, v.Ciphertext); err == nil {
		t.Fatal("a chave de outra senha decifrou a cifra")
	}
	if _, err := clienteDecifra(clienteAESKey(linkKey, nil, nil), publicID, v.Ciphertext); err == nil {
		t.Fatal("a chave sem senha decifrou a cifra de envio com senha")
	}
}

// Envio por persona exige item#campo de coleção concedida à persona. Sem concessão, o erro nomeia o item e a persona,
// nunca o valor; semente TOTP não sai por envio; quem não é persona não cria envio por esse caminho.
func TestEnvioPersonaExigeColecaoConcedida(t *testing.T) {
	e := newSecEnv(t)
	e.svc.PublicURL = "https://hanko.exemplo.com"
	bg := context.Background()
	ana := e.persona(t, "Ana", nil)
	ator := domain.Actor{Type: domain.ActorPersona, ID: &ana.ID}
	personaSemColecao(t, e, "restrito", "infra")
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "restrito",
		service.CreateSecretFieldInput{Name: "totp", Kind: domain.SecretFieldTOTP, SecretFieldValueInput: service.SecretFieldValueInput{Value: "JBSWY3DPEHPK3PXP"}}); err != nil {
		t.Fatalf("criar campo TOTP: %v", err)
	}

	_, err := e.svc.CreatePersonaSend(bg, ator, e.co.ID, service.PersonaSendInput{Item: "restrito", ExpiresIn: time.Hour})
	if err == nil {
		t.Fatal("persona fora da coleção criou envio")
	}
	if !strings.Contains(err.Error(), "restrito") || !strings.Contains(err.Error(), ana.Slug) {
		t.Fatalf("o erro devia nomear o item e a persona: %v", err)
	}
	if strings.Contains(err.Error(), sentinelV1) {
		t.Fatal("o erro vazou o valor")
	}

	grantPersona(t, e, "infra", domain.CollectionSubjectPersona, ana.Slug)
	if _, err := e.svc.CreatePersonaSend(bg, ator, e.co.ID, service.PersonaSendInput{Item: "restrito", Field: "totp", ExpiresIn: time.Hour}); err == nil {
		t.Fatal("semente TOTP saiu por envio")
	}
	if _, err := e.svc.CreatePersonaSend(bg, ator, e.co.ID, service.PersonaSendInput{Item: "restrito", Field: "nao-existe", ExpiresIn: time.Hour}); err == nil {
		t.Fatal("campo inexistente criou envio")
	}
	if _, err := e.svc.CreatePersonaSend(bg, ator, e.co.ID, service.PersonaSendInput{Item: "restrito", ExpiresIn: time.Hour}); err != nil {
		t.Fatalf("com a coleção concedida, o envio devia sair: %v", err)
	}
	if _, err := e.svc.CreatePersonaSend(bg, domain.HumanActor, e.co.ID, service.PersonaSendInput{Item: "restrito", ExpiresIn: time.Hour}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("humano pelo caminho da persona = %v; quer proibido", err)
	}
}

// Criar envio por humano exige reautenticação recente (ADR-0070): sessão sem prova recebe ReauthError.
func TestEnvioHumanoPedeReauth(t *testing.T) {
	f := newReauthFixture(t)
	_, id := f.session(t, f.owner)
	ctx := auth.WithIdentity(context.Background(), id)
	linkKey, publicID := clienteChave(t)
	cifra := clienteCifra(t, clienteAESKey(linkKey, nil, nil), publicID, sentinelEnvio)
	_, err := f.e.svc.CreateSend(ctx, f.owner.Actor(), f.co.ID, service.CreateSendInput{
		PublicID: publicID, Kind: domain.SendText, Ciphertext: cifra, ExpiresIn: time.Hour})
	var re *domain.ReauthError
	if !errors.As(err, &re) {
		t.Fatalf("criar sem prova = %v; quer ReauthError", err)
	}
}

// A página e a API decifram o mesmo formato que o servidor cifra. O teste roda send-crypto.js no Node (o WebCrypto
// do navegador) e confere nos dois sentidos, com e sem senha. Sem Node instalado, o teste não roda.
func TestPaginaDecifraComOMesmoFormato(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node não está no PATH: o teste de interoperabilidade com o WebCrypto não roda")
	}
	modulo, err := filepath.Abs("../ui/static/send-crypto.js")
	if err != nil {
		t.Fatal(err)
	}
	linkKey, publicID := clienteChave(t)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	const senha = "senha de teste"
	pwd, prova := clienteSenha(senha, salt)
	entrada := map[string]string{
		"modulo": "file://" + modulo, "publicId": publicID, "linkKey": b64u(linkKey), "salt": b64u(salt), "senha": senha,
		"cifraComSenha": clienteCifra(t, clienteAESKey(linkKey, pwd, salt), publicID, sentinelEnvio),
		"cifraSemSenha": clienteCifra(t, clienteAESKey(linkKey, nil, nil), publicID, sentinelEnvio),
	}
	dir := t.TempDir()
	entradaArq := filepath.Join(dir, "entrada.json")
	dados, _ := json.Marshal(entrada)
	if err := os.WriteFile(entradaArq, dados, 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "confere.mjs")
	if err := os.WriteFile(script, []byte(nodeScript), 0o600); err != nil {
		t.Fatal(err)
	}
	saida, err := exec.Command(node, script, entradaArq).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, saida)
	}
	var res struct {
		Prova                  string `json:"prova"`
		PlainComSenha          string `json:"plainComSenha"`
		PlainSemSenha          string `json:"plainSemSenha"`
		DecifrouComSenhaErrada bool   `json:"decifrouComSenhaErrada"`
		CifraDoJS              string `json:"cifraDoJS"`
	}
	if err := json.Unmarshal(saida, &res); err != nil {
		t.Fatalf("saída do node %q: %v", saida, err)
	}
	if subtle.ConstantTimeCompare([]byte(res.Prova), []byte(b64u(prova))) != 1 {
		t.Fatal("a prova do WebCrypto difere da prova do servidor")
	}
	if res.PlainComSenha != sentinelEnvio || res.PlainSemSenha != sentinelEnvio {
		t.Fatalf("o navegador não decifrou: com senha %q, sem senha %q", res.PlainComSenha, res.PlainSemSenha)
	}
	if res.DecifrouComSenhaErrada {
		t.Fatal("o navegador decifrou com a senha errada")
	}
	if plain, err := clienteDecifra(clienteAESKey(linkKey, nil, nil), publicID, res.CifraDoJS); err != nil || plain != "resposta do navegador" {
		t.Fatalf("o servidor não decifra a cifra do navegador: %q, %v", plain, err)
	}
}

// nodeScript é o lado do navegador do teste de interoperabilidade: deriva, decifra, tenta a senha errada e cifra.
const nodeScript = `import fs from 'node:fs';
const e = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const c = await import(e.modulo);
const linkKey = c.b64uDecode(e.linkKey);
const salt = c.b64uDecode(e.salt);
const pwd = await c.derivePassword(e.senha, e.salt);
const prova = c.b64uEncode(await c.proofFor(pwd));
const plainComSenha = await c.decryptText(await c.aesKeyFor(linkKey, pwd, salt), e.publicId, e.cifraComSenha);
const plainSemSenha = await c.decryptText(await c.aesKeyFor(linkKey, null, new Uint8Array(0)), e.publicId, e.cifraSemSenha);
let decifrouComSenhaErrada = false;
try {
  const outra = await c.derivePassword('errada', e.salt);
  await c.decryptText(await c.aesKeyFor(linkKey, outra, salt), e.publicId, e.cifraComSenha);
  decifrouComSenhaErrada = true;
} catch {
  decifrouComSenhaErrada = false;
}
const cifraDoJS = await c.encryptText(await c.aesKeyFor(linkKey, null, new Uint8Array(0)), e.publicId, 'resposta do navegador');
console.log(JSON.stringify({ prova, plainComSenha, plainSemSenha, decifrouComSenhaErrada, cifraDoJS }));
`
