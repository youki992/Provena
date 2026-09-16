package config

import (
	"os"
	"strings"
)

// NormalizeExternalMCPServerCommand keeps compatibility with older UI versions
// that stored a complete stdio command in the command field.
func NormalizeExternalMCPServerCommand(server *ExternalMCPServerConfig) {
	if server == nil || server.GetTransportType() != "stdio" || len(server.Args) > 0 {
		return
	}
	command := strings.TrimSpace(server.Command)
	if command == "" {
		return
	}
	// A quoted or unquoted executable path may itself contain spaces. Keep it
	// intact when it points to a real file; otherwise parse it as a command line.
	if executable := strings.Trim(command, "\"'"); executable != command {
		if _, err := os.Stat(executable); err == nil {
			server.Command = executable
			return
		}
	} else if _, err := os.Stat(command); err == nil {
		return
	}
	parts := splitCommandLine(command)
	if len(parts) < 2 {
		return
	}
	server.Command = parts[0]
	server.Args = parts[1:]
}

func splitCommandLine(input string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	escaped := false

	flush := func() {
		if current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
	}

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && inQuotes && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
			escaped = true
			continue
		}
		switch r {
		case '"', '\'':
			inQuotes = !inQuotes
		case ' ', '\t', '\r', '\n':
			if inQuotes {
				current.WriteRune(r)
			} else {
				flush()
			}
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return parts
}
