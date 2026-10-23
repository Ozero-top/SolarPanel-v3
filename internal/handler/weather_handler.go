package handler

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/config"
)

type WeatherHandler struct {
	cfg *config.Config

	flightMu sync.Mutex
	flights  map[string]*weatherFlight
}

// weatherFlight 同一缓存键的并发请求合并为一次上游拉取，避免刷新风暴触发限流与偶发失败
type weatherFlight struct {
	done chan struct{}
	data map[string]any
	msg  string
}

func NewWeatherHandler(cfg *config.Config) *WeatherHandler {
	return &WeatherHandler{cfg: cfg, flights: map[string]*weatherFlight{}}
}

var wmoCodeMap = map[int][2]string{
	0: {"晴", "☀️"}, 1: {"大致晴朗", "🌤️"}, 2: {"多云", "⛅"}, 3: {"阴", "☁️"},
	45: {"雾", "🌫️"}, 48: {"雾凇", "🌫️"},
	51: {"小毛毛雨", "🌦️"}, 53: {"毛毛雨", "🌦️"}, 55: {"浓毛毛雨", "🌧️"},
	56: {"冻毛毛雨", "🌧️"}, 57: {"冻毛毛雨", "🌧️"},
	61: {"小雨", "🌦️"}, 63: {"中雨", "🌧️"}, 65: {"大雨", "🌧️"},
	66: {"冻雨", "🌧️"}, 67: {"冻雨", "🌧️"},
	71: {"小雪", "🌨️"}, 73: {"中雪", "🌨️"}, 75: {"大雪", "❄️"}, 77: {"雪粒", "🌨️"},
	80: {"小阵雨", "🌦️"}, 81: {"阵雨", "🌦️"}, 82: {"强阵雨", "⛈️"},
	85: {"阵雪", "🌨️"}, 86: {"强阵雪", "❄️"},
	95: {"雷阵雨", "⛈️"}, 96: {"雷阵雨伴冰雹", "⛈️"}, 99: {"强雷暴伴冰雹", "⛈️"},
}

var wttrCodeMap = map[int][2]string{
	113: {"晴", "☀️"}, 116: {"多云", "⛅"}, 119: {"阴", "☁️"}, 122: {"阴", "☁️"},
	143: {"薄雾", "🌫️"}, 145: {"雾凇", "🌫️"}, 149: {"霾", "🌫️"},
	153: {"浓雾凇", "🌫️"}, 154: {"薄雾凇", "🌫️"},
	176: {"零星小雨", "🌦️"}, 179: {"零星小雪", "🌨️"}, 182: {"雨夹雪", "🌨️"},
	185: {"冻毛毛雨", "🌧️"}, 200: {"雷阵雨", "⛈️"}, 227: {"吹雪", "🌨️"}, 230: {"暴风雪", "❄️"},
	248: {"雾", "🌫️"}, 260: {"冻雾", "🌫️"},
	263: {"零星毛毛雨", "🌦️"}, 266: {"毛毛雨", "🌦️"}, 281: {"冻毛毛雨", "🌧️"}, 284: {"强冻毛毛雨", "🌧️"},
	293: {"零星小雨", "🌦️"}, 296: {"小雨", "🌦️"}, 299: {"间歇性中雨", "🌧️"}, 302: {"中雨", "🌧️"},
	305: {"间歇性大雨", "🌧️"}, 308: {"大雨", "🌧️"},
	311: {"小冻雨", "🌧️"}, 314: {"强冻雨", "🌧️"}, 317: {"小雨夹雪", "🌨️"}, 320: {"强雨夹雪", "🌨️"},
	323: {"零星小雪", "🌨️"}, 326: {"小雪", "🌨️"}, 329: {"间歇性中雪", "🌨️"}, 332: {"中雪", "🌨️"},
	335: {"间歇性大雪", "❄️"}, 338: {"大雪", "❄️"},
	350: {"冰粒", "🌨️"}, 353: {"小阵雨", "🌦️"}, 356: {"强阵雨", "🌧️"}, 359: {"暴雨", "⛈️"},
	362: {"小阵雨夹雪", "🌨️"}, 365: {"强阵雨夹雪", "🌨️"},
	368: {"小阵雪", "🌨️"}, 371: {"强阵雪", "❄️"}, 374: {"小冰粒阵雨", "🌨️"}, 377: {"强冰粒阵雨", "🌨️"},
	386: {"雷阵雨", "⛈️"}, 389: {"强雷阵雨", "⛈️"}, 392: {"雷阵雪", "⛈️"}, 395: {"强雷阵雪", "⛈️"},
}

