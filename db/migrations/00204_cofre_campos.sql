-- +goose Up
-- Fatia 1 do cofre (ADR-0068 §1.7 e §2.6): cada segredo existente ganha o campo `value` com o mesmo material
-- (mesmo provider, mesma cifra, mesma versão; aad_version = 0 significa AAD legado `secret/<secret_id>`, reescrito
-- pelo próximo rekey), e cada empresa ganha a coleção `padrão` (is_default) com todos os segredos dela, concedida a
-- all_personas. Idempotente: rodar de novo não move nada. As colunas antigas de `secrets` não são tocadas; deixam de
-- ser lidas nesta fatia e saem numa migration posterior, depois de um release.

-- 1. Campo `value` de cada segredo (a constraint de `secrets` garante local→value_enc, vault→external_ref), com um
--    fato por empresa dizendo quantos campos entrou (nunca valores).
WITH migrated AS (
    INSERT INTO secret_fields (id, company_id, secret_id, name, kind, sensitive, provider, value_enc, external_ref,
                               aad_version, position, version, revealed_at, rotated_at, created_at, updated_at)
    SELECT gen_random_uuid(), s.company_id, s.id, 'value', 'text', true, s.provider,
           CASE WHEN s.provider = 'vault' THEN '' ELSE s.value_enc END,
           CASE WHEN s.provider = 'vault' THEN s.external_ref ELSE '' END,
           0, 0, s.version, s.revealed_at, s.updated_at, s.created_at, s.updated_at
    FROM secrets s
    WHERE NOT EXISTS (SELECT 1 FROM secret_fields f WHERE f.secret_id = s.id AND f.name = 'value')
    RETURNING company_id
)
INSERT INTO activity_events (id, company_id, actor_type, verb, subject_type, subject_id, data, occurred_at)
SELECT gen_random_uuid(), company_id, 'system', 'secret.fields_migrated', 'company', company_id,
       jsonb_build_object('fields', count(*)), now()
FROM migrated GROUP BY company_id;

-- 2. Coleção `padrão` em cada empresa que ainda não a tem.
WITH new_defaults AS (
    INSERT INTO secret_collections (id, company_id, name, description, is_default,
                                    created_by_type, created_by_id, created_at, updated_at)
    SELECT gen_random_uuid(), c.id, 'padrão', 'Criada na migração do cofre; todas as personas da empresa a usam',
           true, 'system', NULL, now(), now()
    FROM companies c
    WHERE NOT EXISTS (SELECT 1 FROM secret_collections d WHERE d.company_id = c.id AND d.is_default)
    RETURNING id, company_id
)
INSERT INTO activity_events (id, company_id, actor_type, verb, subject_type, subject_id, data, occurred_at)
SELECT gen_random_uuid(), company_id, 'system', 'collection.created', 'collection', id,
       jsonb_build_object('name', 'padrão', 'is_default', true), now()
FROM new_defaults;

-- 3. Todos os segredos da empresa entram na `padrão` (segredo de usuário não entra em coleção, ADR-0068 §2.1).
INSERT INTO secret_collection_items (company_id, collection_id, secret_id, added_at)
SELECT s.company_id, d.id, s.id, now()
FROM secrets s
JOIN secret_collections d ON d.company_id = s.company_id AND d.is_default
WHERE s.scope = 'company'
ON CONFLICT (collection_id, secret_id) DO NOTHING;

-- 4. A `padrão` é concedida a todas as personas da empresa, inclusive as contratadas depois (ADR-0068 §2.6).
WITH new_grants AS (
    INSERT INTO secret_collection_grants (id, company_id, collection_id, subject_type, subject_id, permission,
                                           created_by_type, created_by_id, created_at)
    SELECT gen_random_uuid(), d.company_id, d.id, 'all_personas', NULL, 'use', 'system', NULL, now()
    FROM secret_collections d
    WHERE d.is_default
      AND NOT EXISTS (SELECT 1 FROM secret_collection_grants g
                      WHERE g.collection_id = d.id AND g.subject_type = 'all_personas'
                        AND g.subject_id IS NULL AND g.permission = 'use')
    RETURNING company_id, collection_id
)
INSERT INTO activity_events (id, company_id, actor_type, verb, subject_type, subject_id, data, occurred_at)
SELECT gen_random_uuid(), company_id, 'system', 'collection.granted', 'collection', collection_id,
       jsonb_build_object('subject_type', 'all_personas', 'permission', 'use'), now()
FROM new_grants;

-- +goose Down
-- Desfaz a cópia: remove os campos migrados (aad_version = 0 só existe neles) e as coleções `padrão` criadas aqui,
-- com itens e concessões. Os fatos de activity_events ficam (auditoria; não referenciam valor). Nada criado depois
-- desta migration é afetado: campos com aad_version = 1 e coleções sem is_default continuam.
DELETE FROM secret_collection_grants
WHERE subject_type = 'all_personas' AND permission = 'use'
  AND collection_id IN (SELECT id FROM secret_collections WHERE is_default);
DELETE FROM secret_collection_items WHERE collection_id IN (SELECT id FROM secret_collections WHERE is_default);
DELETE FROM secret_collections WHERE is_default;
DELETE FROM secret_fields WHERE name = 'value' AND aad_version = 0;
