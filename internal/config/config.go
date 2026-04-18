package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Cache     CacheConfig     `mapstructure:"cache"`
	JWT       JWTConfig       `mapstructure:"jwt"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Document  DocumentConfig  `mapstructure:"document"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	AI        AIConfig        `mapstructure:"ai"`
	AITask    AITaskConfig    `mapstructure:"ai_task"`
	Agent     AgentConfig     `mapstructure:"agent"`
	Log       LogConfig       `mapstructure:"log"`
}

// LogConfig controls file-based log output.
type LogConfig struct {
	// Dir is the directory where daily log files are stored (e.g. "./logs").
	// File names follow the pattern app-YYYY-MM-DD.log.
	Dir     string `mapstructure:"dir"`
	Enabled bool   `mapstructure:"enabled"`
}

type AIConfig struct {
	// Legacy single-provider settings (kept for backward compatibility).
	Endpoint     string `mapstructure:"endpoint"`
	APIKey       string `mapstructure:"api_key"`
	DefaultModel string `mapstructure:"default_model"`
	// ChatModel is the model used exclusively for AI chat (POST /ai/chat and /ai/chat/stream).
	// Defaults to DefaultModel when empty.
	ChatModel string `mapstructure:"chat_model"`

	// MaxCompletionTokens controls request-level max_completion_tokens.
	// nil = use provider defaults; 0 = disable the field; >0 = explicitly set.
	MaxCompletionTokens *int           `mapstructure:"max_completion_tokens"`
	ModelMaxCompletion  map[string]int `mapstructure:"model_max_completion_tokens"`

	// Multi-provider settings (qwen/mimo, etc.).
	DefaultProvider string                      `mapstructure:"default_provider"`
	Providers       map[string]AIProviderConfig `mapstructure:"providers"`
	Models          []AIModelConfig             `mapstructure:"models"`

	Timeout time.Duration `mapstructure:"timeout"`
}

type AIModelConfig struct {
	// Value is the model token sent by clients (e.g. "qwen3.5-flash" or "mimo:mimo-thinking").
	Value string `mapstructure:"value"`
	// Model is an alias for Value in config.
	Model string `mapstructure:"model"`
	Label string `mapstructure:"label"`
	// Provider can be used to auto-prefix model values for routing (qwen/mimo).
	Provider    string `mapstructure:"provider"`
	Recommended bool   `mapstructure:"recommended"`
}

type AIProviderConfig struct {
	Endpoint     string `mapstructure:"endpoint"`
	APIKey       string `mapstructure:"api_key"`
	DefaultModel string `mapstructure:"default_model"`
	ChatModel    string `mapstructure:"chat_model"`

	// ModelPrefixes are used by the router to select this provider by requested model name.
	ModelPrefixes []string `mapstructure:"model_prefixes"`

	// nil = fallback to global/default behavior; 0 = disable; >0 = force set.
	MaxCompletionTokens *int           `mapstructure:"max_completion_tokens"`
	ModelMaxCompletion  map[string]int `mapstructure:"model_max_completion_tokens"`
}

// FaultTreeNodeDefaultsConfig holds default field values applied to each AI-generated
// fault tree node when the AI returns an empty or zero value for that field.
type FaultTreeNodeDefaultsConfig struct {
	// Width / Height: default node dimensions in pixels (applied when AI returns 0 or negative).
	Width  float64 `mapstructure:"width"`
	Height float64 `mapstructure:"height"`

	// Priority: default node priority (applied when AI returns 0).
	Priority int `mapstructure:"priority"`

	// ShowProbability: whether to show probability on new nodes by default.
	ShowProbability bool `mapstructure:"show_probability"`

	// ErrorLevel: default error level string (applied when AI returns empty).
	// Leave blank to keep the field unset (nil).
	ErrorLevel string `mapstructure:"error_level"`

	// InvestigateMethod: default investigation method (applied when AI returns empty).
	// Leave blank to keep the field unset (nil).
	InvestigateMethod string `mapstructure:"investigate_method"`

	// Description: default description (applied when AI returns empty).
	// Leave blank to keep the field unset (nil).
	Description string `mapstructure:"description"`
}

// AITaskConfig holds queue and callback settings for async AI generation tasks.
type AITaskConfig struct {
	Stream         string `mapstructure:"stream"`
	StreamMaxLen   int64  `mapstructure:"stream_max_len"`
	CallbackHeader string `mapstructure:"callback_header"`
	CallbackToken  string `mapstructure:"callback_token"`

	ProducerStream      string        `mapstructure:"producer_stream"`
	ProducerGroup       string        `mapstructure:"producer_group"`
	ProducerReadCount   int64         `mapstructure:"producer_read_count"`
	ProducerBlockMs     int64         `mapstructure:"producer_block_ms"`
	DispatcherWorkers   int           `mapstructure:"dispatcher_workers"`
	ProducerDelayedZSet string        `mapstructure:"producer_delayed_zset"`
	ProducerRetryDelay  int64         `mapstructure:"producer_retry_delay_ms"`
	ProjectLockTTL      time.Duration `mapstructure:"project_lock_ttl"`
	CallbackDedupeTTL   time.Duration `mapstructure:"callback_dedupe_ttl"`
	SnapshotTTL         time.Duration `mapstructure:"snapshot_ttl"`

	// NodeDefaults contains per-field default values for AI-generated fault tree nodes.
	NodeDefaults FaultTreeNodeDefaultsConfig `mapstructure:"node_defaults"`
}

