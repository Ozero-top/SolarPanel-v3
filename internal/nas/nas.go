// Package nas 采集宿主 NAS 指标与 Docker 容器状态。
//
// 兼容性说明：容器内 /proc/stat、/proc/meminfo、/proc/cpuinfo 反映的是宿主机全局数据，
// 因此 CPU / 内存 / 核数可直接读取；磁盘、温度、系统发行版需要把宿主机根目录只读挂载到
// NAS_HOST_ROOT（默认 /host）；容器指标通过 Docker socket 获取（各 NAS 均为
// /var/run/docker.sock）。网络指标在非 host 网络模式下只能反映容器自身网卡，会在
// Net.Scope 中标注 container，由前端提示口径。
package nas

import (
	"encoding/json"
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

// TempInfo 单个温度传感器（value = -1 表示不可读）
type TempInfo struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// FanInfo 单个风扇转速
type FanInfo struct {
	Name string  `json:"name"`
	RPM  float64 `json:"rpm"`
}

// HostInfo 宿主机概览
type HostInfo struct {
	Hostname  string     `json:"hostname"`
	OSName    string     `json:"os_name"`
	OSFamily  string     `json:"os_family"`
	Kernel    string     `json:"kernel"`
	Arch      string     `json:"arch"`
	Uptime    int64      `json:"uptime"`
	Cores     int        `json:"cores"`
	Load1     float64    `json:"load1"`
	Load5     float64    `json:"load5"`
	Load15    float64    `json:"load15"`
	CPUPct    float64    `json:"cpu_pct"`
	CPUCores  []float64  `json:"cpu_cores"`
	TempC     float64    `json:"temp_c"`
	TempName  string     `json:"temp_name"`
	Temps     []TempInfo `json:"temps"`
	Fans      []FanInfo  `json:"fans"`
	MemTotal  uint64     `json:"mem_total"`
	MemUsed   uint64     `json:"mem_used"`
	MemCache  uint64     `json:"mem_cache"`
	SwapTotal uint64     `json:"swap_total"`
	SwapUsed  uint64     `json:"swap_used"`
	// IP 宿主机内网地址（优先私网地址）
	IP string `json:"ip"`
	// Virtualized 表示系统运行在虚拟机中（虚拟化平台通常不暴露 CPU 温度传感器）
	Virtualized bool `json:"virtualized"`
}

// DiskInfo 存储池
type DiskInfo struct {
	Dev      string  `json:"dev"`
	Mount    string  `json:"mount"`
	FSType   string  `json:"fstype"`
	Total    uint64  `json:"total"`
	Used     uint64  `json:"used"`
	ReadBps  float64 `json:"read_bps"`
	WriteBps float64 `json:"write_bps"`
	IOPS     float64 `json:"iops"`
	Await    float64 `json:"await"`
}

// DiskIOInfo 物理盘 IO 合计；ops / ioMs 仅用于加权计算 await，不参与 JSON
type DiskIOInfo struct {
	ReadBps  float64 `json:"read_bps"`
	WriteBps float64 `json:"write_bps"`
	IOPS     float64 `json:"iops"`
	Await    float64 `json:"await"`
	ops      uint64
	ioMs     uint64
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
	// RestartCount 容器重启次数；Health 健康检查状态（无健康检查时为空）
	RestartCount int    `json:"restart_count"`
	Health       string `json:"health"`
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
	DiskIO      DiskIOInfo      `json:"disk_io"`
	History     History         `json:"history"`
	HistoryHour History         `json:"history_hour"`
	HistoryDay  History         `json:"history_day"`
	DockerOK    bool            `json:"docker_ok"`
	HostRoot    string          `json:"host_root"`
	GuestMasked bool            `json:"guest_masked"`
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
	rx, tx           uint64
	cpuTotal, sysCPU uint64
	at               time.Time
}

// containerStatsTTL 容器 stats 结果缓存时长；dockerd 单次 stats 需要约 1s，短缓存可显著降低压力
const containerStatsTTL = 5 * time.Second

// statsCacheEntry 单个容器的 stats 结果快照
type statsCacheEntry struct {
	info ContainerInfo
	at   time.Time
}

// diskstatSample /proc/diskstats 累计值快照（不含分区与 dm/md/loop/ram）
type diskstatSample struct {
	reads, writes               uint64
	sectorsRead, sectorsWritten uint64
	msRead, msWrite             uint64
	at                          time.Time
}

// histTier 一档历史环形缓冲
type histTier struct {
	step, max   int
	rx, tx, cpu []float64
}

func (t *histTier) push(cpu, rx, tx float64) {
	t.cpu = appendRing(t.cpu, cpu, t.max)
	t.rx = appendRing(t.rx, rx, t.max)
	t.tx = appendRing(t.tx, tx, t.max)
}

func (t *histTier) snapshot() History {
	return History{
		Interval: t.step,
		Span:     t.step * t.max,
		Rx:       append([]float64{}, t.rx...),
		Tx:       append([]float64{}, t.tx...),
		CPU:      append([]float64{}, t.cpu...),
	}
}

// Collector 带缓存的采集器（前端轮询间隔通常 2~10s，缓存 2~3s 避免重复采集）
type Collector struct {
	hostRoot     string
	dockerSocket string
	stateDir     string

	mu     sync.Mutex
	snap   *Snapshot
	snapAt time.Time

	// sampleMu 保护 cpuPrev / cpuCorePrev / netPrev：主采集与后台历史采样会并发读取 /proc
	sampleMu    sync.Mutex
	cpuPrev     cpuSample
	cpuCorePrev []cpuSample
	netPrev     netSample

	// ctMu 保护 ctPrev：容器 stats 由多个 goroutine 并发写入
	ctMu   sync.Mutex
	ctPrev map[string]ctSample

	// statsMu 保护 statsCache：容器 stats 单次约 1s 且会阻塞 docker daemon，
	// 短缓存避免频繁轮询时把 daemon（宝塔 Docker 页共用）拖慢
	statsMu    sync.Mutex
	statsCache map[string]statsCacheEntry

	// diskPrev 只在 Collect（受 mu 串行）中读写
	diskPrev map[string]diskstatSample

	histMu   sync.Mutex
	hist10   histTier // 5s × 120 = 10 分钟（面板 history 语义不变）
	hist60   histTier // 30s × 120 = 1 小时
	hist24   histTier // 600s × 144 = 24 小时
	histTick int

	// 后台任务按需启停（nas_show=1 或 nas_alert_enable=1 才运行）
	histStop   chan struct{}
	histOn     bool
	histStopMu sync.Mutex
}

func NewCollector(hostRoot, dockerSocket, stateDir string) *Collector {
	c := &Collector{
		hostRoot:     hostRoot,
		dockerSocket: dockerSocket,
		stateDir:     stateDir,
		ctPrev:       make(map[string]ctSample),
		statsCache:   make(map[string]statsCacheEntry),
		diskPrev:     make(map[string]diskstatSample),
		hist10:       histTier{step: 5, max: 120},
		hist60:       histTier{step: 30, max: 120},
		hist24:       histTier{step: 600, max: 144},
	}
	c.loadHistory()
	// 不再无条件启动 historyLoop，由 SetHistoryEnabled 按需控制
	return c
}

// SetHistoryEnabled 按需启停后台历史采样（避免容器启动即常驻 /proc 读取）
func (c *Collector) SetHistoryEnabled(on bool) {
	c.histStopMu.Lock()
	defer c.histStopMu.Unlock()
	if on && !c.histOn {
		c.histStop = make(chan struct{})
		c.histOn = true
		go c.historyLoop(c.histStop)
	} else if !on && c.histOn {
		close(c.histStop)
		c.histOn = false
	}
}

// historyLoop 后台轻量采样（只读 /proc，不触发 Docker 查询），保证曲线在刷新页面后依然完整
func (c *Collector) historyLoop(stop <-chan struct{}) {
	t := time.NewTicker(time.Duration(c.hist10.step) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			c.sampleHistory()
		case <-stop:
			return
		}
	}
}

