package nas

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Docker API 固定使用 v1.41（Docker 20.10 起支持），
// 可同时兼容 DSM / QTS / UGOS / fnOS / ZOS 上的老版本 Docker daemon。
const dockerAPIPrefix = "/v1.41"

func dockerAvailable(socket string) bool {
	if socket == "" {
		return false
	}
	st, err := os.Stat(socket)
	return err == nil && !st.IsDir()
}

func (c *Collector) dockerClient() *http.Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", c.dockerSocket)
		},
		MaxIdleConns:    8,
		IdleConnTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

type dockerPort struct {
	IP          string `json:"IP"`
	PrivatePort uint16 `json:"PrivatePort"`
	PublicPort  uint16 `json:"PublicPort"`
	Type        string `json:"Type"`
}

type dockerContainer struct {
	ID      string       `json:"Id"`
	Names   []string     `json:"Names"`
	Image   string       `json:"Image"`
	State   string       `json:"State"`
	Created int64        `json:"Created"`
	Ports   []dockerPort `json:"Ports"`
}

// formatPorts 只输出对外映射的端口，形如 10086→10086/tcp
func formatPorts(ports []dockerPort) []string {
	out := make([]string, 0, len(ports))
	seen := make(map[string]bool, len(ports))
	for _, p := range ports {
		if p.PublicPort == 0 {
			continue
		}
		s := fmt.Sprintf("%d→%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

type dockerStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			Cache             uint64 `json:"cache"`
			InactiveFile      uint64 `json:"inactive_file"`
			TotalInactiveFile uint64 `json:"total_inactive_file"`
		} `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

func (c *Collector) dockerGet(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.dockerClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("docker api %s: %s", path, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// dockerInspect 容器 inspect 中需要的字段（启动时间、重启次数、健康状态）
type dockerInspect struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		StartedAt string `json:"StartedAt"`
		Health    *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

// collectContainers 拉取容器列表与运行中容器的资源占用
func (c *Collector) collectContainers() []ContainerInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	body, err := c.dockerGet(ctx, dockerAPIPrefix+"/containers/json?all=1")
	if err != nil {
		// 部分 NAS 的 daemon 版本过旧，v1.41 不可用时回退到无版本前缀
		body, err = c.dockerGet(ctx, "/containers/json?all=1")
		if err != nil {
			return nil
		}
	}

	var raw []dockerContainer
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	// 运行中的容器优先，避免容器数量多时被截断丢掉活跃项
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].State == "running" && raw[j].State != "running" })

	now := time.Now().Unix()
	items := make([]ContainerInfo, 0, len(raw))
	for _, r := range raw {
		name := r.ID
		if len(r.Names) > 0 {
			name = strings.TrimPrefix(r.Names[0], "/")
		}
		var up int64
		if r.Created > 0 && now > r.Created {
			up = now - r.Created
		}
		items = append(items, ContainerInfo{
			ID: r.ID, Name: name, Image: r.Image, State: r.State, Uptime: up, Ports: formatPorts(r.Ports),
		})
		if len(items) >= 24 {
			break
		}
	}

	// stats 单次约 1s（daemon 需要两个采样点）。这里刻意串行：
	// dockerd 是所有 Docker API 的单点，并发拉取会直接拖慢同机的宝塔容器页。
	sem := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for i := range items {
		if items[i].State != "running" {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			c.fillContainerStats(ctx, &items[idx])
		}(i)
	}
	wg.Wait()

	// 清理已消失容器的差分状态，避免长期运行内存增长
	alive := make(map[string]ctSample, len(items))
	c.ctMu.Lock()
	for _, it := range items {
		if s, ok := c.ctPrev[it.ID]; ok {
			alive[it.ID] = s
		}
	}
	c.ctPrev = alive
	c.ctMu.Unlock()

	// 同步清理 stats 缓存中已消失/已过期的条目
	present := make(map[string]bool, len(items))
	for _, it := range items {
		present[it.ID] = true
	}
	c.statsMu.Lock()
	for id, e := range c.statsCache {
		if !present[id] || time.Since(e.at) > containerStatsTTL {
			delete(c.statsCache, id)
		}
	}
	c.statsMu.Unlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].State != items[j].State {
			return items[i].State == "running"
		}
		return items[i].CPUPct > items[j].CPUPct
	})
	return items
}

func (c *Collector) fillContainerStats(ctx context.Context, it *ContainerInfo) {
	// 命中短缓存直接复用，避免频繁轮询反复阻塞 dockerd
	c.statsMu.Lock()
	if e, ok := c.statsCache[it.ID]; ok && time.Since(e.at) < containerStatsTTL && e.info.State == it.State {
		cached := e.info
		c.statsMu.Unlock()
		it.CPUPct = cached.CPUPct
		it.MemUsed = cached.MemUsed
		it.MemLimit = cached.MemLimit
		it.RxBps = cached.RxBps
		it.TxBps = cached.TxBps
		it.RestartCount = cached.RestartCount
		it.Health = cached.Health
		it.Uptime = cached.Uptime
		return
	}
	c.statsMu.Unlock()

	// inspect 先拿真实启动时间（容器重启后 Created 不变）、重启次数与健康状态
	if body, err := c.dockerGet(ctx, dockerAPIPrefix+"/containers/"+it.ID+"/json"); err == nil {
		var ins dockerInspect
		if json.Unmarshal(body, &ins) == nil {
			it.RestartCount = ins.RestartCount
			if ins.State.Health != nil {
				it.Health = ins.State.Health.Status
			}
			if t, err := time.Parse(time.RFC3339Nano, ins.State.StartedAt); err == nil {
				if up := time.Now().Unix() - t.Unix(); up > 0 {
					it.Uptime = up
				}
			}
		}
	}

	body, err := c.dockerGet(ctx, dockerAPIPrefix+"/containers/"+it.ID+"/stats?stream=false")
	if err != nil {
		return
	}
	var st dockerStats
	if err := json.Unmarshal(body, &st); err != nil {
		return
	}

	// CPU：两次采集的累计值差分。新版 Docker 的非流式 stats 里 precpu_stats
	// 往往与当前值相同（差分为 0），因此不再依赖它，统一用本地上一次样本。
	cpus := st.CPUStats.OnlineCPUs
	if cpus == 0 {
		cpus = 1
	}

	// 内存：扣除 page cache，limit 超过 1PB 视为未限制
	used := st.MemoryStats.Usage
	cache := st.MemoryStats.Stats.InactiveFile
	if cache == 0 {
		cache = st.MemoryStats.Stats.TotalInactiveFile
	}
	if cache == 0 {
		cache = st.MemoryStats.Stats.Cache
	}
	if cache < used {
		used -= cache
	}
	it.MemUsed = used
	if st.MemoryStats.Limit > 0 && st.MemoryStats.Limit < 1<<60 {
		it.MemLimit = st.MemoryStats.Limit
	}

	// 网络：stats 只给累计值，用两次采集差分
	var rx, tx uint64
	for _, n := range st.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}
	now := time.Now()
	curCPU := st.CPUStats.CPUUsage.TotalUsage
	curSys := st.CPUStats.SystemUsage
	c.ctMu.Lock()
	prev, ok := c.ctPrev[it.ID]
	c.ctMu.Unlock()
	if ok && !prev.at.IsZero() {
		if curSys > prev.sysCPU && curCPU >= prev.cpuTotal {
			it.CPUPct = float64(curCPU-prev.cpuTotal) / float64(curSys-prev.sysCPU) * float64(cpus) * 100
		}
		if d := now.Sub(prev.at).Seconds(); d > 0.2 {
			if rx >= prev.rx {
				it.RxBps = float64(rx-prev.rx) / d
			}
			if tx >= prev.tx {
				it.TxBps = float64(tx-prev.tx) / d
			}
		}
	}
	c.ctMu.Lock()
	c.ctPrev[it.ID] = ctSample{rx: rx, tx: tx, cpuTotal: curCPU, sysCPU: curSys, at: now}
	c.ctMu.Unlock()

	// 写入短缓存，后续 5 秒内的轮询直接复用
	c.statsMu.Lock()
	c.statsCache[it.ID] = statsCacheEntry{info: *it, at: now}
	c.statsMu.Unlock()
}
