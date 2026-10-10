package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const MiB int64 = 1 << 20

type Config struct {
	Listen          string   `json:"listen"`
	DataDir         string   `json:"data_dir"`
	AdminHost       string   `json:"admin_host"`
	BaseDomain      string   `json:"base_domain"`
	Scheme          string   `json:"scheme"`
	Dev             bool     `json:"dev"`
	TrustCloudflare bool     `json:"trust_cloudflare"`
	UploadBytes     int64    `json:"upload_bytes"`
	SiteBytes       int64    `json:"site_bytes"`
	TotalBytes      int64    `json:"total_bytes"`
	UserBytes       int64    `json:"user_bytes"`
	MinFreeBytes    int64    `json:"min_free_bytes"`
	MaxFiles        int      `json:"max_files"`
	Reserved        []string `json:"reserved"`
}

func DevConfig(dir string) Config {
	return Config{Listen: "127.0.0.1:8088", DataDir: dir, AdminHost: "app.localhost", BaseDomain: "localhost", Scheme: "http", Dev: true}
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65536))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return cfg, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return cfg, errors.New("config phải chỉ chứa một đối tượng JSON")
	}
	return cfg.Validated()
}

var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validHost(h string) bool {
	if len(h) == 0 || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if !domainLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func (c Config) Validated() (Config, error) {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8088"
	}
	if c.DataDir == "" {
		return c, errors.New("data_dir là bắt buộc")
	}
	if !filepath.IsAbs(c.DataDir) {
		return c, errors.New("data_dir phải là đường dẫn tuyệt đối")
	}
	c.DataDir = filepath.Clean(c.DataDir)
	if c.DataDir == "/" {
		return c, errors.New("không dùng thư mục gốc làm data_dir")
	}
	if !validHost(c.AdminHost) || !validHost(c.BaseDomain) || c.AdminHost == c.BaseDomain {
		return c, errors.New("admin_host/base_domain không hợp lệ")
	}
	if c.Scheme == "" {
		c.Scheme = "https"
	}
	if (!c.Dev && c.Scheme != "https") || (c.Dev && c.Scheme != "http") {
		return c, errors.New("production yêu cầu https, dev yêu cầu http")
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return c, fmt.Errorf("listen không hợp lệ: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return c, errors.New("listen cần cổng từ 1 đến 65535")
	}
	// Development cookies are deliberately HTTP-only for loopback workstations.
	if c.Dev && host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return c, errors.New("dev chỉ lắng nghe địa chỉ loopback")
	}
	if c.Dev && (!strings.HasSuffix(c.AdminHost, ".localhost") || c.BaseDomain != "localhost") {
		return c, errors.New("dev dùng app.localhost và localhost")
	}
	if c.TrustCloudflare && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return c, errors.New("trust_cloudflare yêu cầu listener IP loopback, cloudflared cùng máy")
	}
	if c.UploadBytes == 0 {
		c.UploadBytes = 50 * MiB
	}
	if c.SiteBytes == 0 {
		c.SiteBytes = 100 * MiB
	}
	if c.TotalBytes == 0 {
		c.TotalBytes = 512 * MiB
	}
	if c.UserBytes == 0 {
		c.UserBytes = 128 * MiB
	}
	if c.MinFreeBytes == 0 && !c.Dev {
		c.MinFreeBytes = 512 * MiB
	}
	if c.MaxFiles == 0 {
		c.MaxFiles = 5000
	}
	if c.UploadBytes < 1 || c.SiteBytes < 1 || c.TotalBytes < 1 || c.UserBytes < 1 || c.MinFreeBytes < 0 || c.MaxFiles < 1 || c.MaxFiles > 100000 {
		return c, errors.New("giới hạn dung lượng/số file không hợp lệ")
	}
	if c.UploadBytes > 1<<30 || c.SiteBytes > 1<<30 {
		return c, errors.New("giới hạn upload/website tối đa 1 GiB")
	}
	c.Reserved = append([]string{"app", "www", "api", "admin", "mail", "localhost"}, c.Reserved...)
	if strings.HasSuffix(c.AdminHost, "."+c.BaseDomain) {
		c.Reserved = append(c.Reserved, strings.TrimSuffix(c.AdminHost, "."+c.BaseDomain))
	}
	return c, nil
}
