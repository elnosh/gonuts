package main

import (
	"log"

	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/mint/config"
)

const configPath = "../mint/config/config.json"

func main() {
	mintConfig := config.GetConfig(configPath)
	mintServer, err := mint.SetupMintServer(mintConfig)
	if err != nil {
		log.Fatalf("error starting mint server: %v", err)
	}

	mint.StartMintServer(mintServer)
}
