// Package help holds the work instructions (WI) and knowledge base (KB) shown in the Help window
// and readable by the AI assistant. The articles are Markdown files in docs.
package help

import (
	"embed"
	"sort"
	"strings"
)

//go:embed docs/*.md
var docs embed.FS

// Article is one help page.
type Article struct {
	ID    string `json:"id"`    // file name without .md, e.g. WI-02
	Group string `json:"group"` // "Work instructions" or "Knowledge base"
	Title string `json:"title"`
}

// List returns all articles: start page, then work instructions, then knowledge base.
func List() []Article {
	es, _ := docs.ReadDir("docs")
	var out []Article
	for _, e := range es {
		id := strings.TrimSuffix(e.Name(), ".md")
		a := Article{ID: id, Group: "Start"}
		switch {
		case strings.HasPrefix(id, "WI-"):
			a.Group = "Work instructions"
		case strings.HasPrefix(id, "KB-"):
			a.Group = "Knowledge base"
		}
		if b, err := docs.ReadFile("docs/" + e.Name()); err == nil {
			a.Title = strings.TrimSpace(strings.TrimPrefix(strings.SplitN(strings.ReplaceAll(string(b), "\r", ""), "\n", 2)[0], "#"))
		}
		out = append(out, a)
	}
	rank := map[string]int{"Start": 0, "Work instructions": 1, "Knowledge base": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Group] != rank[out[j].Group] {
			return rank[out[i].Group] < rank[out[j].Group]
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns the Markdown text of one article ("" when there is none).
func Get(id string) string {
	for _, a := range List() {
		if strings.EqualFold(a.ID, id) {
			b, _ := docs.ReadFile("docs/" + a.ID + ".md")
			return strings.ReplaceAll(string(b), "\r", "")
		}
	}
	return ""
}
