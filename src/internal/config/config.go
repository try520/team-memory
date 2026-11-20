package config

import (
	"os"
	"strings"
)

type Config struct {
	DataDir string
	Addr    string
}

// Load 读取环境变量：DATA_DIR（默认 ./data）、ADDR 或 PORT（默认 8080）。
func Load() Config {
	data := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if data == "" {
		data = "./data"
	}
	addr := strings.TrimSpace(os.Getenv("ADDR"))
	if addr == "" {
		port := strings.TrimSpace(os.Getenv("PORT"))
		if port == "" {
			port = "8080"
		}
		addr = ":" + port
	}
	return Config{DataDir: data, Addr: addr}
}
