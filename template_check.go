package main

import (
	"fmt"
	"text/template"
)

// Validate the embedded XMLTV template before any network scraping begins.
// This prevents a syntax error from being discovered only after a long scrape.
func init() {
	if _, err := template.ParseFS(guideTmplFS, "guide.tmpl"); err != nil {
		panic(fmt.Sprintf("invalid embedded guide template: %v", err))
	}
}
