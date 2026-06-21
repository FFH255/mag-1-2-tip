package config

import "os"

type Config struct {
	DB_DSN     string
	BcryptCost int // например, 12
	Addr       string
}

func Load() Config {
	cost := 12
	if v := os.Getenv("BCRYPT_COST"); v != "" {
		// необязательно: распарсить int, при ошибке оставить 12
	}
	addr := os.Getenv("APP_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	return Config{
		DB_DSN:     os.Getenv("DB_DSN"), // например: postgres://user:pass@localhost:5432/pz9?sslmode=disable
		BcryptCost: cost,
		Addr:       addr,
	}
}
