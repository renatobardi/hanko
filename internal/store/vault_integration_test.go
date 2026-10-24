//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/testdb"
)

// Queries do contrato do cofre (ADR-0068/0069/0070) com regra no SQL: alcance da persona por coleção e organograma,
// abertura atômica do envio, bloqueio por senha errada, desafio WebAuthn de uso único, prazo de rotação e repetidos.
func TestVaultQueries(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	co := uuid.New()
	exec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, co)
	persona := func(slug string, reportsTo *uuid.UUID) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO personas (id, company_id, slug, name, agent, reports_to) VALUES ($1, $2, $3, $3, 'fake', $4)`, id, co, slug, reportsTo)
		return id
	}
	cto := persona("cto", nil)
	eng := persona("eng", &cto)
	junior := persona("junior", &eng)
	outsider := persona("outsider", nil)
	secret := func(name string) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO secrets (id, company_id, name, value_enc, created_by_type) VALUES ($1, $2, $3, 'enc', 'human')`, id, co, name)
		return id
	}
	gh, prd, open := secret("gh"), secret("prd"), secret("open")
	collection := func(name string, rotate *int32) uuid.UUID {
		c, err := q.CreateSecretCollection(ctx, store.CreateSecretCollectionParams{ID: uuid.New(), CompanyID: co, Name: name,
			RotateAfterDays: rotate, CreatedByType: "human", Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	thirty := int32(30)
	infra, devs, all := collection("infra", &thirty), collection("devs", nil), collection("todas", nil)
	add := func(col, sec uuid.UUID) {
		t.Helper()
		if err := q.AddSecretToCollection(ctx, store.AddSecretToCollectionParams{CompanyID: co, CollectionID: col, SecretID: sec, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	add(infra, prd)
	add(devs, gh)
	add(all, open)
	grant := func(col uuid.UUID, subject string, id *uuid.UUID, perm string) {
		t.Helper()
		if _, err := q.CreateCollectionGrant(ctx, store.CreateCollectionGrantParams{ID: uuid.New(), CompanyID: co, CollectionID: col,
			SubjectType: subject, SubjectID: id, Permission: perm, CreatedByType: "human", Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	grant(infra, "persona", &cto, "use")
	grant(devs, "persona_tree", &eng, "use")
	grant(all, "all_personas", nil, "use")

	cases := []struct {
		name    string
		persona uuid.UUID
		secret  uuid.UUID
		want    bool
	}{
		{"concessão direta", cto, prd, true},
		{"concessão direta não desce", eng, prd, false},
		{"subárvore: a própria persona", eng, gh, true},
		{"subárvore: quem responde a ela", junior, gh, true},
		{"subárvore não sobe", cto, gh, false},
		{"fora do organograma", outsider, gh, false},
		{"todas as personas", outsider, open, true},
	}
	for _, c := range cases {
		allowed, err := q.PersonaCanUseSecret(ctx, store.PersonaCanUseSecretParams{CompanyID: co, PersonaID: c.persona, SecretID: c.secret})
		if err != nil {
			t.Fatal(err)
		}
		if allowed != c.want {
			t.Errorf("%s: PersonaCanUseSecret = %v, quer %v", c.name, allowed, c.want)
		}
	}

	// Envio: o limite de acessos é atômico e o bloqueio por senha errada vale até locked_until.
	one := int32(1)
	send, err := q.CreateSecretSend(ctx, store.CreateSecretSendParams{ID: uuid.New(), CompanyID: co, PublicID: "AAAAAAAAAAAAAAAAAAAAAA",
		Kind: "text", Ciphertext: "ct", KdfSalt: "salt", PasswordVerifier: "v", MaxAccesses: &one, ExpiresAt: now.Add(time.Hour),
		CreatedByType: "human", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		lock := now.Add(15 * time.Minute)
		r, err := q.FailSecretSendPassword(ctx, store.FailSecretSendPasswordParams{MaxFails: 5, LockUntil: &lock, PublicID: send.PublicID})
		if err != nil {
			t.Fatal(err)
		}
		if (i == 4) != (r.LockedUntil != nil) {
			t.Fatalf("falha %d: locked_until = %v", i+1, r.LockedUntil)
		}
	}
	if _, err := q.OpenSecretSend(ctx, store.OpenSecretSendParams{Now: now, PublicID: send.PublicID}); err == nil {
		t.Fatal("envio bloqueado não deveria abrir")
	}
	later := now.Add(16 * time.Minute)
	opened, err := q.OpenSecretSend(ctx, store.OpenSecretSendParams{Now: later, PublicID: send.PublicID})
	if err != nil || opened.AccessCount != 1 || opened.FailedAttempts != 0 {
		t.Fatalf("abrir depois do bloqueio = %+v (%v)", opened, err)
	}
	if _, err := q.OpenSecretSend(ctx, store.OpenSecretSendParams{Now: later, PublicID: send.PublicID}); err == nil {
		t.Fatal("envio no limite de acessos não deveria abrir de novo")
	}
	toPurge, err := q.ListSecretSendsToPurge(ctx, store.ListSecretSendsToPurgeParams{Now: later, Lim: 10})
	if err != nil || len(toPurge) != 1 {
		t.Fatalf("envios a apagar = %d (%v), quer 1", len(toPurge), err)
	}

	// Desafio WebAuthn: uso único, só do mesmo usuário e propósito, só dentro do prazo.
	uid := uuid.New()
	exec(`INSERT INTO users (id, email, provider, password_hash) VALUES ($1, 'a@b.c', 'local', 'x')`, uid)
	ch, err := q.CreateWebauthnChallenge(ctx, store.CreateWebauthnChallengeParams{ID: uuid.New(), UserID: uid, Purpose: "reauth",
		Challenge: []byte("desafio"), ExpiresAt: now.Add(2 * time.Minute), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.ConsumeWebauthnChallenge(ctx, store.ConsumeWebauthnChallengeParams{ID: ch.ID, UserID: uid, Purpose: "register", Now: now}); err == nil {
		t.Fatal("propósito errado não deveria consumir")
	}
	if _, err := q.ConsumeWebauthnChallenge(ctx, store.ConsumeWebauthnChallengeParams{ID: ch.ID, UserID: uid, Purpose: "reauth", Now: now.Add(3 * time.Minute)}); err == nil {
		t.Fatal("desafio vencido não deveria consumir")
	}
	if _, err := q.ConsumeWebauthnChallenge(ctx, store.ConsumeWebauthnChallengeParams{ID: ch.ID, UserID: uid, Purpose: "reauth", Now: now}); err != nil {
		t.Fatalf("consumir: %v", err)
	}
	if _, err := q.ConsumeWebauthnChallenge(ctx, store.ConsumeWebauthnChallengeParams{ID: ch.ID, UserID: uid, Purpose: "reauth", Now: now}); err == nil {
		t.Fatal("desafio já consumido não deveria valer de novo")
	}

	// Rotação vencida pelo prazo da coleção, e valores repetidos pelo HMAC.
	field := func(sec uuid.UUID, name, hmac string, rotated time.Time) {
		t.Helper()
		if _, err := q.CreateSecretField(ctx, store.CreateSecretFieldParams{ID: uuid.New(), CompanyID: co, SecretID: sec, Name: name, Kind: "text",
			Sensitive: true, Provider: "local", ValueEnc: "enc", ValueHmac: hmac, AadVersion: 1, Now: rotated}); err != nil {
			t.Fatal(err)
		}
	}
	field(prd, "password", "h1", now.Add(-31*24*time.Hour))
	field(prd, "username", "", now.Add(-time.Hour))
	field(gh, "token", "h1", now.Add(-365*24*time.Hour))
	vencidos, err := q.ListSecretsDueRotation(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// a consulta é global (o alerta roda sobre todas as empresas): aqui só interessa a linha desta empresa
	var due []store.ListSecretsDueRotationRow
	for _, d := range vencidos {
		if d.CompanyID == co {
			due = append(due, d)
		}
	}
	if len(due) != 1 || due[0].SecretName != "prd" || due[0].FieldName != "password" || due[0].LimitDays != 30 {
		t.Fatalf("rotação vencida = %+v, quer só prd#password (gh não tem prazo)", due)
	}
	dups, err := q.ListDuplicateSecretFieldHmacs(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	if len(dups) != 2 {
		t.Fatalf("repetidos = %+v, quer prd#password e gh#token", dups)
	}
}
