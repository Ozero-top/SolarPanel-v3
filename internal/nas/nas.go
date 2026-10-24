// Package nas 采集宿主 NAS 指标与 Docker 容器状态。
//
// 兼容性说明：容器内 /proc/stat、/proc/meminfo、/proc/cpuinfo 反映的是宿主机全局数据，
// 因此 CPU / 内存 / 核数可直接读取；磁盘、温度、系统发行版需要把宿主机根目录只读挂载到
// NAS_HOST_ROOT（默认 /host）；容器指标通过 Docker socket 获取（各 NAS 均为
// /var/run/docker.sock）。网络指标在非 host 网络模式下只能反映容器自身网卡，会在
// Net.Scope 中标注 container，由前端提示口径。
package nas

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HostInfo 宿主机概览
type HostInfo struct {
	Hostname  string  `json:"hostname"`
	OSName    string  `json:"os_name"`
	OSFamily  string  `json:"os_family"`
	Kernel    string  `json:"kernel"`
	Arch      string  `json:"arch"`
	Uptime    int64   `json:"uptime"`
	Cores     int     `json:"cores"`
	Load1     float64 `json:"load1"`
	CPUPct    float64 `json:"cpu_pct"`
	TempC     float64 `json:"temp_c"`
	MemTotal  uint64  `json:"mem_total"`
	MemUsed   uint64  `json:"mem_used"`
	SwapTotal uint64  `json:"swap_total"`
	SwapUsed  uint64  `json:"swap_used"`
	// IP 宿主机内网地址（优先私网地址）
	IP string `json:"ip"`
	// Virtualized 表示系统运行在虚拟机中（虚拟化平台通常不暴露 CPU 温度传感器）
	Virtualized bool `json:"virtualized"`
}

// DiskInfo 存储池
type DiskInfo struct {
	Mount  string `json:"mount"`
	FSType string `json:"fstype"`
	Total  uint64 `json:"total"`
	Used   uint64 `json:"used"`
}

// NetInfo 网络吞吐
type NetInfo struct {
	RxBps float64 `json:"rx_bps"`
	TxBps float64 `json:"tx_bps"`
	Scope string  `json:"scope"` // host = 宿主机口径；container = 容器网卡口径
}

// ContainerInfo 容器状态
type ContainerInfo struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Image    string   `json:"image"`
	State    string   `json:"state"`
	CPUPct   float64  `json:"cpu_pct"`
	MemUsed  uint64   `json:"mem_used"`
	MemLimit uint64   `json:"mem_limit"`
	RxBps    float64  `json:"rx_bps"`
	TxBps    float64  `json:"tx_bps"`
	Uptime   int64    `json:"uptime"`
	Ports    []string `json:"ports"` // 端口映射，如 10086→10086/tcp
}

// History 近 10 分钟采样历史；后端持续采样，刷新页面不会丢失曲线
type History struct {
	Interval int       `json:"interval"` // 采样间隔（秒）
	Span     int       `json:"span"`     // 覆盖时长（秒）
	Rx       []float64 `json:"rx"`
	Tx       []float64 `json:"tx"`
	CPU      []float64 `json:"cpu"`
}

// Snapshot 一次采集结果
type Snapshot struct {
	Host        HostInfo        `json:"host"`
	Disks       []DiskInfo      `json:"disks"`
	Net         NetInfo         `json:"net"`
	Containers  []ContainerInfo `json:"containers"`
	History     History         `json:"history"`
	DockerOK    bool            `json:"docker_ok"`
	HostRoot    string          `json:"host_root"`
	CollectedAt int64           `json:"collected_at"`
}

type cpuSample struct {
	total uint64
	idle  uint64
}

type netSample struct {
	rx, tx uint64
	at     time.Time
}

type ctSample struct {
	rx, tx uint64
	at     time.Time
}

// Collector 带缓存的采集器（前端轮询间隔通常 2~10s，缓存 2s 避免重复采集）
type Collector struct {
	hostRoot     string
	dockerSocket string

	mu     sync.Mutex
	snap   *Snapshot
	snapAt time.Time

	// sampleMu 保护 cpuPrev / netPrev：主采集与后台历史采样会并发读取 /proc
	sampleMu sync.Mutex
	cpuPrev  cpuSample
	netPrev  netSample
	ctPrev   map[string]ctSample

	histMu   sync.Mutex
	histRx   []float64
	histTx   []float64
	histCPU  []float64
	histMax  int
	histStep int
}

