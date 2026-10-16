package httpx

import (
	"net"
	"testing"
)

func TestIsDisallowedIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"public ipv4", "93.184.216.34", false},
		{"public ipv6", "2606:2800:220:1:248:1893:25c8:1946", false},

		{"loopback ipv4", "127.0.0.1", true},
		{"loopback ipv6", "::1", true},
		{"ipv4-mapped ipv6 loopback", "::ffff:127.0.0.1", true},

		{"link-local unicast ipv4", "169.254.1.1", true},
		{"link-local unicast ipv6", "fe80::1", true},

		{"private ipv4 10/8", "10.0.0.1", true},
		{"private ipv4 172.16/12", "172.16.0.1", true},
		{"private ipv4 192.168/16", "192.168.1.1", true},
		{"private ipv6 ULA", "fd00::1", true},
		{"ipv4-mapped ipv6 private", "::ffff:10.0.0.1", true},

		{"unspecified ipv4", "0.0.0.0", true},
		{"unspecified ipv6", "::", true},

		{"multicast ipv4", "224.0.0.1", true},
		{"multicast ipv6", "ff02::1", true},

		{"cgnat shared address space", "100.64.0.1", true},
		{"cgnat range upper bound is still blocked", "100.127.255.255", true},
		{"just above cgnat range is allowed", "100.128.0.1", false},

		{"ietf protocol assignment", "192.0.0.1", true},
		{"test-net-1", "192.0.2.1", true},
		{"benchmarking", "198.18.0.1", true},
		{"test-net-2", "198.51.100.1", true},
		{"test-net-3", "203.0.113.1", true},
		{"reserved 240/4", "240.0.0.1", true},
		{"ipv6 discard-only", "100::1", true},
		{"ipv6 documentation", "2001:db8::1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) failed", tt.ip)
			}

			if got := isDisallowedIP(ip); got != tt.want {
				t.Errorf("isDisallowedIP(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
