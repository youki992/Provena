package security

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestResolveToolCommandFallsBackBetweenPythonNames(t *testing.T) {
	lookup := func(available map[string]string) func(string) (string, error) {
		return func(name string) (string, error) {
			if path, ok := available[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		}
	}

	if got := resolveToolCommandWithLookup("python3", lookup(map[string]string{"python": "/bin/python"})); got != "/bin/python" {
		t.Fatalf("python3 fallback = %q, want /bin/python", got)
	}
	if got := resolveToolCommandWithLookup("python", lookup(map[string]string{"python3": "/usr/bin/python3"})); got != "/usr/bin/python3" {
		t.Fatalf("python fallback = %q, want /usr/bin/python3", got)
	}
	if got := resolveToolCommandWithLookup("python3", lookup(map[string]string{"py": "C:/Windows/py.exe"})); got != "C:/Windows/py.exe" {
		t.Fatalf("Windows launcher fallback = %q", got)
	}
	if got := resolveToolCommandWithLookup("nuclei", lookup(nil)); got != "nuclei" {
		t.Fatalf("non-Python command changed to %q", got)
	}
}

func TestPrepareToolCommandMovesLongPythonInlineScriptToFile(t *testing.T) {
	script := "print('" + strings.Repeat("x", windowsPythonInlineScriptLimit+100) + "')"
	command, args, cleanup, err := prepareToolCommandForOS("python", []string{"-c", script, "--flag"}, true)
	if err != nil {
		t.Fatalf("prepare long Python command: %v", err)
	}
	defer cleanup()
	if command != "python" || len(args) != 2 || args[1] != "--flag" {
		t.Fatalf("unexpected prepared command: %q %#v", command, args)
	}
	if args[0] == "" || strings.Contains(args[0], script) {
		t.Fatalf("inline script was not replaced: %#v", args)
	}
	body, err := os.ReadFile(args[0])
	if err != nil {
		t.Fatalf("read temporary script: %v", err)
	}
	if string(body) != script {
		t.Fatalf("temporary script changed: got %d bytes, want %d", len(body), len(script))
	}
	if _, err := os.Stat(args[0]); err != nil {
		t.Fatalf("temporary script removed too early: %v", err)
	}
	cleanup()
	if _, err := os.Stat(args[0]); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove temporary script: %v", err)
	}
}

func TestPrepareToolCommandKeepsShortPythonAndNonWindowsCommands(t *testing.T) {
	short := []string{"-c", "print('ok')"}
	command, args, cleanup, err := prepareToolCommandForOS("python", short, true)
	if err != nil || command != "python" || len(args) != len(short) || args[1] != short[1] {
		t.Fatalf("short Python command changed: %q %#v err=%v", command, args, err)
	}
	cleanup()
	command, args, cleanup, err = prepareToolCommandForOS("python", []string{"-c", strings.Repeat("x", windowsPythonInlineScriptLimit+1)}, false)
	if err != nil || command != "python" || len(args) != 2 {
		t.Fatalf("non-Windows command changed: %q %#v err=%v", command, args, err)
	}
	cleanup()
	command, args, cleanup, err = prepareToolCommandForOS("nuclei", []string{"-c", strings.Repeat("x", windowsPythonInlineScriptLimit+1)}, true)
	if err != nil || command != "nuclei" || len(args) != 2 {
		t.Fatalf("non-Python command changed: %q %#v err=%v", command, args, err)
	}
	cleanup()
}
