// Package dnsclient 提供可自定义的 DNS 解析，支持多服务器容灾、任意厂商自由填写。
// 支持：系统默认、普通 DNS (UDP/TCP)、DNS over TLS (DoT)、DNS over HTTPS (DoH)。
package dnsclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Mode 是 DNS 解析模式。
type Mode string

const (
	ModeSystem Mode = "system" // 系统默认 DNS
	ModePlain  Mode = "plain"  // 普通 DNS (UDP/TCP)，Server 形如 "8.8.8.8:53"
	ModeDoT    Mode = "dot"    // DNS over TLS，Server 形如 "1.1.1.1:853"
	ModeDoH    Mode = "doh"    // DNS over HTTPS，Server 形如 "https://1.1.1.1/dns-query"
)

// Server 是一个 DNS 服务器条目。
type Server struct {
	Mode   Mode   `json:"mode"`
	Server string `json:"server"`
}

// Config 描述自定义 DNS 配置。Servers 为空时等价于系统默认。
type Config struct {
	Servers []Server `json:"servers"`
}

// Presets 是常见 DNS 厂商的预设，供前端快速添加。
var Presets = map[string]Server{
	"cloudflare":    {Mode: ModeDoH, Server: "https://1.1.1.1/dns-query"},
	"cloudflare-tls": {Mode: ModeDoT, Server: "1.1.1.1:853"},
	"google":        {Mode: ModeDoH, Server: "https://dns.google/dns-query"},
	"google-tls":    {Mode: ModeDoT, Server: "8.8.8.8:853"},
	"quad9":         {Mode: ModeDoH, Server: "https://dns.quad9.net/dns-query"},
	"quad9-tls":     {Mode: ModeDoT, Server: "9.9.9.9:853"},
	"alidns":        {Mode: ModeDoH, Server: "https://dns.alidns.com/dns-query"},
	"alidns-tls":    {Mode: ModeDoT, Server: "223.5.5.5:853"},
	"dnspod":        {Mode: ModeDoH, Server: "https://doh.pub/dns-query"},
	"opendns":       {Mode: ModeDoH, Server: "https://doh.opendns.com/dns-query"},
}

func (c Config) isCustom() bool {
	for _, s := range c.Servers {
		if s.Mode != "" && s.Mode != ModeSystem && strings.TrimSpace(s.Server) != "" {
			return true
		}
	}
	return false
}

var systemResolver = net.DefaultResolver

func lookupPlain(ctx context.Context, server, host string) ([]net.IP, error) {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 8 * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
	return r.LookupIP(ctx, "ip", host)
}

// ---- DNS wire format ----

type dnsWire struct {
	data []byte
	pos  int
}

func newDNSQuery(domain string, qtype uint16) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint16(0x1234))
	binary.Write(&buf, binary.BigEndian, uint16(0x0100))
	binary.Write(&buf, binary.BigEndian, uint16(1))
	binary.Write(&buf, binary.BigEndian, uint16(0))
	binary.Write(&buf, binary.BigEndian, uint16(0))
	binary.Write(&buf, binary.BigEndian, uint16(0))
	for _, label := range strings.Split(domain, ".") {
		if label == "" {
			continue
		}
		buf.WriteByte(byte(len(label)))
		buf.WriteString(label)
	}
	buf.WriteByte(0)
	binary.Write(&buf, binary.BigEndian, qtype)
	binary.Write(&buf, binary.BigEndian, uint16(1))
	return buf.Bytes()
}

func (w *dnsWire) skipName() {
	for {
		if w.pos >= len(w.data) {
			return
		}
		b := w.data[w.pos]
		if b == 0 {
			w.pos++
			return
		}
		if b&0xc0 == 0xc0 {
			w.pos += 2
			return
		}
		w.pos += int(b) + 1
	}
}

func (w *dnsWire) readUint16() uint16 {
	v := binary.BigEndian.Uint16(w.data[w.pos : w.pos+2])
	w.pos += 2
	return v
}

func (w *dnsWire) readUint32() uint32 {
	v := binary.BigEndian.Uint32(w.data[w.pos : w.pos+4])
	w.pos += 4
	return v
}

func parseDNSResponse(data []byte, qtype uint16) ([]net.IP, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("DNS 响应过短")
	}
	w := &dnsWire{data: data, pos: 12}
	w.skipName()
	w.pos += 4
	var ips []net.IP
	ancount := binary.BigEndian.Uint16(data[6:8])
	for i := 0; i < int(ancount); i++ {
		w.skipName()
		rt := w.readUint16()
		w.pos += 2
		w.readUint32()
		rdlen := int(w.readUint16())
		if rt == 1 && qtype == 1 && rdlen == 4 {
			ip := net.IPv4(w.data[w.pos], w.data[w.pos+1], w.data[w.pos+2], w.data[w.pos+3])
			ips = append(ips, ip)
		} else if rt == 28 && qtype == 28 && rdlen == 16 {
			ip := make(net.IP, 16)
			copy(ip, w.data[w.pos:w.pos+16])
			ips = append(ips, ip)
		}
		w.pos += rdlen
	}
	return ips, nil
}