// AgentConfig controls mixed agent execution behavior.
type AgentConfig struct {
	Enabled                  bool          `mapstructure:"enabled"`
	MaxRounds                int           `mapstructure:"max_rounds"`
	MaxToolCalls             int           `mapstructure:"max_tool_calls"`
	MaxNodesPerSession       int           `mapstructure:"max_nodes_per_session"`
	ConfirmTimeout           time.Duration `mapstructure:"confirm_timeout"`
	PreviewTimeout           time.Duration `mapstructure:"preview_timeout"`
	SessionTTL               time.Duration `mapstructure:"session_ttl"`
	ToolCallRateLimit        int           `mapstructure:"tool_call_rate_limit"`
	EnableFallbackParser     bool          `mapstructure:"enable_fallback_parser"`
	EnablePlannerPhase       bool          `mapstructure:"enable_planner_phase"`
	EnableLoopSoftWarning    bool          `mapstructure:"enable_loop_soft_warning"`
	AgentModel               string        `mapstructure:"agent_model"`
	PromptVersion            string        `mapstructure:"prompt_version"`
	IncludeHybridTools       bool          `mapstructure:"include_hybrid_tools"`
	MaxToolSummaryChars      int           `mapstructure:"max_tool_summary_chars"`
	FullContextNodeThreshold int           `mapstructure:"full_context_node_threshold"`
}

type ServerConfig struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	Mode         string        `mapstructure:"mode"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type DatabaseConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"`
	DBName          string        `mapstructure:"dbname"`
	SSLMode         string        `mapstructure:"sslmode"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

type RedisConfig struct {
	Addr         string `mapstructure:"addr"`
	Password     string `mapstructure:"password"`
	DB           int    `mapstructure:"db"`
	MaxRetries   int    `mapstructure:"max_retries"`
	PoolSize     int    `mapstructure:"pool_size"`
	MinIdleConns int    `mapstructure:"min_idle_conns"`
}

type CacheConfig struct {
	Enabled               bool          `mapstructure:"enabled"`
	ProjectDetailEnabled  bool          `mapstructure:"project_detail_enabled"`
	ProjectListEnabled    bool          `mapstructure:"project_list_enabled"`
	VersionListEnabled    bool          `mapstructure:"version_list_enabled"`
	FaultTreeGraphEnabled bool          `mapstructure:"fault_tree_graph_enabled"`
	KnowledgeGraphEnabled bool          `mapstructure:"knowledge_graph_enabled"`
	ProjectDetailTTL      time.Duration `mapstructure:"project_detail_ttl"`
	ProjectListTTL        time.Duration `mapstructure:"project_list_ttl"`
	ProjectListIndexTTL   time.Duration `mapstructure:"project_list_index_ttl"`
	VersionListTTL        time.Duration `mapstructure:"version_list_ttl"`
	VersionListIndexTTL   time.Duration `mapstructure:"version_list_index_ttl"`
	FaultTreeGraphTTL     time.Duration `mapstructure:"fault_tree_graph_ttl"`
	KnowledgeGraphTTL     time.Duration `mapstructure:"knowledge_graph_ttl"`
}

type JWTConfig struct {
	Secret            string        `mapstructure:"secret"`
	AccessExpire      time.Duration `mapstructure:"access_expire"`
	RefreshExpire     time.Duration `mapstructure:"refresh_expire"`
	RefreshExpireLong time.Duration `mapstructure:"refresh_expire_long"`
}

type StorageConfig struct {
	LocalPath         string   `mapstructure:"local_path"`
	BaseURL           string   `mapstructure:"base_url"`
	MaxFileSize       int64    `mapstructure:"max_file_size"`
	AllowedImageTypes []string `mapstructure:"allowed_image_types"`
	AllowedDocTypes   []string `mapstructure:"allowed_doc_types"`
}

type DocumentConfig struct {
	ConversionWorkers      int           `mapstructure:"conversion_workers"`
	ConversionPollInterval time.Duration `mapstructure:"conversion_poll_interval"`
}

type RateLimitConfig struct {
	Enabled           bool    `mapstructure:"enabled"`
	RequestsPerSecond float64 `mapstructure:"requests_per_second"`
	Burst             int     `mapstructure:"burst"`
}

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath(".")

	viper.SetEnvPrefix("OPTITREE")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.SetDefault("agent.enable_planner_phase", true)
	viper.SetDefault("agent.enable_loop_soft_warning", true)

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
