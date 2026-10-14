package ui

import (
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

func TestRelTime(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "agora"},
		{4 * time.Minute, "4 min"},
		{59 * time.Minute, "59 min"},
		{3 * time.Hour, "3 h"},
		{20 * time.Hour, "20 h"},
		{30 * time.Hour, "ontem"},
		{3*24*time.Hour + time.Hour, "3 dias"},
		{20 * 24 * time.Hour, "23 set"},
		{-time.Minute, "agora"},
	}
	for _, c := range cases {
		if got := relTime(now.Add(-c.d), now, loc); got != c.want {
			t.Errorf("relTime(-%v) = %q, want %q", c.d, got, c.want)
		}
	}
	if got := ago(now.Add(-4*time.Minute), now, loc); got != "há 4 min" {
		t.Errorf("ago = %q", got)
	}
	if got := ago(now.Add(-30*time.Hour), now, loc); got != "ontem" {
		t.Errorf("ago = %q", got)
	}
	if got := relTime(time.Time{}, now, loc); got != "—" {
		t.Errorf("zero = %q", got)
	}
}

func TestFmtUSD(t *testing.T) {
	cases := map[float64]string{0: "US$ 0,00", 0.06: "US$ 0,06", 412.8: "US$ 412,80", 1234.5: "US$ 1.234,50", 1234567.891: "US$ 1.234.567,89", 0.0004: "US$ 0,0004"}
	for in, want := range cases {
		if got := fmtUSD(in); got != want {
			t.Errorf("fmtUSD(%v) = %q, want %q", in, got, want)
		}
	}
	if fmtCostPtr(nil) != "—" {
		t.Error("nil cost")
	}
}

func TestFmtTokensAndDuration(t *testing.T) {
	if got := fmtTokens(842); got != "842" {
		t.Error(got)
	}
	if got := fmtTokens(84200); got != "84,2k" {
		t.Error(got)
	}
	if got := fmtTokens(2_500_000); got != "2,5M" {
		t.Error(got)
	}
	if got := fmtUsage(domain.Usage{}); got != "—" {
		t.Error(got)
	}
	if got := fmtUsage(domain.Usage{InputTokens: 84200, OutputTokens: 11900}); got != "84,2k / 11,9k" {
		t.Error(got)
	}
	for d, want := range map[time.Duration]string{8 * time.Second: "8s", 4*time.Minute + 12*time.Second: "4m 12s", 2*time.Hour + 5*time.Minute: "2h 05m", -time.Second: "0s"} {
		if got := fmtDuration(d); got != want {
			t.Errorf("fmtDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestHeartbeatLabel(t *testing.T) {
	for sec, want := range map[int]string{0: "manual", 900: "a cada 15 min", 3600: "a cada 1 h", 45: "a cada 45 s"} {
		if got := heartbeatLabel(sec); got != want {
			t.Errorf("heartbeatLabel(%d) = %q", sec, got)
		}
	}
}

func TestVerbPhrases(t *testing.T) {
	if verbTail("task.status_changed") != "status" || verbTail("weird") != "weird" {
		t.Error("verbTail")
	}
	if verbPhrase("comment.added") != "comentou em" || verbPhrase("x.y") != "x.y" {
		t.Error("verbPhrase")
	}
}
