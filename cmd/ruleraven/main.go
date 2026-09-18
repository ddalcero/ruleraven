package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ddalcero/ruleraven/internal/health"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := health.NewServer(":8080", 5*time.Second, 5*time.Second, 30*time.Second)
	if err := server.Run(ctx); err != nil {
		log.Printf("health server failed: %v", err)
		os.Exit(1)
	}
}
