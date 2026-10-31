package handler

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"solarpanel/internal/settings"
)

// Sun Panel 导出结构（只保留转换需要的字段）。
// 顶层可能是 {appName, icons}，也可能是 {code, data:{appName, icons}}。
type sunPanelBackup struct {
	AppName string          `json:"appName"`
	Icons   []sunPanelNode  `json:"icons"`
	Data    *sunPanelBackup `json:"data"`
}

type sunPanelNode struct {
	Title           string         `json:"title"`
	Sort            int            `json:"sort"`
	URL             string         `json:"url"`
	LanURL          string         `json:"lanUrl"`
	Description     string         `json:"description"`
	OpenMethod      int            `json:"openMethod"`
	BackgroundColor string         `json:"backgroundColor"`
	Icon            sunPanelIcon   `json:"icon"`
	Children        []sunPanelNode `json:"children"`
}

type sunPanelIcon struct {
	Src             string `json:"src"`
	Text            string `json:"text"`
	BackgroundColor string `json:"backgroundColor"`
}

// parseUploadedBackup 解析上传内容：Sun Panel 导出自动转换，其余按 SolarPanel 备份解析。
func parseUploadedBackup(raw []byte) (backupPayload, bool, error) {
	var doc sunPanelBackup
	if err := json.Unmarshal(raw, &doc); err == nil {
		appName := strings.ToLower(doc.AppName)
		if doc.Data != nil {
			if doc.Data.AppName != "" {
				appName = strings.ToLower(doc.Data.AppName)
			}
			if len(doc.Data.Icons) > 0 {
				doc = *doc.Data
			}
		}
		// 空分组导出也应该转换，否则会被当成空备份把现有数据清空
		if len(doc.Icons) > 0 || strings.Contains(appName, "sun-panel") || strings.Contains(appName, "sunpanel") {
			return convertSunPanelBackup(doc), true, nil
		}
	}
	var payload backupPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, false, err
	}
	return payload, false, nil
}

// convertSunPanelBackup 把 Sun Panel 的分组/卡片转成 SolarPanel 备份结构。
// 设置项使用默认值（导入本身是整体替换，站点外观需导入后再调）。
func convertSunPanelBackup(doc sunPanelBackup) backupPayload {
	now := time.Now().Format("2006-01-02 15:04:05")
	payload := backupPayload{Version: "1.0", Time: now, Files: map[string]string{}}

	defaults := settings.Defaults()
	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		payload.Settings = append(payload.Settings, SettingRow{ID: i + 1, ConfigName: k, ConfigValue: defaults[k]})
	}

	gid, iid := 0, 0
	var walk func(node sunPanelNode, parentID int)
	walk = func(node sunPanelNode, parentID int) {
		gid++
		myID := gid
		title := strings.TrimSpace(node.Title)
		if title == "" {
			title = "未命名分组"
		}
		payload.Groups = append(payload.Groups, ItemGroupRow{
			ID:        myID,
			ParentID:  parentID,
			Title:     title,
			Sort:      sortOr(node.Sort, myID),
			IsVisible: 1,
			UserID:    1,
			CreatedAt: now,
		})
		for _, child := range node.Children {
			// 极少数版本支持分组嵌套：有子节点且自身没有链接的，按子分组处理
			if len(child.Children) > 0 && strings.TrimSpace(child.URL) == "" && strings.TrimSpace(child.LanURL) == "" {
				walk(child, myID)
				continue
			}
			url := strings.TrimSpace(child.URL)
			lan := strings.TrimSpace(child.LanURL)
			if url == "" && lan == "" {
				continue
			}
			iid++
			iconType, iconValue, iconBG := sunPanelIconFields(child)
			openMethod := 1
			if child.OpenMethod == 2 || child.OpenMethod == 3 {
				openMethod = child.OpenMethod
			}
			payload.Items = append(payload.Items, ItemRow{
				ID:          iid,
				GroupID:     myID,
				Title:       strings.TrimSpace(child.Title),
				URL:         url,
				LanURL:      lan,
				Description: strings.TrimSpace(child.Description),
				IconType:    iconType,
				IconValue:   iconValue,
				IconBG:      iconBG,
				OpenMethod:  openMethod,
				Sort:        sortOr(child.Sort, iid),
				IsHidden:    0,
				UserID:      1,
				CreatedAt:   now,
			})
		}
	}

	for _, node := range doc.Icons {
		walk(node, 0)
	}
	return payload
}

// sunPanelIconFields 决定卡片图标：网络图片沿用，相对路径（旧站上传目录）与
// 空图标统一回退 favicon 自动抓取，有文字图标则优先文字。
func sunPanelIconFields(node sunPanelNode) (string, string, string) {
	bg := strings.TrimSpace(node.BackgroundColor)
	if bg == "" {
		bg = strings.TrimSpace(node.Icon.BackgroundColor)
	}
	src := strings.TrimSpace(node.Icon.Src)
	text := strings.TrimSpace(node.Icon.Text)

	if src != "" && isRemoteIconSrc(src) {
		return "image", src, bg
	}
	if text != "" {
		return "text", text, bg
	}
	if strings.TrimSpace(node.URL) != "" || strings.TrimSpace(node.LanURL) != "" {
		return "favicon", "", bg
	}
	title := strings.TrimSpace(node.Title)
	if title != "" {
		return "text", string([]rune(title)[0]), bg
	}
	return "text", "?", bg
}

func isRemoteIconSrc(src string) bool {
	lower := strings.ToLower(src)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(src, "//") || strings.HasPrefix(lower, "data:image/")
}

func sortOr(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
