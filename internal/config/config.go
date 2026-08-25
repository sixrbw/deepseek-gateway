package config

import (
	"fmt"
	"os"
	"time"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

var validate = validator.New()

type Config struct {
	Server       ServerConfig       `yaml:"server" validate:"required"`
	Database     DatabaseConfig     `yaml:"database" validate:"required"`
	JWT          JWTConfig          `yaml:"jwt" validate:"required"`
	Models       []ModelConfig      `yaml:"models" validate:"dive"`
	Policies     []PolicyConfig     `yaml:"quota_policies" validate:"dive"`
	Admin        AdminConfig        `yaml:"admin"`
	Logs         LogConfig          `yaml:"logs"`
	Frontend     FrontendConfig     `yaml:"frontend"`
	SSO          SSOConfig          `yaml:"sso"`
	ClientFilter ClientFilterConfig `yaml:"client_filter"`
}

// ClientFilterRule 客户端封禁规则
type ClientFilterRule struct {
	Name    string `yaml:"name" json:"name"`       // 规则显示名称
	Pattern string `yaml:"pattern" json:"pattern"` // User-Agent 子串匹配（不区分大小写）
	Enabled bool   `yaml:"enabled" json:"enabled"` // true = 封禁此类客户端
}

// ClientFilterConfig 客户端过滤配置
type ClientFilterConfig struct {
	Rules []ClientFilterRule `yaml:"rules" json:"rules"`
}

type ServerConfig struct {
	Port            int           `yaml:"port" validate:"required,min=1,max=65535"`
	Mode            string        `yaml:"mode" validate:"oneof=debug release test"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	IdleTimeout     time.Duration `yaml:"idle_timeout"`
	MaxHeaderBytes  int           `yaml:"max_header_bytes"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

type DatabaseConfig struct {
	Path string `yaml:"path" validate:"required"`
}

type JWTConfig struct {
	Secret      string `yaml:"secret" validate:"required,min=8"`
	ExpireHours int    `yaml:"expire_hours" validate:"required,min=1"`
}

type LogConfig struct {
	Path          string `yaml:"path"`
	RetentionDays int    `yaml:"retention_days" validate:"min=0"`
	LogPayloads   bool   `yaml:"log_payloads"`
	RawDumps      string `yaml:"raw_dumps" validate:"oneof=none error full"`
}

type FIMConfig struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	Mode           string `yaml:"mode" json:"mode"` // auto | native | prompt | disabled
	Prefix         string `yaml:"prefix" json:"prefix"`
	Suffix         string `yaml:"suffix" json:"suffix"`
	Middle         string `yaml:"middle" json:"middle"`
	SystemPrompt   string `yaml:"system_prompt" json:"system_prompt"`
	TrimWhitespace bool   `yaml:"trim_whitespace" json:"trim_whitespace"`
}

type ResponsesConfig struct {
	MaxOutputTokensField string `yaml:"max_output_tokens_field" json:"max_output_tokens_field"` // max_tokens | max_completion_tokens
	ReasoningEvent       string `yaml:"reasoning_event" json:"reasoning_event"`                 // summary | text
}

type ModelConfig struct {
	ID            string                 `yaml:"id" validate:"required"`
	Name          string                 `yaml:"name" validate:"required"`
	Description   string                 `yaml:"description"`
	Enabled       bool                   `yaml:"enabled"`
	ContextWindow int                    `yaml:"context_window" validate:"min=0"`
	ModelParams   map[string]interface{} `yaml:"model_params"`
	FIM           FIMConfig              `yaml:"fim"`
	Responses     ResponsesConfig        `yaml:"responses"`
	Backends      []BackendConfig        `yaml:"backends" validate:"dive"`
}

type BackendConfig struct {
	ID             string `yaml:"id" validate:"required"`
	BaseURL        string `yaml:"base_url" validate:"required,url"`
	APIKey         string `yaml:"api_key"`
	ModelName      string `yaml:"model_name"`
	SourcePlatform string `yaml:"source_platform,omitempty" json:"source_platform,omitempty"`
	SourceGroup    string `yaml:"source_group,omitempty" json:"source_group,omitempty"`
	Weight         int    `yaml:"weight" validate:"min=0"`
	Enabled        bool   `yaml:"enabled"`
	MaxConcurrency int    `yaml:"max_concurrency" validate:"min=0"` // 该后端最大并发请求数，0 表示不限制
}

type TimeRangeConfig struct {
	Start string `yaml:"start" validate:"omitempty,datetime=15:04"` // "HH:MM" 格式
	End   string `yaml:"end" validate:"omitempty,datetime=15:04"`   // "HH:MM" 格式
}

type PolicyConfig struct {
	Name                string            `yaml:"name" validate:"required"`
	RateLimit           int               `yaml:"rate_limit" validate:"min=0"`
	RateLimitWindow     int               `yaml:"rate_limit_window" validate:"min=0"`
	RequestQuotaDaily   int               `yaml:"request_quota_daily" validate:"min=0"`
	AvailableTimeRanges []TimeRangeConfig `yaml:"available_time_ranges" validate:"dive"`
	Models              []string          `yaml:"models"`
	Description         string            `yaml:"description"`
	DefaultModel        string            `yaml:"default_model"`
}

type AdminConfig struct {
	DefaultEmail    string `yaml:"default_email" validate:"omitempty,email"`
	DefaultPassword string `yaml:"default_password" validate:"omitempty,min=6"`
}

type FrontendConfig struct {
	FeedbackURL         string `yaml:"feedback_url" json:"feedback_url" validate:"omitempty,url"`
	DevManualURL        string `yaml:"dev_manual_url" json:"dev_manual_url" validate:"omitempty,url"`
	RegistrationEnabled bool   `yaml:"registration_enabled" json:"registration_enabled"`
}

type SSOConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Provider     string `yaml:"provider" validate:"required_if=Enabled true"`
	ClientID     string `yaml:"client_id" validate:"required_if=Enabled true"`
	ClientSecret string `yaml:"client_secret" validate:"required_if=Enabled true"`
	IssuerURL    string `yaml:"issuer_url" validate:"required_if=Enabled true"`
	EmailClaim   string `yaml:"email_claim"`
}

