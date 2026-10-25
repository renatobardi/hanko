package domain

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestValidateSecretField(t *testing.T) {
	cases := []struct {
		name      string
		field     string
		kind      SecretFieldKind
		sensitive bool
		provider  SecretProvider
		wantField string // campo do erro; "" = válido
	}{
		{"texto secreto", "password", SecretFieldText, true, SecretProviderLocal, ""},
		{"texto visível", "username", SecretFieldText, false, SecretProviderLocal, ""},
		{"texto no cofre externo", "token", SecretFieldText, true, SecretProviderVault, ""},
		{"totp", "totp", SecretFieldTOTP, true, SecretProviderLocal, ""},
		{"arquivo", "kubeconfig", SecretFieldFile, true, SecretProviderLocal, ""},
		{"campo padrão", DefaultSecretField, SecretFieldText, true, SecretProviderLocal, ""},
		{"nome com hífen e sublinhado", "api_key-2", SecretFieldText, true, SecretProviderLocal, ""},
		{"nome começa com número", "2fa", SecretFieldText, true, SecretProviderLocal, "name"},
		{"nome com ponto", "a.b", SecretFieldText, true, SecretProviderLocal, "name"},
		{"nome com cerquilha", "a#b", SecretFieldText, true, SecretProviderLocal, "name"},
		{"nome vazio", "", SecretFieldText, true, SecretProviderLocal, "name"},
		{"tipo desconhecido", "x", "card", true, SecretProviderLocal, "kind"},
		{"provider desconhecido", "x", SecretFieldText, true, "aws", "provider"},
		{"totp visível", "totp", SecretFieldTOTP, false, SecretProviderLocal, "sensitive"},
		{"arquivo visível", "pem", SecretFieldFile, false, SecretProviderLocal, "sensitive"},
		{"arquivo no cofre externo", "pem", SecretFieldFile, true, SecretProviderVault, "provider"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertInvalidField(t, ValidateSecretField(c.field, c.kind, c.sensitive, c.provider), c.wantField)
		})
	}
	long := "a"
	for len(long) < 64 {
		long += "b"
	}
	if !ValidSecretFieldName(long) || ValidSecretFieldName(long+"c") {
		t.Fatal("limite do nome de campo deve ser 64")
	}
}

func TestValidatePasswordMark(t *testing.T) {
	cases := []struct {
		name      string
		kind      SecretFieldKind
		sensitive bool
		password  bool
		wantField string // campo do erro; "" = válido
	}{
		{"sem marca", SecretFieldFile, false, false, ""},
		{"senha de texto secreto", SecretFieldText, true, true, ""},
		{"senha visível", SecretFieldText, false, true, "password"},
		{"senha em totp", SecretFieldTOTP, true, true, "password"},
		{"senha em arquivo", SecretFieldFile, true, true, "password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertInvalidField(t, ValidatePasswordMark(c.kind, c.sensitive, c.password), c.wantField)
		})
	}
}

func TestPruneHistory(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	ages := func(days ...int) []time.Time {
		out := make([]time.Time, len(days))
		for i, d := range days {
			out[i] = now.Add(-time.Duration(d) * day)
		}
		return out
	}
	cases := []struct {
		name        string
		replacedAt  []time.Time
		keepN, keep int
		want        []int
	}{
		{"vazio", nil, 5, 90, nil},
		{"tudo dentro", ages(1, 2, 3), 5, 90, nil},
		{"passa do número", ages(1, 2, 3, 4, 5, 6, 7), 5, 90, []int{5, 6}},
		{"passa da idade", ages(1, 100, 200), 5, 90, []int{1, 2}},
		{"vale o que guarda menos", ages(1, 2, 3, 91), 2, 90, []int{2, 3}},
		{"exatamente no limite de dias fica", ages(90), 5, 90, nil},
		{"zero cai no padrão", ages(1, 2, 3, 4, 5, 6), 0, 0, []int{5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PruneHistory(c.replacedAt, now, c.keepN, c.keep)
			if !slices.Equal(got, c.want) {
				t.Fatalf("PruneHistory = %v, quer %v", got, c.want)
			}
		})
	}
}

func TestValidateCollectionGrant(t *testing.T) {
	cases := []struct {
		name      string
		subject   CollectionSubject
		hasID     bool
		perm      CollectionPermission
		wantField string
	}{
		{"persona use", CollectionSubjectPersona, true, CollectionUse, ""},
		{"subárvore use", CollectionSubjectPersonaTree, true, CollectionUse, ""},
		{"todas as personas use", CollectionSubjectAllPersonas, false, CollectionUse, ""},
		{"humano view", CollectionSubjectUser, true, CollectionView, ""},
		{"humano manage", CollectionSubjectUser, true, CollectionManage, ""},
		{"sujeito desconhecido", "team", true, CollectionUse, "subject_type"},
		{"permissão desconhecida", CollectionSubjectPersona, true, "read", "permission"},
		{"persona sem id", CollectionSubjectPersona, false, CollectionUse, "subject_id"},
		{"todas as personas com id", CollectionSubjectAllPersonas, true, CollectionUse, "subject_id"},
		{"humano use", CollectionSubjectUser, true, CollectionUse, "permission"},
		{"persona manage", CollectionSubjectPersona, true, CollectionManage, "permission"},
		{"todas as personas view", CollectionSubjectAllPersonas, false, CollectionView, "permission"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertInvalidField(t, ValidateCollectionGrant(c.subject, c.hasID, c.perm), c.wantField)
		})
	}
}

