package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/viper"
)

const (
	insecureDefaultJWTSecret     = "change-me"
	insecureDefaultAdminPassword = "admin123"
	minJWTSecretLen              = 32
)

type Config struct {
	Role     string         `mapstructure:"role"`
	Listen   string         `mapstructure:"listen"`
	LogLevel string         `mapstructure:"log_level"`
	Master   MasterConfig   `mapstructure:"master"`
	Agent    AgentConfig    `mapstructure:"agent"`
	Relay    RelayConfig    `mapstructure:"relay"`
	EventBus EventBusConfig `mapstructure:"eventbus"`
}

type MasterConfig struct {
	Listen             string   `mapstructure:"listen"`
	DBPath             string   `mapstructure:"db_path"`
	JWTSecret          string   `mapstructure:"jwt_secret"`
	BYOKKEK            string   `mapstructure:"byok_kek"` // base64 32B; empty → HKDF derive from JWTSecret
	EnrollmentTokenTTL int      `mapstructure:"enrollment_token_ttl"`
	AdminUser          string   `mapstructure:"admin_user"`
	AdminPassword      string   `mapstructure:"admin_password"`
	ProxyURL           string   `mapstructure:"proxy_url"`
	PublicBaseURLs     []string `mapstructure:"public_base_urls"`
}

type AgentAddress struct {
	URL string `json:"url" mapstructure:"url"`
	Tag string `json:"tag" mapstructure:"tag"`
}

type AgentConfig struct {
	Listen              string            `mapstructure:"listen"`
	MasterURL           string            `mapstructure:"master_url"`
	EnrollmentToken     string            `mapstructure:"enrollment_token"`
	CredentialsFile     string            `mapstructure:"credentials_file"`
	FullSyncInterval    int               `mapstructure:"full_sync_interval"`
	ReportBufferSize    int               `mapstructure:"report_buffer_size"`
	ReportFlushInterval int               `mapstructure:"report_flush_interval"`
	HeartbeatInterval   int               `mapstructure:"heartbeat_interval"`
	RetryMax            int               `mapstructure:"retry_max"`
	HTTPAddresses       []AgentAddress    `mapstructure:"http_addresses"`
	Tags                string            `mapstructure:"tags"`
	ProxyURL            string            `mapstructure:"proxy_url"`
	PreferredAddrTag    string            `mapstructure:"preferred_address_tag"`
	Cache               AgentCacheConfig  `mapstructure:"cache"`
}

// AgentCacheConfig 控制 agent 端 LRU 缓存的容量与负缓存 TTL。
type AgentCacheConfig struct {
	TokenCapacity           int `mapstructure:"token_capacity"`
	UserCapacity            int `mapstructure:"user_capacity"`
	UserRoutingsCapacity    int `mapstructure:"user_routings_capacity"`
	PrivateChannelsCapacity int `mapstructure:"private_channels_capacity"`
	NegativeTTLSeconds      int `mapstructure:"negative_ttl_seconds"`
}

type RelayConfig struct {
	Timeout             int `mapstructure:"timeout"`
	MaxIdleConns        int `mapstructure:"max_idle_conns"`
	MaxIdleConnsPerHost int `mapstructure:"max_idle_conns_per_host"`
}

type EventBusConfig struct {
	Type    string `mapstructure:"type"`
	NatsURL string `mapstructure:"nats_url"`
}

type RuntimeConfig struct {
	RelayTimeout        int `mapstructure:"relay_timeout"`
	FullSyncInterval    int `mapstructure:"full_sync_interval"`
	ReportBufferSize    int `mapstructure:"report_buffer_size"`
	ReportFlushInterval int `mapstructure:"report_flush_interval"`
	HeartbeatInterval   int `mapstructure:"heartbeat_interval"`
	RetryMax            int `mapstructure:"retry_max"`
}

type FileConfig struct {
	LogLevel string        `mapstructure:"log_level"`
	Master   MasterConfig  `mapstructure:"master"`
	Agent    AgentConfig   `mapstructure:"agent"`
	Runtime  RuntimeConfig `mapstructure:"runtime"`
	Relay    RelayConfig   `mapstructure:"relay"`
}

type MasterRuntimeConfig struct {
	LogLevel string
	Master   MasterConfig
	Runtime  RuntimeConfig
	Relay    RelayConfig
}

type AgentRuntimeConfig struct {
	LogLevel string
	Agent    AgentConfig
	Runtime  RuntimeConfig
	Relay    RelayConfig
}

type MasterRuntimeProvider interface {
	ToMasterRuntimeConfig() *MasterRuntimeConfig
}

type AgentRuntimeProvider interface {
	ToAgentRuntimeConfig() *AgentRuntimeConfig
}

