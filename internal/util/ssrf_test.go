package util

import (
	"net"
	"testing"
)

func TestIsPrivateHostname_StringLevel(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"foo.local", true},
		{"bar.internal", true},
		{"svc.localhost", true},
		{"example.com", false},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"google.com", false},
	}
	for _, c := range cases {
		if got := IsPrivateHostname(c.host); got != c.want {
			t.Errorf("IsPrivateHostname(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestIsPrivateIP_V4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"172.16.5.5", true},
		{"172.31.255.255", true},
		{"172.32.0.0", false}, // 边界外
		{"192.168.1.1", true},
		{"169.254.1.2", true},
		{"127.0.0.1", true},
		{"0.0.0.0", true},
		{"100.64.0.1", true}, // CGNAT
		{"100.127.0.1", true},
		{"100.128.0.1", false}, // 边界外
		{"224.0.0.1", true},    // multicast
		{"240.0.0.1", true},    // reserved
		{"8.8.8.8", false},
		{"1.1.1.1", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("invalid test input: %s", c.ip)
		}
		if got := isPrivateIP(ip); got != c.want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestIsPrivateIP_V6(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"::1", true},
		{"::", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"fd12:3456:789a::1", true},
		{"2001:db8::1", true},
		{"2002::1", true}, // 6to4
		{"::ffff:10.0.0.1", true},
		{"::ffff:192.168.1.1", true},
		{"::ffff:8.8.8.8", false},
		{"2606:4700:4700::1111", false}, // Cloudflare DNS 公网
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("invalid test input: %s", c.ip)
		}
		if got := isPrivateIP(ip); got != c.want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}
