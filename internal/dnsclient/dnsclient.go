// Package dnsclient 提供可自定义的 DNS 解析，支持系统默认、普通 DNS (UDP/TCP)、
// DNS over TLS (DoT) 与 DNS over HTTPS (DoH)。用于解决 API 端点域名解析失败
// 或 DNS 污染导致的 connection refused 问题。
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

// Config 描述自定义 DNS 配置。Mode 为空时等价于 system。
type Config struct {
	Mode   Mode
	Server string
}

// isCustom 表示是否需要自定义解析（非系统默认）。
func (c Config) isCustom() bool {
	return c.Mode != "" && c.Mode != ModeSystem && strings.TrimSpace(c.Server) != ""
}

var systemResolver = net.DefaultResolver

func dialPlain(ctx context.Context, server, network string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 8 * time.Second}
	return d.DialContext(ctx, network, server)
}

func lookupPlain(ctx context.Context, server, host string) ([]net.IP, error) {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialPlain(ctx, server, network)
		},
	}
	return r.LookupIP(ctx, "ip", host)
}

// dnsWire 提供 DNS wire format 的最小编解码（仅支持 A / AAAA 查询与响应解析）。
type dnsWire struct {
	data []byte
	pos  int
}

func newDNSQuery(domain string, qtype uint16) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint16(0x1234)) // ID
	binary.Write(&buf, binary.BigEndian, uint16(0x0100)) // Flags: RD
	binary.Write(&buf, binary.BigEndian, uint16(1))      // QDCOUNT
	binary.Write(&buf, binary.BigEndian, uint16(0))      // ANCOUNT
	binary.Write(&buf, binary.BigEndian, uint16(0))      // NSCOUNT
	binary.Write(&buf, binary.BigEndian, uint16(0))      // ARCOUNT
	for _, label := range strings.Split(domain, ".") {
		if label == "" {
			continue
		}
		buf.WriteByte(byte(len(label)))
		buf.WriteString(label)
	}
	buf.WriteByte(0)
	binary.Write(&buf, binary.BigEndian, qtype)  // QTYPE: A=1, AAAA=28
	binary.Write(&buf, binary.BigEndian, uint16(1)) // QCLASS: IN
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
	w := &dnsWire{data: data, pos: 0}
	if len(data) < 12 {
		return nil, fmt.Errorf("DNS 响应过短")
	}
	w.pos = 12 // 跳过 header
	for i := 0; i < 1; i++ {
		w.skipName()
		w.pos += 4 // QTYPE + QCLASS
	}
	var ips []net.IP
	ancount := binary.BigEndian.Uint16(data[6:8])
	for i := 0; i < int(ancount); i++ {
		w.skipName()
		rt := w.readUint16()
		w.pos += 2 // CLASS
		w.readUint32() // TTL
		rdlen := int(w.readUint16())
		if rt == 1 && qtype == 1 && rdlen == 4 { // A
			ip := net.IPv4(w.data[w.pos], w.data[w.pos+1], w.data[w.pos+2], w.data[w.pos+3])
			ips = append(ips, ip)
		} else if rt == 28 && qtype == 28 && rdlen == 16 { // AAAA
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
	d := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tlsConn := tls.Client(conn, &tls.Config{ServerName: server[:strings.LastIndex(server, ":")]})
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

// LookupIP 解析 host 的 IP 列表。mode 为空或 system 时使用系统默认。
func (c Config) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	if !c.isCustom() {
		return systemResolver.LookupIP(ctx, "ip", host)
	}
	host = strings.TrimSuffix(host, ".")
	type result struct {
		ips []net.IP
		err error
	}
	ch := make(chan result, 2)
	go func() {
		ips, err := c.lookupByType(ctx, host, 1) // A
		ch <- result{ips, err}
	}()
	go func() {
		ips, err := c.lookupByType(ctx, host, 28) // AAAA
		ch <- result{ips, err}
	}()
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

func (c Config) lookupByType(ctx context.Context, host string, qtype uint16) ([]net.IP, error) {
	switch c.Mode {
	case ModePlain:
		return lookupPlain(ctx, c.Server, host)
	case ModeDoT:
		return dotQuery(ctx, c.Server, host, qtype)
	case ModeDoH:
		return dohQuery(ctx, c.Server, host, qtype)
	default:
		return nil, fmt.Errorf("不支持的 DNS 模式 %q", c.Mode)
	}
}

// Resolver 返回一个可复用的解析函数，供 net.Transport.DialContext 使用。
func (c Config) Resolver() func(ctx context.Context, network, address string) (net.Conn, error) {
	if !c.isCustom() {
		return nil
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			host = address
			port = portForNetwork(network)
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

func portForNetwork(network string) string {
	switch {
	case strings.HasSuffix(network, "4"):
		return "0"
	case strings.HasSuffix(network, "6"):
		return "0"
	default:
		return "443"
	}
}
