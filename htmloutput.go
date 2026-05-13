package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

// SavePromptHTML writes the system prompt and user message to a
// self-contained HTML file for easy inspection.
func SavePromptHTML(path, title, systemPrompt, userMessage string) error {
	var b strings.Builder

	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title>
<style>
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    max-width: 960px;
    margin: 2rem auto;
    padding: 0 1rem;
    background: #f8f9fa;
    color: #212529;
  }
  h1 { font-size: 1.4rem; border-bottom: 2px solid #dee2e6; padding-bottom: 0.5rem; }
  details { margin: 1rem 0; }
  summary {
    font-weight: 600;
    font-size: 1.1rem;
    cursor: pointer;
    padding: 0.5rem;
    background: #e9ecef;
    border-radius: 4px;
  }
  summary:hover { background: #dee2e6; }
  pre {
    background: #fff;
    border: 1px solid #dee2e6;
    border-radius: 4px;
    padding: 1rem;
    overflow-x: auto;
    white-space: pre-wrap;
    word-wrap: break-word;
    font-size: 0.85rem;
    line-height: 1.5;
  }
</style>
</head>
<body>
<h1>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</h1>

<details>
<summary>System Prompt</summary>
<pre>`)
	b.WriteString(html.EscapeString(systemPrompt))
	b.WriteString(`</pre>
</details>

<details open>
<summary>User Message</summary>
<pre>`)
	b.WriteString(html.EscapeString(userMessage))
	b.WriteString(`</pre>
</details>

</body>
</html>
`)

	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("writing prompt HTML: %w", err)
	}
	return nil
}

// fileHyperlink returns an OSC 8 terminal hyperlink for a local file
// path. Terminals that support OSC 8 (GNOME Terminal, iTerm2, Konsole,
// Windows Terminal, etc.) will render this as a clickable link that
// opens the file in the default application. Terminals that do not
// support OSC 8 will simply display the path text.
func fileHyperlink(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	fileURL := "file://" + absPath
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", fileURL, path)
}
