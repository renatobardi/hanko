package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/storage/storagetest"
)

// BlobStore (ADR-0033): artefatos no S3 (falso), convivência com o postgres e migração entre drivers.

type blobEnv struct {
	*env
	s3   *storagetest.FakeS3
	pg   *storage.Postgres
	logs *bytes.Buffer
}

// newBlobEnv monta o service com o driver s3 ativo (e o postgres disponível para leitura/migração).
func newBlobEnv(t *testing.T, active string) *blobEnv {
	t.Helper()
	e := newEnv(t)
	f := storagetest.New(t, "AK", "SK-secret", "hanko-art", "us-east-1")
	s3, err := storage.NewS3(storage.S3Config{Endpoint: f.URL(), Bucket: "hanko-art", AccessKey: "AK", SecretKey: "SK-secret", PathStyle: true, Prefix: "t"})
	if err != nil {
		t.Fatal(err)
	}
	pg := storage.NewPostgres(e.svc.Pool)
	if active == storage.DriverS3 {
		e.svc.Blobs = storage.NewRegistry(s3, pg)
	} else {
		e.svc.Blobs = storage.NewRegistry(pg, s3)
	}
	logs := &bytes.Buffer{}
	e.svc.Log = slog.New(slog.NewTextHandler(logs, nil))
	return &blobEnv{env: e, s3: f, pg: pg, logs: logs}
}

func (e *blobEnv) storageOf(t *testing.T, id domain.ID) string {
	t.Helper()
	var s string
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT storage FROM artifacts WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *blobEnv) pgRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_blobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestArtifactGoesToS3(t *testing.T) {
	e := newBlobEnv(t, storage.DriverS3)
	bg := context.Background()
	tk := e.task(t, service.CreateTaskInput{Title: "relatório"})
	content := bytes.Repeat([]byte("linha do relatório\n"), 500)

	v := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "relatorio.md", Content: content, TaskID: &tk.ID})
	if e.storageOf(t, v.ID) != storage.DriverS3 || e.pgRows(t) != 0 {
		t.Fatalf("storage = %s, linhas no postgres = %d", e.storageOf(t, v.ID), e.pgRows(t))
	}
	keys := e.s3.Keys()
	if len(keys) != 1 || !strings.HasSuffix(keys[0], "/"+v.ID.String()) || !strings.HasPrefix(keys[0], "t/"+e.co.ID.String()+"/") {
		t.Fatalf("chaves no S3 = %v", keys)
	}
	if e.s3.ContentType(keys[0]) != "text/markdown" {
		t.Errorf("content-type = %q", e.s3.ContentType(keys[0]))
	}
	a, got, err := e.svc.ArtifactContent(bg, e.co.ID, v.ID)
	if err != nil || !bytes.Equal(got, content) || a.ID != v.ID {
		t.Fatalf("leitura pelo S3: %d bytes, %v", len(got), err)
	}

	// o mesmo conteúdo não cria versão e não deixa objeto órfão
	again := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "relatorio.md", Content: content, TaskID: &tk.ID})
	if again.ID != v.ID || len(e.s3.Keys()) != 1 {
		t.Fatalf("repetição: id=%s chaves=%v", again.ID, e.s3.Keys())
	}

	// nova versão = novo objeto; o diff lê as duas pelo S3
	v2 := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "relatorio.md", Content: append(content, []byte("mais\n")...), TaskID: &tk.ID})
	if v2.Version != 2 || len(e.s3.Keys()) != 2 {
		t.Fatalf("v2 = %+v chaves=%v", v2, e.s3.Keys())
	}
	if d, err := e.svc.ArtifactDiff(bg, e.co.ID, v.ID, v2.ID); err != nil || d.Added == 0 {
		t.Fatalf("diff pelo S3: %+v %v", d, err)
	}

	// o export lê o conteúdo pelo driver de cada versão
	doc, err := e.svc.ExportCompany(bg, e.co.ID, service.ExportOptions{WithArtifacts: true})
	if err != nil || len(doc.Artifacts) != 2 || !bytes.Equal(doc.Artifacts[0].Content, content) {
		t.Fatalf("export: %d artefatos, %v", len(doc.Artifacts), err)
	}

	// apagar remove os objetos depois do commit
	if err := e.svc.DeleteArtifact(bg, domain.HumanActor, e.co.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	if len(e.s3.Keys()) != 0 {
		t.Fatalf("objetos depois de apagar: %v", e.s3.Keys())
	}
}

