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
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(getConfigFile(), data, 0644)

	// 处理注册表自启
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
