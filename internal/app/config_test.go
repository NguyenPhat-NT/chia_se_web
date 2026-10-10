package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestConfigDefaultsAndDomainReservations(t *testing.T) {
	dir := t.TempDir()
	cfg, err := DevConfig(dir).Validated()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UploadBytes != 50*MiB || cfg.SiteBytes != 100*MiB || cfg.TotalBytes != 512*MiB || cfg.UserBytes != 128*MiB || cfg.MaxFiles != 5000 || cfg.MinFreeBytes != 0 {
		t.Fatal("development resource defaults are incorrect")
	}
	if !slices.Contains(cfg.Reserved, "app") || !slices.Contains(cfg.Reserved, "api") {
		t.Fatal("service domains are not reserved")
	}
	cfg, err = (Config{DataDir: dir, AdminHost: "panel.example.com", BaseDomain: "example.com", Reserved: []string{"existing-service"}}).Validated()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8088" || cfg.Scheme != "https" || cfg.MinFreeBytes != 512*MiB {
		t.Fatal("production defaults are incorrect")
	}
	if !slices.Contains(cfg.Reserved, "panel") || !slices.Contains(cfg.Reserved, "existing-service") {
		t.Fatal("admin or explicitly configured service domain reservation was lost")
	}
	for _, listen := range []string{"127.0.0.1:8088", "[::1]:8088", "localhost:8088"} {
		dev := DevConfig(dir)
		dev.Listen = listen
		if _, err := dev.Validated(); err != nil {
			t.Fatalf("loopback development listen %q rejected: %v", listen, err)
		}
	}
}

