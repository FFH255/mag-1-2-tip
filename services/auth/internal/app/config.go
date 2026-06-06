package app

import (
	"log"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddress             string        `env:"HTTP_ADDRESS" envDefault:":8080"`
	JWTSecret               string        `env:"JWT_SECRET" envDefault:"dev-secret"`
	JWTExpirationDuration   time.Duration `env:"JWT_EXPIRATION_DURATION" envDefault:"24h"`
	GracefulShutdownTimeout time.Duration `env:"GRACEFUL_SHUTDOWN_TIMEOUT" envDefault:"5s"`
	ReadTimeout             time.Duration `env:"READ_TIMEOUT" envDefault:"5s"`
	WriteTimeout            time.Duration `env:"WRITE_TIMEOUT" envDefault:"10s"`
	IdleTimeout             time.Duration `env:"IDLE_TIMEOUT" envDefault:"60s"`
}

func newConfig() *Config {
	for _, path := range []string{".env", "services/auth/.env"} {
		if _, err := os.Stat(path); err != nil {
			if !os.IsNotExist(err) {
				log.Fatalf("failed to stat .env file %q: %v", path, err)
			}
			continue
		}
		if err := godotenv.Load(path); err != nil {
			log.Fatalf("failed to load .env file %q: %v", path, err)
		}
	}

	var config Config
	if err := env.Parse(&config); err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	return &config
}
