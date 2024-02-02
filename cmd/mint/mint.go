package main

import (
	"log"

	"github.com/elnosh/gonuts/mint"
	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("error loading .env file")
	}
	mintConfig := mint.GetConfig()

	mintServer, err := mint.SetupMintServer(mintConfig)
	if err != nil {
		log.Fatalf("error starting mint server: %v", err)
	}

	mint.StartMintServer(mintServer)
}