func TestArtifactS3FailureLeavesNothing(t *testing.T) {
	e := newBlobEnv(t, storage.DriverS3)
	bg := context.Background()
	e.s3.SetFailures(true, false)
	_, err := e.svc.PutArtifact(bg, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "a.txt", Content: []byte("x")})
	if err == nil {
		t.Fatal("o S3 recusou, deveria falhar")
	}
	var n int
	_ = e.svc.Pool.QueryRow(bg, `SELECT count(*) FROM artifacts`).Scan(&n)
	if n != 0 {
		t.Fatalf("artefatos criados apesar da falha: %d", n)
	}
	e.s3.SetFailures(false, false)

	// cota estourada depois do upload: o objeto é apagado (compensação)
	e.svc.ArtifactMaxBytes, e.svc.ArtifactQuotaBytes = 10, 10
	if _, err := e.svc.PutArtifact(bg, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "a.txt", Content: []byte("123456")}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.PutArtifact(bg, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: "b.txt", Content: []byte("123456")})
	var lim *service.ArtifactLimitError
	if !errors.As(err, &lim) || !lim.Quota {
		t.Fatalf("esperava recusa por cota: %v", err)
	}
	if len(e.s3.Keys()) != 1 {
		t.Fatalf("o objeto da versão recusada deveria ter sido apagado: %v", e.s3.Keys())
	}

	// falha ao apagar vira log de órfão, sem derrubar o DeleteArtifact
	e.s3.SetFailures(false, true)
	list, _ := e.svc.ListArtifacts(bg, e.co.ID, service.ArtifactFilter{})
	if err := e.svc.DeleteArtifact(bg, domain.HumanActor, e.co.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.logs.String(), "órfão") {
		t.Errorf("faltou o log do objeto órfão: %s", e.logs.String())
	}
}

func TestBlobsMigrateBetweenDrivers(t *testing.T) {
	e := newBlobEnv(t, storage.DriverPostgres) // escritas novas no postgres; s3 só disponível
	bg := context.Background()
	big := bytes.Repeat([]byte("0123456789"), 2000)
	var ids []domain.ID
	for _, name := range []string{"a.txt", "b.txt", "c.bin"} {
		v := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: name, Content: append([]byte(name+":"), big...)})
		ids = append(ids, v.ID)
	}
	if e.pgRows(t) != 3 || len(e.s3.Keys()) != 0 {
		t.Fatalf("antes: pg=%d s3=%d", e.pgRows(t), len(e.s3.Keys()))
	}
	reg := e.svc.Blobs

	// dry-run não muda nada
	rep, err := storage.Migrate(bg, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "s3", DryRun: true})
	if err != nil || rep.Scanned != 3 || rep.Moved != 3 || e.pgRows(t) != 3 || len(e.s3.Keys()) != 0 {
		t.Fatalf("dry-run: %+v %v", rep, err)
	}

	// com --keep-source copia e troca o registro, mas mantém a origem
	rep, err = storage.Migrate(bg, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "s3", KeepSource: true, Batch: 2})
	if err != nil || rep.Moved != 3 || rep.Failed != 0 || len(e.s3.Keys()) != 3 || e.pgRows(t) != 3 {
		t.Fatalf("keep-source: %+v %v pg=%d", rep, err, e.pgRows(t))
	}
	for _, id := range ids {
		if e.storageOf(t, id) != "s3" {
			t.Fatalf("registro %s não trocou de driver", id)
		}
		if _, b, err := e.svc.ArtifactContent(bg, e.co.ID, id); err != nil || len(b) < len(big) {
			t.Fatalf("leitura depois de migrar: %v", err)
		}
	}
	// reexecutar não acha mais nada na origem (retomável/idempotente)
	rep, err = storage.Migrate(bg, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "s3"})
	if err != nil || rep.Scanned != 0 {
		t.Fatalf("reexecução: %+v %v", rep, err)
	}

	// de volta ao postgres, apagando a origem (S3)
	if _, err := e.pg.Stat(bg, storage.Key{CompanyID: e.co.ID, ArtifactID: ids[0]}); err != nil {
		t.Fatalf("a cópia keep-source deveria estar no postgres: %v", err)
	}
	rep, err = storage.Migrate(bg, e.svc.Pool, reg, storage.MigrateOptions{From: "s3", To: "postgres"})
	if err != nil || rep.Moved != 3 || rep.Failed != 0 || len(e.s3.Keys()) != 0 || e.pgRows(t) != 3 {
		t.Fatalf("s3→postgres: %+v %v s3=%d pg=%d", rep, err, len(e.s3.Keys()), e.pgRows(t))
	}
	for _, id := range ids {
		if e.storageOf(t, id) != "postgres" {
			t.Fatalf("registro %s ficou em %s", id, e.storageOf(t, id))
		}
	}
	if _, b, err := e.svc.ArtifactContent(bg, e.co.ID, ids[1]); err != nil || !bytes.HasPrefix(b, []byte("b.txt:")) {
		t.Fatalf("conteúdo depois da volta: %v", err)
	}
}

