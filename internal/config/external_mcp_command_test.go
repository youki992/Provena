package config

import "testing"

func TestExternalMCPServerConfigGetTransportTypeSupportsLegacyField(t *testing.T) {
	server := ExternalMCPServerConfig{Transport: "sse", URL: "http://127.0.0.1:8081/sse"}
	if got := server.GetTransportType(); got != "sse" {
		t.Fatalf("transport = %q, want sse", got)
	}

	server.Type = "http"
	if got := server.GetTransportType(); got != "http" {
		t.Fatalf("type must take precedence, got %q", got)
	}
}

func TestNormalizeExternalMCPServerCommand(t *testing.T) {
	server := ExternalMCPServerConfig{Command: `python C:\tools\sqlmap.py -h`}
	NormalizeExternalMCPServerCommand(&server)
	if server.Command != "python" {
		t.Fatalf("command = %q, want python", server.Command)
	}
	want := []string{`C:\tools\sqlmap.py`, "-h"}
	if len(server.Args) != len(want) || server.Args[0] != want[0] || server.Args[1] != want[1] {
		t.Fatalf("args = %#v, want %#v", server.Args, want)
	}
}

func TestNormalizeExternalMCPServerCommandPreservesQuotedArguments(t *testing.T) {
	server := ExternalMCPServerConfig{Command: `python "C:\Program Files\tool\server.py" --name "demo tool"`}
	NormalizeExternalMCPServerCommand(&server)
	want := []string{"C:\\Program Files\\tool\\server.py", "--name", "demo tool"}
	if server.Command != "python" {
		t.Fatalf("command = %q, want python", server.Command)
	}
	if len(server.Args) != len(want) {
		t.Fatalf("args = %#v, want %#v", server.Args, want)
	}
	for i := range want {
		if server.Args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, server.Args[i], want[i])
		}
	}
}
