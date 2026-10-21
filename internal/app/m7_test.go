package app

import (
	"testing"

	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/storage"
)

func TestNewBlobRegistry(t *testing.T) {
	cfg := config.Defaults()
	reg, err := NewBlobRegistry(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Active().Name() != storage.DriverPostgres || len(reg.Names()) != 1 {
		t.Fatalf("padrão: ativo=%s drivers=%v", reg.Active().Name(), reg.Names())
	}

	cfg.Storage.S3 = config.S3{Endpoint: "https://s3.exemplo.com", Bucket: "hanko-art", AccessKey: "ak", SecretKey: "sk", Region: "us-east-1", PathStyle: true}
	reg, err = NewBlobRegistry(cfg, nil)
	if err != nil || reg.Active().Name() != storage.DriverPostgres || len(reg.Names()) != 2 {
		t.Fatalf("s3 configurado, postgres ativo: %v %v", reg, err)
	}
	cfg.Storage.Driver = config.BlobS3
	reg, err = NewBlobRegistry(cfg, nil)
	if err != nil || reg.Active().Name() != storage.DriverS3 || len(reg.Names()) != 2 {
		t.Fatalf("s3 ativo: %v", err)
	}

	cfg.Storage.S3 = config.S3{}
	if _, err := NewBlobRegistry(cfg, nil); err == nil {
		t.Fatal("driver s3 sem configuração deve falhar")
	}
	cfg.Storage.S3 = config.S3{Endpoint: "ftp://x", Bucket: "hanko-art", AccessKey: "ak", SecretKey: "sk"}
	if _, err := NewBlobRegistry(cfg, nil); err == nil {
		t.Fatal("endpoint inválido deve falhar")
	}
}

// Toda chave do registro de settings precisa de um valor-base vindo da configuração; senão ela nasceria zerada.
func TestSettingsBaseCoversRegistry(t *testing.T) {
	cfg := config.Defaults()
	base := settingsBase(cfg)
	for _, d := range settings.Defs {
		if _, ok := base[d.Key]; !ok {
			t.Errorf("settingsBase não cobre %s", d.Key)
		}
	}
	m := settings.NewManager(base, nil)
	if m.Int(settings.ArtifactsMaxBytes) != cfg.ArtifactMaxBytes || m.Int(settings.AlertsSilentFactor) != int64(cfg.AlertSilentFactor) ||
		m.Duration(settings.AlertsRunStuckAfter) != cfg.AlertRunStuckAfter || m.Float(settings.RunReserveUSD) != cfg.RunReserveUSD || m.Bool(settings.ACPAllow) {
		t.Fatalf("valores-base não batem com a configuração")
	}
}
