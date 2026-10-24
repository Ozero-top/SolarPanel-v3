//go:build windows

package nas

import "errors"

// diskUsage Windows 本地开发环境不支持（生产运行于 Linux 容器）
func diskUsage(path string) (total, avail uint64, err error) {
	return 0, 0, errors.New("disk usage not supported on windows")
}