func wmoMap(code int) (string, string) {
	if v, ok := wmoCodeMap[code]; ok {
		return v[0], v[1]
	}
	return "未知", "🌡️"
}
func wttrMap(code int) (string, string) {
	if v, ok := wttrCodeMap[code]; ok {
		return v[0], v[1]
	}
	return wmoMap(code)
}

type geoResult struct {
	Lat  float64
	Lon  float64
	City string
}
type weatherResult struct {
	Temp     int
	Desc     string
	Emoji    string
	Humidity int
	Wind     float64
	City     string
}

func (h *WeatherHandler) cacheDir() string {
	return filepath.Join(h.cfg.UploadDir, "weather")
}
func (h *WeatherHandler) cacheKey(prefix, key string) string {
	hash := md5.Sum([]byte(prefix + ":" + key))
	return filepath.Join(h.cacheDir(), hex.EncodeToString(hash[:])+".json")
}
// readCache 读取磁盘缓存；ttl<=0 表示不校验过期（用于上游故障时回退陈旧数据）
func (h *WeatherHandler) readCache(prefix, key string, ttl time.Duration, requireKey string) (map[string]any, bool) {
	p := h.cacheKey(prefix, key)
	info, err := os.Stat(p)
	if err != nil {
		return nil, false
	}
	if ttl > 0 && time.Since(info.ModTime()) > ttl {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var cached map[string]any
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, false
	}
	if requireKey != "" {
		if _, ok := cached[requireKey]; !ok {
			return nil, false
		}
	}
	cached["cached"] = true
	return cached, true
}

func (h *WeatherHandler) writeCache(prefix, key string, data map[string]any) {
	cd := h.cacheDir()
	if err := os.MkdirAll(cd, 0755); err != nil {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	p := h.cacheKey(prefix, key)
	// 先写临时文件再 rename：避免并发读到半截 JSON 被判为缓存失效，造成天气偶发不显示
	tmp, err := os.CreateTemp(cd, "tmp-*.json")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	tmp.Close()
	if err := os.Rename(tmpName, p); err != nil {
		os.Remove(tmpName)
		return
	}
	h.trimCache(cd, 300)
}
func (h *WeatherHandler) trimCache(dir string, max int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fi struct {
		name    string
		modTime time.Time
	}
	var files []fi
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fi{name: e.Name(), modTime: info.ModTime()})
	}
	if len(files) <= max {
		return
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})
	for i := 0; i < len(files)-max; i++ {
		os.Remove(filepath.Join(dir, files[i].name))
	}
}

func (h *WeatherHandler) httpFetch(u string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "SolarPanel-Go/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

func isPublicIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	// 复用 items_handler.go 中预解析的内网网段，避免每次调用都 ParseCIDR
	return !isPrivateIP(ip)
}

func (h *WeatherHandler) clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		for _, ip := range strings.Split(fwd, ",") {
			ip = strings.TrimSpace(ip)
			if net.ParseIP(ip) != nil && isPublicIP(ip) {
				return ip
			}
		}
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" && net.ParseIP(realIP) != nil {
		return realIP
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	if net.ParseIP(host) != nil {
		return host
	}
	return ""
}

func (h *WeatherHandler) locateByCity(city string) *geoResult {
	u := fmt.Sprintf("https://geocoding-api.open-meteo.com/v1/search?name=%s&count=1&language=zh&format=json", url.QueryEscape(city))
	body, err := h.httpFetch(u, 6*time.Second)
	if err == nil {
		var r struct {
			Results []struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
				Name      string  `json:"name"`
			} `json:"results"`
		}
		if json.Unmarshal(body, &r) == nil && len(r.Results) > 0 {
			return &geoResult{Lat: r.Results[0].Latitude, Lon: r.Results[0].Longitude, City: r.Results[0].Name}
		}
	}
	wttrURL := fmt.Sprintf("https://wttr.in/%s?format=j1", url.QueryEscape(city))
	body, err = h.httpFetch(wttrURL, 6*time.Second)
	if err == nil {
		var wt struct {
			NearestArea []struct {
				Latitude  string `json:"latitude"`
				Longitude string `json:"longitude"`
			} `json:"nearest_area"`
		}
		if json.Unmarshal(body, &wt) == nil && len(wt.NearestArea) > 0 {
			lat, _ := strconv.ParseFloat(wt.NearestArea[0].Latitude, 64)
			lon, _ := strconv.ParseFloat(wt.NearestArea[0].Longitude, 64)
			return &geoResult{Lat: lat, Lon: lon, City: city}
		}
	}
	return nil
}

