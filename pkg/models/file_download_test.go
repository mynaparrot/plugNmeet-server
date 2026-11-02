package models

import (
	"net/netip"
	"testing"
)

func TestValidateRemoteFileUrl(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https", url: "https://example.com/file.pdf"},
		{name: "http with port", url: "http://example.com:8080/file.pdf"},
		{name: "missing scheme", url: "example.com/file.pdf", wantErr: true},
		{name: "file scheme", url: "file:///etc/passwd", wantErr: true},
		{name: "gopher scheme", url: "gopher://example.com/file.pdf", wantErr: true},
		{name: "empty host", url: "http:///file.pdf", wantErr: true},
		{name: "empty", url: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRemoteFileUrl(tt.url)
			if tt.wantErr != (err != nil) {
				t.Fatalf("validateRemoteFileUrl(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestIsBlockedRemoteIP(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1", want: true},
		{addr: "127.10.20.30", want: true},
		{addr: "::1", want: true},
		{addr: "0.0.0.0", want: true},
		{addr: "::", want: true},
		{addr: "169.254.169.254", want: true},
		{addr: "fe80::1", want: true},
		{addr: "ff02::1", want: true},
		{addr: "10.0.0.1", want: true},
		{addr: "172.16.5.4", want: true},
		{addr: "192.168.1.1", want: true},
		{addr: "::ffff:192.168.1.1", want: true},
		{addr: "8.8.8.8", want: false},
		{addr: "1.1.1.1", want: false},
		{addr: "2606:4700:4700::1111", want: false},
		{addr: "invalid", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			ip, err := netip.ParseAddr(tt.addr)
			if err != nil {
				ip = netip.Addr{}
			}
			if got := isBlockedRemoteIP(ip); got != tt.want {
				t.Fatalf("isBlockedRemoteIP(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