func NewCollector(hostRoot, dockerSocket string) *Collector {
	c := &Collector{
		hostRoot:     hostRoot,
		dockerSocket: dockerSocket,
		ctPrev:       make(map[string]ctSample),
		histMax:      120, // 5s × 120 = 10 分钟
		histStep:     5,
	}
	go c.historyLoop()
	return c
}

// historyLoop 后台轻量采样（只读 /proc，不触发 Docker 查询），保证曲线在刷新页面后依然完整
func (c *Collector) historyLoop() {
	t := time.NewTicker(time.Duration(c.histStep) * time.Second)
	defer t.Stop()
	for range t.C {
		c.sampleHistory()
	}
}

func (c *Collector) sampleHistory() {
	cpu := c.sampleCPU(c.hostPath("/proc/stat"))
	net := c.collectNet()

	c.histMu.Lock()
	defer c.histMu.Unlock()
	c.histCPU = appendRing(c.histCPU, cpu, c.histMax)
	c.histRx = appendRing(c.histRx, net.RxBps, c.histMax)
	c.histTx = appendRing(c.histTx, net.TxBps, c.histMax)
}

func appendRing(arr []float64, v float64, max int) []float64 {
	arr = append(arr, v)
	if len(arr) > max {
		arr = arr[len(arr)-max:]
	}
	return arr
}

func (c *Collector) historySnapshot() History {
	c.histMu.Lock()
	defer c.histMu.Unlock()
	return History{
		Interval: c.histStep,
		Span:     c.histStep * c.histMax,
		Rx:       append([]float64{}, c.histRx...),
		Tx:       append([]float64{}, c.histTx...),
		CPU:      append([]float64{}, c.histCPU...),
	}
}

// Collect 返回快照；cacheTTL 内的重复调用直接返回缓存
func (c *Collector) Collect(diskPaths []string) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.snap != nil && time.Since(c.snapAt) < 3*time.Second {
		cp := *c.snap
		cp.History = c.historySnapshot()
		return cp
	}

	s := Snapshot{
		Host:        c.collectHost(),
		Disks:       c.collectDisks(diskPaths),
		Net:         c.collectNet(),
		History:     c.historySnapshot(),
		DockerOK:    dockerAvailable(c.dockerSocket),
		HostRoot:    c.hostRoot,
		CollectedAt: time.Now().Unix(),
	}
	if s.DockerOK {
		s.Containers = c.collectContainers()
	}
	if s.Containers == nil {
		s.Containers = []ContainerInfo{}
	}
	if s.Disks == nil {
		s.Disks = []DiskInfo{}
	}

	c.snap = &s
	c.snapAt = time.Now()
	return s
}

func (c *Collector) hostPath(rel string) string {
	if c.hostRoot == "" {
		return rel
	}
	return filepath.Join(c.hostRoot, rel)
}

func readFileTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readLines(path string) []string {
	s := readFileTrim(path)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

/* ================= 宿主机 ================= */

func (c *Collector) collectHost() HostInfo {
	h := HostInfo{Arch: runtime.GOARCH, TempC: -1}

	// 宿主 UTS namespace 的主机名在 /proc/1/sys/kernel/hostname；
	// OpenWrt 系没有 /etc/hostname，容器自身 /proc/sys/kernel/hostname 只会返回容器 ID。
	h.Hostname = readFileTrim(c.hostPath("/proc/1/sys/kernel/hostname"))
	if h.Hostname == "" {
		h.Hostname = readFileTrim(c.hostPath("/etc/hostname"))
	}
	if h.Hostname == "" {
		h.Hostname = readFileTrim(c.hostPath("/proc/sys/kernel/hostname"))
	}
	if h.Hostname == "" {
		if name, err := os.Hostname(); err == nil {
			h.Hostname = name
		}
	}

	version := readFileTrim(c.hostPath("/proc/version"))
	if f := strings.Fields(version); len(f) >= 3 {
		h.Kernel = f[2]
	}

	h.OSFamily, h.OSName = detectOS(c.hostRoot)

	h.Cores = countCores(c.hostPath("/proc/cpuinfo"))
	if h.Cores == 0 {
		h.Cores = runtime.NumCPU()
	}

	if f := strings.Fields(readFileTrim(c.hostPath("/proc/loadavg"))); len(f) > 0 {
		h.Load1, _ = strconv.ParseFloat(f[0], 64)
	}
	if f := strings.Fields(readFileTrim(c.hostPath("/proc/uptime"))); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			h.Uptime = int64(v)
		}
	}

	h.CPUPct = c.sampleCPU(c.hostPath("/proc/stat"))
	c.readMemory(&h)
	h.Virtualized = c.detectVirtualized()
	h.IP = c.detectIP()
	h.TempC = readTemp(c.hostPath("/sys/class/thermal"), c.hostPath("/sys/class/hwmon"))
	return h
}