func (c *MasterRuntimeConfig) ToMasterRuntimeConfig() *MasterRuntimeConfig {
	if c == nil {
		return nil
	}

	masterCfg := c.Master
	masterCfg.Listen = normalizeDefault(masterCfg.Listen, ":8140")
	masterCfg.DBPath = normalizeDefault(masterCfg.DBPath, "./data/master.db")
	masterCfg.EnrollmentTokenTTL = normalizeDefaultInt(masterCfg.EnrollmentTokenTTL, 3600)
	masterCfg.AdminUser = normalizeDefault(masterCfg.AdminUser, "admin")

	runtimeCfg := normalizeRuntimeConfig(c.Runtime)

	return &MasterRuntimeConfig{
		LogLevel: normalizeDefault(c.LogLevel, "info"),
		Master:   masterCfg,
		Runtime:  runtimeCfg,
		Relay:    normalizeRelayConfig(c.Relay),
	}
}

func (c *Config) ToMasterRuntimeConfig() *MasterRuntimeConfig {
	if c == nil {
		return nil
	}

	masterCfg := c.Master
	masterCfg.Listen = normalizeDefault(masterCfg.Listen, c.Listen)
	masterCfg.Listen = normalizeDefault(masterCfg.Listen, ":8140")
	masterCfg.DBPath = normalizeDefault(masterCfg.DBPath, "./data/master.db?_pragma=journal_mode(WAL)")
	masterCfg.EnrollmentTokenTTL = normalizeDefaultInt(masterCfg.EnrollmentTokenTTL, 3600)
	masterCfg.AdminUser = normalizeDefault(masterCfg.AdminUser, "admin")

	runtimeCfg := normalizeRuntimeConfig(RuntimeConfig{
		RelayTimeout:        c.Relay.Timeout,
		FullSyncInterval:    c.Agent.FullSyncInterval,
		ReportBufferSize:    c.Agent.ReportBufferSize,
		ReportFlushInterval: c.Agent.ReportFlushInterval,
		HeartbeatInterval:   c.Agent.HeartbeatInterval,
		RetryMax:            c.Agent.RetryMax,
	})

	return &MasterRuntimeConfig{
		LogLevel: normalizeDefault(c.LogLevel, "info"),
		Master:   masterCfg,
		Runtime:  runtimeCfg,
		Relay:    normalizeRelayConfig(c.Relay),
	}
}

func (c *AgentRuntimeConfig) ToAgentRuntimeConfig() *AgentRuntimeConfig {
	if c == nil {
		return nil
	}

	agentCfg := c.Agent
	agentCfg.Listen = normalizeDefault(agentCfg.Listen, ":8139")
	agentCfg.CredentialsFile = normalizeDefault(agentCfg.CredentialsFile, "./data/agent_credentials.json")

	runtimeCfg := normalizeRuntimeConfig(c.Runtime)

	return &AgentRuntimeConfig{
		LogLevel: normalizeDefault(c.LogLevel, "info"),
		Agent:    agentCfg,
		Runtime:  runtimeCfg,
		Relay:    normalizeRelayConfig(c.Relay),
	}
}

func (c *Config) ToAgentRuntimeConfig() *AgentRuntimeConfig {
	if c == nil {
		return nil
	}

	agentCfg := c.Agent
	agentCfg.Listen = normalizeDefault(agentCfg.Listen, c.Listen)
	agentCfg.Listen = normalizeDefault(agentCfg.Listen, ":8139")
	agentCfg.CredentialsFile = normalizeDefault(agentCfg.CredentialsFile, "./data/agent_credentials.json")

	runtimeCfg := normalizeRuntimeConfig(RuntimeConfig{
		RelayTimeout:        c.Relay.Timeout,
		FullSyncInterval:    c.Agent.FullSyncInterval,
		ReportBufferSize:    c.Agent.ReportBufferSize,
		ReportFlushInterval: c.Agent.ReportFlushInterval,
		HeartbeatInterval:   c.Agent.HeartbeatInterval,
		RetryMax:            c.Agent.RetryMax,
	})

	return &AgentRuntimeConfig{
		LogLevel: normalizeDefault(c.LogLevel, "info"),
		Agent:    agentCfg,
		Runtime:  runtimeCfg,
		Relay:    normalizeRelayConfig(c.Relay),
	}
}

