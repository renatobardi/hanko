package backup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/renatobardi/hanko/internal/syscmd"
)

// O backup cobre só o schema corrente da conexão (ADR-0092): num Postgres compartilhado o papel do Hanko vê apenas o
// próprio schema (search_path fixo), não cria banco nem schema e não pode tocar em `public`. Na instalação com banco
// próprio o schema corrente é `public` e o comportamento é o de antes.

// DefaultSchema é o schema dos backups gravados antes de o manifest registrar o schema.
const DefaultSchema = "public"

// schemaRe aceita só identificadores simples em minúsculas: o nome entra no SQL do restore e no padrão do pg_dump.
var schemaRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// currentSchema pergunta ao banco qual é o schema corrente (o primeiro do search_path que existe).
func (m *Manager) currentSchema(ctx context.Context, env []string) (string, error) {
	var out bytes.Buffer
	err := m.Cmd.Run(ctx, syscmd.Cmd{Name: m.psqlBin(), Env: env, Stdout: &out,
		Args: []string{"-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", "SELECT current_schema()"}})
	if err != nil {
		return "", fmt.Errorf("backup: ler o schema corrente: %w", err)
	}
	s := strings.TrimSpace(out.String())
	if s == "" {
		return "", errors.New("backup: a conexão não tem schema corrente (search_path vazio ou inexistente)")
	}
	if !schemaRe.MatchString(s) {
		return "", fmt.Errorf("backup: schema corrente %q fora do formato aceito (minúsculas, dígitos e _)", s)
	}
	return s, nil
}

// clearSchemaSQL apaga todo objeto do schema sem apagar o schema: o papel do Hanko num banco compartilhado é dono do
// schema mas não pode recriá-lo. Roda na mesma transação do restore.
func clearSchemaSQL(schema string) string {
	return strings.ReplaceAll(clearSchemaTemplate, "__SCHEMA__", schema)
}

// clearSchemaTemplate é o SQL de clearSchemaSQL com o schema em __SCHEMA__.
const clearSchemaTemplate = `DO $hanko_clear$
DECLARE
  s text := '__SCHEMA__';
  r record;
BEGIN
  FOR r IN SELECT c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
           WHERE n.nspname = s AND c.relkind IN ('v', 'm') LOOP
    EXECUTE format(CASE r.relkind WHEN 'm' THEN 'DROP MATERIALIZED VIEW IF EXISTS %I.%I CASCADE'
                                  ELSE 'DROP VIEW IF EXISTS %I.%I CASCADE' END, s, r.relname);
  END LOOP;
  FOR r IN SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
           WHERE n.nspname = s AND c.relkind IN ('r', 'p') AND NOT c.relispartition LOOP
    EXECUTE format('DROP TABLE IF EXISTS %I.%I CASCADE', s, r.relname);
  END LOOP;
  FOR r IN SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
           WHERE n.nspname = s AND c.relkind = 'S' LOOP
    EXECUTE format('DROP SEQUENCE IF EXISTS %I.%I CASCADE', s, r.relname);
  END LOOP;
  FOR r IN SELECT p.oid::regprocedure AS sig, p.prokind FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
           WHERE n.nspname = s LOOP
    EXECUTE format(CASE r.prokind WHEN 'a' THEN 'DROP AGGREGATE IF EXISTS %s CASCADE'
                                  WHEN 'p' THEN 'DROP PROCEDURE IF EXISTS %s CASCADE'
                                  ELSE 'DROP FUNCTION IF EXISTS %s CASCADE' END, r.sig);
  END LOOP;
  FOR r IN SELECT t.typname, t.typtype FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
           WHERE n.nspname = s AND t.typtype IN ('e', 'd', 'r', 'm', 'b')
             AND NOT EXISTS (SELECT 1 FROM pg_type e WHERE e.typarray = t.oid) LOOP
    EXECUTE format(CASE r.typtype WHEN 'd' THEN 'DROP DOMAIN IF EXISTS %I.%I CASCADE' ELSE 'DROP TYPE IF EXISTS %I.%I CASCADE' END, s, r.typname);
  END LOOP;
  FOR r IN SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace JOIN pg_class c ON c.oid = t.typrelid
           WHERE n.nspname = s AND t.typtype = 'c' AND c.relkind = 'c' LOOP
    EXECUTE format('DROP TYPE IF EXISTS %I.%I CASCADE', s, r.typname);
  END LOOP;
  FOR r IN SELECT cfgname FROM pg_ts_config c JOIN pg_namespace n ON n.oid = c.cfgnamespace WHERE n.nspname = s LOOP
    EXECUTE format('DROP TEXT SEARCH CONFIGURATION IF EXISTS %I.%I CASCADE', s, r.cfgname);
  END LOOP;
  FOR r IN SELECT collname FROM pg_collation c JOIN pg_namespace n ON n.oid = c.collnamespace WHERE n.nspname = s LOOP
    EXECUTE format('DROP COLLATION IF EXISTS %I.%I CASCADE', s, r.collname);
  END LOOP;
END
$hanko_clear$;
`

// withoutSchemaEntries tira da lista do pg_restore (-l) a criação do schema e o comentário dele: o schema já existe e
// pertence ao dono do banco ou ao papel do Hanko; recriar falharia.
func withoutSchemaEntries(list []byte) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(list))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		f := strings.Fields(line)
		if !strings.HasPrefix(line, ";") && len(f) >= 5 {
			if f[3] == "SCHEMA" && f[4] == "-" {
				continue
			}
			if len(f) >= 6 && f[3] == "COMMENT" && f[4] == "-" && f[5] == "SCHEMA" {
				continue
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}
