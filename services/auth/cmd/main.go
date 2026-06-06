package main

import (
	"context"

	"github.com/FFH255/mag-1-2-tip/services/auth/internal/app"
)

func main() {
	app.New().Run(context.Background())
}
