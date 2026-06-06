package main

import (
	"context"
	"flag"

	"github.com/FFH255/mag-1-2-tip/services/auth/internal/app"
)

func main() {
	envFilePath := flag.String("env-file", "services/auth/.env", "path to .env file")
	flag.Parse()

	app.New(*envFilePath).Run(context.Background())
}
