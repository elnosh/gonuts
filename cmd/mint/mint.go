package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/elnosh/gonuts/mint"
	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("error loading .env file")
	}
	mintConfig, err := mint.ConfigFromEnv()
	if err != nil {
		log.Fatalf("error reading config: %v", err)
	}

	mintServer, err := mint.SetupMintServer(*mintConfig)
	if err != nil {
		log.Fatalf("error starting mint server: %v", err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		<-c
		mintServer.Shutdown()
	}()

	if err := mintServer.Start(); err != nil {
		log.Fatalf("error running mint: %v\n", err)
	}
}