func (s SSOConfig) GetAuthorizeURL() string {
	if s.Provider == "azure" {
		return s.IssuerURL + "/oauth2/v2.0/authorize"
	}
	return s.IssuerURL + "/authorize"
}

func (s SSOConfig) GetTokenURL() string {
	if s.Provider == "azure" {
		return s.IssuerURL + "/oauth2/v2.0/token"
	}
	return s.IssuerURL + "/token"
}

// Validate 校验配置
func (c *Config) Validate() error {
	return validate.Struct(c)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Auto-create a minimal default config.yaml
			defaultConfig := []byte(`# Auto-generated minimal config.yaml
server:
  port: 8080
  mode: release
database:
  path: modelgate.db
jwt:
  secret: default-secret-change-in-production
  expire_hours: 24
`)
			if writeErr := os.WriteFile(path, defaultConfig, 0644); writeErr != nil {
				return nil, fmt.Errorf("config file not found, and failed to create default: %w", writeErr)
			}
			data = defaultConfig
		} else {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults
	setDefaults(&cfg)

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "release"
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 60 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 30 * time.Minute
	}
	if cfg.Server.IdleTimeout == 0 {
		cfg.Server.IdleTimeout = 300 * time.Second
	}
	if cfg.Server.MaxHeaderBytes == 0 {
		cfg.Server.MaxHeaderBytes = 1 << 20 // 1MB
	}
	if cfg.Server.ShutdownTimeout == 0 {
		cfg.Server.ShutdownTimeout = 30 * time.Second
	}
	if cfg.JWT.Secret == "" {
		cfg.JWT.Secret = "default-secret-change-in-production"
	}
	if cfg.JWT.ExpireHours == 0 {
		cfg.JWT.ExpireHours = 24
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "modelgate.db"
	}
	if cfg.Logs.Path == "" {
		cfg.Logs.Path = "logs"
	}
	if cfg.Logs.RetentionDays == 0 {
		cfg.Logs.RetentionDays = 7
	}
	if cfg.Logs.RawDumps == "" {
		cfg.Logs.RawDumps = "none"
	}
	if cfg.SSO.Enabled && cfg.SSO.EmailClaim == "" {
		cfg.SSO.EmailClaim = "email"
	}
	// 默认客户端封禁规则：Claude Code 默认封禁
	if cfg.ClientFilter.Rules == nil {
		cfg.ClientFilter.Rules = []ClientFilterRule{
			{
				Name:    "Claude Code",
				Pattern: "claude-code",
				Enabled: true,
			},
		}
	}
}
