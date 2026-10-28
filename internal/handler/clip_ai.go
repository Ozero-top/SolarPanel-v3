package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

const (
	clipAIMaxInput   = 6000
	clipAIMaxSummary = 300
	clipAIMaxTags    = 6
)

// clipImageDomains 读取快照图片域名白名单（空 = 不限制）
func clipImageDomains() []string {
	raw := strings.TrimSpace(db.GetSetting("clip_image_domains"))
	out := []string{}
	if raw == "" {
		return out
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || unicode.IsSpace(r)
	})
	for _, f := range fields {
		d := strings.ToLower(strings.TrimSpace(f))
		if d == "" {
			continue
		}
		// 允许直接粘贴完整 URL，这里只取主机名
		if strings.Contains(d, "://") {
			if u, err := url.Parse(d); err == nil && u.Hostname() != "" {
				d = u.Hostname()
			}
		}
		d = strings.TrimPrefix(d, "*.")
		d = strings.Trim(d, "./")
		if d == "" || len(d) > 100 {
			continue
		}
		out = append(out, d)
		if len(out) >= 50 {
			break
		}
	}
	return out
}

// enrichClipSnapshot 为快照生成摘要与自动标签：默认本地规则，配置外部 API 时优先调用，失败回退本地
func enrichClipSnapshot(itemID int, title string, art *clipArticle) {
	if db.DB == nil || art == nil || itemID <= 0 {
		return
	}
	if db.GetSetting("clip_ai_enabled") == "0" {
		return
	}
	plain := html.UnescapeString(clipPlainText(art.Markdown))
	if plain == "" {
		return
	}

	summary, tags := "", []string{}
	if strings.EqualFold(strings.TrimSpace(db.GetSetting("clip_ai_provider")), "api") {
		baseURL := strings.TrimSpace(db.GetSetting("clip_ai_base_url"))
		apiKey := strings.TrimSpace(db.GetSetting("clip_ai_api_key"))
		model := strings.TrimSpace(db.GetSetting("clip_ai_model"))
		if baseURL != "" && apiKey != "" {
			if s, t, err := callClipAI(baseURL, apiKey, model, title, plain); err == nil {
				summary, tags = s, t
			}
		}
	}
	if strings.TrimSpace(summary) == "" {
		summary = clipLocalSummary(plain, art.Excerpt)
	}
	if len(tags) == 0 {
		tags = clipLocalTags(title, plain)
	}
	if summary == "" && len(tags) == 0 {
		return
	}
	tags = normalizeTags(tags)
	if len(tags) > clipAIMaxTags {
		tags = tags[:clipAIMaxTags]
	}
	db.DB.Exec(`UPDATE clip_contents SET summary = ?, auto_tags = ? WHERE item_id = ?`,
		trimRunes(summary, clipAIMaxSummary), strings.Join(tags, ","), itemID)
}

/* ---------- 本地规则：词频 + 抽取式摘要 ---------- */

