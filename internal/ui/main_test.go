package ui

import (
	"os"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// TestMain pins the clock of the sample data and of the test server (noon of 2026-10-14 in the
// company time zone): month-to-date figures in the sample depend on the day of the month.
func TestMain(m *testing.M) {
	uitest.Fixed = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	os.Exit(m.Run())
}
