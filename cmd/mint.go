package main

import (
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/mint/config"
)

const configPath = "../mint/config/config.json"

func main() {
	mintConfig := config.GetConfig(configPath)
	mintServer := mint.SetupMintServer(mintConfig)
	mint.StartMintServer(mintServer)
}