func dohQuery(ctx context.Context, server, host string, qtype uint16) ([]net.IP, error) {
	query := newDNSQuery(host, qtype)
	url := server
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH 返回 %d", resp.StatusCode)
	}
	return parseDNSResponse(body, qtype)
}

func dotQuery(ctx context.Context, server, host string, qtype uint16) ([]net.IP, error) {
	if !strings.Contains(server, ":") {
		server += ":853"
	}
	hostOnly := server[:strings.LastIndex(server, ":")]
	d := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tlsConn := tls.Client(conn, &tls.Config{ServerName: hostOnly})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	query := newDNSQuery(host, qtype)
	var msg bytes.Buffer
	binary.Write(&msg, binary.BigEndian, uint16(len(query)))
	msg.Write(query)
	if _, err := tlsConn.Write(msg.Bytes()); err != nil {
		return nil, err
	}
	var hdr [2]byte
	if _, err := io.ReadFull(tlsConn, hdr[:]); err != nil {
		return nil, err
	}
	respLen := binary.BigEndian.Uint16(hdr[:])
	resp := make([]byte, respLen)
	if _, err := io.ReadFull(tlsConn, resp); err != nil {
		return nil, err
	}
	return parseDNSResponse(resp, qtype)
}

func (srv Server) lookup(ctx context.Context, host string, qtype uint16) ([]net.IP, error) {
	switch srv.Mode {
	case ModePlain:
		return lookupPlain(ctx, srv.Server, host)
	case ModeDoT:
		return dotQuery(ctx, srv.Server, host, qtype)
	case ModeDoH:
		return dohQuery(ctx, srv.Server, host, qtype)
	default:
		return nil, fmt.Errorf("不支持的 DNS 模式 %q", srv.Mode)
	}
}

// LookupIP 解析 host 的 IP 列表。多服务器时并发取最快结果。
func (c Config) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	servers := c.activeServers()
	if len(servers) == 0 {
		return systemResolver.LookupIP(ctx, "ip", host)
	}
	host = strings.TrimSuffix(host, ".")

	type result struct {
		ips []net.IP
		err error
	}
	ch := make(chan result, 2)
	try := func(srv Server) {
		ips, err := srv.lookup(ctx, host, 1)
		ch <- result{ips, err}
		ips6, err6 := srv.lookup(ctx, host, 28)
		ch <- result{ips6, err6}
	}

	if len(servers) == 1 {
		go try(servers[0])
	} else {
		ctx2, cancel := context.WithCancel(ctx)
		defer cancel()
		var wg sync.WaitGroup
		errCh := make(chan result, len(servers)*2)
		for i := range servers {
			wg.Add(1)
			go func(srv Server) {
				defer wg.Done()
				ips, err := srv.lookup(ctx2, host, 1)
				select {
				case errCh <- result{ips, err}:
				case <-ctx2.Done():
				}
				ips6, err6 := srv.lookup(ctx2, host, 28)
				select {
				case errCh <- result{ips6, err6}:
				case <-ctx2.Done():
				}
			}(servers[i])
		}
		go func() { wg.Wait(); close(errCh) }()
		var allIPs []net.IP
		for r := range errCh {
			if r.err == nil && len(r.ips) > 0 {
				allIPs = append(allIPs, r.ips...)
			}
		}
		if len(allIPs) > 0 {
			return allIPs, nil
		}
		return nil, fmt.Errorf("所有 DNS 服务器均解析失败")
	}

	var ips []net.IP
	var firstErr error
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		ips = append(ips, r.ips...)
	}
	if len(ips) == 0 {
		return nil, firstErr
	}
	return ips, nil
}

func (c Config) activeServers() []Server {
	var out []Server
	for _, s := range c.Servers {
		if s.Mode != "" && s.Mode != ModeSystem && strings.TrimSpace(s.Server) != "" {
			out = append(out, s)
		}
	}
	return out
}

// Resolver 返回一个可复用的拨号函数，供 http.Transport.DialContext 使用。
func (c Config) Resolver() func(ctx context.Context, network, address string) (net.Conn, error) {
	if !c.isCustom() {
		return nil
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			host = address
			port = "443"
		}
		if host == "" {
			return nil, fmt.Errorf("DNS 解析: 空地址")
		}
		ips, err := c.LookupIP(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("DNS 解析 %s 失败: %w", host, err)
		}
		d := &net.Dialer{Timeout: 15 * time.Second}
		var lastErr error
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("DNS 解析 %s: 无可用地址", host)
	}
}
