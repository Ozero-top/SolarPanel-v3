package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"codeberg.org/readeck/go-readability/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
)

// clipArticle 正文快照的提取结果
type clipArticle struct {
	Title     string
	Markdown  string
	Excerpt   string
	WordCount int
	Hash      string
	Error     string
}

var (
	// 正文 HTML → Markdown，只保留标准 Markdown 语法，便于前端安全渲染
	clipMDConverter = converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
	))
	reBlankLines = regexp.MustCompile(`\n{3,}`)
)

// extractClipArticle 从整页 HTML 提取正文并转成 Markdown。
// base 用于把相对链接/图片转成绝对地址；只做提取，不做任何落盘。
func extractClipArticle(html string, base *url.URL) *clipArticle {
	out := &clipArticle{}
	art, err := readability.FromReader(strings.NewReader(html), base)
	if err != nil || art.Node == nil {
		out.Error = "正文提取失败"
		return out
	}

	var htmlBuf bytes.Buffer
	if err := art.RenderHTML(&htmlBuf); err != nil || htmlBuf.Len() == 0 {
		out.Error = "正文提取失败"
		return out
	}

	domain := ""
	if base != nil {
		domain = base.Scheme + "://" + base.Host
	}
	md, err := clipMDConverter.ConvertString(htmlBuf.String(), converter.WithDomain(domain))
	if err != nil {
		out.Error = "正文转换失败"
		return out
	}
	md = normalizeClipMarkdown(md)
	if md == "" {
		out.Error = "正文为空"
		return out
	}

	var textBuf bytes.Buffer
	art.RenderText(&textBuf)
	plain := strings.TrimSpace(textBuf.String())

	out.Title = strings.TrimSpace(art.Title())
	out.Markdown = md
	out.Excerpt = clipExcerpt(art.Excerpt(), plain)
	out.WordCount = clipWordCount(plain)
	sum := sha256.Sum256([]byte(md))
	out.Hash = hex.EncodeToString(sum[:])
	return out
}

// normalizeClipMarkdown 收敛空行、去掉零宽字符，保持 Markdown 简洁稳定
func normalizeClipMarkdown(md string) string {
	md = strings.ReplaceAll(md, "\u200b", "")
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = reBlankLines.ReplaceAllString(md, "\n\n")
	return strings.TrimSpace(md)
}

// clipExcerpt 摘要优先用页面自带描述，否则截取正文纯文本开头
func clipExcerpt(fromPage, plain string) string {
	s := strings.TrimSpace(fromPage)
	if s == "" {
		s = strings.TrimSpace(plain)
	}
	return trimRunes(s, 200)
}

// clipWordCount 统计字数：CJK 按字计，拉丁按词计
func clipWordCount(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			count++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				count++
				inWord = true
			}
		default:
			inWord = false
		}
	}
	return count
}

// clipPlainText 把 Markdown 粗转为纯文本，供检索摘要与字数统计使用
func clipPlainText(md string) string {
	var b strings.Builder
	for i := 0; i < len(md); i++ {
		c := md[i]
		switch c {
		case '#', '*', '`', '>', '~', '[', ']', '(', ')':
			continue
		case '\n':
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
