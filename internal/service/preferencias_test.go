package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// Preferências da pessoa no servidor (ADR-0061): favoritos, recentes, perfil e dispensa do "O que há de novo".

func navPaths(items []domain.NavItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Path
	}
	return out
}

func TestFavoritesArePerPersonAndEmitActivity(t *testing.T) {
	e, ana, bruno := caixaEnv(t)
	if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "/tasks/HNK-1?x=1", "  Revisar \n contrato ", true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "/personas/ana", "Ana", true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "/personas/ana", "Ana", true); err != nil { // idempotente
		t.Fatal(err)
	}
	favs, recs, err := e.svc.NavItems(as(ana), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(navPaths(favs), ","); got != "/tasks/HNK-1,/personas/ana" || len(recs) != 0 {
		t.Fatalf("favoritos da Ana (na ordem em que marcou): %s, recentes %d", got, len(recs))
	}
	if favs[0].Title != "Revisar contrato" {
		t.Errorf("título deve vir limpo: %q", favs[0].Title)
	}
	if bf, _, _ := e.svc.NavItems(as(bruno), e.co.ID); len(bf) != 0 {
		t.Fatalf("favorito é de quem marcou: Bruno vê %d", len(bf))
	}
	n := countActivity(t, e, "nav.favorite_added")
	if n != 2 {
		t.Fatalf("fato por favorito novo (não por repetição): %d", n)
	}

	if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "/tasks/HNK-1", "", false); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "/tasks/HNK-1", "", false); err != nil { // remover o que não existe não falha
		t.Fatal(err)
	}
	if favs, _, _ = e.svc.NavItems(as(ana), e.co.ID); len(favs) != 1 || countActivity(t, e, "nav.favorite_removed") != 1 {
		t.Fatalf("remoção: %v", navPaths(favs))
	}
	if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "https://evil.com/x", "x", true); err == nil {
		t.Fatal("caminho de fora deve ser recusado")
	}
}