func TestBlobsMigrateDetectsCorruptionAndSurvivesFailures(t *testing.T) {
	e := newBlobEnv(t, storage.DriverPostgres)
	bg := context.Background()
	good := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "bom.txt", Content: []byte("conteudo bom")})
	bad := e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "ruim.txt", Content: []byte("conteudo original")})
	// corrompe o conteúdo guardado no postgres: não bate com o sha256 registrado
	if _, err := e.svc.Pool.Exec(bg, `UPDATE artifact_blobs SET content=$2 WHERE artifact_id=$1`, bad.ID, []byte("conteudo adulterad")); err != nil {
		t.Fatal(err)
	}
	rep, err := storage.Migrate(bg, e.svc.Pool, e.svc.Blobs, storage.MigrateOptions{From: "postgres", To: "s3"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Moved != 1 || rep.Failed != 1 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], bad.ID.String()) {
		t.Fatalf("relatório: %+v", rep)
	}
	if e.storageOf(t, good.ID) != "s3" || e.storageOf(t, bad.ID) != "postgres" {
		t.Fatal("o bom deve ir e o corrompido deve ficar onde está")
	}
	if strings.Contains(strings.Join(rep.Errors, " "), "conteudo") {
		t.Fatal("o relatório não pode carregar conteúdo")
	}

	// S3 recusando gravação: a falha é contada e o registro não troca
	e.s3.SetFailures(true, false)
	rep, _ = storage.Migrate(bg, e.svc.Pool, e.svc.Blobs, storage.MigrateOptions{From: "postgres", To: "s3"})
	if rep.Failed != 1 || rep.Moved != 0 {
		t.Fatalf("com S3 fora: %+v", rep)
	}
	// mesmo driver / driver desconhecido
	if _, err := storage.Migrate(bg, e.svc.Pool, e.svc.Blobs, storage.MigrateOptions{From: "s3", To: "s3"}); err == nil {
		t.Fatal("origem = destino deve falhar")
	}
	if _, err := storage.Migrate(bg, e.svc.Pool, e.svc.Blobs, storage.MigrateOptions{From: "ftp", To: "s3"}); err == nil {
		t.Fatal("driver desconhecido deve falhar")
	}
}

func TestBlobStatusAndLimits(t *testing.T) {
	e := newBlobEnv(t, storage.DriverS3)
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "a.txt", Content: []byte("12345")})
	st, err := e.svc.BlobStorageStatus(context.Background())
	if err != nil || st.Active != "s3" || len(st.Usage) != 1 || st.Usage[0].Driver != "s3" || st.Usage[0].Bytes != 5 {
		t.Fatalf("status = %+v %v", st, err)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "SK-secret") {
		t.Fatal("o status não pode carregar credencial")
	}
}