func (c *Collector) sampleHistory() {
	cpu := c.sampleCPU(c.hostPath("/proc/stat"))
	net := c.collectNet()

	c.histMu.Lock()
	c.hist10.push(cpu, net.RxBps, net.TxBps)
	c.histTick++
	if c.histTick%6 == 0 {
		c.hist60.push(cpu, net.RxBps, net.TxBps)
	}
	if c.histTick%120 == 0 {
		c.hist24.push(cpu, net.RxBps, net.TxBps)
	}
	save := c.histTick%60 == 0 // 60 × 5s = 5 分钟落盘一次
	c.histMu.Unlock()

	if save {
		c.saveHistory()
	}
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
	return c.hist10.snapshot()
}

func (c *Collector) historyHourSnapshot() History {
	c.histMu.Lock()
	defer c.histMu.Unlock()
	return c.hist60.snapshot()
}

func (c *Collector) historyDaySnapshot() History {
	c.histMu.Lock()
	defer c.histMu.Unlock()
	return c.hist24.snapshot()
}

// historyFile 三档历史的落盘格式
type historyFile struct {
	Minute History `json:"minute"`
	Hour   History `json:"hour"`
	Day    History `json:"day"`
}

func (c *Collector) historyPath() string {
	if c.stateDir == "" {
		return ""
	}
	return filepath.Join(c.stateDir, "nas_history.json")
}

