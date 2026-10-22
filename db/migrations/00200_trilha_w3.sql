-- +goose Up
-- Trilha contínua, onda 3 (ADR-0060): perfil próprio, favoritos e recentes, itens dispensáveis por pessoa e o estado
-- do onboarding. Tudo aditivo.

-- Perfil próprio (#69). O nome continua em users.name; aqui ficam avatar, fuso e idioma. Sem linha = padrões
-- (avatar com a inicial, fuso da empresa, idioma do navegador).
CREATE TABLE user_profiles (
    user_id      uuid        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    avatar_glyph text        NOT NULL DEFAULT '' CHECK (char_length(avatar_glyph) <= 8),
    avatar_tone  text        NOT NULL DEFAULT '' CHECK (avatar_tone IN ('','accent','green','amber','red','blue','violet','gray')),
    timezone     text        NOT NULL DEFAULT '' CHECK (char_length(timezone) <= 64),
    locale       text        NOT NULL DEFAULT '' CHECK (locale IN ('','pt-BR','en')),
    updated_at   timestamptz NOT NULL
);

-- Favoritos e recentes da sidebar (#69), por empresa e pessoa. subject é o id do usuário (modo authenticated) ou
-- 'local' (modo trusted, onde há uma pessoa só); sem FK de propósito, porque o modo trusted não tem usuário.
-- path é relativo à empresa ("/tasks/HNK-1"); o título é o da página no momento em que foi marcada/visitada.
CREATE TABLE nav_items (
    company_id uuid        NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    subject    text        NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 40),
    kind       text        NOT NULL CHECK (kind IN ('favorite','recent')),
    path       text        NOT NULL CHECK (path LIKE '/%' AND char_length(path) <= 300),
    title      text        NOT NULL DEFAULT '' CHECK (char_length(title) <= 200),
    touched_at timestamptz NOT NULL,
    PRIMARY KEY (company_id, subject, kind, path)
);
CREATE INDEX nav_items_list_idx ON nav_items (company_id, subject, kind, touched_at DESC);

-- Avisos que a pessoa dispensou (#61): key = 'whats_new', value = a versão cujas novidades ela já viu. Mesma
-- convenção de subject de nav_items.
CREATE TABLE user_dismissals (
    subject    text        NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 40),
    key        text        NOT NULL CHECK (char_length(key) BETWEEN 1 AND 40),
    value      text        NOT NULL DEFAULT '' CHECK (char_length(value) <= 100),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (subject, key)
);

-- Onboarding (#67), uma linha por empresa. Os passos são derivados do que já existe (persona, task, run); aqui fica só o
-- que não se deduz: o último teste do harness escolhido (feito antes de existir persona), os passos pulados e o
-- fechamento do assistente.
CREATE TABLE onboarding_state (
    company_id         uuid        PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    harness_agent      text        NOT NULL DEFAULT '',
    harness_model      text        NOT NULL DEFAULT '',
    harness_status     text        NOT NULL DEFAULT '' CHECK (harness_status IN ('','ready','not_ready')),
    harness_reason     text        NOT NULL DEFAULT '',
    harness_steps      jsonb       NOT NULL DEFAULT '[]',
    harness_checked_at timestamptz,
    skipped            text[]      NOT NULL DEFAULT '{}',
    first_task_id      uuid        REFERENCES tasks(id) ON DELETE SET NULL,
    dismissed_at       timestamptz,
    updated_at         timestamptz NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS onboarding_state;
DROP TABLE IF EXISTS user_dismissals;
DROP TABLE IF EXISTS nav_items;
DROP TABLE IF EXISTS user_profiles;
