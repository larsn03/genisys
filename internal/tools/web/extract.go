// extract.go — readability-lite: fetch a URL (10 s, 5 MB cap, ≤3
// redirects — SPEC §16), pick the main content, convert to markdown.
// Client-side extraction only; no third-party extractor API.

package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	extractTimeout = 10 * time.Second
	extractMaxBody = 5 << 20
	extractMaxHTML = 2 << 20 // parse budget after the wire cap
)

// fetchGET retrieves a page with the SPEC §16 budgets.
func fetchGET(ctx context.Context, rawURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	hc := &http.Client{
		Timeout: extractTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "genisys/0.1 (+https://github.com/larsn03/genisys)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, extractMaxBody))
	if err != nil {
		return "", fmt.Errorf("read failed: %w", err)
	}
	return string(b), nil
}

// htmlToMarkdown converts HTML to readable markdown (best-effort readability).
func htmlToMarkdown(htmlSrc string) string {
	doc, err := html.Parse(strings.NewReader(htmlSrc))
	if err != nil {
		return ""
	}
	main := pickMain(doc)
	if main == nil {
		main = doc
	}
	var b strings.Builder
	render(&b, main, renderCtx{})
	md := b.String()
	for strings.Contains(md, "\n\n\n") {
		md = strings.ReplaceAll(md, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(md)
}

type renderCtx struct {
	inPre bool
}

var dropTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true,
	"nav": true, "footer": true, "header": true, "aside": true,
	"form": true, "button": true, "iframe": true, "canvas": true,
}

// pickMain finds the content subtree: <article> → <main> → the div/section
// with the most text (classic readability density heuristic).
func pickMain(n *html.Node) *html.Node {
	var best *html.Node
	bestLen := 0
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode {
			switch x.Data {
			case "article":
				if l := textLen(x); l > bestLen {
					best, bestLen = x, l
				}
			case "main":
				if l := textLen(x); l > bestLen/2 { // main wins over divs unless a much bigger article exists
					if best == nil || best.Data != "article" {
						best, bestLen = x, l
					}
				}
			case "div", "section":
				if l := textLen(x); l > bestLen {
					if best == nil || (best.Data != "article" && best.Data != "main") {
						best, bestLen = x, l
					}
				}
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return best
}

func textLen(n *html.Node) int {
	total := 0
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && dropTags[x.Data] {
			return
		}
		if x.Type == html.TextNode {
			total += len(strings.TrimSpace(x.Data))
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return total
}

func render(b *strings.Builder, n *html.Node, ctx renderCtx) {
	if n.Type == html.TextNode {
		t := strings.Join(strings.Fields(n.Data), " ")
		if t == "" {
			return
		}
		if ctx.inPre {
			b.WriteString(n.Data)
			return
		}
		b.WriteString(t)
		b.WriteString(" ")
		return
	}
	if n.Type != html.ElementNode {
		return
	}
	if dropTags[n.Data] {
		return
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		lvl := int(n.Data[1] - '0')
		b.WriteString("\n\n" + strings.Repeat("#", lvl) + " ")
		renderChildren(b, n, ctx)
		b.WriteString("\n\n")
	case "p":
		b.WriteString("\n\n")
		renderChildren(b, n, ctx)
		b.WriteString("\n")
	case "br":
		b.WriteString("\n")
	case "li":
		b.WriteString("\n- ")
		renderChildren(b, n, ctx)
	case "ul", "ol":
		b.WriteString("\n")
		renderChildren(b, n, ctx)
		b.WriteString("\n")
	case "pre":
		b.WriteString("\n\n```\n")
		c2 := ctx
		c2.inPre = true
		renderChildren(b, n, c2)
		b.WriteString("\n```\n\n")
	case "code":
		if ctx.inPre {
			renderChildren(b, n, ctx)
			return
		}
		writeInline(b, "`", n, ctx, "` ")
	case "strong", "b":
		writeInline(b, "**", n, ctx, "** ")
	case "em", "i":
		writeInline(b, "*", n, ctx, "* ")
	case "a":
		href := attr(n, "href")
		if href != "" {
			writeInline(b, "[", n, ctx, "]("+href+") ")
		} else {
			writeInline(b, "[", n, ctx, "] ")
		}
	case "table":
		renderTable(b, n)
	case "img":
		if alt := attr(n, "alt"); alt != "" {
			fmt.Fprintf(b, "![%s] ", alt)
		}
	case "blockquote":
		b.WriteString("\n> ")
		renderChildren(b, n, ctx)
		b.WriteString("\n")
	case "hr":
		b.WriteString("\n---\n")
	default:
		renderChildren(b, n, ctx)
	}
}

// writeInline renders children into a scratch buffer, trims, then wraps —
// so "**bold **" never gets a space before the closing marker.
func writeInline(b *strings.Builder, open string, n *html.Node, ctx renderCtx, close string) {
	var sub strings.Builder
	renderChildren(&sub, n, ctx)
	b.WriteString(open + strings.TrimSpace(sub.String()) + close)
}

func renderChildren(b *strings.Builder, n *html.Node, ctx renderCtx) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		render(b, c, ctx)
	}
}

func renderTable(b *strings.Builder, n *html.Node) {
	b.WriteString("\n\n")
	//nolint:staticcheck // SA1021: recursive closure needs the var first
	var walkRow func(*html.Node, bool)
	walkRow = func(row *html.Node, header bool) {
		b.WriteString("|")
		for c := row.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
				var cell strings.Builder
				renderChildren(&cell, c, renderCtx{})
				b.WriteString(" " + strings.TrimSpace(cell.String()) + " |")
			}
		}
		b.WriteString("\n")
		if header {
			// count cells for the separator row
			cells := 0
			for c := row.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
					cells++
				}
			}
			b.WriteString("|" + strings.Repeat(" --- |", cells) + "\n")
		}
	}
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode {
			if x.Data == "tr" {
				walkRow(x, hasTh(x))
				return
			}
			if dropTags[x.Data] {
				return
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	b.WriteString("\n")
}

func hasTh(row *html.Node) bool {
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "th" {
			return true
		}
	}
	return false
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}
