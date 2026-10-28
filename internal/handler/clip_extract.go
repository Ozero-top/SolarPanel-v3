package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"codeberg.org/readeck/go-readability/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/ledongthuc/pdf"
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

// clipArticleFromPage 按抓取类型选择 HTML 提取或 PDF 文本提取
func clipArticleFromPage(page *clipPage) *clipArticle {
	if page == nil {
		return nil
	}
	if page.IsPDF {
		return extractPDFArticle(page)
	}
	return extractClipArticle(page.HTML, page.FinalURL)
}

// extractPDFArticle 把 PDF 纯文本整理成 Markdown 快照；解析库对损坏文件可能 panic，统一兜底
func extractPDFArticle(page *clipPage) (out *clipArticle) {
	out = &clipArticle{}
	if len(page.PDFData) == 0 {
		out.Error = "PDF 内容为空"
		return out
	}
	defer func() {
		if recover() != nil {
			out = &clipArticle{Error: "PDF 解析失败"}
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(page.PDFData), int64(len(page.PDFData)))
	if err != nil {
		out.Error = "PDF 解析失败"
		return out
	}
	var raw strings.Builder
	for i := 1; i <= reader.NumPage(); i++ {
		p := reader.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		raw.WriteString(text)
		raw.WriteString("\n\n")
	}
	paragraphs := pdfPlainParagraphs(raw.String())
	if len(paragraphs) == 0 {
		out.Error = "PDF 无可提取文本"
		return out
	}
	plain := strings.Join(paragraphs, "\n\n")
	md := normalizeClipMarkdown(strings.Join(paragraphs, "\n\n"))
	if md == "" {
		out.Error = "PDF 无可提取文本"
		return out
	}
	out.Title = page.Title
	out.Markdown = md
	out.Excerpt = clipExcerpt("", plain)
	out.WordCount = clipWordCount(plain)
	sum := sha256.Sum256([]byte(md))
	out.Hash = hex.EncodeToString(sum[:])
	return out
}

// pdfPlainParagraphs 把 PDF 提取出的文本按行重组为段落：
// 行尾是句末标点或空行视为换段，中文续行直接拼接，英文续行补一个空格。
func pdfPlainParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\u00a0", " ")
	var paragraphs []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		s = strings.Join(strings.Fields(s), " ")
		if s != "" {
			paragraphs = append(paragraphs, s)
		}
		cur.Reset()
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		if cur.Len() > 0 {
			last, _ := utf8.DecodeLastRuneInString(cur.String())
			if unicode.Is(unicode.Han, last) || unicode.IsPunct(last) || unicode.IsSymbol(last) {
				cur.WriteString(line)
			} else {
				cur.WriteString(" ")
				cur.WriteString(line)
			}
		} else {
			cur.WriteString(line)
		}
		last, _ := utf8.DecodeLastRuneInString(line)
		if strings.ContainsRune("。！？.!?；;：:", last) {
			flush()
		}
	}
	flush()
	return paragraphs
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
