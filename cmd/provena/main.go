// Command provena is the single entrypoint for the Provena security testing
// agent: a headless CLI plus an optional web console.
package main

import (
	"fmt"
	"os"
	"strings"
)

// version can be overridden at build time:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/provena
var version = "v0.1.0"

const defaultConfigPath = "config.yaml"

const usagePrefix = `Provena - evidence-driven security testing agent

Usage:
  provena <command> [flags]

Commands:
`

const usageSuffix = `
Run "provena <command> -h" for the flags of a specific command.
`

// usageText lists the commands this binary actually has. serve is only
// advertised when it was compiled in, so a CLI-only build never points at a web
// console it cannot start.
func usageText() string {
	var b strings.Builder
	b.WriteString(usagePrefix)
	b.WriteString("  chat               Talk to the agent in an interactive terminal session\n")
	b.WriteString("  run                Run a bounded, headless CLI test against one target\n")
	b.WriteString("  doctor             Check environment, configuration and dependencies\n")
	b.WriteString("  init               Create a local config file from the bundled example\n")
	b.WriteString("  config validate    Validate the configuration file\n")
	b.WriteString("  version            Print the version\n")
	if serveCompiled {
		b.WriteString("  serve              Start the web console, MCP endpoints and built-in tools\n")
		b.WriteString("\nEverything except serve is pure CLI and binds no port. Only \"provena serve\"\n")
		b.WriteString("starts a listening web console; \"provena run\" keeps output on the terminal.\n")
	} else {
		b.WriteString("\nThis build is CLI-only: no web console, and no port is ever bound.\n")
		b.WriteString("Build with -tags webconsole to add the console back.\n")
	}
	b.WriteString(usageSuffix)
	return b.String()
}

func main() {
	args := os.Args[1:]

	// Backwards compatibility: a bare flag list means "serve", so existing
	// launchers (run.sh, systemd units, container entrypoints) keep working.
	// A CLI-only build keeps the rule too, and runServe explains the situation.
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		args = append([]string{"serve"}, args...)
	}

	if len(args) == 0 {
		fmt.Print(usageText())
		return
	}

	command, rest := args[0], args[1:]

	var err error
	switch command {
	case "serve":
		err = runServe(rest)
	case "run":
		err = runRun(rest)
	case "chat":
		err = runChat(rest)
	case "doctor":
		err = runDoctor(rest)
	case "init":
		err = runInit(rest)
	case "config":
		err = runConfig(rest)
	case "version", "-v", "--version":
		fmt.Printf("provena %s\n", version)
	case "help", "-h", "--help":
		fmt.Print(usageText())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", command, usageText())
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
