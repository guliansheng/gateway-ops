package storage

import (
	"strings"
	"testing"
)

func TestMarkdownToHTMLConvertsLegacyHelpSubset(t *testing.T) {
	input := "# 快速开始\n" +
		"\n" +
		"这是 **加粗** 与 `code` 还有 [文档](https://example.com/a?x=1&y=2)。\n" +
		"第二段含尖括号 <script>alert(1)</script>。\n" +
		"\n" +
		"- 第一项\n" +
		"- 第二项\n" +
		"\n" +
		"> 引用一行\n" +
		"\n" +
		"```json\n" +
		`{"a": "<b>"}` + "\n" +
		"```\n" +
		"## 小节 #"

	got := MarkdownToHTML(input)
	wantParts := []string{
		"<h1>快速开始</h1>",
		`<p>这是 <strong>加粗</strong> 与 <code>code</code> 还有 <a href="https://example.com/a?x=1&amp;y=2" target="_blank" rel="noopener noreferrer">文档</a>。</p>`,
		"<p>第二段含尖括号 &lt;script&gt;alert(1)&lt;/script&gt;。</p>",
		"<ul><li>第一项</li><li>第二项</li></ul>",
		"<blockquote><p>引用一行</p></blockquote>",
		// html.EscapeString 把双引号转义为 &#34;。
		`<pre><code>{&#34;a&#34;: &#34;&lt;b&gt;&#34;}</code></pre>`,
		"<h2>小节</h2>",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("MarkdownToHTML() = %q, want substring %q", got, want)
		}
	}
}

func TestMarkdownToHTMLKeepsUnclosedCodeFence(t *testing.T) {
	got := MarkdownToHTML("```\nline1\nline2")
	if got != "<pre><code>line1\nline2</code></pre>" {
		t.Fatalf("unclosed fence = %q", got)
	}
}