func TestConfigRejectsUnsafeAndInvalidValues(t *testing.T) {
	valid := DevConfig(t.TempDir())
	for _, test := range []struct {
		name   string
		change func(*Config)
	}{
		{"missing-data", func(c *Config) { c.DataDir = "" }},
		{"relative-data", func(c *Config) { c.DataDir = "./data" }},
		{"root-data", func(c *Config) { c.DataDir = "/" }},
		{"missing-host", func(c *Config) { c.AdminHost = "" }},
		{"host-with-port", func(c *Config) { c.AdminHost = "app.localhost:8088" }},
		{"host-with-path", func(c *Config) { c.AdminHost = "app.localhost/path" }},
		{"host-wildcard", func(c *Config) { c.BaseDomain = "*.localhost" }},
		{"host-empty-label", func(c *Config) { c.AdminHost = "app..localhost" }},
		{"host-invalid-label", func(c *Config) { c.AdminHost = "-app.localhost" }},
		{"host-overlong-label", func(c *Config) { c.AdminHost = strings.Repeat("x", 64) + ".localhost" }},
		{"host-collision", func(c *Config) { c.AdminHost = c.BaseDomain }},
		{"development-public-listen", func(c *Config) { c.Listen = "0.0.0.0:8088" }},
		{"development-public-ipv6", func(c *Config) { c.Listen = "[::]:8088" }},
		{"development-public-hostname", func(c *Config) { c.Listen = "router.local:8088" }},
		{"development-wrong-domain", func(c *Config) { c.BaseDomain = "example.com" }},
		{"development-wrong-admin", func(c *Config) { c.AdminHost = "app.example.com" }},
		{"development-https", func(c *Config) { c.Scheme = "https" }},
		{"listen-missing-port", func(c *Config) { c.Listen = "127.0.0.1" }},
		{"listen-empty-port", func(c *Config) { c.Listen = "127.0.0.1:" }},
		{"listen-nonnumeric-port", func(c *Config) { c.Listen = "127.0.0.1:bad" }},
		{"listen-zero-port", func(c *Config) { c.Listen = "127.0.0.1:0" }},
		{"listen-negative-port", func(c *Config) { c.Listen = "127.0.0.1:-1" }},
		{"listen-overflow-port", func(c *Config) { c.Listen = "127.0.0.1:65536" }},
		{"trusted-tunnel-hostname-listen", func(c *Config) { c.TrustCloudflare = true; c.Listen = "localhost:8088" }},
		{"negative-upload", func(c *Config) { c.UploadBytes = -1 }},
		{"negative-site", func(c *Config) { c.SiteBytes = -1 }},
		{"negative-total", func(c *Config) { c.TotalBytes = -1 }},
		{"negative-user", func(c *Config) { c.UserBytes = -1 }},
		{"negative-free-space", func(c *Config) { c.MinFreeBytes = -1 }},
		{"negative-files", func(c *Config) { c.MaxFiles = -1 }},
		{"too-many-files", func(c *Config) { c.MaxFiles = 100001 }},
		{"huge-upload", func(c *Config) { c.UploadBytes = 1<<30 + 1 }},
		{"huge-site", func(c *Config) { c.SiteBytes = 1<<30 + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.change(&cfg)
			if _, err := cfg.Validated(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	for _, scheme := range []string{"http", "ftp", "HTTPS"} {
		cfg := Config{DataDir: t.TempDir(), AdminHost: "app.example.com", BaseDomain: "example.com", Scheme: scheme}
		if _, err := cfg.Validated(); err == nil {
			t.Fatalf("invalid production scheme %q accepted", scheme)
		}
	}
	for _, listen := range []string{"0.0.0.0:8088", "[::]:8088", "192.168.1.1:8088", "router.local:8088"} {
		cfg := Config{DataDir: t.TempDir(), AdminHost: "app.example.com", BaseDomain: "example.com", TrustCloudflare: true, Listen: listen}
		if _, err := cfg.Validated(); err == nil {
			t.Fatalf("trusted tunnel with nonloopback listener %q accepted", listen)
		}
	}
	for _, listen := range []string{"127.0.0.1:1", "[::1]:65535"} {
		cfg := DevConfig(t.TempDir())
		cfg.TrustCloudflare = true
		cfg.Listen = listen
		if _, err := cfg.Validated(); err != nil {
			t.Fatalf("trusted tunnel with loopback listener %q rejected: %v", listen, err)
		}
	}
}

func TestLoadConfigRequiresOneStrictJSONObject(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	valid, err := json.Marshal(DevConfig(filepath.Join(dir, "data")))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, content string
		success       bool
	}{
		{"valid", string(valid), true},
		{"trailing-whitespace", string(valid) + "\n\t", true},
		{"unknown-field", strings.TrimSuffix(string(valid), "}") + `,"unknown":true}`, false},
		{"second-object", string(valid) + ` {}`, false},
		{"trailing-garbage", string(valid) + ` broken`, false},
		{"array", `[]`, false},
		{"null", `null`, false},
		{"malformed", `{`, false},
		{"over-limit", `{"data_dir":"` + strings.Repeat("a", 66000) + `"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(configPath, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(configPath)
			if (err == nil) != test.success {
				t.Fatalf("LoadConfig success = %v; want %v", err == nil, test.success)
			}
		})
	}
	if _, err := LoadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing configuration file accepted")
	}
}

func TestNewEnforcesExclusiveDataDirectory(t *testing.T) {
	cfg := DevConfig(t.TempDir())
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(cfg)
	if err == nil {
		second.Close()
		first.Close()
		t.Fatal("two server instances acquired the same data directory")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := New(cfg)
	if err != nil {
		t.Fatalf("data directory cannot be reopened after Close: %v", err)
	}
	if err = third.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(cfg.DataDir, "guiweb.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("database permissions expose private account data")
	}
}

func TestNewRejectsSymlinkDataDirectory(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if server, err := New(DevConfig(link)); err == nil {
		server.Close()
		t.Fatal("symlink data directory accepted")
	}
}

func TestNewPreservesUnrelatedNonemptyDirectory(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.Mkdir(staging, 0700); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(staging, "my-file.txt")
	const contents = "This is unrelated user data and must survive startup."
	if err := os.WriteFile(precious, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	if server, err := New(DevConfig(dir)); err == nil {
		server.Close()
		t.Fatal("startup accepted an unrelated nonempty directory")
	}
	data, err := os.ReadFile(precious)
	if err != nil || string(data) != contents {
		t.Fatalf("unrelated user data was changed: %v", err)
	}
	for _, name := range []string{".guiweb", ".lock", "guiweb.sqlite", "sites"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("failed startup created %s in an unrelated directory", name)
		}
	}
}

func TestNewRejectsInvalidMarkerAndManagedSymlinks(t *testing.T) {
	for _, name := range []string{".guiweb", ".lock", "guiweb.sqlite", "guiweb.sqlite-wal", "guiweb.sqlite-shm", "sites", "staging"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cfg := DevConfig(filepath.Join(root, "app-data"))
			server, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = server.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(cfg.DataDir, name)
			if err = os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "unrelated-target")
			precious := target
			contents := "do not modify unrelated data"
			if name == ".guiweb" {
				contents = "guiweb-v1\n"
			}
			if name == "sites" || name == "staging" {
				if err = os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				precious = filepath.Join(target, "keep.txt")
			}
			if err = os.WriteFile(precious, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			server, err = New(cfg)
			if err == nil {
				server.Close()
				t.Fatal("managed symlink was accepted")
			}
			if name != ".guiweb" && !strings.Contains(err.Error(), name) {
				t.Fatalf("startup failed for a different reason than the managed symlink: %v", err)
			}
			data, err := os.ReadFile(precious)
			if err != nil || string(data) != contents {
				t.Fatalf("startup modified the symlink target: %v", err)
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("startup modified the managed symlink: %v", err)
			}
		})
	}
	for _, marker := range []string{"", "other-application\n", "guiweb-v2\n"} {
		t.Run("marker-"+marker, func(t *testing.T) {
			cfg := DevConfig(t.TempDir())
			path := filepath.Join(cfg.DataDir, ".guiweb")
			if err := os.WriteFile(path, []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
			if server, err := New(cfg); err == nil {
				server.Close()
				t.Fatal("invalid marker was accepted")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != marker {
				t.Fatalf("invalid marker was overwritten: %v", err)
			}
		})
	}
}