func (h *WeatherHandler) locateByIP(ip string) *geoResult {
	u := fmt.Sprintf("http://ip-api.com/json/%s?lang=zh-CN&fields=status,city,regionName,lat,lon", url.QueryEscape(ip))
	body, err := h.httpFetch(u, 5*time.Second)
	if err == nil {
		var j struct {
			Status     string  `json:"status"`
			City       string  `json:"city"`
			RegionName string  `json:"regionName"`
			Lat        float64 `json:"lat"`
			Lon        float64 `json:"lon"`
		}
		if json.Unmarshal(body, &j) == nil && j.Status == "success" {
			city := j.City
			if city == "" {
				city = j.RegionName
			}
			return &geoResult{Lat: j.Lat, Lon: j.Lon, City: city}
		}
	}
	body, err = h.httpFetch("https://ipapi.co/"+url.QueryEscape(ip)+"/json/", 5*time.Second)
	if err == nil {
		var j struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			City      string  `json:"city"`
		}
		if json.Unmarshal(body, &j) == nil && j.Latitude != 0 {
			return &geoResult{Lat: j.Latitude, Lon: j.Longitude, City: j.City}
		}
	}
	return nil
}

func (h *WeatherHandler) locateByServerIP() *geoResult {
	body, err := h.httpFetch("http://ip-api.com/json/?lang=zh-CN&fields=status,city,regionName,lat,lon", 5*time.Second)
	if err == nil {
		var j struct {
			Status     string  `json:"status"`
			City       string  `json:"city"`
			RegionName string  `json:"regionName"`
			Lat        float64 `json:"lat"`
			Lon        float64 `json:"lon"`
		}
		if json.Unmarshal(body, &j) == nil && j.Status == "success" {
			city := j.City
			if city == "" {
				city = j.RegionName
			}
			return &geoResult{Lat: j.Lat, Lon: j.Lon, City: city}
		}
	}
	return nil
}

func (h *WeatherHandler) providerOpenMeteo(lat, lon float64) *weatherResult {
	u := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,relative_humidity_2m,weather_code,wind_speed_10m,is_day&timezone=auto", lat, lon)
	body, err := h.httpFetch(u, 6*time.Second)
	if err != nil {
		return nil
	}
	var j struct {
		Error   bool `json:"error"`
		Current struct {
			TempC       float64 `json:"temperature_2m"`
			Humidity    int     `json:"relative_humidity_2m"`
			WeatherCode int     `json:"weather_code"`
			WindSpeed   float64 `json:"wind_speed_10m"`
		} `json:"current"`
	}
	if json.Unmarshal(body, &j) != nil || j.Error {
		return nil
	}
	desc, emoji := wmoMap(j.Current.WeatherCode)
	return &weatherResult{
		Temp: int(round(j.Current.TempC)), Desc: desc, Emoji: emoji,
		Humidity: j.Current.Humidity, Wind: round1(j.Current.WindSpeed),
	}
}

func (h *WeatherHandler) providerWttr(query string) *weatherResult {
	u := "https://wttr.in/" + url.QueryEscape(query) + "?format=j1"
	body, err := h.httpFetch(u, 8*time.Second)
	if err != nil {
		return nil
	}
	var j struct {
		CurrentCondition []struct {
			TempC         string `json:"temp_C"`
			Humidity      string `json:"humidity"`
			WindspeedKmph string `json:"windspeedKmph"`
			WeatherCode   string `json:"weatherCode"`
		} `json:"current_condition"`
		NearestArea []struct {
			AreaName []struct {
				Value string `json:"value"`
			} `json:"areaName"`
		} `json:"nearest_area"`
	}
	if json.Unmarshal(body, &j) != nil || len(j.CurrentCondition) == 0 {
		return nil
	}
	cc := j.CurrentCondition[0]
	code, _ := strconv.Atoi(cc.WeatherCode)
	desc, emoji := wttrMap(code)
	temp, _ := strconv.ParseFloat(cc.TempC, 64)
	hum, _ := strconv.Atoi(cc.Humidity)
	wind, _ := strconv.ParseFloat(cc.WindspeedKmph, 64)
	var cityName string
	if len(j.NearestArea) > 0 && len(j.NearestArea[0].AreaName) > 0 {
		cityName = j.NearestArea[0].AreaName[0].Value
	}
	return &weatherResult{Temp: int(round(temp)), Desc: desc, Emoji: emoji, Humidity: hum, Wind: round1(wind), City: cityName}
}

func round(f float64) float64 {
	if f < 0 {
		return float64(int(f - 0.5))
	}
	return float64(int(f + 0.5))
}
func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func (h *WeatherHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	h.Get(w, r)
}

