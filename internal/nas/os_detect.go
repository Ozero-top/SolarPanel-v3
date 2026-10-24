package nas

import (
	"os"
	"path/filepath"
	"strings"
)

// detectOS 识别 NAS 系统，返回 family（dsm/ugos/fnos/qts/zos/generic）与展示名。
// 依据顺序：发行版标识文件 → os-release ID/NAME → 默认名。
func detectOS(root string) (family, name string) {
	hp := func(p string) string {
		if root == "" {
			return p
		}
		return filepath.Join(root, p)
	}
	exists := func(p string) bool {
		_, err := os.Stat(hp(p))
		return err == nil
	}

	kv := parseKV(readFileTrim(hp("/etc/os-release")))
	if len(kv) == 0 {
		kv = parseKV(readFileTrim(hp("/usr/lib/os-release")))
	}
	id := strings.ToLower(kv["ID"])
	pretty := kv["PRETTY_NAME"]
	lower := strings.ToLower(pretty + " " + kv["NAME"])

	// OpenWrt 系（iStoreOS / ImmortalWrt / 原生 OpenWrt）：软路由、ARM 小主机常见
	ow := parseKV(readFileTrim(hp("/etc/openwrt_release")))
	owLower := strings.ToLower(ow["distrib_description"] + " " + ow["distrib_id"])

	switch {
	case exists("/etc.istoreos_release") || exists("/etc/istoreos_release") || strings.Contains(lower, "istoreos") || strings.Contains(owLower, "istoreos"):
		family = "istoreos"
		name = owrtName(ow["distrib_description"], ow["distrib_version"], "iStoreOS")
	case strings.Contains(owLower, "immortalwrt") || strings.Contains(lower, "immortalwrt") || strings.Contains(id, "immortalwrt"):
		family = "openwrt"
		name = owrtName(ow["distrib_description"], ow["distrib_version"], "ImmortalWrt")
	case exists("/etc/openwrt_release") || strings.Contains(id, "openwrt") || strings.Contains(lower, "openwrt") || strings.Contains(lower, "lede"):
		family = "openwrt"
		name = owrtName(ow["distrib_description"], ow["distrib_version"], "OpenWrt")
	case exists("/etc.defaults/VERSION") || strings.Contains(id, "dsm") || strings.Contains(id, "synology") || strings.Contains(lower, "synology") || strings.Contains(lower, "dsm"):
		family = "dsm"
		if v := parseKV(readFileTrim(hp("/etc.defaults/VERSION"))); v["productversion"] != "" {
			name = "DSM " + v["productversion"]
		}
	case exists("/etc/config/uLinux.conf") || strings.Contains(id, "qts") || strings.Contains(id, "qnap") || strings.Contains(lower, "qnap"):
		family = "qts"
		name = qnapVersion(hp("/etc/config/uLinux.conf"))
	case exists("/etc/ugos-release") || strings.Contains(id, "ugos") || strings.Contains(id, "ugreen") || strings.Contains(lower, "ugos") || strings.Contains(lower, "ugreen"):
		family = "ugos"
		name = firstPretty(hp("/etc/ugos-release"))
	case exists("/etc/fnos-release") || exists("/etc/trim") || strings.Contains(id, "fnos") || strings.Contains(lower, "fnos") || strings.Contains(lower, "飞牛"):
		family = "fnos"
		name = firstPretty(hp("/etc/fnos-release"))
		// 飞牛的 os-release 是 Debian，缺少专属版本文件时直接显示 fnOS，避免误显示为 Debian
		if name == "" {
			name = "fnOS"
		}
	case exists("/etc/zos-release") || strings.Contains(id, "zos") || strings.Contains(id, "zspace") || strings.Contains(lower, "zos") || strings.Contains(lower, "极空间"):
		family = "zos"
		name = firstPretty(hp("/etc/zos-release"))
	default:
		family = "generic"
	}

	if name == "" {
		name = pretty
	}
	if name == "" {
		name = kv["NAME"]
	}
	if name == "" {
		name = map[string]string{
			"dsm": "Synology DSM", "ugos": "UGREEN UGOS", "fnos": "fnOS",
			"qts": "QNAP QTS", "zos": "Zspace ZOS",
			"openwrt": "OpenWrt", "istoreos": "iStoreOS", "generic": "Linux",
		}[family]
	}
	return family, strings.Trim(name, `"`)
}

// owrtName 从 OpenWrt 系发行版的 DISTRIB_DESCRIPTION 提取「名称 + 版本」，
// 去掉末尾的 revision（如 "OpenWrt 23.05.3 r23824-ae42e1f7c6" → "OpenWrt 23.05.3"）
func owrtName(desc, version, fallback string) string {
	desc = strings.TrimSpace(desc)
	if desc != "" {
		f := strings.Fields(desc)
		if len(f) >= 2 && !strings.HasPrefix(f[1], "r") {
			return f[0] + " " + f[1]
		}
		return f[0]
	}
	if v := strings.TrimSpace(version); v != "" {
		return fallback + " " + v
	}
	return fallback
}

// parseKV 解析 KEY=VALUE 形式的发行版文件与 VERSION 文件（键统一小写）
func parseKV(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return out
}

func firstPretty(path string) string {
	kv := parseKV(readFileTrim(path))
	if v := kv["pretty_name"]; v != "" {
		return v
	}
	return kv["version"]
}

// qnapVersion 从 uLinux.conf 的 [System] 段读取版本号
func qnapVersion(path string) string {
	var inSystem bool
	var version string
	for _, line := range strings.Split(readFileTrim(path), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inSystem = strings.EqualFold(line, "[System]")
			continue
		}
		if !inSystem {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "version") {
			version = strings.TrimSpace(v)
		}
	}
	if version == "" {
		return "QTS"
	}
	return "QTS " + version
}