var (
	clipSentenceSplit = regexp.MustCompile(`[。！？!?；;.\n]+`)
	clipURLNoise      = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s]+`)
	clipStopChars     = map[rune]bool{
		'的': true, '了': true, '是': true, '在': true, '和': true, '就': true,
		'都': true, '而': true, '及': true, '与': true, '这': true, '那': true,
		'有': true, '我': true, '你': true, '他': true, '她': true, '它': true,
		'们': true, '也': true, '很': true, '但': true, '把': true, '被': true,
		'对': true, '从': true, '到': true, '为': true, '以': true, '于': true,
		'上': true, '下': true, '中': true, '个': true, '一': true, '不': true,
		'着': true, '过': true, '地': true, '得': true, '之': true, '其': true,
	}
	clipStopWords = map[string]bool{
		"the": true, "and": true, "for": true, "that": true, "this": true, "with": true,
		"from": true, "have": true, "has": true, "are": true, "was": true, "were": true,
		"but": true, "not": true, "you": true, "your": true, "they": true, "will": true,
		"can": true, "all": true, "our": true, "out": true, "use": true, "how": true,
		"what": true, "when": true, "which": true, "their": true, "more": true,
		"www": true, "http": true, "https": true, "html": true, "org": true,
		"com": true, "index": true, "page": true, "here": true, "also": true,
	}
)

// clipTermFreq 统计词频：中文取 2 字窗口，英文数字取长度 >= 3 的词
func clipTermFreq(text string) map[string]int {
	freq := map[string]int{}
	var han []rune
	var word []rune
	flushHan := func() {
		if len(han) >= 2 {
			for i := 0; i+1 < len(han); i++ {
				bigram := string(han[i : i+2])
				if clipStopChars[han[i]] || clipStopChars[han[i+1]] {
					continue
				}
				freq[bigram]++
			}
		}
		han = han[:0]
	}
	flushWord := func() {
		if len(word) >= 3 {
			w := strings.ToLower(string(word))
			if !clipStopWords[w] {
				freq[w]++
			}
		}
		word = word[:0]
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			word = append(word, r)
		default:
			flushHan()
			flushWord()
		}
	}
	flushHan()
	flushWord()
	return freq
}

// clipLocalSummary 抽取式摘要：按词频给句子打分，取前 3 句并按原文顺序拼接
func clipLocalSummary(plain, excerpt string) string {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return ""
	}
	if utf8.RuneCountInString(plain) < 180 {
		s := strings.TrimSpace(excerpt)
		if s == "" {
			s = plain
		}
		return trimRunes(s, 200)
	}

	// 去掉正文里裸露的网址，避免按 "." 切句后残留域名片段
	plain = clipURLNoise.ReplaceAllString(plain, " ")
	freq := clipTermFreq(plain)
	// 只用高频词参与打分，避免长尾词把页脚、邮箱等噪声句子抬高
	top := topClipTerms(freq, 10)
	type sentence struct {
		text  string
		score float64
		idx   int
	}
	var sentences []sentence
	for _, raw := range clipSentenceSplit.Split(plain, -1) {
		s := strings.TrimSpace(raw)
		n := utf8.RuneCountInString(s)
		if n < 20 || n > 240 {
			continue
		}
		// 邮箱、网址、路径、版权行通常不是正文摘要
		lower := strings.ToLower(s)
		if strings.ContainsAny(s, "@") || strings.Contains(lower, "http") ||
			strings.Contains(lower, "www.") || strings.Contains(s, "/") {
			continue
		}
		// 英文句子以小写字母开头或词数过少，通常是域名/文件名碎片
		first, _ := utf8.DecodeRuneInString(s)
		if first >= 'a' && first <= 'z' {
			continue
		}
		if first < 128 && unicode.IsLetter(first) && len(strings.Fields(s)) < 5 {
			continue
		}
		score := 0.0
		matched := 0
		for _, term := range top {
			if strings.Contains(s, term) {
				matched++
				score += float64(freq[term])
			}
		}
		if matched < 2 {
			continue
		}
		// 按句长归一化，并给靠前的句子一点位置加权
		posBonus := 1 + 0.25/float64(len(sentences)+1)
		sentences = append(sentences, sentence{
			text:  s,
			score: score / math.Sqrt(float64(n)) * posBonus,
			idx:   len(sentences),
		})
	}
	if len(sentences) == 0 {
		return trimRunes(excerpt, 200)
	}

	picked := append([]sentence(nil), sentences...)
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].score > picked[j].score })
	if len(picked) > 3 {
		picked = picked[:3]
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].idx < picked[j].idx })

	// 逐句累加，超长时舍弃整句，避免从单词中间截断
	out := ""
	for _, p := range picked {
		next := p.text
		if out != "" {
			// 中英文用各自的句末标点衔接，避免英文正文里出现中文句号
			sep := "。"
			if !clipHasCJK(out) && !clipHasCJK(p.text) {
				sep = ". "
			}
			next = out + sep + next
		}
		if utf8.RuneCountInString(next) > 220 {
			if out == "" {
				out = trimRunes(p.text, 220)
			}
			break
		}
		out = next
	}
	return out
}

// clipHasCJK 判断文本是否包含中日韩文字（用于选择摘要拼接标点）
func clipHasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x2E80 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// topClipTerms 取词频最高的前 n 个词（同频按词长优先）
func topClipTerms(freq map[string]int, n int) []string {
	terms := make([]string, 0, len(freq))
	for t := range freq {
		terms = append(terms, t)
	}
	sort.SliceStable(terms, func(i, j int) bool {
		ci, cj := freq[terms[i]], freq[terms[j]]
		if ci != cj {
			return ci > cj
		}
		return utf8.RuneCountInString(terms[i]) > utf8.RuneCountInString(terms[j])
	})
	if len(terms) > n {
		terms = terms[:n]
	}
	return terms
}

// clipLocalTags 从标题与正文提取高频词作为自动标签
func clipLocalTags(title, plain string) []string {
	titleFreq := clipTermFreq(title)
	freq := clipTermFreq(plain)
	score := map[string]int{}
	for t, c := range freq {
		score[t] = c
	}
	for t, c := range titleFreq {
		score[t] += c * 3
	}
	type cand struct {
		term  string
		score int
		first int
	}
	cands := make([]cand, 0, len(score))
	for t, c := range score {
		if c < 2 {
			continue
		}
		n := utf8.RuneCountInString(t)
		if n < 2 || n > 8 {
			continue
		}
		cands = append(cands, cand{term: t, score: c * n})
	}
	// 取首次出现位置，用于同分时保持原文顺序
	for i := range cands {
		cands[i].first = strings.Index(strings.ToLower(plain), strings.ToLower(cands[i].term))
		if cands[i].first < 0 {
			cands[i].first = len(plain)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].first < cands[j].first
	})
	out := make([]string, 0, clipAIMaxTags)
	for _, c := range cands {
		out = append(out, c.term)
		if len(out) >= clipAIMaxTags {
			break
		}
	}
	return out
}

/* ---------- 外部 API：OpenAI 兼容 chat/completions ---------- */

type clipAIResponse struct {
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

// callClipAI 调用 OpenAI 兼容接口生成摘要与标签；失败由调用方回退本地规则
func callClipAI(baseURL, apiKey, model, title, plain string) (string, []string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", nil, fmt.Errorf("接口地址无效")
	}
	if isPrivateHost(u.Hostname()) {
		return "", nil, fmt.Errorf("接口地址位于内网")
	}
	endpoint := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	if strings.TrimSpace(model) == "" {
		model = "gpt-4o-mini"
	}

	content := trimRunes(strings.TrimSpace(plain), clipAIMaxInput)
	userMsg := "标题：" + trimRunes(title, 200) + "\n\n正文：\n" + content
	reqBody := map[string]any{
		"model":       model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": "你是内容整理助手。请阅读文本，输出 JSON：{\"summary\":\"不超过120字的中文摘要\",\"tags\":[\"3到6个简短标签\"]}。只输出 JSON，不要其他文字。"},
			{"role": "user", "content": userMsg},
		},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, err
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 20 * time.Second, Transport: safeHTTPTransport}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("接口返回 HTTP %d", resp.StatusCode)
	}
	var outer struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &outer); err != nil || len(outer.Choices) == 0 {
		return "", nil, fmt.Errorf("接口响应格式无效")
	}
	text := strings.TrimSpace(outer.Choices[0].Message.Content)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		text = text[start : end+1]
	}
	var parsed clipAIResponse
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return "", nil, fmt.Errorf("接口内容不是 JSON")
	}
	return trimRunes(strings.TrimSpace(parsed.Summary), clipAIMaxSummary), normalizeTags(parsed.Tags), nil
}

// removeClipFile 删除剪藏落盘文件（只接受 uploads 下的本地路径，防目录穿越）
func removeClipFile(cfg *config.Config, webPath string) {
	if cfg == nil || webPath == "" {
		return
	}
	rel := ""
	switch {
	case strings.HasPrefix(webPath, "/frontend/uploads/"):
		rel = strings.TrimPrefix(webPath, "/frontend/uploads/")
	case strings.HasPrefix(webPath, "/uploads/"):
		rel = strings.TrimPrefix(webPath, "/uploads/")
	default:
		return
	}
	if rel == "" || strings.Contains(rel, "..") {
		return
	}
	abs := filepath.Join(cfg.UploadDir, filepath.FromSlash(rel))
	if !strings.HasPrefix(filepath.Clean(abs), filepath.Clean(cfg.UploadDir)+string(os.PathSeparator)) {
		return
	}
	os.Remove(abs)
}