func TestDelegation(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	exp, err := DelegationExpiry(now, 0)
	if err != nil || !exp.Equal(now.Add(DefaultDelegationTTL)) {
		t.Fatalf("padrão = %v (%v), quer 24 h", exp, err)
	}
	if _, err := DelegationExpiry(now, MaxDelegationTTL+time.Second); err == nil {
		t.Fatal("prazo acima de 7 dias deveria falhar")
	}
	if _, err := DelegationExpiry(now, -time.Second); err == nil {
		t.Fatal("prazo negativo deveria falhar")
	}
	d := SecretDelegation{ExpiresAt: now.Add(time.Hour)}
	if !d.Active(now) || d.Active(now.Add(time.Hour)) {
		t.Fatal("delegação vale até o vencimento, exclusive")
	}
	revoked := now
	d.RevokedAt = &revoked
	if d.Active(now) {
		t.Fatal("delegação revogada não vale")
	}
	for _, target := range []DelegationTarget{DelegationTask, DelegationConversation, DelegationRun} {
		if !target.Valid() {
			t.Fatalf("%s deveria ser válido", target)
		}
	}
	if DelegationTarget("flow").Valid() {
		t.Fatal("flow não é alvo de delegação")
	}
}

func TestSend(t *testing.T) {
	one, zero, big := 1, 0, MaxSendAccesses+1
	cases := []struct {
		name      string
		kind      SendKind
		ttl       time.Duration
		max       *int
		wantField string
	}{
		{"texto 7 dias", SendText, DefaultSendTTL, nil, ""},
		{"arquivo 30 dias, 1 acesso", SendFile, MaxSendTTL, &one, ""},
		{"tipo desconhecido", "link", time.Hour, nil, "kind"},
		{"sem prazo", SendText, 0, nil, "expires_in"},
		{"mais de 30 dias", SendText, MaxSendTTL + time.Second, nil, "expires_in"},
		{"zero acessos", SendText, time.Hour, &zero, "max_accesses"},
		{"acessos demais", SendText, time.Hour, &big, "max_accesses"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertInvalidField(t, ValidateSend(c.kind, c.ttl, c.max), c.wantField)
		})
	}

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s := SecretSend{ExpiresAt: now.Add(time.Hour), MaxAccesses: &one}
	if !s.Openable(now) {
		t.Fatal("envio novo deveria abrir")
	}
	s.AccessCount = 1
	if s.Openable(now) {
		t.Fatal("envio no limite de acessos não abre")
	}
	s = SecretSend{ExpiresAt: now}
	if s.Openable(now) {
		t.Fatal("envio vencido não abre")
	}
	purged := now
	s = SecretSend{ExpiresAt: now.Add(time.Hour), PurgedAt: &purged}
	if s.Openable(now) {
		t.Fatal("envio apagado não abre")
	}
}

func TestReauthFresh(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	cases := []struct {
		name   string
		at     *time.Time
		window time.Duration
		want   bool
	}{
		{"nunca reautenticou", nil, 0, false},
		{"agora", at(0), 0, true},
		{"dentro da janela padrão", at(4 * time.Minute), 0, true},
		{"no limite da janela", at(5 * time.Minute), 0, true},
		{"fora da janela padrão", at(5*time.Minute + time.Second), 0, false},
		{"janela maior", at(20 * time.Minute), 30 * time.Minute, true},
		{"relógio um pouco adiantado", at(-30 * time.Second), 0, true},
		{"prova no futuro", at(-2 * time.Minute), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ReauthFresh(c.at, now, c.window); got != c.want {
				t.Fatalf("ReauthFresh = %v, quer %v", got, c.want)
			}
		})
	}
	for _, m := range []ReauthMethod{ReauthPasskey, ReauthPassword, ReauthFirebase} {
		if !m.Valid() {
			t.Fatalf("%s deveria ser válido", m)
		}
	}
	if ReauthMethod("sms").Valid() {
		t.Fatal("sms não é método de reautenticação")
	}
}

func TestEnumsMatchSchema(t *testing.T) {
	// Os valores aceitos aqui são os mesmos das constraints da migration 00203.
	for _, k := range []SecretFieldKind{SecretFieldText, SecretFieldTOTP, SecretFieldFile} {
		if !k.Valid() {
			t.Fatalf("%s", k)
		}
	}
	for _, v := range []SecretAccessVia{SecretAccessDispatch, SecretAccessTool, SecretAccessFile, SecretAccessSend} {
		if !v.Valid() {
			t.Fatalf("%s", v)
		}
	}
	if SecretAccessVia("export").Valid() || SecretFieldKind("card").Valid() {
		t.Fatal("valor fora do schema aceito")
	}
}

func assertInvalidField(t *testing.T, err error, wantField string) {
	t.Helper()
	if wantField == "" {
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		return
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("erro = %v, quer ValidationError no campo %q", err, wantField)
	}
	if ve.Field != wantField {
		t.Fatalf("campo do erro = %q (%v), quer %q", ve.Field, err, wantField)
	}
}
