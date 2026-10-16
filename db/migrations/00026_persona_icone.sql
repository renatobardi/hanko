-- +goose Up
-- Identidade visual da persona (Kubo DS): um glifo Lucide monocromático num círculo neutro, escolhido de um
-- catálogo fechado (domain.PersonaIcons). Sem cor e sem emoji; o padrão é o robô genérico.
ALTER TABLE personas ADD COLUMN icon text NOT NULL DEFAULT 'bot';

-- +goose Down
ALTER TABLE personas DROP COLUMN icon;
