package app

import (
	"fmt"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddress             string        `env:"HTTP_ADDRESS"`
	JWTSecret               string        `env:"JWT_SECRET"`
	GracefulShutdownTimeout time.Duration `env:"GRACEFUL_SHUTDOWN_TIMEOUT"`
	ReadTimeout             time.Duration `env:"READ_TIMEOUT"`
	WriteTimeout            time.Duration `env:"WRITE_TIMEOUT"`
	IdleTimeout             time.Duration `env:"IDLE_TIMEOUT"`
}

func parseConfig(path string) (*Config, error) {
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to stat .env file %q: %v", path, err)
		}

		return nil, fmt.Errorf(".env file %q does not exist", path)
	}

	if err := godotenv.Load(path); err != nil {
		return nil, fmt.Errorf("failed to load .env file %q: %v", path, err)
	}

	var config Config
	if err := env.Parse(&config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	return &config, nil
}
