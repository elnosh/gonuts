package mint

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu/nuts/nut06"
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

func getMintInfo() (*nut06.MintInfo, error) {
	mintInfo := nut06.MintInfo{
		Name:        os.Getenv("MINT_NAME"),
		Version:     "gonuts/0.0.1",
		Description: os.Getenv("MINT_DESCRIPTION"),
	}

	mintInfo.LongDescription = os.Getenv("MINT_DESCRIPTION_LONG")
	mintInfo.Motd = os.Getenv("MINT_MOTD")

	privateKey := secp256k1.PrivKeyFromBytes([]byte(os.Getenv("MINT_PRIVATE_KEY")))
	mintInfo.Pubkey = hex.EncodeToString(privateKey.PubKey().SerializeCompressed())

	contact := os.Getenv("MINT_CONTACT_INFO")
	var mintContactInfo [][]string
	if len(contact) > 0 {
		err := json.Unmarshal([]byte(contact), &mintContactInfo)
		if err != nil {
			return nil, fmt.Errorf("error parsing contact info: %v", err)
		}
	}
	mintInfo.Contact = mintContactInfo

	nuts := map[string]interface{}{
		"4": map[string]interface{}{
			"methods": [][]string{
				{"bolt11", "sat"},
			},
		},
		"5": map[string]interface{}{
			"methods": [][]string{
				{"bolt11", "sat"},
			},
		},
	}

	mintInfo.Nuts = nuts
	return &mintInfo, nil
}
