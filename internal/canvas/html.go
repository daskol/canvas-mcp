package canvas

import (
	"strings"

	"golang.org/x/net/html"
)

func addPlainText(value any) {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			addPlainText(item)
		}
	case map[string]any:
		for _, item := range value {
			addPlainText(item)
		}
		for _, key := range []string{"description", "body", "message", "syllabus_body"} {
			if source, ok := value[key].(string); ok {
				value[key+"_text"] = plainText(source)
			}
		}
	}
}

func plainText(source string) string {
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return source
	}
	var out strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			out.WriteString(node.Data)
			return
		}
		block := false
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "noscript":
				return
			case "p", "div", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6", "li", "ul", "ol", "br", "tr", "pre", "blockquote", "hr":
				block = true
				out.WriteByte('\n')
			case "td", "th":
				out.WriteByte(' ')
			case "img":
				for _, attr := range node.Attr {
					if attr.Key == "alt" {
						out.WriteString(attr.Val)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		if node.Type == html.ElementNode && node.Data == "a" {
			for _, attr := range node.Attr {
				if attr.Key == "href" && attr.Val != "" {
					out.WriteString(" (" + attr.Val + ")")
				}
			}
		}
		if block {
			out.WriteByte('\n')
		}
	}
	visit(document)
	var lines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
