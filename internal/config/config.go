package config

import "os"

type Config struct {
	BaseURL string
	JWTSecret string
}

func LoadConfig() *Config {
	return &Config{
		BaseURL: os.Getenv("BASE_URL"),
		JWTSecret: os.Getenv("JWT_SECRET"),
	}
}