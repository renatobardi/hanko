-- name: LockCompanyMembers :exec
-- Serializa mudanças de membros de uma empresa (regra do último dono).
SELECT 1 FROM companies WHERE id = @company_id FOR UPDATE;

-- name: GetMemberRole :one
SELECT role FROM company_members WHERE company_id = @company_id AND user_id = @user_id;

-- name: CountUserMemberships :one
SELECT count(*) FROM company_members WHERE user_id = @user_id;

-- name: AddMember :one
INSERT INTO company_members (company_id, user_id, role, invited_by, created_at, updated_at)
VALUES (@company_id, @user_id, @role, @invited_by, @now, @now)
RETURNING *;

-- name: ListMembers :many
SELECT m.company_id, m.user_id, m.role, m.invited_by, m.created_at, m.updated_at,
       u.email, u.name, u.provider
FROM company_members m JOIN users u ON u.id = m.user_id
WHERE m.company_id = @company_id
ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'member' THEN 2 ELSE 3 END, lower(u.email);

-- name: GetMember :one
SELECT m.company_id, m.user_id, m.role, m.invited_by, m.created_at, m.updated_at,
       u.email, u.name, u.provider
FROM company_members m JOIN users u ON u.id = m.user_id
WHERE m.company_id = @company_id AND m.user_id = @user_id;

-- name: CountOwners :one
SELECT count(*) FROM company_members WHERE company_id = @company_id AND role = 'owner';

-- name: SetMemberRole :exec
UPDATE company_members SET role = @role, updated_at = @now WHERE company_id = @company_id AND user_id = @user_id;

-- name: RemoveMember :execrows
DELETE FROM company_members WHERE company_id = @company_id AND user_id = @user_id;

-- name: ListCompaniesForUser :many
SELECT c.* FROM companies c JOIN company_members m ON m.company_id = c.id
WHERE m.user_id = @user_id ORDER BY c.created_at, c.id;

-- name: RevokeOpenInvitesForEmail :exec
UPDATE company_invites SET revoked_at = @now
WHERE company_id = @company_id AND lower(email) = lower(@email) AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: CreateInvite :one
INSERT INTO company_invites (id, company_id, email, role, token_hash, invited_by, created_at, expires_at)
VALUES (@id, @company_id, @email, @role, @token_hash, @invited_by, @now, @expires_at)
RETURNING *;

-- name: ListOpenInvites :many
SELECT * FROM company_invites
WHERE company_id = @company_id AND accepted_at IS NULL AND revoked_at IS NULL
ORDER BY created_at DESC, id;

-- name: GetInviteByHash :one
SELECT * FROM company_invites WHERE token_hash = @token_hash;

-- name: GetInviteByID :one
SELECT * FROM company_invites WHERE company_id = @company_id AND id = @id;

-- name: RevokeInvite :one
UPDATE company_invites SET revoked_at = @now
WHERE company_id = @company_id AND id = @id AND accepted_at IS NULL AND revoked_at IS NULL
RETURNING *;

-- name: AcceptInvite :one
UPDATE company_invites SET accepted_at = @now, accepted_by = @user_id
WHERE id = @id AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > @now
RETURNING *;

-- name: MarkUserInvited :exec
UPDATE users SET invited = true WHERE id = @id;

-- name: ClearUserInvited :exec
UPDATE users SET invited = false WHERE id = @id;

-- name: CountSoleOwnedCompanies :one
-- Empresas em que o usuário é o único owner (remover o usuário as deixaria sem dono).
SELECT count(*) FROM company_members m
WHERE m.user_id = @user_id AND m.role = 'owner'
  AND (SELECT count(*) FROM company_members o WHERE o.company_id = m.company_id AND o.role = 'owner') = 1;
