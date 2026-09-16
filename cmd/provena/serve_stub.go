//go:build !webconsole

package main

import "fmt"

// serveCompiled tells the usage text whether this binary can start the web
// console. This is the default build, so the command is advertised nowhere and
// fails loudly if someone tries it anyway.
const serveCompiled = false

// runServe stands in for the web console when the console is not compiled in.
// It keeps the same signature so main.go's dispatch — and its backwards
// compatible "bare flags mean serve" rule — still compile, and turns both into a
// clear message instead of a link error.
func runServe([]string) error {
	return fmt.Errorf(`this build is CLI-only: it has no web console and binds no port

The web console is opt-in. Rebuild with the webconsole tag to get it back:

  go build -tags webconsole -o provena.exe ./cmd/provena

For a headless test against one target, use "provena run -t <target>" instead.`)
}