// detectIP 读取宿主机网络命名空间的 fib_trie 获取内网地址。
// /proc/net 是 per-netns 视图，必须用 /proc/1/net（宿主 init 进程）才能拿到宿主 IP。
// 选择顺序：与默认网关同网段 → 私网优先级 → 排除 docker0。
func (c *Collector) detectIP() string {
	ips := c.localIPv4s()
	if len(ips) == 0 {
		return ""
	}
	if gw := c.defaultGateway(); gw != nil {
		for _, ip := range ips {
			if sameV4Subnet(ip, gw, 24) {
				return ip
			}
		}
		for _, ip := range ips {
			if sameV4Subnet(ip, gw, 16) {
				return ip
			}
		}
	}
	best, bestScore := "", -1
	for _, ip := range ips {
		if s := ipScore(ip); s > bestScore {
			best, bestScore = ip, s
		}
	}
	return best
}

func (c *Collector) localIPv4s() []string {
	for _, p := range []string{
		c.hostPath("/proc/1/net/fib_trie"),
		c.hostPath("/proc/net/fib_trie"),
	} {
		lines := readLines(p)
		if len(lines) == 0 {
			continue
		}
		var ips []string
		prev := ""
		for _, ln := range lines {
			if strings.Contains(ln, "host LOCAL") {
				if ip := parseLocalIP(prev); ip != "" {
					ips = append(ips, ip)
				}
			}
			prev = ln
		}
		if len(ips) > 0 {
			return ips
		}
	}
	return nil
}

// defaultGateway 解析 /proc/net/route 的默认路由网关（十六进制小端）
func (c *Collector) defaultGateway() net.IP {
	for _, ln := range readLines(c.hostPath("/proc/1/net/route")) {
		f := strings.Fields(ln)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		v, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || v == 0 {
			continue
		}
		return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	return nil
}

func sameV4Subnet(a string, gw net.IP, prefix int) bool {
	a4, g4 := net.ParseIP(a).To4(), gw.To4()
	if a4 == nil || g4 == nil {
		return false
	}
	mask := net.CIDRMask(prefix, 32)
	return a4.Mask(mask).Equal(g4.Mask(mask))
}

// ipScore 私网优先级：192.168 > 10 > 172.16-31；docker0（172.17）降级
func ipScore(s string) int {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return -2
	}
	if strings.HasPrefix(s, "172.17.") {
		return -1
	}
	switch {
	case strings.HasPrefix(s, "192.168."):
		return 3
	case strings.HasPrefix(s, "10."):
		return 2
	case ip.IsPrivate():
		return 1
	}
	return 0
}

// parseLocalIP 从 fib_trie 的 "|-- 192.168.1.10" 行取 IPv4 地址
func parseLocalIP(line string) string {
	f := strings.Fields(strings.ReplaceAll(line, "|", " "))
	if len(f) == 0 {
		return ""
	}
	ip := f[len(f)-1]
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil || parsed.IsLoopback() || parsed.IsUnspecified() {
		return ""
	}
	return ip
}

// detectVirtualized 通过 CPU hypervisor 标记与 DMI 信息判断是否运行在虚拟机中
func (c *Collector) detectVirtualized() bool {
	for _, line := range readLines(c.hostPath("/proc/cpuinfo")) {
		if strings.HasPrefix(line, "flags") && strings.Contains(line, "hypervisor") {
			return true
		}
	}
	name := strings.ToLower(readFileTrim(c.hostPath("/sys/class/dmi/id/product_name")))
	vendor := strings.ToLower(readFileTrim(c.hostPath("/sys/class/dmi/id/sys_vendor")))
	for _, key := range []string{"qemu", "kvm", "vmware", "virtualbox", "hyper-v", "virtual machine", "proxmox"} {
		if strings.Contains(name, key) || strings.Contains(vendor, key) {
			return true
		}
	}
	return false
}

