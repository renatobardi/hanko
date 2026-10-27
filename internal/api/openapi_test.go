package api

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAPIUpToDate(t *testing.T) {
	got, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "api", "openapi.json")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("lendo %s: %v\ngere com: hanko openapi > api/openapi.json", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("api/openapi.json está desatualizado em relação aos handlers.\nRegenere com: hanko openapi > api/openapi.json\n(ou: go run ./cmd/hanko openapi > api/openapi.json)")
	}
}

func TestSpecStable(t *testing.T) {
	a, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Spec()
	if !bytes.Equal(a, b) {
		t.Fatal("Spec() não é determinístico")
	}
}

func TestOpenAPIIngestUpToDate(t *testing.T) {
	got, err := SpecIngest()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "api", "openapi-ingest.json")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("lendo %s: %v\ngere com: hanko openapi --ingest > api/openapi-ingest.json", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("api/openapi-ingest.json está desatualizado.\nRegenere com: hanko openapi --ingest > api/openapi-ingest.json")
	}
	if b, _ := SpecIngest(); !bytes.Equal(got, b) {
		t.Fatal("SpecIngest() não é determinístico")
	}
}
