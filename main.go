package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/containrrr/watchtower/internal/dockit"
	log "github.com/sirupsen/logrus"
)

func init() {
	log.SetLevel(log.InfoLevel)
}

func main() {
	version := flag.Bool("version", false, "Print version and upstream baseline")
	flag.Parse()
	if *version {
		fmt.Println("Dockit development (Watchtower baseline ca0e86e)")
		return
	}
	if flag.NArg() != 0 {
		log.Error("Dockit uses environment variables and container labels; positional arguments are not supported")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := dockit.Run(ctx); err != nil {
		log.Error(err)
		os.Exit(1)
	}
}
