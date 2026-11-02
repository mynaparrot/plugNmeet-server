package controllers

import "testing"

func TestIsValidRtmpUrl(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: "rtmp://example.com/live/stream", want: true},
		{url: "rtmps://example.com:1936/live/stream", want: true},
		{url: "RTMPS://example.com/live/stream", want: true},
		{url: "http://example.com/live/stream", want: false},
		{url: "file:///etc/passwd", want: false},
		{url: "rtmp:///live/stream", want: false},
		{url: "example.com/live/stream", want: false},
		{url: "", want: false},
	}

	for _, tt := range tests {
		if got := isValidRtmpUrl(tt.url); got != tt.want {
			t.Errorf("isValidRtmpUrl(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
