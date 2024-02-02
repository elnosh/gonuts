package mint

import (
	"os"
)

type Config struct {
	PrivateKey     string
	DerivationPath string
}

func GetConfig() Config {
	return Config{
		PrivateKey:     os.Getenv("MINT_PRIVATE_KEY"),
		DerivationPath: os.Getenv("MINT_DERIVATION_PATH"),
	}
}
