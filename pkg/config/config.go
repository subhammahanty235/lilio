package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Lilio    LilioConfig     `json:"lilio"`
	Metadata *MetadataConfig `json:"metadata,omitempty"`
	Metrics  *MetricsConfig  `json:"metrics,omitempty"`
	Storages []StorageConfig `json:"storages"`
}

// LilioConfig holds core settings
type LilioConfig struct {
	ChunkSize         string        `json:"chunk_size"`
	ReplicationFactor int           `json:"replication_factor"`
	Quorum            *QuorumConfig `json:"quorum,omitempty"`
	MetadataPath      string        `json:"metadata_path"`
	APIPort           int           `json:"api_port"`
}

// QuorumConfig overrides the replica counts derived from replication_factor.
// Leave it out to let Lilio pick a majority for both W and R.
type QuorumConfig struct {
	N int `json:"N"`
	W int `json:"W"`
	R int `json:"R"`
}

// EffectiveN returns the configured N, or the replication factor when N is
// omitted - the common case, since a replica count different from the
// replication factor is rarely what anyone means.
func (q *QuorumConfig) EffectiveN(replicationFactor int) int {
	if q.N > 0 {
		return q.N
	}
	return replicationFactor
}

// MetadataConfig selects the metadata backend. Type is one of "local"
// (the default), "etcd", or "memory".
type MetadataConfig struct {
	Type  string               `json:"type"`
	Local *LocalMetadataConfig `json:"local,omitempty"`
	Etcd  *EtcdMetadataConfig  `json:"etcd,omitempty"`
}

type LocalMetadataConfig struct {
	Path string `json:"path"`
}

type EtcdMetadataConfig struct {
	Endpoints   []string `json:"endpoints"`
	Prefix      string   `json:"prefix,omitempty"`
	DialTimeout string   `json:"dial_timeout,omitempty"` // Go duration, e.g. "5s"
	Username    string   `json:"username,omitempty"`
	Password    string   `json:"password,omitempty"`
}

// MetricsConfig controls the metrics collector. Type is "prometheus"
// (the default) or "nouse" to disable collection.
type MetricsConfig struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type,omitempty"`
	Path    string `json:"path,omitempty"`
}

// DialTimeoutDuration parses DialTimeout, falling back to 5s when unset.
func (e *EtcdMetadataConfig) DialTimeoutDuration() (time.Duration, error) {
	if e.DialTimeout == "" {
		return 5 * time.Second, nil
	}
	return time.ParseDuration(e.DialTimeout)
}

// StorageConfig holds configuration for a single storage backend
type StorageConfig struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Priority int               `json:"priority"`
	Options  map[string]string `json:"options"`
}

func DefaultConfig() *Config {
	return &Config{
		Lilio: LilioConfig{
			ChunkSize: "1MB",
			// 3, not 2: with a replication factor of 2 the derived quorum is
			// N=2, W=2, R=2, so losing either backend fails every read and
			// every write. 3 is the smallest factor that tolerates one loss.
			ReplicationFactor: 3,
			MetadataPath:      "./lilio_data/metadata",
			APIPort:           8080,
		},
		Storages: []StorageConfig{},
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func LoadOrCreate(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := DefaultConfig()
		if err := cfg.Save(path); err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
		fmt.Printf("Created default config at: %s\n", path)
		return cfg, nil
	}

	return Load(path)
}

// Save saves configuration to a JSON file
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Lilio.ReplicationFactor < 1 {
		return fmt.Errorf("replication_factor must be at least 1")
	}

	if q := c.Lilio.Quorum; q != nil {
		// The W+R > N rule is enforced by the storage core, which owns it.
		// Checked here are the bounds that would otherwise fail silently at
		// runtime: a W above N can never be reached, so every write would fail.
		n := q.EffectiveN(c.Lilio.ReplicationFactor)
		if q.W < 1 || q.R < 1 {
			return fmt.Errorf("quorum W and R must both be at least 1")
		}
		if q.W > n {
			return fmt.Errorf("quorum W(%d) cannot exceed N(%d)", q.W, n)
		}
		if q.R > n {
			return fmt.Errorf("quorum R(%d) cannot exceed N(%d)", q.R, n)
		}
	}

	if m := c.Metadata; m != nil {
		switch m.Type {
		case "", "local", "memory":
		case "etcd":
			if m.Etcd == nil || len(m.Etcd.Endpoints) == 0 {
				return fmt.Errorf("metadata type etcd requires etcd.endpoints")
			}
			if _, err := m.Etcd.DialTimeoutDuration(); err != nil {
				return fmt.Errorf("invalid metadata etcd.dial_timeout: %w", err)
			}
		default:
			return fmt.Errorf("unknown metadata type: %s", m.Type)
		}
	}

	if m := c.Metrics; m != nil && m.Enabled {
		switch m.Type {
		case "", "prometheus", "nouse":
		default:
			return fmt.Errorf("unknown metrics type: %s", m.Type)
		}
	}

	names := make(map[string]bool)
	for _, s := range c.Storages {
		if s.Name == "" {
			return fmt.Errorf("storage name cannot be empty")
		}
		if names[s.Name] {
			return fmt.Errorf("duplicate storage name: %s", s.Name)
		}
		names[s.Name] = true

		validTypes := map[string]bool{
			"local": true, "gdrive": true, "dropbox": true, "s3": true, "sftp": true,
		}
		if !validTypes[s.Type] {
			return fmt.Errorf("invalid storage type: %s", s.Type)
		}
	}

	return nil
}

func ParseChunkSize(size string) (int, error) {
	size = strings.TrimSpace(size)
	if size == "" {
		return 1024 * 1024, nil
	}

	var value int
	var unit string

	n, _ := fmt.Sscanf(size, "%d%s", &value, &unit)
	if n == 0 {
		return 0, fmt.Errorf("invalid chunk size: %s", size)
	}

	if n == 1 {
		return value, nil
	}

	unit = strings.ToUpper(strings.TrimSpace(unit))
	switch unit {
	case "B":
		return value, nil
	case "KB", "K":
		return value * 1024, nil
	case "MB", "M":
		return value * 1024 * 1024, nil
	case "GB", "G":
		return value * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("unknown unit: %s", unit)
	}
}

// GetOption gets an option from storage config with default
func (s *StorageConfig) GetOption(key, defaultValue string) string {
	if s.Options == nil {
		return defaultValue
	}
	if val, ok := s.Options[key]; ok {
		return val
	}
	return defaultValue
}

func (c *Config) AddStorage(storage StorageConfig) error {
	for _, s := range c.Storages {
		if s.Name == storage.Name {
			return fmt.Errorf("storage already exists: %s", storage.Name)
		}
	}
	c.Storages = append(c.Storages, storage)
	return nil
}

func (c *Config) RemoveStorage(name string) error {
	for i, s := range c.Storages {
		if s.Name == name {
			c.Storages = append(c.Storages[:i], c.Storages[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("storage not found: %s", name)
}

// GetStorage gets a storage config by name
func (c *Config) GetStorage(name string) (*StorageConfig, error) {
	for _, s := range c.Storages {
		if s.Name == name {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("storage not found: %s", name)
}
