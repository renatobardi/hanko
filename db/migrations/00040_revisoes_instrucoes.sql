-- +goose Up
-- Revisões append-only (ADR-0052). Base reutilizável: uma tabela de revisões por assunto + o gatilho abaixo, que recusa
-- UPDATE. As instruções da persona são o primeiro uso; fluxos e gates devem seguir o mesmo formato.

-- +goose StatementBegin
CREATE FUNCTION hanko_revision_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% é append-only: revisões não se alteram (crie uma revisão nova)', TG_TABLE_NAME
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;
-- +goose StatementEnd

-- Cada escrita de personas.instructions grava uma revisão. author = quem escreveu o texto (humano, ou a persona que
-- propôs); actor = quem o aplicou (o humano que editou, restaurou ou aprovou). Sem chave estrangeira SET NULL (seria um
-- UPDATE do banco): gate_id e restored_from são só referências.
CREATE TABLE persona_instruction_revisions (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    persona_id    uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    revision      integer NOT NULL CHECK (revision >= 1),
    body          text NOT NULL,
    sha256        text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    author_type   text NOT NULL CHECK (author_type IN ('human','persona','system')),
    author_id     uuid,
    actor_type    text NOT NULL CHECK (actor_type IN ('human','persona','system')),
    actor_id      uuid,
    reason        text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
    origin        text NOT NULL CHECK (origin IN ('backfill','hire','edit','restore','proposal','import')),
    restored_from integer,
    gate_id       uuid,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (persona_id, revision)
);
CREATE INDEX persona_instruction_revisions_company_idx ON persona_instruction_revisions (company_id);
CREATE TRIGGER persona_instruction_revisions_immutable BEFORE UPDATE ON persona_instruction_revisions
    FOR EACH ROW EXECUTE FUNCTION hanko_revision_immutable();

-- Revisão vigente, lida junto da linha da persona: o texto e o número nunca se separam.
ALTER TABLE personas ADD COLUMN instruction_revision integer NOT NULL DEFAULT 0;

-- Backfill: o texto atual de cada persona vira a revisão 1 (as anteriores não existiam em lugar nenhum).
INSERT INTO persona_instruction_revisions (id, company_id, persona_id, revision, body, sha256, author_type, actor_type, origin, created_at)
SELECT gen_random_uuid(), p.company_id, p.id, 1, p.instructions, encode(sha256(convert_to(p.instructions, 'UTF8')), 'hex'),
       'system', 'system', 'backfill', p.updated_at
FROM personas p;
UPDATE personas SET instruction_revision = 1;

-- A run grava a revisão com que rodou (nulo em runs antigas).
ALTER TABLE runs ADD COLUMN instruction_revision integer;
ALTER TABLE runs ADD COLUMN instruction_sha256 text CHECK (instruction_sha256 IS NULL OR instruction_sha256 ~ '^[0-9a-f]{64}$');

-- Proposta de mudança de instrução de uma persona (1:1 com o gate kind=instruction). A decisão fica no gate.
CREATE TABLE persona_instruction_proposals (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    persona_id    uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    gate_id       uuid NOT NULL UNIQUE REFERENCES gates(id) ON DELETE CASCADE,
    base_revision integer NOT NULL CHECK (base_revision >= 1),
    body          text NOT NULL,
    reason        text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
    run_id        uuid,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX persona_instruction_proposals_persona_idx ON persona_instruction_proposals (persona_id, created_at DESC);

-- gates.kind: a lista COMPLETA vigente (00027) mais 'instruction'.
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire','memory','tool','instruction'));

-- +goose Down
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
DELETE FROM gates WHERE kind = 'instruction';
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire','memory','tool'));
DROP TABLE persona_instruction_proposals;
ALTER TABLE runs DROP COLUMN instruction_sha256;
ALTER TABLE runs DROP COLUMN instruction_revision;
ALTER TABLE personas DROP COLUMN instruction_revision;
DROP TABLE persona_instruction_revisions;
DROP FUNCTION hanko_revision_immutable();