func countCores(path string) int {
	n := 0
	for _, line := range readLines(path) {
		if strings.HasPrefix(line, "processor") {
			n++
		}
	}
	return n
}

// sampleCPU 用两次 /proc/stat 采样差分计算使用率（首次调用返回 0）
func (c *Collector) sampleCPU(path string) float64 {
	c.sampleMu.Lock()
	defer c.sampleMu.Unlock()

	lines := readLines(path)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "cpu ") {
		return 0
	}
	var vals []uint64
	for _, f := range strings.Fields(lines[0])[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0
		}
		vals = append(vals, v)
	}
	if len(vals) < 4 {
		return 0
	}
	var total, idle uint64
	for i, v := range vals {
		total += v
		if i == 3 || i == 4 { // idle + iowait
			idle += v
		}
	}
	prev := c.cpuPrev
	c.cpuPrev = cpuSample{total: total, idle: idle}
	if prev.total == 0 || total <= prev.total {
		return 0
	}
	busy := (total - prev.total) - (idle - prev.idle)
	return float64(busy) / float64(total-prev.total) * 100
}

func (c *Collector) readMemory(h *HostInfo) {
	var total, available, swapTotal, swapFree uint64
	for _, line := range readLines(c.hostPath("/proc/meminfo")) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		v *= 1024 // kB → B
		switch strings.TrimSuffix(f[0], ":") {
		case "MemTotal":
			total = v
		case "MemAvailable":
			available = v
		case "SwapTotal":
			swapTotal = v
		case "SwapFree":
			swapFree = v
		}
	}
	h.MemTotal = total
	if total > available {
		h.MemUsed = total - available
	}
	h.SwapTotal = swapTotal
	if swapTotal > swapFree {
		h.SwapUsed = swapTotal - swapFree
	}
}

