-- name: InsertPersonaInstructionRevision :one
INSERT INTO persona_instruction_revisions (id, company_id, persona_id, revision, body, sha256, author_type, author_id,
                                           actor_type, actor_id, reason, origin, restored_from, gate_id, created_at)
VALUES (@id, @company_id, @persona_id, @revision, @body, @sha256, @author_type, @author_id,
        @actor_type, @actor_id, @reason, @origin, @restored_from, @gate_id, @now)
RETURNING *;

-- name: SetPersonaInstructionRevision :exec
UPDATE personas SET instruction_revision = @revision WHERE company_id = @company_id AND id = @id;

-- name: ListPersonaInstructionRevisions :many
-- Sem o corpo (só o tamanho): a lista não lê texto grande.
SELECT id, company_id, persona_id, revision, length(body)::bigint AS body_bytes, sha256, author_type, author_id,
       actor_type, actor_id, reason, origin, restored_from, gate_id, created_at
FROM persona_instruction_revisions
WHERE company_id = @company_id AND persona_id = @persona_id
ORDER BY revision DESC
LIMIT @lim;

-- name: GetPersonaInstructionRevision :one
SELECT * FROM persona_instruction_revisions WHERE company_id = @company_id AND persona_id = @persona_id AND revision = @revision;

-- name: CreatePersonaInstructionProposal :one
INSERT INTO persona_instruction_proposals (id, company_id, persona_id, gate_id, base_revision, body, reason, run_id, created_at)
VALUES (@id, @company_id, @persona_id, @gate_id, @base_revision, @body, @reason, @run_id, @now)
RETURNING *;

-- name: GetPersonaInstructionProposalByGate :one
SELECT * FROM persona_instruction_proposals WHERE company_id = @company_id AND gate_id = @gate_id;

-- name: SetRunInstructionRevision :execrows
-- Grava a revisão das instruções com que a run rodou. Só uma vez (a primeira gravação vale) e só numa revisão que existe.
UPDATE runs r SET instruction_revision = v.revision, instruction_sha256 = v.sha256
FROM persona_instruction_revisions v
WHERE r.company_id = @company_id AND r.id = @run_id AND r.instruction_revision IS NULL
  AND v.persona_id = r.persona_id AND v.revision = @revision;

-- name: SetPersonaInstructions :one
-- Restaurar e aprovar uma proposta trocam só o texto e o número da revisão vigente.
UPDATE personas SET instructions = @instructions, instruction_revision = @revision, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;