// resolveLocation 定位结果单独缓存（6 小时），减少上游定位服务抖动导致的整块天气失败
func (h *WeatherHandler) resolveLocation(city, ip string) *geoResult {
	key := "server"
	switch {
	case city != "":
		key = "city:" + city
	case isPublicIP(ip):
		key = "ip:" + ip
	}

	fromCache := func(c map[string]any) *geoResult {
		lat, _ := c["lat"].(float64)
		lon, _ := c["lon"].(float64)
		name, _ := c["city"].(string)
		return &geoResult{Lat: lat, Lon: lon, City: name}
	}

	if c, ok := h.readCache("geo2", key, 6*time.Hour, "lat"); ok {
		return fromCache(c)
	}

	var loc *geoResult
	if city != "" {
		loc = h.locateByCity(city)
	} else {
		if isPublicIP(ip) {
			loc = h.locateByIP(ip)
		}
		if loc == nil {
			loc = h.locateByServerIP()
		}
	}
	if loc == nil {
		// 上游定位失败时回退陈旧定位缓存，天气数据仍可刷新
		if c, ok := h.readCache("geo2", key, 0, "lat"); ok {
			return fromCache(c)
		}
		return nil
	}
	h.writeCache("geo2", key, map[string]any{"lat": loc.Lat, "lon": loc.Lon, "city": loc.City})
	return loc
}

// runFlight 同一 key 的并发请求只执行一次 fn，其余请求等待并复用结果
func (h *WeatherHandler) runFlight(key string, fn func() (map[string]any, string)) (map[string]any, string) {
	h.flightMu.Lock()
	if f, ok := h.flights[key]; ok {
		h.flightMu.Unlock()
		<-f.done
		return f.data, f.msg
	}
	f := &weatherFlight{done: make(chan struct{})}
	h.flights[key] = f
	h.flightMu.Unlock()

	f.data, f.msg = fn()
	close(f.done)

	h.flightMu.Lock()
	delete(h.flights, key)
	h.flightMu.Unlock()
	return f.data, f.msg
}

func (h *WeatherHandler) Get(w http.ResponseWriter, r *http.Request) {
	city := strings.TrimSpace(r.URL.Query().Get("city"))
	ip := h.clientIP(r)

	var cachePrefix, cacheKeyStr string
	if city != "" {
		cachePrefix = "city3"
		cacheKeyStr = city
	} else {
		cachePrefix = "ip3"
		// 私网 / 无法识别的 IP（Docker 反代直连）统一按服务器定位缓存，
		// 避免所有访客共用同一个空 key 互相覆盖、也避免反复触发上游定位
		cacheKeyStr = ip
		if !isPublicIP(ip) {
			cacheKeyStr = "server"
		}
	}

	if cached, ok := h.readCache(cachePrefix, cacheKeyStr, 30*time.Minute, "temp"); ok {
		OK(w, cached)
		return
	}

	data, msg := h.runFlight(cachePrefix+":"+cacheKeyStr, func() (map[string]any, string) {
		if cached, ok := h.readCache(cachePrefix, cacheKeyStr, 30*time.Minute, "temp"); ok {
			return cached, ""
		}

		loc := h.resolveLocation(city, ip)
		if loc == nil {
			if city != "" {
				return nil, fmt.Sprintf("城市「%s」定位失败：名称可能有误，或定位服务暂不可达", city)
			}
			return nil, "定位失败：无法确定地区，可在后台手动指定城市"
		}

		wr := h.providerOpenMeteo(loc.Lat, loc.Lon)
		if wr == nil {
			var wttrQuery string
			if city != "" {
				wttrQuery = city
			} else {
				wttrQuery = fmt.Sprintf("%.4f,%.4f", loc.Lat, loc.Lon)
			}
			wr = h.providerWttr(wttrQuery)
			if wr != nil && loc.City != "" {
				wr.City = loc.City
			}
		}
		if wr == nil {
			return nil, "天气数据获取失败（天气服务暂不可用或服务器网络受限），请稍后重试"
		}

		cityName := wr.City
		if cityName == "" {
			if loc.City != "" {
				cityName = loc.City
			} else if city != "" {
				cityName = city
			} else {
				cityName = "未知地区"
			}
		}

		d := map[string]any{
			"city": cityName, "temp": wr.Temp, "desc": wr.Desc, "emoji": wr.Emoji,
			"humidity": wr.Humidity, "wind": wr.Wind, "cached": false,
		}
		h.writeCache(cachePrefix, cacheKeyStr, d)
		return d, ""
	})
	if data != nil {
		OK(w, data)
		return
	}

	// 上游整体不可用时回退过期缓存：宁可展示旧天气，也不让天气模块整块消失
	if stale, ok := h.readCache(cachePrefix, cacheKeyStr, 0, "temp"); ok {
		OK(w, stale)
		return
	}
	Fail(w, msg)
}