func Load(path string) (*Config, error) {
	viper.SetConfigFile(path)
	viper.SetDefault("role", "master")
	viper.SetDefault("listen", ":8140")
	viper.SetDefault("log_level", "info")
	viper.SetDefault("master.db_path", "./data/master.db?_pragma=journal_mode(WAL)")
	viper.SetDefault("master.enrollment_token_ttl", 3600)
	viper.SetDefault("agent.listen", ":8139")
	viper.SetDefault("agent.credentials_file", "./data/agent_credentials.json")
	viper.SetDefault("agent.full_sync_interval", 300)
	viper.SetDefault("agent.report_buffer_size", 1000)
	viper.SetDefault("agent.report_flush_interval", 5)
	viper.SetDefault("agent.heartbeat_interval", 30)
	viper.SetDefault("agent.retry_max", 5)
	viper.SetDefault("agent.cache.token_capacity", 20000)
	viper.SetDefault("agent.cache.user_capacity", 20000)
	viper.SetDefault("agent.cache.user_routings_capacity", 5000)
	viper.SetDefault("agent.cache.negative_ttl_seconds", 30)
	viper.SetDefault("relay.timeout", 300)
	viper.SetDefault("relay.max_idle_conns", 100)
	viper.SetDefault("relay.max_idle_conns_per_host", 10)
	viper.SetDefault("master.admin_user", "admin")
	viper.SetDefault("master.proxy_url", "")
	viper.SetDefault("eventbus.type", "memory")

	if err := viper.ReadInConfig(); err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			// File exists but couldn't be parsed
			return nil, err
		}
		// File doesn't exist — proceed with defaults
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadMaster(path string) (*MasterRuntimeConfig, error) {
	cfg, err := loadFileConfig(path)
	if err != nil {
		return nil, err
	}

	cfg.Runtime = normalizeRuntimeConfig(cfg.Runtime)
	cfg.Master.Listen = normalizeDefault(cfg.Master.Listen, ":8140")
	cfg.Master.DBPath = normalizeDefault(cfg.Master.DBPath, "./data/master.db")
	cfg.Master.EnrollmentTokenTTL = normalizeDefaultInt(cfg.Master.EnrollmentTokenTTL, 3600)
	cfg.Master.AdminUser = normalizeDefault(cfg.Master.AdminUser, "admin")

	runtimeCfg := &MasterRuntimeConfig{
		LogLevel: normalizeDefault(cfg.LogLevel, "info"),
		Master:   cfg.Master,
		Runtime:  cfg.Runtime,
		Relay:    normalizeRelayConfig(cfg.Relay),
	}

	if err := validateMaster(runtimeCfg); err != nil {
		return nil, err
	}

	return runtimeCfg, nil
}

func LoadAgent(path string) (*AgentRuntimeConfig, error) {
	cfg, err := loadFileConfig(path)
	if err != nil {
		return nil, err
	}

	cfg.Runtime = normalizeRuntimeConfig(cfg.Runtime)
	cfg.Agent.Listen = normalizeDefault(cfg.Agent.Listen, ":8139")
	cfg.Agent.CredentialsFile = normalizeDefault(cfg.Agent.CredentialsFile, "./data/agent_credentials.json")

	runtimeCfg := &AgentRuntimeConfig{
		LogLevel: normalizeDefault(cfg.LogLevel, "info"),
		Agent:    cfg.Agent,
		Runtime:  cfg.Runtime,
		Relay:    normalizeRelayConfig(cfg.Relay),
	}

	if err := validateAgent(runtimeCfg); err != nil {
		return nil, err
	}

	return runtimeCfg, nil
}

func loadFileConfig(path string) (*FileConfig, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetDefault("log_level", "info")
	v.SetDefault("master.listen", ":8140")
	v.SetDefault("master.db_path", "./data/master.db")
	v.SetDefault("master.enrollment_token_ttl", 3600)
	v.SetDefault("master.admin_user", "admin")
	v.SetDefault("master.proxy_url", "")
	v.SetDefault("agent.listen", ":8139")
	v.SetDefault("agent.credentials_file", "./data/agent_credentials.json")
	v.SetDefault("agent.full_sync_interval", 300)
	v.SetDefault("agent.report_buffer_size", 1000)
	v.SetDefault("agent.report_flush_interval", 5)
	v.SetDefault("agent.heartbeat_interval", 30)
	v.SetDefault("agent.retry_max", 3)
	v.SetDefault("agent.proxy_url", "")
	v.SetDefault("runtime.relay_timeout", 300)
	v.SetDefault("runtime.full_sync_interval", 300)
	v.SetDefault("runtime.report_buffer_size", 1000)
	v.SetDefault("runtime.report_flush_interval", 5)
	v.SetDefault("runtime.heartbeat_interval", 30)
	v.SetDefault("runtime.retry_max", 3)

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			if _, statErr := os.Stat(path); statErr == nil {
				return nil, err
			}
		}
	}

	var cfg FileConfig
	if err := v.UnmarshalExact(&cfg); err != nil {
		return nil, normalizeFileConfigError(err)
	}
	return &cfg, nil
}

