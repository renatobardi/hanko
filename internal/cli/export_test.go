package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// listDir devolve os nomes do diretório (para provar que não sobra arquivo temporário).
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// hanko export -o só toca o destino no fim: uma conexão rompida no meio, ou um JSON truncado, não deixam arquivo
// quebrado nem apagam o que já existia, e não sobra temporário.
func TestExportOutputAtomico(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "arq.json")
	mode := "abort"
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/export": func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "abort": // 200 e meio arquivo, depois a conexão cai (o que o servidor faz num erro no meio da escrita)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"format":"hanko.company.v1","comp`))
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		case "truncado": // encerra limpo, mas o JSON está incompleto
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"format":"hanko.company.v1","comp`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"format":"hanko.company.v1","company":{"name":"Acme"}}`))
		}
	}})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}

	if err := os.WriteFile(dest, []byte("ANTIGO"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"abort", "truncado"} {
		mode = m
		if code, _, _ := exec(t, Runtime{}, env, "", "export", "-o", dest); code == 0 {
			t.Fatalf("%s: o export interrompido deveria falhar", m)
		}
		if b, err := os.ReadFile(dest); err != nil || string(b) != "ANTIGO" {
			t.Fatalf("%s: o destino foi alterado: %q %v", m, b, err)
		}
		if names := listDir(t, dir); len(names) != 1 {
			t.Fatalf("%s: sobrou lixo no diretório: %v", m, names)
		}
	}
	// sem arquivo prévio, nada é criado
	_ = os.Remove(dest)
	mode = "abort"
	if code, _, _ := exec(t, Runtime{}, env, "", "export", "-o", dest); code == 0 {
		t.Fatal("deveria falhar")
	}
	if names := listDir(t, dir); len(names) != 0 {
		t.Fatalf("arquivo criado apesar do erro: %v", names)
	}

	mode = "ok"
	// o destino é substituído por rename (outro inode), não regravado no lugar: um leitor concorrente ou uma queda
	// no meio da escrita nunca veem o arquivo pela metade
	if err := os.WriteFile(dest, []byte("ANTIGO"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(dest)
	if code, out, errOut := exec(t, Runtime{}, env, "", "export", "-o", dest); code != 0 || !strings.Contains(out, dest) {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	b, err := os.ReadFile(dest)
	if err != nil || !strings.Contains(string(b), `"company"`) {
		t.Fatalf("%q %v", b, err)
	}
	st, _ := os.Stat(dest)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("modo %v", st.Mode())
	}
	if os.SameFile(before, st) {
		t.Fatal("o destino foi regravado no lugar em vez de substituído por rename")
	}
	if names := listDir(t, dir); len(names) != 1 {
		t.Fatalf("temporário não removido: %v", names)
	}
}
