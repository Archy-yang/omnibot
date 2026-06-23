package config

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config 应用配置结构体
type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Wechat   WechatConfig   `mapstructure:"wechat"`
	Feishu   FeishuConfig   `mapstructure:"feishu"`
	LLM      LLMConfig      `mapstructure:"llm"`
	Memory   MemoryConfig   `mapstructure:"memory"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Logger   LoggerConfig   `mapstructure:"logger"`
	Database DatabaseConfig `mapstructure:"database"`
}

// AppConfig 应用基本配置
type AppConfig struct {
	Name string `mapstructure:"name"`
	Env  string `mapstructure:"env"`
	Port int    `mapstructure:"port"`
}

// WechatConfig 微信公众号配置
type WechatConfig struct {
	AppID          string `mapstructure:"app_id"`
	AppSecret      string `mapstructure:"app_secret"`
	Token          string `mapstructure:"token"`
	EncodingAESKey string `mapstructure:"encoding_aes_key"`
	CallbackURL    string `mapstructure:"callback_url"`
}

// FeishuConfig 飞书机器人配置(v1.6)。
// 通过长连接(WebSocket)接收消息,无需公网回调地址;凭证仅 config.yaml 内存,
// 不入库不日志(安全红线)。enabled=false 时跳过飞书 channel 初始化,
// 不影响 Web/微信正常启动(开发态友好)。
type FeishuConfig struct {
	AppID     string `mapstructure:"app_id"`
	AppSecret string `mapstructure:"app_secret"`
	Enabled   bool   `mapstructure:"enabled"`
}

// LLMConfig 大模型配置
type LLMConfig struct {
	Providers map[string]ProviderConfig `mapstructure:"providers"`
	Routing   LMRoutingConfig           `mapstructure:"routing"`
}

// ProviderConfig 单个LLM提供商配置
type ProviderConfig struct {
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"`
	Model   string `mapstructure:"model"`
	Timeout string `mapstructure:"timeout"`
}

// LMRoutingConfig LLM路由配置
type LMRoutingConfig struct {
	Default       string   `mapstructure:"default"`
	FallbackOrder []string `mapstructure:"fallback_order"`
}

// MemoryConfig 记忆系统配置
type MemoryConfig struct {
	Extraction ExtractionConfig `mapstructure:"extraction"`
	Storage    StorageConfig    `mapstructure:"storage"`
}

// ExtractionConfig 记忆提取配置
type ExtractionConfig struct {
	Enabled   bool `mapstructure:"enabled"`
	BatchSize int  `mapstructure:"batch_size"`
}

// StorageConfig 记忆存储配置
type StorageConfig struct {
	VectorDB VectorDBConfig `mapstructure:"vector_db"`
	Postgres PostgresConfig `mapstructure:"postgres"`
}

// VectorDBConfig 向量数据库配置
type VectorDBConfig struct {
	URL    string `mapstructure:"url"`
	APIKey string `mapstructure:"api_key"`
}

// PostgresConfig PostgreSQL配置
type PostgresConfig struct {
	DSN      string `mapstructure:"dsn"`
	MaxConns int    `mapstructure:"max_conns"`
}

// RedisConfig Redis配置
type RedisConfig struct {
	URL      string `mapstructure:"url"`
	PoolSize int    `mapstructure:"pool_size"`
}

// LoggerConfig 日志配置
type LoggerConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Driver   string `mapstructure:"driver"`   // sqlite, mysql
	DSN      string `mapstructure:"dsn"`      // 连接字符串
	MaxConns int    `mapstructure:"max_conns"`
}

// Load 加载配置文件
func Load(configPath string) (*Config, error) {
	// 创建Viper实例
	v := viper.New()

	// 如果没有指定配置文件路径，则使用默认路径
	if configPath == "" {
		configDir := "configs"
		configFile := "config.yaml"
		configPath = filepath.Join(configDir, configFile)

		// 检查配置文件是否存在，如果不存在则使用示例配置
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			// 使用示例配置文件
			configPath = filepath.Join(configDir, "config.example.yaml")
		}
	}

	v.SetConfigFile(configPath)

	// 读取环境变量
	v.AutomaticEnv()
	v.SetEnvPrefix("WECHAT_BOT")
	v.AllowEmptyEnv(true)

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	// 解析配置到结构体
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}
