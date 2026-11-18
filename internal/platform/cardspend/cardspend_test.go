package cardspend

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSpendDate(t *testing.T) {
	cases := []struct {
		purchase string
		n        int
		want     string
	}{
		{"2026-01-22", 0, "2026-01-22"},
		{"2026-01-22", 1, "2026-01-22"},
		{"2026-01-22", 5, "2026-05-22"},
		{"2026-01-31", 2, "2026-02-28"},
		{"2025-12-15", 3, "2026-02-15"},
	}
	for _, c := range cases {
		if got := SpendDate(day(c.purchase), c.n).Format("2006-01-02"); got != c.want {
			t.Errorf("SpendDate(%s, %d) = %s, want %s", c.purchase, c.n, got, c.want)
		}
	}
}

func TestParseInstallment(t *testing.T) {
	cases := []struct {
		desc     string
		n, total int
		ok       bool
	}{
		{"CASASBAHIACOM 05/10", 5, 10, true},
		{"MERCADOLIVRE*ELEME03/10", 3, 10, true},
		{"JIM.COM* JEN-CT R 02/05", 2, 5, true},
		{"Loja X (3/12)", 3, 12, true},
		{"Uber UBER *TRIP HELP.U", 0, 0, false},
		{"ANUIDADE 13/12", 0, 0, false},
		{"ALGO 01/01", 0, 0, false},
	}
	for _, c := range cases {
		n, total, ok := ParseInstallment(c.desc)
		if n != c.n || total != c.total || ok != c.ok {
			t.Errorf("ParseInstallment(%q) = %d, %d, %v", c.desc, n, total, ok)
		}
	}
}
