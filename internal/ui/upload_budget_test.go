package ui

import "testing"

// O envio soma no máximo 10 arquivos no limite por arquivo, sem passar de 128 MiB (ADR-0065).
func TestUploadBudget(t *testing.T) {
	for _, c := range []struct{ maxFile, want int64 }{
		{5 << 20, 50 << 20},
		{16 << 20, 128 << 20},
		{64 << 20, 128 << 20},
	} {
		if got := uploadBudget(c.maxFile); got != c.want {
			t.Errorf("uploadBudget(%d) = %d, want %d", c.maxFile, got, c.want)
		}
	}
}
