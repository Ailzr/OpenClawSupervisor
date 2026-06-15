package main

import (
	"encoding/json"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
)

type AppConfig struct {
	AutoStart    bool `json:"auto_start"`
	TargetStatus bool `json:"target_status"` // 记录是否应该运行 openclaw
	Port         int  `json:"port"`
	Interval     int  `json:"interval"`
}

const configFileName = "supervisor_config.json"
const registryKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const registryAppName = "OpenClawSupervisor"

func getConfigFile() string {
	execPath, _ := os.Executable()
	return filepath.Join(filepath.Dir(execPath), configFileName)
}

func LoadConfig() AppConfig {
	cfg := AppConfig{AutoStart: false, TargetStatus: false, Port: 18789, Interval: 15}
	file, err := os.ReadFile(getConfigFile())
	if err == nil {
		json.Unmarshal(file, &cfg)
	}
	return cfg
}

func SaveConfig(cfg AppConfig) {
	configPath := getConfigFile()
	configDir := filepath.Dir(configPath)

	// 确保目录存在（首次运行或从临时目录启动时目录可能不存在）
	if err := os.MkdirAll(configDir, 0755); err != nil {
		// 目录创建失败，尝试写入当前工作目录
		configPath = filepath.Join(".", configFileName)
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		// 最后的保险：写到一个确定能写的位置
		os.WriteFile(configFileName, data, 0644)
	}

	// 处理注册表自启（失败不影响主流程）
	execPath, _ := os.Executable()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, registryKeyPath, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()

	if cfg.AutoStart {
		_ = k.SetStringValue(registryAppName, `"`+execPath+`"`)
	} else {
		_ = k.DeleteValue(registryAppName)
	}
}
