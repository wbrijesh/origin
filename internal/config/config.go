package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SSHConfig is the configuration for the SSH server.
type SSHConfig struct {
	ListenAddr  string `yaml:"listen_addr"`
	HostKeyPath string `yaml:"host_key_path"`
}

// HTTPConfig is the configuration for the HTTP server.
type HTTPConfig struct {
	ListenAddr  string `yaml:"listen_addr"`
	PublicURL   string `yaml:"public_url"`
	TLSCertPath string `yaml:"tls_cert_path"`
	TLSKeyPath  string `yaml:"tls_key_path"`
}

// Config is the top-level configuration for Origin.
type Config struct {
	Name     string     `yaml:"name"`
	DataPath string     `yaml:"data_path"`
	SSH      SSHConfig  `yaml:"ssh"`
	HTTP     HTTPConfig `yaml:"http"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Name:     "Origin",
		DataPath: "./data",
		SSH: SSHConfig{
			ListenAddr: ":22222",
		},
		HTTP: HTTPConfig{
			ListenAddr: ":3443",
			PublicURL:  "https://localhost:3443",
		},
	}
}

// Load reads the config from the given YAML file path, then applies
// environment variable overrides. If the file does not exist, defaults are used.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		if err := parseFile(cfg, path); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("config: %w", err)
			}
			// File not found — use defaults
		}
	}

	parseEnv(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return cfg, nil
}

func parseFile(cfg *Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return yaml.NewDecoder(f).Decode(cfg)
}

func parseEnv(cfg *Config) {
	if v := os.Getenv("ORIGIN_NAME"); v != "" {
		cfg.Name = v
	}
	if v := os.Getenv("ORIGIN_DATA_PATH"); v != "" {
		cfg.DataPath = v
	}
	if v := os.Getenv("ORIGIN_SSH_LISTEN_ADDR"); v != "" {
		cfg.SSH.ListenAddr = v
	}
	if v := os.Getenv("ORIGIN_SSH_HOST_KEY_PATH"); v != "" {
		cfg.SSH.HostKeyPath = v
	}
	if v := os.Getenv("ORIGIN_HTTP_LISTEN_ADDR"); v != "" {
		cfg.HTTP.ListenAddr = v
	}
	if v := os.Getenv("ORIGIN_HTTP_PUBLIC_URL"); v != "" {
		cfg.HTTP.PublicURL = v
	}
	if v := os.Getenv("ORIGIN_HTTP_TLS_CERT_PATH"); v != "" {
		cfg.HTTP.TLSCertPath = v
	}
	if v := os.Getenv("ORIGIN_HTTP_TLS_KEY_PATH"); v != "" {
		cfg.HTTP.TLSKeyPath = v
	}
}

// Validate checks the config for consistency and resolves relative paths
// to absolute paths based on the data directory.
func (c *Config) Validate() error {
	// Resolve data path to absolute
	if !filepath.IsAbs(c.DataPath) {
		abs, err := filepath.Abs(c.DataPath)
		if err != nil {
			return fmt.Errorf("resolve data_path: %w", err)
		}
		c.DataPath = abs
	}

	// Trim trailing slashes from public URL
	c.HTTP.PublicURL = strings.TrimSuffix(c.HTTP.PublicURL, "/")

	// Resolve SSH host key path relative to data dir
	if c.SSH.HostKeyPath != "" && !filepath.IsAbs(c.SSH.HostKeyPath) {
		c.SSH.HostKeyPath = filepath.Join(c.DataPath, c.SSH.HostKeyPath)
	}

	// Resolve TLS paths relative to data dir
	if c.HTTP.TLSCertPath != "" && !filepath.IsAbs(c.HTTP.TLSCertPath) {
		c.HTTP.TLSCertPath = filepath.Join(c.DataPath, c.HTTP.TLSCertPath)
	}
	if c.HTTP.TLSKeyPath != "" && !filepath.IsAbs(c.HTTP.TLSKeyPath) {
		c.HTTP.TLSKeyPath = filepath.Join(c.DataPath, c.HTTP.TLSKeyPath)
	}

	return nil
}

// ReposPath returns the path to the repositories directory.
func (c *Config) ReposPath() string {
	return filepath.Join(c.DataPath, "repos")
}

// SSHHostKeyPath returns the effective SSH host key path,
// defaulting to {data_path}/ssh/host_ed25519 if not configured.
func (c *Config) SSHHostKeyPath() string {
	if c.SSH.HostKeyPath != "" {
		return c.SSH.HostKeyPath
	}
	return filepath.Join(c.DataPath, "ssh", "host_ed25519")
}

// DBPath returns the path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataPath, "origin.db")
}

// EnsureDirectories creates the required data directories if they don't exist.
func (c *Config) EnsureDirectories() error {
	dirs := []string{
		c.DataPath,
		c.ReposPath(),
		filepath.Dir(c.SSHHostKeyPath()),
		filepath.Join(c.DataPath, "log"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// HasTLS returns true if TLS certificate and key paths are configured.
func (c *Config) HasTLS() bool {
	return c.HTTP.TLSCertPath != "" && c.HTTP.TLSKeyPath != ""
}
