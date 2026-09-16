package packetcapture

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/database"
	"go.uber.org/zap"
)

type Service struct {
	mu          sync.RWMutex
	config      config.PacketCaptureConfig
	ca          *CertificateAuthority
	db          *database.DB
	logger      *zap.Logger
	server      *http.Server
	listener    net.Listener
	address     string
	ownerUserID string
}

func NewService(cfg config.PacketCaptureConfig, db *database.DB, logger *zap.Logger) (*Service, error) {
	ca, err := LoadOrCreateCA(cfg.CAPath, cfg.CAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("初始化抓包 CA 失败: %w", err)
	}
	return &Service{config: cfg, ca: ca, db: db, logger: logger}, nil
}

func (s *Service) SetOwnerUserID(id string) {
	s.mu.Lock()
	s.ownerUserID = strings.TrimSpace(id)
	s.mu.Unlock()
}
func (s *Service) Configure(cfg config.PacketCaptureConfig) error {
	ca, err := LoadOrCreateCA(cfg.CAPath, cfg.CAKeyPath)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.config = cfg
	s.ca = ca
	running := s.listener != nil
	s.mu.Unlock()
	if running {
		if err := s.Stop(context.Background()); err != nil {
			return err
		}
	}
	if cfg.Enabled {
		return s.Start()
	}
	return nil
}
func (s *Service) CA() *CertificateAuthority { s.mu.RLock(); defer s.mu.RUnlock(); return s.ca }
func (s *Service) Config() config.PacketCaptureConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}
func (s *Service) Address() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.address }
func (s *Service) Running() bool   { s.mu.RLock(); defer s.mu.RUnlock(); return s.listener != nil }

func (s *Service) FullPromptBlock(groupIDs []string) string {
	block, _, _ := s.FullPromptBlockFiltered(groupIDs, nil)
	return block
}

// ScopeRequestForGroups returns the hosts explicitly selected by the user.
// It is used when a packet-analysis request has no textual domain: the
// selected groups are the scope candidate, while FullPromptBlockFiltered can
// still apply the normal host filter before raw packets enter Pi.
func (s *Service) ScopeRequestForGroups(groupIDs []string) string {
	if s == nil || s.db == nil || len(groupIDs) == 0 {
		return ""
	}
	owner := func() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.ownerUserID }()
	seen := make(map[string]struct{})
	values := make([]string, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		group, err := s.db.GetPacketGroup(strings.TrimSpace(groupID), owner)
		if err != nil {
			continue
		}
		packets, err := s.db.ListCapturedPackets(group.ID, owner, 20)
		if err != nil {
			continue
		}
		for _, packet := range packets {
			host := strings.TrimSpace(packet.Host)
			if host == "" {
				continue
			}
			scheme := strings.TrimSpace(packet.Scheme)
			if scheme == "" {
				scheme = "https"
			}
			target := scheme + "://" + host
			if packet.Port > 0 && !((scheme == "https" && packet.Port == 443) || (scheme == "http" && packet.Port == 80)) {
				target = scheme + "://" + net.JoinHostPort(host, strconv.Itoa(packet.Port))
			}
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			values = append(values, target)
		}
	}
	return strings.Join(values, "\n")
}

// FullPromptBlockFiltered returns only packets accepted by the caller's
// frozen task scope. A nil filter keeps the legacy behavior for internal use.
func (s *Service) FullPromptBlockFiltered(groupIDs []string, include func(scheme, host string, port int) bool) (string, int, int) {
	if s == nil || s.db == nil || len(groupIDs) == 0 {
		return "", 0, 0
	}
	owner := func() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.ownerUserID }()
	var b strings.Builder
	b.WriteString("## 用户选定的抓包请求（完整原始包，仅用于本次分析）\n")
	included, excluded := 0, 0
	for _, groupID := range groupIDs {
		group, err := s.db.GetPacketGroup(strings.TrimSpace(groupID), owner)
		if err != nil {
			continue
		}
		packets, err := s.db.ListCapturedPackets(group.ID, owner, 20)
		if err != nil {
			continue
		}
		var groupBlock strings.Builder
		groupIncluded := 0
		for _, packet := range packets {
			if include != nil && !include(packet.Scheme, packet.Host, packet.Port) {
				excluded++
				continue
			}
			included++
			groupIncluded++
			writeFullPacketEvidence(&groupBlock, packet)
		}
		if groupIncluded > 0 {
			fmt.Fprintf(&b, "\n### 分组：%s（保留 %d / 原始 %d 个请求）\n%s", group.Name, groupIncluded, len(packets), groupBlock.String())
		}
	}
	if included == 0 {
		return "", 0, excluded
	}
	return strings.TrimSpace(b.String()), included, excluded
}

