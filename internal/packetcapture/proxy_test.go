package packetcapture

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/database"
	"go.uber.org/zap"
)

func TestHTTPProxyForwardsAndCreatesLeafCertificate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Capture-Test") != "yes" {
			t.Errorf("header not forwarded")
		}
		_, _ = w.Write([]byte("plain-response"))
	}))
	defer upstream.Close()
	port := freePort(t)
	dir := t.TempDir()
	service, err := NewService(config.PacketCaptureConfig{Host: "127.0.0.1", Port: port, CAPath: filepath.Join(dir, "ca.crt"), CAKeyPath: filepath.Join(dir, "ca.key")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer service.Stop(nil)
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return url.Parse(proxyURL) }}}
	req, _ := http.NewRequest(http.MethodGet, upstream.URL+"/evidence?q=1", nil)
	req.Header.Set("X-Capture-Test", "yes")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	caBlock, _ := pem.Decode(service.CA().CertPEM())
	if caBlock == nil {
		t.Fatal("missing CA PEM")
	}
	if _, err := x509.ParseCertificate(caBlock.Bytes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(service.CA().CertPath()); err != nil {
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestSelectedPacketGroupsProvideScopeAndFilterForeignPackets(t *testing.T) {
	db, err := database.NewDB(filepath.Join(t.TempDir(), "packets.db"), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service, err := NewService(config.PacketCaptureConfig{
		CAPath:    filepath.Join(t.TempDir(), "ca.crt"),
		CAKeyPath: filepath.Join(t.TempDir(), "ca.key"),
	}, db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	service.SetOwnerUserID("packet-user")

	targetGroup, err := db.SaveCapturedPacket(&database.CapturedPacket{
		ID: "target-packet", Method: "GET", Scheme: "https", Host: "api.target.test", Port: 443,
		Path: "/api/orders", Query: "id=1", RequestHeaders: "Authorization: Bearer test",
		ResponseStatus: 200, ResponseHeaders: "Content-Type: application/json", ResponseBody: []byte(`{"id":1}`),
	}, "packet-user")
	if err != nil {
		t.Fatal(err)
	}
	foreignGroup, err := db.SaveCapturedPacket(&database.CapturedPacket{
		ID: "foreign-packet", Method: "GET", Scheme: "https", Host: "cdn.third-party.test", Port: 443,
		Path: "/bundle.js", ResponseStatus: 200, ResponseBody: []byte("bundle"),
	}, "packet-user")
	if err != nil {
		t.Fatal(err)
	}

	scope := service.ScopeRequestForGroups([]string{targetGroup})
	if scope != "https://api.target.test" {
		t.Fatalf("selected packet group scope = %q, want target host", scope)
	}
	block, included, excluded := service.FullPromptBlockFiltered(
		[]string{targetGroup, foreignGroup},
		func(_, host string, _ int) bool { return host == "api.target.test" },
	)
	if included != 1 || excluded != 1 {
		t.Fatalf("packet counts = included %d, excluded %d; want 1/1", included, excluded)
	}
	if !strings.Contains(block, "api.target.test") || strings.Contains(block, "cdn.third-party.test") {
		t.Fatalf("filtered packet block contains wrong hosts: %s", block)
	}
	for _, want := range []string{
		"GET /api/orders?id=1 HTTP/1.1",
		"Host: api.target.test",
		"Authorization: Bearer test",
		"HTTP/1.1 200",
		`{"id":1}`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("full packet evidence missing %q: %s", want, block)
		}
	}
}