// skipNetIface 跳过回环、网桥与容器虚拟网卡：
// OpenWrt 的 br-lan 与其成员物理口会重复计数，docker0/veth 属于容器内部流量。
func skipNetIface(name string) bool {
	if name == "" || name == "lo" || strings.HasPrefix(name, "Inter-Process") {
		return true
	}
	for _, p := range []string{"veth", "docker", "virbr", "ifb", "dummy"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	// br-lan / br0 / br1 / br-wan 等桥接口
	if strings.HasPrefix(name, "br") {
		rest := name[2:]
		if rest == "" || rest[0] == '-' || (rest[0] >= '0' && rest[0] <= '9') ||
			strings.HasPrefix(rest, "lan") || strings.HasPrefix(rest, "wan") {
			return true
		}
	}
	return false
}

func (c *Collector) collectNet() NetInfo {
	c.sampleMu.Lock()
	defer c.sampleMu.Unlock()

	n := NetInfo{Scope: "host"}
	path := c.hostPath("/proc/net/dev")
	if !c.hostNetNS() {
		n.Scope = "container"
		path = "/proc/net/dev"
	}

	var rx, tx uint64
	for _, line := range readLines(path) {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if skipNetIface(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, err1 := strconv.ParseUint(f[0], 10, 64)
		t, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx += r
		tx += t
	}

	now := time.Now()
	prev := c.netPrev
	if !prev.at.IsZero() {
		if d := now.Sub(prev.at).Seconds(); d > 0.2 {
			if rx >= prev.rx {
				n.RxBps = float64(rx-prev.rx) / d
			}
			if tx >= prev.tx {
				n.TxBps = float64(tx-prev.tx) / d
			}
		}
	}
	c.netPrev = netSample{rx: rx, tx: tx, at: now}
	return n
}

// hostNetNS 判断当前是否与宿主机共享网络命名空间
func (c *Collector) hostNetNS() bool {
	if c.hostRoot == "" {
		return true
	}
	self, err1 := os.Stat("/proc/self/ns/net")
	host, err2 := os.Stat(c.hostPath("/proc/1/ns/net"))
	if err1 != nil || err2 != nil {
		return false
	}
	return os.SameFile(self, host)
}

/* ================= 磁盘 ================= */

var allowedFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "btrfs": true, "xfs": true,
	"f2fs": true, "zfs": true, "ntfs": true, "ntfs3": true, "exfat": true, "vfat": true,
}

// 各 NAS / 软路由常见存储挂载点前缀：
// 群晖 /volume1..N、绿联 /volume1、飞牛 /vol1、威联通 /share/CACHEDEV1_DATA、
// 极空间 /volume1 或 /media/*、OpenWrt 系 /overlay（根分区可写层）与 /mnt/*
var autoMountPrefix = []string{"/volume", "/vol", "/share", "/mnt", "/media", "/data", "/srv", "/home", "/overlay"}

var skipMountPrefix = []string{"/proc", "/sys", "/dev", "/run", "/tmp", "/var/lib", "/snap", "/boot", "/usr", "/etc"}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

func (c *Collector) collectDisks(custom []string) []DiskInfo {
	type mount struct {
		dev, mp, fstype string
	}
	best := map[string]mount{}

	// /proc/mounts 是当前挂载命名空间的视图，容器内看到的宿主盘会以 /host/vol1 形式出现；
	// /proc/1/mounts 是宿主 PID 1 的挂载表，挂载点即宿主真实路径，优先使用。
	lines := readLines(c.hostPath("/proc/1/mounts"))
	if len(lines) == 0 {
		lines = readLines(c.hostPath("/proc/mounts"))
	}

	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		dev, mp, fs := unescapeMount(f[0]), unescapeMount(f[1]), f[2]
		// 容器内 bind mount 形式（/host/vol1）还原为宿主真实路径
		if c.hostRoot != "" && strings.HasPrefix(mp, c.hostRoot+"/") {
			mp = strings.TrimPrefix(mp, c.hostRoot)
		}
		// ZFS 的挂载源是 pool/dataset 形式，不以 /dev/ 开头
		if !allowedFS[fs] || (!strings.HasPrefix(dev, "/dev/") && fs != "zfs") {
			continue
		}
		skip := false
		for _, p := range skipMountPrefix {
			if mp == p || strings.HasPrefix(mp, p+"/") {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if len(custom) > 0 {
			if !matchAnyPrefix(mp, custom) {
				continue
			}
		} else if !matchAnyPrefix(mp, autoMountPrefix) {
			continue
		}
		// 同一分区可能被多次挂载（bind mount），保留路径最短的挂载点
		if old, ok := best[dev]; !ok || len(mp) < len(old.mp) {
			best[dev] = mount{dev: dev, mp: mp, fstype: fs}
		}
	}

	list := make([]mount, 0, len(best))
	for _, m := range best {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mp < list[j].mp })

	out := make([]DiskInfo, 0, len(list))
	for _, m := range list {
		total, avail, err := diskUsage(c.hostPath(m.mp))
		if err != nil || total == 0 {
			continue
		}
		out = append(out, DiskInfo{Mount: m.mp, FSType: m.fstype, Total: total, Used: total - avail})
		if len(out) >= 8 {
			break
		}
	}
	// 兜底：OpenWrt 系根分区直接挂 ext4/f2fs（无 /overlay 分层）时，用根分区容量补一条
	if len(out) == 0 {
		if total, avail, err := diskUsage(c.hostPath("/")); err == nil && total > 0 {
			out = append(out, DiskInfo{Mount: "/", FSType: "root", Total: total, Used: total - avail})
		}
	}
	return out
}

func matchAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// NAS 存储池是 /volume1、/vol1、/share/CACHEDEV1_DATA 这类前缀命名，
		// 必须按字符串前缀匹配（不能要求分隔符），否则识别不到。
		p = strings.TrimSuffix(p, "/")
		if p != "" && strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

/* ================= 温度 ================= */

func readTemp(thermalDir, hwmonDir string) float64 {
	best := -1.0
	for _, zone := range globDirs(thermalDir, "thermal_zone") {
		if v := parseMilliC(readFileTrim(filepath.Join(zone, "temp"))); v > 0 {
			if best < 0 || v < best {
				best = v
			}
		}
	}
	if best > 0 {
		return best
	}
	for _, hw := range globDirs(hwmonDir, "hwmon") {
		matches, _ := filepath.Glob(filepath.Join(hw, "temp*_input"))
		for _, m := range matches {
			if v := parseMilliC(readFileTrim(m)); v > 0 {
				if best < 0 || v < best {
					best = v
				}
			}
		}
	}
	if best < 0 || best > 125 {
		return -1
	}
	return best
}

func globDirs(dir, prefix string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, prefix+"*"))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.IsDir() {
			out = append(out, m)
		}
	}
	return out
}

func parseMilliC(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return -1
	}
	v /= 1000
	if v <= 0 || v > 125 {
		return -1
	}
	return v
}
