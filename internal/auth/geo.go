package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 归属地缓存：同一 IP 24 小时内只查一次，避免审计写入频繁打外部接口
type geoCacheEntry struct {
	region string
	at     time.Time
}

var geoRegionCache sync.Map

const geoRegionTTL = 24 * time.Hour

// RegionForIP 查询 IP 归属地（国家 + 省 + 市）。
// 内网/回环返回「内网/本地」，查询失败返回空串（不阻塞调用方）。
func RegionForIP(ipStr string) string {
	ipStr = strings.TrimSpace(ipStr)
	if ipStr == "" {
		return ""
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if isPrivateAddr(ip) {
		return "内网/本地"
	}
	if v, ok := geoRegionCache.Load(ipStr); ok {
		if e, ok := v.(geoCacheEntry); ok && time.Since(e.at) < geoRegionTTL {
			return e.region
		}
	}
	region := lookupRegion(ipStr)
	geoRegionCache.Store(ipStr, geoCacheEntry{region: region, at: time.Now()})
	return region
}

type ipapiResp struct {
	Status     string `json:"status"`
	Country    string `json:"country"`
	RegionName string `json:"regionName"`
	City       string `json:"city"`
}

type ipapiCoResp struct {
	CountryName string `json:"country_name"`
	Region      string `json:"region"`
	City        string `json:"city"`
}

func lookupRegion(ip string) string {
	client := &http.Client{Timeout: 3 * time.Second}

	if body, err := httpGetBody(client, "http://ip-api.com/json/"+ip+"?lang=zh-CN&fields=status,country,regionName,city"); err == nil {
		var r ipapiResp
		if json.Unmarshal(body, &r) == nil && r.Status == "success" {
			if s := joinRegion(r.Country, r.RegionName, r.City); s != "" {
				return s
			}
		}
	}

	if body, err := httpGetBody(client, "https://ipapi.co/"+ip+"/json/"); err == nil {
		var r ipapiCoResp
		if json.Unmarshal(body, &r) == nil {
			if s := joinRegion(r.CountryName, r.Region, r.City); s != "" {
				return s
			}
		}
	}
	return ""
}

func httpGetBody(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

// joinRegion 拼接「国家 · 省 · 市」，自动去重与去空
func joinRegion(parts ...string) string {
	seen := make(map[string]bool, len(parts))
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, " · ")
}
