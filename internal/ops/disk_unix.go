//go:build unix

package ops

import "syscall"

// DiskFree devolve os bytes livres (para usuários comuns) do sistema de arquivos de path.
func DiskFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:gosec,unconvert // tipos variam entre linux e darwin
}