func normalizeFileConfigError(err error) error {
	msg := err.Error()
	idx := strings.Index(msg, "invalid keys:")
	if idx == -1 {
		return err
	}

	keysPart := strings.TrimSpace(msg[idx+len("invalid keys:"):])
	for _, rawKey := range strings.Split(keysPart, ",") {
		key := strings.TrimSpace(rawKey)
		switch key {
		case "role":
			return fmt.Errorf(`unknown field "role": role is determined by subcommand, remove this key`)
		case "listen":
			return fmt.Errorf(`unknown field "listen": use master.listen or agent.listen instead of top-level listen`)
		case "eventbus":
			return fmt.Errorf(`unknown section "eventbus": event bus is no longer configurable`)
		}
	}

	return err
}

func hasValidCredentialsFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var creds struct {
		AgentID string `json:"agent_id"`
		Secret  string `json:"secret"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return false
	}

	return strings.TrimSpace(creds.AgentID) != "" && strings.TrimSpace(creds.Secret) != ""
}

func validateMaster(cfg *MasterRuntimeConfig) error {
	if cfg == nil {
		return fmt.Errorf("master runtime config is required")
	}
	secret := strings.TrimSpace(cfg.Master.JWTSecret)
	if secret == "" {
		return fmt.Errorf("master.jwt_secret is required")
	}
	if secret == insecureDefaultJWTSecret {
		return fmt.Errorf("master.jwt_secret is using the insecure default %q; set a strong secret (>= %d bytes)",
			insecureDefaultJWTSecret, minJWTSecretLen)
	}
	if len(secret) < minJWTSecretLen {
		return fmt.Errorf("master.jwt_secret too short (%d bytes); need >= %d", len(secret), minJWTSecretLen)
	}
	pw := strings.TrimSpace(cfg.Master.AdminPassword)
	if pw == "" {
		return fmt.Errorf("master.admin_password is required (cannot be empty)")
	}
	if pw == insecureDefaultAdminPassword {
		return fmt.Errorf("master.admin_password is using the insecure default %q; set a strong password in config or env",
			insecureDefaultAdminPassword)
	}
	seen := make(map[string]struct{}, len(cfg.Master.PublicBaseURLs))
	for i, raw := range cfg.Master.PublicBaseURLs {
		o, err := normalizePublicBaseURL(raw)
		if err != nil {
			return fmt.Errorf("master.public_base_urls[%d]: %w", i, err)
		}
		if _, dup := seen[o]; dup {
			return fmt.Errorf("master.public_base_urls: duplicate origin %q", o)
		}
		seen[o] = struct{}{}
	}
	return nil
}

// normalizePublicBaseURL 与 oauth.parseOrigin 等价的精简版本（避免 config 反向依赖 oauth 包）。
func normalizePublicBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty origin")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("host is empty")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("origin must not contain path, query, or fragment")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, nil
	}
	return scheme + "://" + host, nil
}

func validateAgent(cfg *AgentRuntimeConfig) error {
	if cfg == nil {
		return fmt.Errorf("agent runtime config is required")
	}
	if strings.TrimSpace(cfg.Agent.MasterURL) == "" {
		return fmt.Errorf("agent.master_url is required")
	}
	if !hasValidCredentialsFile(cfg.Agent.CredentialsFile) && strings.TrimSpace(cfg.Agent.EnrollmentToken) == "" {
		return fmt.Errorf("agent.enrollment_token is required when credentials_file is missing or invalid")
	}
	return nil
}

func normalizeRuntimeConfig(cfg RuntimeConfig) RuntimeConfig {
	cfg.RelayTimeout = normalizeDefaultInt(cfg.RelayTimeout, 300)
	cfg.FullSyncInterval = normalizeDefaultInt(cfg.FullSyncInterval, 300)
	cfg.ReportBufferSize = normalizeDefaultInt(cfg.ReportBufferSize, 1000)
	cfg.ReportFlushInterval = normalizeDefaultInt(cfg.ReportFlushInterval, 5)
	cfg.HeartbeatInterval = normalizeDefaultInt(cfg.HeartbeatInterval, 30)
	cfg.RetryMax = normalizeDefaultInt(cfg.RetryMax, 5)
	return cfg
}

func normalizeDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeDefaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

// normalizeRelayConfig 给 RelayConfig 字段填默认值。
// dead config 时代这些字段被 viper.SetDefault 设过，但 LoadAgent 不读；
// 走 LoadAgent 路径时这里做兜底，跟 viper 默认值保持一致。
func normalizeRelayConfig(c RelayConfig) RelayConfig {
	if c.Timeout <= 0 {
		c.Timeout = 300
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = 100
	}
	if c.MaxIdleConnsPerHost <= 0 {
		c.MaxIdleConnsPerHost = 10
	}
	return c
}
