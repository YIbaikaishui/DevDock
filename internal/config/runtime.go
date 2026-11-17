package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Runtime 是运行中实例的交接信息，供 CLI / MCP / 第二实例发现服务。
// 只存最小必要信息，退出时删除。
type Runtime struct {
	Port         int    `json:"port"`
	Token        string `json:"token"`
	PID          uint32 `json:"pid"`
	CreationTime uint64 `json:"creationTime"`
	Version      string `json:"version"`
	StartedAt    string `json:"startedAt"`
}

// runtimePath 返回 runtime.json 路径。
func runtimePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runtime.json"), nil
}

// WriteRuntime 写入运行态交接文件。
func WriteRuntime(rt Runtime) error {
	path, err := runtimePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(rt, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 runtime.json 失败: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入 runtime.json 失败: %w", err)
	}
	return nil
}

// ReadRuntime 读取运行态交接文件。不存在时返回 (nil, nil)。
func ReadRuntime() (*Runtime, error) {
	path, err := runtimePath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 runtime.json 失败: %w", err)
	}
	var rt Runtime
	if err := json.Unmarshal(raw, &rt); err != nil {
		// 损坏的 runtime.json 不是致命问题，当作不存在。
		return nil, nil
	}
	return &rt, nil
}

// RemoveRuntime 删除运行态交接文件。
func RemoveRuntime() {
	path, err := runtimePath()
	if err != nil {
		return
	}
	os.Remove(path)
}
