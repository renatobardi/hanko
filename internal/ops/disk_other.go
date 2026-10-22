//go:build !unix

package ops

import "errors"

// DiskFree não é suportado fora de Unix.
func DiskFree(string) (uint64, error) {
	return 0, errors.New("medição de disco indisponível nesta plataforma")
}
