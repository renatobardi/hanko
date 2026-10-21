package config

import (
	"strings"
	"testing"
)

const m7DB = "postgres://u@h:5432/d"

func m7Env(extra map[string]string) func(string) string {
	m := map[string]string{"HANKO_DATABASE_URL": m7DB, "HANKO_MODE": "trusted"}
	for k, v := range extra {
		m[k] = v
	}
	return envMap(m)
}

func TestStorageDefaultsToPostgres(t *testing.T) {
	c, err := LoadFrom(m7Env(nil), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage.Driver != BlobPostgres || c.S3Configured() || c.MaxArtifactBytes() != MaxArtifactBytesPostgres || c.Vault.Addr != "" {
		t.Fatalf("defaults: %+v", c.Storage)
	}
	if !c.Storage.S3.PathStyle || c.Storage.S3.Region != "us-east-1" || c.Vault.Mount != "secret" {
		t.Fatalf("defaults do S3/cofre: %+v %+v", c.Storage.S3, c.Vault)
	}
}

func TestS3DriverNeedsItsSettings(t *testing.T) {
	_, err := LoadFrom(m7Env(map[string]string{"HANKO_BLOB_DRIVER": "s3"}), files(nil))
	if err == nil || !strings.Contains(err.Error(), "HANKO_S3_ENDPOINT") {
		t.Fatalf("s3 sem configuração: %v", err)
	}
	c, err := LoadFrom(m7Env(map[string]string{"HANKO_BLOB_DRIVER": "S3", "HANKO_S3_ENDPOINT": "https://s3.exemplo.com", "HANKO_S3_BUCKET": "hanko",
		"HANKO_S3_ACCESS_KEY": "ak", "HANKO_S3_SECRET_KEY": "sk", "HANKO_S3_PATH_STYLE": "false", "HANKO_ARTIFACT_MAX_BYTES": "33554432"}), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage.Driver != BlobS3 || c.Storage.S3.PathStyle || !c.S3Configured() || c.MaxArtifactBytes() != MaxArtifactBytesS3 || c.ArtifactMaxBytes != 32<<20 {
		t.Fatalf("s3: %+v max=%d", c.Storage, c.ArtifactMaxBytes)
	}
	// o limite maior só vale com s3
	_, err = LoadFrom(m7Env(map[string]string{"HANKO_ARTIFACT_MAX_BYTES": "33554432"}), files(nil))
	if err == nil || !strings.Contains(err.Error(), "HANKO_ARTIFACT_MAX_BYTES") {
		t.Fatalf("32 MiB com postgres: %v", err)
	}
	// acima de 64 MiB nem com s3
	_, err = LoadFrom(m7Env(map[string]string{"HANKO_BLOB_DRIVER": "s3", "HANKO_S3_ENDPOINT": "https://s3.exemplo.com", "HANKO_S3_BUCKET": "hanko",
		"HANKO_S3_ACCESS_KEY": "ak", "HANKO_S3_SECRET_KEY": "sk", "HANKO_ARTIFACT_MAX_BYTES": "99999999"}), files(nil))
	if err == nil {
		t.Fatal("acima de 64 MiB deve falhar")
	}
	if _, err := LoadFrom(m7Env(map[string]string{"HANKO_BLOB_DRIVER": "ftp"}), files(nil)); err == nil || !strings.Contains(err.Error(), "HANKO_BLOB_DRIVER") {
		t.Fatalf("driver inválido: %v", err)
	}
}

// S3 configurado com o driver postgres ativo continua disponível para ler e migrar.
func TestS3ConfiguredWhilePostgresIsActive(t *testing.T) {
	c, err := LoadFrom(m7Env(map[string]string{"HANKO_S3_ENDPOINT": "https://s3.exemplo.com", "HANKO_S3_BUCKET": "hanko", "HANKO_S3_ACCESS_KEY": "ak", "HANKO_S3_SECRET_KEY": "sk"}), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage.Driver != BlobPostgres || !c.S3Configured() {
		t.Fatalf("%+v", c.Storage)
	}
}

func TestS3CredentialsOnlyFromEnvironment(t *testing.T) {
	yaml := "storage:\n  driver: s3\n  s3:\n    endpoint: https://s3.exemplo.com\n    bucket: hanko\n    access_key: do-yaml\n    secret_key: do-yaml\n"
	_, err := LoadFrom(m7Env(map[string]string{"HANKO_CONFIG": "c.yaml"}), files(map[string]string{"c.yaml": yaml}))
	if err == nil {
		t.Fatal("chaves de acesso no yaml devem ser recusadas (segredo só por ambiente)")
	}
}

func TestVaultSettings(t *testing.T) {
	if _, err := LoadFrom(m7Env(map[string]string{"HANKO_VAULT_ADDR": "https://vault.exemplo.com"}), files(nil)); err == nil || !strings.Contains(err.Error(), "HANKO_VAULT_TOKEN") {
		t.Fatalf("addr sem token: %v", err)
	}
	if _, err := LoadFrom(m7Env(map[string]string{"HANKO_VAULT_TOKEN": "t"}), files(nil)); err == nil {
		t.Fatal("token sem addr deve falhar")
	}
	c, err := LoadFrom(m7Env(map[string]string{"HANKO_VAULT_ADDR": " https://vault.exemplo.com ", "HANKO_VAULT_TOKEN": "t", "HANKO_VAULT_NAMESPACE": "time-a"}), files(nil))
	if err != nil || c.Vault.Addr != "https://vault.exemplo.com" || c.Vault.Namespace != "time-a" {
		t.Fatalf("%+v %v", c.Vault, err)
	}
}

// EnvSet marca as variáveis definidas: é o que trava as chaves de instance_settings na UI.
func TestEnvSetMarksDefinedVariables(t *testing.T) {
	c, err := LoadFrom(m7Env(map[string]string{"HANKO_ACP_ALLOW": "true", "HANKO_ALERT_SILENT_FACTOR": ""}), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.EnvSet["HANKO_ACP_ALLOW"] || c.EnvSet["HANKO_ALERT_SILENT_FACTOR"] || c.EnvSet["HANKO_INTEGRATIONS_ALLOW_STDIO"] {
		t.Fatalf("EnvSet = %v", c.EnvSet)
	}
}
