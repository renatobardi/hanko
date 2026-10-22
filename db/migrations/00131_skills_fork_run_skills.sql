-- +goose Up
-- Skills (ADR-0044): fork de skill de fonte e as revisões de skill que cada run recebeu.
ALTER TABLE skills ADD COLUMN forked_from_id uuid REFERENCES skills(id) ON DELETE SET NULL;
ALTER TABLE skills ADD COLUMN forked_from_revision integer CHECK (forked_from_revision IS NULL OR forked_from_revision >= 1);

-- Retrato imutável do que a run recebeu: skill, revisão e sha256 do corpo. Gravado no despacho; some com a run.
CREATE TABLE run_skills (
    run_id     uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    skill_id   uuid REFERENCES skills(id) ON DELETE SET NULL,
    slug       text NOT NULL,
    revision   integer NOT NULL CHECK (revision >= 1),
    sha256     text NOT NULL DEFAULT '',
    pinned     boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, slug)
);
CREATE INDEX run_skills_skill_idx ON run_skills (skill_id) WHERE skill_id IS NOT NULL;
CREATE INDEX run_skills_company_idx ON run_skills (company_id, created_at DESC);

-- +goose Down
DROP TABLE run_skills;
ALTER TABLE skills DROP COLUMN forked_from_revision;
ALTER TABLE skills DROP COLUMN forked_from_id;
