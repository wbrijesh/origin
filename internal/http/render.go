package http

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

//go:embed templates/*
var templateFS embed.FS

// renderer holds parsed templates and rendering utilities.
type renderer struct {
	templates *template.Template
	markdown  goldmark.Markdown
	sanitizer *bluemonday.Policy
}

func newRenderer() *renderer {
	funcMap := template.FuncMap{
		"timeAgo":   timeAgo,
		"shortHash": shortHash,
		"highlight":  highlightCode,
		"renderMarkdown": func(s string) template.HTML {
			return "" // placeholder, replaced after construction
		},
		"join":     strings.Join,
		"trimSpace": strings.TrimSpace,
		"firstLine": firstLine,
		"pathJoin":  filepath.Join,
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}

	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"))

	md := goldmark.New()
	sanitizer := bluemonday.UGCPolicy()
	sanitizer.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code", "pre", "span", "div")

	r := &renderer{
		templates: tmpl,
		markdown:  md,
		sanitizer: sanitizer,
	}

	// Replace the renderMarkdown function with one that uses the renderer
	tmpl.Funcs(template.FuncMap{
		"renderMarkdown": r.renderMarkdown,
	})

	return r
}

// render executes a named template with the given data.
func (r *renderer) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	var buf bytes.Buffer
	if err := r.templates.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("template render failed", "template", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	buf.WriteTo(w) //nolint:errcheck
}

// renderMarkdown converts markdown to sanitized HTML.
func (r *renderer) renderMarkdown(input string) template.HTML {
	var buf bytes.Buffer
	if err := r.markdown.Convert([]byte(input), &buf); err != nil {
		slog.Error("markdown render failed", "error", err)
		return template.HTML("<pre>" + template.HTMLEscapeString(input) + "</pre>")
	}
	safe := r.sanitizer.SanitizeBytes(buf.Bytes())
	return template.HTML(safe) //nolint:gosec
}

// highlightCode applies syntax highlighting to source code.
func highlightCode(code, filename string) template.HTML {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}

	formatter := chromahtml.New(
		chromahtml.WithClasses(true),
		chromahtml.WithLineNumbers(true),
		chromahtml.TabWidth(4),
	)

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return template.HTML("<pre><code>" + template.HTMLEscapeString(code) + "</code></pre>")
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return template.HTML("<pre><code>" + template.HTMLEscapeString(code) + "</code></pre>")
	}

	return template.HTML(buf.String()) //nolint:gosec
}

// writeCSS writes the Chroma CSS for syntax highlighting.
func writeChromaCSS(w io.Writer) error {
	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	return formatter.WriteCSS(w, style)
}

// timeAgo returns a human-readable relative time string.
func timeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case d < 365*24*time.Hour:
		months := int(d.Hours() / 24 / 30)
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := int(d.Hours() / 24 / 365)
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
