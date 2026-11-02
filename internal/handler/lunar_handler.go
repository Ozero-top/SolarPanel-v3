package handler

import (
	"container/list"
	"net/http"
	"sync"
	"time"

	"github.com/6tail/lunar-go/calendar"
)

// LunarHandler 农历与宜忌：按自然日缓存，纯本地计算，不依赖外部服务
type LunarHandler struct {
	mu   sync.Mutex
	day  string
	data map[string]any
}

func NewLunarHandler() *LunarHandler {
	return &LunarHandler{}
}

func (h *LunarHandler) Get(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	today := now.Format("2006-01-02")

	h.mu.Lock()
	if h.day == today && h.data != nil {
		data := h.data
		h.mu.Unlock()
		OK(w, data)
		return
	}
	h.mu.Unlock()

	lunar := calendar.NewLunarFromDate(now)
	data := map[string]any{
		"date":       today,
		"lunar":      lunar.GetMonthInChinese() + "月" + lunar.GetDayInChinese(),
		"yearGanZhi": lunar.GetYearInGanZhi(),
		"shengXiao":  lunar.GetYearShengXiao(),
		"week":       "星期" + lunar.GetWeekInChinese(),
		"jieQi":      lunar.GetJieQi(),
		"yi":         listToSlice(lunar.GetDayYi()),
		"ji":         listToSlice(lunar.GetDayJi()),
	}

	h.mu.Lock()
	h.day = today
	h.data = data
	h.mu.Unlock()

	OK(w, data)
}

func listToSlice(l *list.List) []string {
	out := make([]string, 0, l.Len())
	for e := l.Front(); e != nil; e = e.Next() {
		if s, ok := e.Value.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