func countActivity(t *testing.T, e *env, verb string) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = $2`, e.co.ID, verb).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFavoritesLimit(t *testing.T) {
	e, ana, _ := caixaEnv(t)
	for i := range domain.MaxFavorites {
		if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, fmt.Sprintf("/tasks/HNK-%d", i), "t", true); err != nil {
			t.Fatalf("favorito %d: %v", i, err)
		}
	}
	err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "/tasks/HNK-999", "t", true)
	wantErr(t, err, domain.ErrInvalid)
	// repetir um que já existe continua valendo no limite
	if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "/tasks/HNK-0", "t", true); err != nil {
		t.Fatalf("repetir no limite: %v", err)
	}
}

func TestRecentsKeepTheNewestAndIgnoreListPages(t *testing.T) {
	e, ana, _ := caixaEnv(t)
	for i := range domain.MaxRecents + 3 {
		if err := e.svc.TouchRecent(as(ana), e.co.ID, fmt.Sprintf("/tasks/HNK-%d", i), fmt.Sprintf("Task %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.TouchRecent(as(ana), e.co.ID, "/tasks", "Tasks"); err != nil { // lista não é detalhe: ignorada
		t.Fatal(err)
	}
	if err := e.svc.TouchRecent(as(ana), e.co.ID, "/nada/1", "x"); err != nil { // seção desconhecida: ignorada sem erro
		t.Fatal(err)
	}
	_, recs, err := e.svc.NavItems(as(ana), e.co.ID)
	if err != nil || len(recs) != domain.MaxRecents {
		t.Fatalf("recentes = %d %v", len(recs), err)
	}
	if recs[0].Path != fmt.Sprintf("/tasks/HNK-%d", domain.MaxRecents+2) {
		t.Fatalf("o mais novo vem primeiro: %v", navPaths(recs))
	}
	// revisitar sobe para o topo sem duplicar
	if err := e.svc.TouchRecent(as(ana), e.co.ID, "/tasks/HNK-5", "Task 5 (novo título)"); err != nil {
		t.Fatal(err)
	}
	_, recs, _ = e.svc.NavItems(as(ana), e.co.ID)
	if len(recs) != domain.MaxRecents || recs[0].Path != "/tasks/HNK-5" || recs[0].Title != "Task 5 (novo título)" {
		t.Fatalf("revisita: %v", navPaths(recs))
	}
	if countActivity(t, e, "nav.recent") != 0 {
		t.Fatal("recentes não geram fato")
	}
}

func TestNavPrefsInTrustedModeUseTheLocalSubject(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	if err := e.svc.SetFavorite(bg, domain.HumanActor, e.co.ID, "/projetos/site", "Site", true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.TouchRecent(bg, e.co.ID, "/personas/ana", "Ana"); err != nil {
		t.Fatal(err)
	}
	favs, recs, err := e.svc.NavItems(bg, e.co.ID)
	if err != nil || len(favs) != 1 || len(recs) != 1 {
		t.Fatalf("modo trusted: %v %v %v", favs, recs, err)
	}
}

func TestProfileRoundTripAndTrustedRefusal(t *testing.T) {
	e, ana, _ := caixaEnv(t)
	ctx := as(ana)
	p, err := e.svc.GetProfile(ctx)
	if err != nil || p.Email != ana.Email || p.Timezone != "" || p.Locale != "" {
		t.Fatalf("perfil padrão: %+v %v", p, err)
	}
	name, tz, loc, glyph, tone := "Ana Souza", "America/Sao_Paulo", "en", "AS", "violet"
	got, err := e.svc.UpdateProfile(ctx, domain.ProfileInput{Name: &name, Timezone: &tz, Locale: &loc, AvatarGlyph: &glyph, AvatarTone: &tone})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != name || got.Timezone != tz || got.Locale != loc || got.AvatarGlyph != glyph || got.AvatarTone != tone {
		t.Fatalf("perfil gravado: %+v", got)
	}
	again, _ := e.svc.GetProfile(ctx)
	if again != got {
		t.Fatalf("persistido: %+v != %+v", again, got)
	}
	if u, err := e.svc.GetUserByEmail(context.Background(), ana.Email); err != nil || u.Name != name {
		t.Fatalf("o nome mora em users.name: %q %v", u.Name, err)
	}
	// atualização parcial preserva o resto
	loc2 := "pt-BR"
	got, err = e.svc.UpdateProfile(ctx, domain.ProfileInput{Locale: &loc2})
	if err != nil || got.Locale != "pt-BR" || got.Timezone != tz || got.AvatarGlyph != glyph {
		t.Fatalf("parcial: %+v %v", got, err)
	}
	bad := "Marte/Olympus"
	if _, err := e.svc.UpdateProfile(ctx, domain.ProfileInput{Timezone: &bad}); err == nil {
		t.Fatal("fuso inválido deve ser recusado")
	}
	if pr := e.svc.ProfileOf(context.Background(), ana.ID); pr.Locale != "pt-BR" || pr.Timezone != tz {
		t.Fatalf("ProfileOf: %+v", pr)
	}
	if pr := e.svc.ProfileOf(context.Background(), domain.NewID()); pr.UserID != (domain.ID{}) {
		t.Fatalf("usuário inexistente: perfil vazio, veio %+v", pr)
	}
	// modo trusted: sem conta, sem perfil
	if _, err := e.svc.GetProfile(context.Background()); err == nil {
		t.Fatal("trusted não tem perfil")
	}
	if _, err := e.svc.UpdateProfile(context.Background(), domain.ProfileInput{Name: &name}); err == nil {
		t.Fatal("trusted não edita perfil")
	}
}

func TestWhatsNewDismissalIsPerPerson(t *testing.T) {
	e, ana, bruno := caixaEnv(t)
	if v, err := e.svc.WhatsNewSeen(as(ana)); err != nil || v != "" {
		t.Fatalf("nada visto: %q %v", v, err)
	}
	if err := e.svc.DismissWhatsNew(as(ana), "0.3.0"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DismissWhatsNew(as(ana), "0.3.1"); err != nil { // sobrescreve
		t.Fatal(err)
	}
	if v, _ := e.svc.WhatsNewSeen(as(ana)); v != "0.3.1" {
		t.Fatalf("Ana viu %q", v)
	}
	if v, _ := e.svc.WhatsNewSeen(as(bruno)); v != "" {
		t.Fatalf("a dispensa é da Ana: Bruno vê %q", v)
	}
	if err := e.svc.DismissWhatsNew(as(ana), "<b>"); err == nil {
		t.Fatal("versão com marcação deve ser recusada")
	}
	// trusted
	if err := e.svc.DismissWhatsNew(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.WhatsNewSeen(context.Background()); v != "dev" {
		t.Fatalf("trusted: %q", v)
	}
}

func TestRemovingAUserClearsTheirPreferences(t *testing.T) {
	e, ana, _ := caixaEnv(t)
	if err := e.svc.SetFavorite(as(ana), ana.Actor(), e.co.ID, "/tasks/HNK-1", "t", true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DismissWhatsNew(as(ana), "1.0.0"); err != nil {
		t.Fatal(err)
	}
	owner := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	if err := e.svc.AdminRemoveUser(as(owner), ana.Email); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM nav_items WHERE subject = $1) + (SELECT count(*) FROM user_dismissals WHERE subject = $1)`, ana.ID.String()).Scan(&n); err != nil || n != 0 {
		t.Fatalf("sobras das preferências: %d %v", n, err)
	}
}
