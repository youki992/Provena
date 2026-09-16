package app

import (
	"net"
	"strings"
	"testing"
)

func TestListenMainServerReportsOccupiedPort(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test port: %v", err)
	}
	defer occupied.Close()

	ln, err := listenMainServer(occupied.Addr().String())
	if ln != nil {
		ln.Close()
		t.Fatal("expected occupied port not to return a listener")
	}
	if err == nil {
		t.Fatal("expected occupied port to return an error")
	}
	if got := err.Error(); !strings.Contains(got, "另一个 Provena 实例占用") {
		t.Fatalf("listen error %q does not explain the likely conflict", got)
	}
}

func TestNotifyMainServerReady(t *testing.T) {
	called := false
	notifyMainServerReady(func() { called = true })
	if !called {
		t.Fatal("ready callback was not invoked")
	}
	notifyMainServerReady(nil)
}
