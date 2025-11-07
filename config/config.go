package config

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"

	"go_proxy/fetcher"
)

// AppConfig 应用程序配置结构体
type AppConfig struct {
	MaxLatency         float64               `json:"max_latency"`
	MinSpeed           float64               `json:"min_speed"`
	RotationInterval   int                   `json:"rotation_interval"`
	ThemeName          string                `json:"theme_name"`
	ProxySources       []fetcher.ProxySource `json:"proxy_sources"`
	ProxyMode          string                `json:"proxy_mode"`          // 代理模式: per_request 或 fixed
	HealthCheckInterval int                  `json:"health_check_interval"` // 健康检查间隔（分钟）
	AllowedCountries   []string              `json:"allowed_countries"`   // 允许的国家列表
}

// NewDefaultConfig 创建默认配置
func NewDefaultConfig() *AppConfig {
	return &AppConfig{
		MaxLatency:         -1,    // No limit
		MinSpeed:           -1,    // No limit
		RotationInterval:   60,    // 60 seconds
		ThemeName:          "自定义", // Default theme
		ProxySources:       fetcher.GetDefaultSources(),
		ProxyMode:          "fixed", // 默认使用固定模式
		HealthCheckInterval: 5,     // 默认5分钟检查一次
		AllowedCountries:   []string{}, // 默认不限制国家
	}
}

// configFilePath 获取配置文件的路径
func configFilePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configDir := filepath.Join(homeDir, ".go_proxy")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(configDir, "config.json"), nil
}

// LoadConfig 从文件加载配置
func LoadConfig() (*AppConfig, error) {
	path, err := configFilePath()
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewDefaultConfig(), nil // Return default if file not found
		}
		return nil, err
	}

	var config AppConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// SaveConfig 将配置保存到文件
func SaveConfig(cfg *AppConfig) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return ioutil.WriteFile(path, data, 0644)
}
