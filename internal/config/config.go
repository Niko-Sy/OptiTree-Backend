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
	JWT       JWTConfig       `mapstructure:"jwt"`
	Storage   StorageConfig   `mapstructure:"storage"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	AI        AIConfig        `mapstructure:"ai"`
	AITask    AITaskConfig    `mapstructure:"ai_task"`
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
	Endpoint     string `mapstructure:"endpoint"`
	APIKey       string `mapstructure:"api_key"`
	DefaultModel string `mapstructure:"default_model"`
	// ChatModel is the model used exclusively for AI chat (POST /ai/chat and /ai/chat/stream).
	// Defaults to DefaultModel when empty.
	ChatModel string        `mapstructure:"chat_model"`
	Timeout   time.Duration `mapstructure:"timeout"`
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

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
