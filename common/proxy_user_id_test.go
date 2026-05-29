package common

import (
	"testing"
)

func TestInjectUserIdInProxyURL(t *testing.T) {
	tests := []struct {
		name         string
		proxyURL     string
		injectUserId bool
		userId       int
		want         string
	}{
		{
			name:         "no-op when injectUserId is false",
			proxyURL:     "socks5://user:pass@host:1080",
			injectUserId: false,
			userId:       42,
			want:         "socks5://user:pass@host:1080",
		},
		{
			name:         "no-op when proxyURL is empty",
			proxyURL:     "",
			injectUserId: true,
			userId:       42,
			want:         "",
		},
		{
			name:         "no-op when userId is 0",
			proxyURL:     "socks5://user:pass@host:1080",
			injectUserId: true,
			userId:       0,
			want:         "socks5://user:pass@host:1080",
		},
		{
			name:         "no-op when URL has no userinfo",
			proxyURL:     "socks5://host:1080",
			injectUserId: true,
			userId:       42,
			want:         "socks5://host:1080",
		},
		{
			name:         "inject into username with password",
			proxyURL:     "socks5://user:pass@host:1080",
			injectUserId: true,
			userId:       42,
			want:         "socks5://user%4042:pass@host:1080",
		},
		{
			name:         "inject into username without password",
			proxyURL:     "socks5://user@host:1080",
			injectUserId: true,
			userId:       42,
			want:         "socks5://user%4042@host:1080",
		},
		{
			name:         "inject with large user ID",
			proxyURL:     "socks5://admin:secret@proxy.example.com:9050",
			injectUserId: true,
			userId:       999999,
			want:         "socks5://admin%40999999:secret@proxy.example.com:9050",
		},
		{
			name:         "inject into http proxy",
			proxyURL:     "http://user:pass@proxy:8080",
			injectUserId: true,
			userId:       7,
			want:         "http://user%407:pass@proxy:8080",
		},
		{
			name:         "inject into socks5h proxy",
			proxyURL:     "socks5h://user:pass@host:1080",
			injectUserId: true,
			userId:       1,
			want:         "socks5h://user%401:pass@host:1080",
		},
		{
			name:         "no-op on invalid URL",
			proxyURL:     "://invalid",
			injectUserId: true,
			userId:       42,
			want:         "://invalid",
		},
		{
			name:         "special chars in password preserved",
			proxyURL:     "socks5://user:p%40ss@host:1080",
			injectUserId: true,
			userId:       3,
			want:         "socks5://user%403:p%40ss@host:1080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InjectUserIdInProxyURL(tt.proxyURL, tt.injectUserId, tt.userId)
			if got != tt.want {
				t.Errorf("InjectUserIdInProxyURL(%q, %v, %d) = %q, want %q",
					tt.proxyURL, tt.injectUserId, tt.userId, got, tt.want)
			}
		})
	}
}
