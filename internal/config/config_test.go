package config

import (
	"testing"
)

func TestParseOrigins(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "http://localhost:5173", []string{"http://localhost:5173"}},
		{"comma-separated", "http://a.com,http://b.com", []string{"http://a.com", "http://b.com"}},
		{"with-spaces", " http://a.com , http://b.com ", []string{"http://a.com", "http://b.com"}},
		{"drops-empty", "http://a.com,,http://b.com,", []string{"http://a.com", "http://b.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseOrigins(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v want %v", got, tc.want)
			}
			for i, v := range got {
				if v != tc.want[i] {
					t.Fatalf("index %d: got %q want %q", i, v, tc.want[i])
				}
			}
		})
	}
}

func TestParseCookieSecure(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		cors     string
		want     bool
	}{
		{"explicit true", "true", "http://x", true},
		{"explicit false", "false", "https://x", false},
		{"https single", "", "https://app.example.com", true},
		{"http single", "", "http://localhost:5173", false},
		{"multi any-https → secure", "", "http://localhost:5173,https://app.example.com", true},
		{"multi all-http → not secure", "", "http://localhost:5173,http://127.0.0.1:5173", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCookieSecure(tc.explicit, tc.cors); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestConfigValidate_CORSOrigins(t *testing.T) {
	base := func() *Config {
		return &Config{
			NodeEnv: "production",
			JWT: JWTConfig{
				Secret:        "0123456789abcdef0123456789abcdef",
				RefreshSecret: "fedcba9876543210fedcba9876543210",
			},
			Proxy: ProxyConfig{Secret: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		}
	}

	t.Run("empty rejected", func(t *testing.T) {
		c := base()
		c.CORS.Origins = nil
		if err := c.validate(); err == nil {
			t.Fatal("empty origins should be rejected in production")
		}
	})
	t.Run("star rejected", func(t *testing.T) {
		c := base()
		c.CORS.Origins = []string{"*"}
		if err := c.validate(); err == nil {
			t.Fatal("* should be rejected")
		}
	})
	t.Run("mixed with star rejected", func(t *testing.T) {
		c := base()
		c.CORS.Origins = []string{"https://app.example.com", "*"}
		if err := c.validate(); err == nil {
			t.Fatal("any * should be rejected")
		}
	})
	t.Run("valid single", func(t *testing.T) {
		c := base()
		c.CORS.Origins = []string{"https://app.example.com"}
		if err := c.validate(); err != nil {
			t.Fatalf("valid single origin should pass: %v", err)
		}
	})
	t.Run("valid multi", func(t *testing.T) {
		c := base()
		c.CORS.Origins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}
		if err := c.validate(); err != nil {
			t.Fatalf("valid multi origins should pass: %v", err)
		}
	})
}