// loadHistory 启动时读回落盘历史；缺失或损坏直接忽略
func (c *Collector) loadHistory() {
	p := c.historyPath()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var f historyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return
	}
	c.histMu.Lock()
	defer c.histMu.Unlock()
	c.hist10.rx, c.hist10.tx, c.hist10.cpu = trimRing(f.Minute.Rx, c.hist10.max), trimRing(f.Minute.Tx, c.hist10.max), trimRing(f.Minute.CPU, c.hist10.max)
	c.hist60.rx, c.hist60.tx, c.hist60.cpu = trimRing(f.Hour.Rx, c.hist60.max), trimRing(f.Hour.Tx, c.hist60.max), trimRing(f.Hour.CPU, c.hist60.max)
	c.hist24.rx, c.hist24.tx, c.hist24.cpu = trimRing(f.Day.Rx, c.hist24.max), trimRing(f.Day.Tx, c.hist24.max), trimRing(f.Day.CPU, c.hist24.max)
}

func trimRing(arr []float64, max int) []float64 {
	if len(arr) > max {
		arr = arr[len(arr)-max:]
	}
	return append([]float64{}, arr...)
}

// saveHistory 每 5 分钟落盘一次三档历史，先写临时文件再 rename，避免半截文件
func (c *Collector) saveHistory() {
	p := c.historyPath()
	if p == "" {
		return
	}
	c.histMu.Lock()
	f := historyFile{Minute: c.hist10.snapshot(), Hour: c.hist60.snapshot(), Day: c.hist24.snapshot()}
	c.histMu.Unlock()

	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return
	}
	os.Rename(tmp, p)
}

// Collect 返回快照；ttl 内的重复调用直接返回缓存（ttl 由调用方按刷新间隔传入）
func (c *Collector) Collect(diskPaths []string, ttl time.Duration) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ttl <= 0 {
		ttl = 3 * time.Second
	}
	if c.snap != nil && time.Since(c.snapAt) < ttl {
		cp := *c.snap
		cp.History = c.historySnapshot()
		cp.HistoryHour = c.historyHourSnapshot()
		cp.HistoryDay = c.historyDaySnapshot()
		return cp
	}

	disks, diskIO := c.collectDisks(diskPaths)
	s := Snapshot{
		Host:        c.collectHost(),
		Disks:       disks,
		Net:         c.collectNet(),
		DiskIO:      diskIO,
		History:     c.historySnapshot(),
		HistoryHour: c.historyHourSnapshot(),
		HistoryDay:  c.historyDaySnapshot(),
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
	h := HostInfo{
		Arch: runtime.GOARCH, TempC: -1,
		CPUCores: []float64{}, Temps: []TempInfo{}, Fans: []FanInfo{},
	}

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

	if f := strings.Fields(readFileTrim(c.hostPath("/proc/loadavg"))); len(f) >= 3 {
		h.Load1, _ = strconv.ParseFloat(f[0], 64)
		h.Load5, _ = strconv.ParseFloat(f[1], 64)
		h.Load15, _ = strconv.ParseFloat(f[2], 64)
	}
	if f := strings.Fields(readFileTrim(c.hostPath("/proc/uptime"))); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			h.Uptime = int64(v)
		}
	}

	h.CPUPct = c.sampleCPU(c.hostPath("/proc/stat"))
	h.CPUCores = c.sampleCPUCores(c.hostPath("/proc/stat"))
	c.readMemory(&h)
	h.Virtualized = c.detectVirtualized()
	h.IP = c.detectIP()
	h.TempC, h.TempName, h.Temps = readTemps(c.hostPath("/sys/class/thermal"), c.hostPath("/sys/class/hwmon"))
	h.Fans = readFans(c.hostPath("/sys/class/hwmon"))
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

