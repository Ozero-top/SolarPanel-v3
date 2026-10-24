//go:build !windows

package nas

import "syscall"

// diskUsage 返回挂载点的总容量与可用容量（字节）
func diskUsage(path string) (total, avail uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := uint64(st.Bsize)
	return uint64(st.Blocks) * bs, uint64(st.Bavail) * bs, nil
}
