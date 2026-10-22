package db

import (
	"reflect"
	"testing"
	"testing/fstest"
)

func TestVersionsOfParsesPrefixes(t *testing.T) {
	fsys := fstest.MapFS{
		"00171_a.sql": {}, "00005_b.sql": {}, "embed.go": {}, "semprefixo.sql": {}, "00100_c.sql": {}, "xx_y.sql": {},
	}
	if got := versionsOf(fsys); !reflect.DeepEqual(got, []int64{5, 100, 171}) {
		t.Fatalf("versões = %v", got)
	}
}

func TestPendingVersionsIgnoresOrder(t *testing.T) {
	got := PendingVersions([]int64{1, 2, 50, 100}, []int64{1, 2, 100})
	if !reflect.DeepEqual(got, []int64{50}) {
		t.Fatalf("pendentes = %v", got)
	}
	if got := PendingVersions([]int64{1}, []int64{1}); len(got) != 0 {
		t.Fatalf("pendentes = %v", got)
	}
}

func TestLatestEmbeddedKnowsTheRealMigrations(t *testing.T) {
	if LatestEmbedded() < 171 {
		t.Fatalf("LatestEmbedded = %d", LatestEmbedded())
	}
}