// sampleCPUCores 用两次 /proc/stat 采样差分计算每核使用率（首次采样返回空数组，最多 32 核）
func (c *Collector) sampleCPUCores(path string) []float64 {
	c.sampleMu.Lock()
	defer c.sampleMu.Unlock()

	var rows []cpuSample
	for _, line := range readLines(path) {
		if !strings.HasPrefix(line, "cpu") || strings.HasPrefix(line, "cpu ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		var total, idle uint64
		ok := true
		for i, v := range f[1:] {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				ok = false
				break
			}
			total += n
			if i == 3 || i == 4 { // idle + iowait
				idle += n
			}
		}
		if !ok || total == 0 {
			continue
		}
		rows = append(rows, cpuSample{total: total, idle: idle})
	}

	prev := c.cpuCorePrev
	c.cpuCorePrev = rows
	if len(prev) == 0 || len(prev) != len(rows) {
		return []float64{} // 首次采样或核数变化，无法差分
	}
	cores := make([]float64, 0, len(rows))
	for i := range rows {
		if len(cores) >= 32 {
			break
		}
		if rows[i].total <= prev[i].total || rows[i].idle < prev[i].idle {
			cores = append(cores, 0)
			continue
		}
		busy := (rows[i].total - prev[i].total) - (rows[i].idle - prev[i].idle)
		v := float64(busy) / float64(rows[i].total-prev[i].total) * 100
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		cores = append(cores, v)
	}
	return cores
}

func (c *Collector) readMemory(h *HostInfo) {
	var total, available, cached, swapTotal, swapFree uint64
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
		case "Cached":
			cached = v
		case "SwapTotal":
			swapTotal = v
		case "SwapFree":
			swapFree = v
		}
	}
	h.MemTotal = total
	h.MemCache = cached
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

// splitDiskPartition 去掉设备名的分区号；isPart 表示原名称是否为分区
func splitDiskPartition(name string) (string, bool) {
	// nvme0n1p1 / mmcblk0p1 / md0p1 / nbd0p1 的分区号在 p 之后
	if strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "mmcblk") ||
		strings.HasPrefix(name, "md") || strings.HasPrefix(name, "nbd") {
		if i := strings.LastIndex(name, "p"); i > 0 {
			if _, err := strconv.Atoi(name[i+1:]); err == nil {
				return name[:i], true
			}
		}
		return name, false
	}
	// sda1 / hda1 / vda1 这类末尾数字即分区号
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if i > 0 && i < len(name) {
		return name[:i], true
	}
	return name, false
}

// deviceName /dev/sda1 → sda；nvme0n1p3 → nvme0n1；非 /dev 源（ZFS）保留原样
func deviceName(dev string) string {
	name := strings.TrimPrefix(dev, "/dev/")
	if name == dev {
		return dev
	}
	base, _ := splitDiskPartition(name)
	return base
}

