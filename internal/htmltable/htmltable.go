// Package htmltable pulls plain-text tables out of an HTML page, each one tagged with the heading that precedes it.
package htmltable

import (
	"io"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Table is a parsed <table>: Heading is the text of the closest <h1>…<h6> before it,
// Rows holds the text of every <td>/<th>, with whitespace collapsed.
type Table struct {
	Heading string
	Rows    [][]string
}

// Parse reads every top-level table in the document, in order.
func Parse(r io.Reader) ([]Table, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var (
		tables  []Table
		heading string
	)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
				heading = Text(n)
				return
			case atom.Table:
				tables = append(tables, Table{Heading: heading, Rows: rows(n)})
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return tables, nil
}

func rows(table *html.Node) [][]string {
	var out [][]string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Tr {
			var cells []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.DataAtom == atom.Td || c.DataAtom == atom.Th) {
					cells = append(cells, Text(c))
				}
			}
			out = append(out, cells)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(table)
	return out
}

// Text returns the visible text of a node with runs of whitespace collapsed to one space.
func Text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(strings.ReplaceAll(b.String(), " ", " ")), " ")
}

// Options returns the visible text of every <option> inside the <select name="name">.
func Options(r io.Reader, name string) ([]string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []string
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inside bool) {
		if n.Type == html.ElementNode {
			if n.DataAtom == atom.Select {
				inside = attr(n, "name") == name
			}
			if inside && n.DataAtom == atom.Option {
				value := attr(n, "value")
				if value == "" {
					value = Text(n)
				}
				out = append(out, value)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inside)
		}
	}
	walk(doc, false)
	return out, nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
