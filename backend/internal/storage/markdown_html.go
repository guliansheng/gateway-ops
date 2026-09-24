package storage

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// MarkdownToHTML 把旧版帮助文档支持的 Markdown 子集转换成富文本 HTML，
// 覆盖范围与原前端渲染器一致：标题、围栏代码块、无序列表、引用、
// 加粗、行内代码和链接，其余按普通段落输出。
func MarkdownToHTML(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	var out []string
	var bullets []string
	inCode := false
	var codeLines []string

	flushBullets := func() {
		if len(bullets) == 0 {
			return
		}
		items := make([]string, 0, len(bullets))
		for _, item := range bullets {
			items = append(items, "<li>"+inlineMarkdown(item)+"</li>")
		}
		out = append(out, "<ul>"+strings.Join(items, "")+"</ul>")
		bullets = nil
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if inCode {
				out = append(out, "<pre><code>"+html.EscapeString(strings.Join(codeLines, "\n"))+"</code></pre>")
				codeLines = nil
				inCode = false
			} else {
				flushBullets()
				inCode = true
			}
			continue
		}
		if inCode {
			codeLines = append(codeLines, line)
			continue
		}
		if match := headingRE.FindStringSubmatch(line); match != nil {
			flushBullets()
			level := len(match[1])
			out = append(out, fmt.Sprintf("<h%d>%s</h%d>", level, inlineMarkdown(strings.TrimSpace(match[2])), level))
			continue
		}
		if match := bulletRE.FindStringSubmatch(line); match != nil {
			bullets = append(bullets, match[1])
			continue
		}
		if strings.TrimSpace(line) == "" {
			flushBullets()
			continue
		}
		flushBullets()
		if rest, found := strings.CutPrefix(line, ">"); found {
			out = append(out, "<blockquote><p>"+inlineMarkdown(strings.TrimSpace(rest))+"</p></blockquote>")
			continue
		}
		out = append(out, "<p>"+inlineMarkdown(line)+"</p>")
	}
	if inCode && len(codeLines) > 0 {
		out = append(out, "<pre><code>"+html.EscapeString(strings.Join(codeLines, "\n"))+"</code></pre>")
	}
	flushBullets()
	return strings.Join(out, "\n")
}

var (
	headingRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
	bulletRE  = regexp.MustCompile(`^\s*[-*]\s+(.+)$`)
	inlineRE  = regexp.MustCompile("(`[^`]+`|\\*\\*[^*]+\\*\\*|\\[[^\\]]+\\]\\([^)]+\\))")
	linkRE    = regexp.MustCompile(`^\[([^\]]+)\]\(([^)]+)\)$`)
)

// inlineMarkdown 先转义 HTML，再套用行内 Markdown 语法，
// 保证旧文档里的尖括号等内容不会被浏览器当作标签执行。
func inlineMarkdown(text string) string {
	var b strings.Builder
	last := 0
	for _, loc := range inlineRE.FindAllStringIndex(text, -1) {
		b.WriteString(html.EscapeString(text[last:loc[0]]))
		token := text[loc[0]:loc[1]]
		switch {
		case strings.HasPrefix(token, "`"):
			b.WriteString("<code>" + html.EscapeString(token[1:len(token)-1]) + "</code>")
		case strings.HasPrefix(token, "**"):
			b.WriteString("<strong>" + html.EscapeString(token[2:len(token)-2]) + "</strong>")
		default:
			if match := linkRE.FindStringSubmatch(token); match != nil {
				b.WriteString(`<a href="` + html.EscapeString(match[2]) + `" target="_blank" rel="noopener noreferrer">` + html.EscapeString(match[1]) + `</a>`)
			} else {
				b.WriteString(html.EscapeString(token))
			}
		}
		last = loc[1]
	}
	b.WriteString(html.EscapeString(text[last:]))
	return b.String()
}