// skipDiskStats 排除分区与 dm/md/loop/ram，避免与整盘或阵列重复计数
func skipDiskStats(name string) bool {
	for _, p := range []string{"dm-", "md", "loop", "ram"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	_, part := splitDiskPartition(name)
	return part
}

func parseU(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

// collectDiskIO 读取 /proc/diskstats 并与上次采样差分，返回各物理盘的实时速率
func (c *Collector) collectDiskIO() map[string]DiskIOInfo {
	now := time.Now()
	curr := make(map[string]diskstatSample)
	for _, line := range readLines(c.hostPath("/proc/diskstats")) {
		f := strings.Fields(line)
		if len(f) < 11 {
			continue
		}
		name := f[2]
		if skipDiskStats(name) {
			continue
		}
		curr[name] = diskstatSample{
			reads:          parseU(f[3]),
			sectorsRead:    parseU(f[5]),
			msRead:         parseU(f[6]),
			writes:         parseU(f[7]),
			sectorsWritten: parseU(f[9]),
			msWrite:        parseU(f[10]),
			at:             now,
		}
	}

	out := make(map[string]DiskIOInfo, len(curr))
	for name, s := range curr {
		d := DiskIOInfo{}
		if prev, ok := c.diskPrev[name]; ok {
			dt := s.at.Sub(prev.at).Seconds()
			if dt >= 0.2 && s.reads >= prev.reads && s.writes >= prev.writes &&
				s.sectorsRead >= prev.sectorsRead && s.sectorsWritten >= prev.sectorsWritten &&
				s.msRead >= prev.msRead && s.msWrite >= prev.msWrite {
				dr := s.reads - prev.reads
				dw := s.writes - prev.writes
				d.ops = dr + dw
				d.ioMs = (s.msRead - prev.msRead) + (s.msWrite - prev.msWrite)
				d.ReadBps = float64(s.sectorsRead-prev.sectorsRead) * 512 / dt
				d.WriteBps = float64(s.sectorsWritten-prev.sectorsWritten) * 512 / dt
				d.IOPS = float64(d.ops) / dt
				if d.ops > 0 {
					d.Await = float64(d.ioMs) / float64(d.ops)
				}
			}
		}
		out[name] = d
	}
	c.diskPrev = curr
	return out
}

func (c *Collector) collectDisks(custom []string) ([]DiskInfo, DiskIOInfo) {
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

	ioByDev := c.collectDiskIO()

	out := make([]DiskInfo, 0, len(list))
	for _, m := range list {
		total, avail, err := diskUsage(c.hostPath(m.mp))
		if err != nil || total == 0 {
			continue
		}
		d := DiskInfo{Dev: deviceName(m.dev), Mount: m.mp, FSType: m.fstype, Total: total, Used: total - avail}
		if io, ok := ioByDev[d.Dev]; ok {
			d.ReadBps, d.WriteBps, d.IOPS, d.Await = io.ReadBps, io.WriteBps, io.IOPS, io.Await
		}
		out = append(out, d)
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

	var totalIO DiskIOInfo
	for _, d := range ioByDev {
		totalIO.ReadBps += d.ReadBps
		totalIO.WriteBps += d.WriteBps
		totalIO.IOPS += d.IOPS
		totalIO.ops += d.ops
		totalIO.ioMs += d.ioMs
	}
	if totalIO.ops > 0 {
		totalIO.Await = float64(totalIO.ioMs) / float64(totalIO.ops)
	}
	return out, totalIO
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

// cpuTempName 判断传感器名是否属于 CPU 类关键字
func cpuTempName(name string) bool {
	n := strings.ToLower(name)
	for _, k := range []string{"cpu", "coretemp", "k10temp", "soc", "x86_pkg_temp", "package", "tctl", "tdie", "cpu-thermal"} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}

type tempSensor struct {
	name  string
	value float64
	cpu   bool
}

// readTemps 汇总 thermal / hwmon 传感器：CPU 类优先作为主温度，否则取全部最大值。
// 返回主温度、主传感器名称与最多 8 个传感器明细（不可读为 -1）。
func readTemps(thermalDir, hwmonDir string) (float64, string, []TempInfo) {
	var sensors []tempSensor
	for _, zone := range globDirs(thermalDir, "thermal_zone") {
		name := readFileTrim(filepath.Join(zone, "type"))
		if name == "" {
			name = filepath.Base(zone)
		}
		sensors = append(sensors, tempSensor{
			name:  name,
			value: parseMilliC(readFileTrim(filepath.Join(zone, "temp"))),
			cpu:   cpuTempName(name),
		})
	}
	for _, hw := range globDirs(hwmonDir, "hwmon") {
		base := readFileTrim(filepath.Join(hw, "name"))
		matches, _ := filepath.Glob(filepath.Join(hw, "temp*_input"))
		for _, m := range matches {
			name := readFileTrim(strings.TrimSuffix(m, "_input") + "_label")
			if name == "" {
				name = base
			}
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(m), "_input")
			}
			sensors = append(sensors, tempSensor{
				name:  name,
				value: parseMilliC(readFileTrim(m)),
				cpu:   cpuTempName(strings.Join([]string{name, base}, " ")),
			})
		}
	}

	// CPU 类靠前（同类按温度降序），便于前端优先展示 CPU 温度
	sort.SliceStable(sensors, func(i, j int) bool {
		if sensors[i].cpu != sensors[j].cpu {
			return sensors[i].cpu
		}
		return sensors[i].value > sensors[j].value
	})

	temps := make([]TempInfo, 0, 8)
	for _, s := range sensors {
		if len(temps) >= 8 {
			break
		}
		temps = append(temps, TempInfo{Name: s.name, Value: s.value})
	}

	// 主温度：优先第一个可读 CPU 类传感器，未命中则取全部可读传感器最大值（排序后首个可读项）
	for _, s := range sensors {
		if s.cpu && s.value > 0 {
			return s.value, s.name, temps
		}
	}
	for _, s := range sensors {
		if s.value > 0 {
			return s.value, s.name, temps
		}
	}
	return -1, "", temps
}

// readFans 读取 hwmon 风扇转速（fan*_input），最多 8 个
func readFans(hwmonDir string) []FanInfo {
	out := make([]FanInfo, 0, 8)
	for _, hw := range globDirs(hwmonDir, "hwmon") {
		base := readFileTrim(filepath.Join(hw, "name"))
		matches, _ := filepath.Glob(filepath.Join(hw, "fan*_input"))
		for _, m := range matches {
			v, err := strconv.ParseFloat(readFileTrim(m), 64)
			if err != nil || v < 0 {
				continue
			}
			name := readFileTrim(strings.TrimSuffix(m, "_input") + "_label")
			if name == "" {
				name = base
			}
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(m), "_input")
			}
			out = append(out, FanInfo{Name: name, RPM: v})
			if len(out) >= 8 {
				return out
			}
		}
	}
	return out
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