// writeFullPacketEvidence renders the captured exchange in a replay-friendly
// form. The scope filter is deliberately applied before this function; once a
// packet is accepted, its headers, cookies, authorization state and bodies are
// kept verbatim (subject only to the capture-size truncation recorded on the
// packet).
func writeFullPacketEvidence(b *strings.Builder, packet *database.CapturedPacket) {
	if b == nil || packet == nil {
		return
	}
	scheme := strings.TrimSpace(packet.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	host := strings.TrimSpace(packet.Host)
	authority := host
	if packet.Port > 0 && !((scheme == "https" && packet.Port == 443) || (scheme == "http" && packet.Port == 80)) {
		authority = net.JoinHostPort(host, strconv.Itoa(packet.Port))
	}
	path := packet.Path
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	if packet.Query != "" {
		path += "?" + packet.Query
	}

	fmt.Fprintf(b, "\n--- packet_id=%s（完整原始交换；不脱敏）---\n", packet.ID)
	fmt.Fprintf(b, "请求报文：\n%s %s HTTP/1.1\nHost: %s\n%s\n", packet.Method, path, authority, packet.RequestHeaders)
	b.Write(packet.RequestBody)
	if len(packet.RequestBody) > 0 && packet.RequestBody[len(packet.RequestBody)-1] != '\n' {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "响应报文：\nHTTP/1.1 %d\n%s\n", packet.ResponseStatus, packet.ResponseHeaders)
	b.Write(packet.ResponseBody)
	if len(packet.ResponseBody) > 0 && packet.ResponseBody[len(packet.ResponseBody)-1] != '\n' {
		b.WriteByte('\n')
	}
	if packet.ScopeStatus == "truncated" {
		b.WriteString("[注意：抓包正文超过配置上限，以上请求体或响应体为截断内容，不能冒充完整原文。]\n")
	}
}

func (s *Service) Start() error {
	s.mu.Lock()
	if s.listener != nil {
		s.mu.Unlock()
		return nil
	}
	cfg, err := normalizeConfig(s.config)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.HostEffective(), strconv.Itoa(cfg.PortEffective())))
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("启动抓包代理失败: %w", err)
	}
	s.config = cfg
	s.listener = ln
	s.address = ln.Addr().String()
	server := &http.Server{Handler: s, ReadHeaderTimeout: 20 * time.Second}
	s.server = server
	s.mu.Unlock()
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed && s.logger != nil {
			s.logger.Error("抓包代理退出", zap.Error(err))
		}
	}()
	if s.logger != nil {
		s.logger.Info("抓包代理已启动", zap.String("address", s.Address()), zap.String("ca", s.ca.CertPath()))
	}
	return nil
}

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	server := s.server
	s.listener = nil
	s.server = nil
	s.address = ""
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return server.Shutdown(ctx)
}

