-- +goose Up
-- Modelos de acesso do assistente Conectar (ADR-0089). Modelo é dado por empresa, cópia no uso: a conexão leva
-- o valor escolhido, nunca um link (editar um modelo depois não muda conexão existente). Duas linhas semeadas por
-- empresa: "Somente leitura" (allow/off/off) e "Leitura livre, escrita com aprovação" (allow/ask/ask, a sugerida).
-- Modo por risco: allow = liberado; ask = passa pelo gate de aprovação; off = excluído do perfil.
CREATE TABLE access_templates (
    id                uuid PRIMARY KEY,
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    slug              text NOT NULL CHECK (length(slug) BETWEEN 1 AND 80),
    name              text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description       text NOT NULL DEFAULT '' CHECK (length(description) <= 300),
    read_mode         text NOT NULL CHECK (read_mode IN ('allow','ask','off')),
    write_mode        text NOT NULL CHECK (write_mode IN ('allow','ask','off')),
    destructive_mode  text NOT NULL CHECK (destructive_mode IN ('allow','ask','off')),
    suggested         boolean NOT NULL DEFAULT false,
    created_by_type   text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id     uuid,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, slug)
);

-- Semeia os dois modelos em cada empresa que já existe (empresas novas recebem na criação, no service).
WITH seeded AS (
    INSERT INTO access_templates (id, company_id, slug, name, description,
                                  read_mode, write_mode, destructive_mode, suggested,
                                  created_by_type, created_by_id, created_at, updated_at)
    SELECT gen_random_uuid(), c.id, seed.slug, seed.name, seed.description,
           seed.read_mode, seed.write_mode, seed.destructive_mode, seed.suggested,
           'system', NULL, now(), now()
    FROM companies c
    CROSS JOIN (VALUES
        ('somente-leitura', 'Somente leitura', 'Ler sem restrição; escrever e apagar ficam fora do perfil.', 'allow', 'off', 'off', false),
        ('leitura-livre-escrita-com-aprovacao', 'Leitura livre, escrita com aprovação', 'Ler sem restrição; escrever e apagar passam por aprovação.', 'allow', 'ask', 'ask', true)
    ) AS seed(slug, name, description, read_mode, write_mode, destructive_mode, suggested)
    WHERE NOT EXISTS (SELECT 1 FROM access_templates a WHERE a.company_id = c.id)
    RETURNING id, company_id, slug, name
)
INSERT INTO activity_events (id, company_id, actor_type, verb, subject_type, subject_id, data, occurred_at)
SELECT gen_random_uuid(), company_id, 'system', 'access_template.seeded', 'access_template', id,
       jsonb_build_object('slug', slug, 'name', name), now()
FROM seeded;

-- +goose Down
DROP TABLE access_templates;
