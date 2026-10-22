-- +goose Up
-- Pacotes de time hanko.team.v1 (ADR-0044): registro de cada instalação (quem instalou, de onde, o quê). O conteúdo em si
-- vira personas, skills e fluxos comuns da empresa pelo motor de import com merge (ADR-0057).
CREATE TABLE team_installs (
    id               uuid PRIMARY KEY,
    company_id       uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    package_slug     text NOT NULL,
    package_name     text NOT NULL,
    package_version  text NOT NULL DEFAULT '',
    source           text NOT NULL CHECK (source IN ('catalog','file','export')),
    sha256           text NOT NULL,
    counts           jsonb NOT NULL DEFAULT '{}',
    installed_by_type text NOT NULL CHECK (installed_by_type IN ('human','persona','system')),
    installed_by_id  uuid,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX team_installs_company_idx ON team_installs (company_id, created_at DESC);

-- +goose Down
DROP TABLE team_installs;