func normalizeConfig(cfg config.PacketCaptureConfig) (config.PacketCaptureConfig, error) {
	if net.ParseIP(cfg.HostEffective()) == nil && strings.TrimSpace(cfg.Host) != "localhost" {
		return cfg, fmt.Errorf("抓包代理监听地址必须是 IP 或 localhost")
	}
	return cfg, nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.allowedSource(r.RemoteAddr) {
		http.Error(w, "proxy access denied", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	s.handleRequest(w, r, nil)
}

func (s *Service) allowedSource(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	cfg := func() config.PacketCaptureConfig { s.mu.RLock(); defer s.mu.RUnlock(); return s.config }()
	if len(cfg.AllowedCIDRs) == 0 {
		return host == "127.0.0.1" || host == "::1" || host == "localhost"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, raw := range cfg.AllowedCIDRs {
		_, block, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err == nil && block.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Service) handleConnect(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "CONNECT unsupported", http.StatusHTTPVersionNotSupported)
		return
	}
	host, _ := splitHostPort(r.Host)
	cert, err := s.ca.Leaf(host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	client, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = client.Close()
		return
	}
	if err = rw.Flush(); err != nil {
		_ = client.Close()
		return
	}
	tlsConn := tls.Server(&bufferedConn{Conn: client, reader: rw.Reader}, &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12})
	if err = tlsConn.Handshake(); err != nil {
		_ = tlsConn.Close()
		return
	}
	defer tlsConn.Close()
	reader := bufio.NewReader(tlsConn)
	for {
		req, readErr := http.ReadRequest(reader)
		if readErr != nil {
			return
		}
		req.URL.Scheme = "https"
		req.URL.Host = r.Host
		req.Host = host
		closeConn := s.handleRequest(nil, req, tlsConn)
		if closeConn || req.Close {
			return
		}
	}
}

// handleRequest returns true when the client connection should be closed.
func (s *Service) handleRequest(w http.ResponseWriter, r *http.Request, conn net.Conn) bool {
	cfg := func() config.PacketCaptureConfig { s.mu.RLock(); defer s.mu.RUnlock(); return s.config }()
	body, tooLarge := readLimited(r.Body, cfg.MaxBodyBytesEffective())
	if r.Body != nil {
		_ = r.Body.Close()
	}
	if tooLarge {
		body = body[:cfg.MaxBodyBytesEffective()]
	}
	if r.URL.Scheme == "" {
		r.URL.Scheme = "http"
	}
	if r.URL.Host == "" {
		r.URL.Host = r.Host
	}
	r.RequestURI = ""
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	for _, key := range []string{"Proxy-Connection", "Connection", "Keep-Alive"} {
		r.Header.Del(key)
	}
	resp, err := (&http.Transport{Proxy: nil, MaxIdleConns: 32, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 15 * time.Second}).RoundTrip(r)
	if err != nil {
		if w != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		} else {
			_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		}
		return true
	}
	respBody, respTooLarge := readLimited(resp.Body, cfg.MaxBodyBytesEffective())
	_ = resp.Body.Close()
	if respTooLarge {
		respBody = respBody[:cfg.MaxBodyBytesEffective()]
	}
	packet := s.makePacket(r, body, resp, respBody, tooLarge, respTooLarge)
	s.mu.RLock()
	owner := s.ownerUserID
	s.mu.RUnlock()
	if s.db != nil {
		if _, err := s.db.SaveCapturedPacket(packet, owner); err != nil && s.logger != nil {
			s.logger.Warn("保存抓包失败", zap.Error(err))
		}
	}
	if w != nil {
		copyHeaders(w.Header(), resp.Header)
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Length", strconv.Itoa(len(respBody)))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return reqClose(r, resp)
	}
	return writeRawResponse(conn, resp, respBody) || reqClose(r, resp)
}

func (s *Service) makePacket(r *http.Request, reqBody []byte, resp *http.Response, respBody []byte, reqTrunc, respTrunc bool) *database.CapturedPacket {
	host, port := splitHostPort(r.URL.Host)
	if port == 0 {
		if r.URL.Scheme == "https" {
			port = 443
		} else {
			port = 80
		}
	}
	scope := "unknown"
	if reqTrunc || respTrunc {
		scope = "truncated"
	}
	return &database.CapturedPacket{Method: r.Method, Scheme: r.URL.Scheme, Host: host, Port: port, Path: requestPath(r.URL), Query: r.URL.RawQuery, RequestHeaders: headersText(r.Header), RequestBody: reqBody, ResponseStatus: resp.StatusCode, ResponseHeaders: headersText(resp.Header), ResponseBody: respBody, ContentType: resp.Header.Get("Content-Type"), SessionKey: r.Header.Get("Cookie"), ScopeStatus: scope, Source: "yakit-burp-proxy"}
}

func readLimited(body io.ReadCloser, limit int) ([]byte, bool) {
	if body == nil {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	return data, err == nil && len(data) > limit
}
func headersText(h http.Header) string { data, _ := json.Marshal(h); return string(data) }
func requestPath(u *url.URL) string {
	if u == nil || u.Path == "" {
		return "/"
	}
	return u.EscapedPath()
}
func splitHostPort(raw string) (string, int) {
	raw = strings.TrimSpace(raw)
	if host, port, err := net.SplitHostPort(raw); err == nil {
		p, _ := strconv.Atoi(port)
		return host, p
	}
	return raw, 0
}
func copyHeaders(dst, src http.Header) {
	for k, values := range src {
		for _, value := range values {
			dst.Add(k, value)
		}
	}
}
func reqClose(r *http.Request, resp *http.Response) bool {
	return r.Close || resp.Close || strings.EqualFold(resp.Header.Get("Connection"), "close")
}
func writeRawResponse(conn net.Conn, resp *http.Response, body []byte) bool {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	err := resp.Write(conn)
	return err != nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
