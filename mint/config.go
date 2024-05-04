package mint

import (
	cashurpc "buf.build/gen/go/cashu/rpc/protocolbuffers/go"
	"encoding/hex"
	"os"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
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

// getMintInfo returns information about the mint as
// defined in NUT-06: https://github.com/cashubtc/nuts/blob/main/06.md
func getMintInfo() (*cashurpc.InfoResponse, error) {
	mintInfo := cashurpc.InfoResponse{
		Name:        os.Getenv("MINT_NAME"),
		Version:     "gonuts/0.0.1",
		Description: os.Getenv("MINT_DESCRIPTION"),
	}

	mintInfo.DescriptionLong = os.Getenv("MINT_DESCRIPTION_LONG")
	mintInfo.Motd = os.Getenv("MINT_MOTD")

	privateKey := secp256k1.PrivKeyFromBytes([]byte(os.Getenv("MINT_PRIVATE_KEY")))
	mintInfo.Pubkey = hex.EncodeToString(privateKey.PubKey().SerializeCompressed())

	//	contact := os.Getenv("MINT_CONTACT_INFO")
	var mintContactInfo []string
	/*if len(contact) > 0 {
		err := json.Unmarshal([]byte(contact), &mintContactInfo)
		if err != nil {
			return nil, fmt.Errorf("error parsing contact info: %v", err)
		}
	}*/
	mintInfo.Contact = mintContactInfo

	nuts := make(map[int32]*cashurpc.NutDetails)
	nuts[4] = &cashurpc.NutDetails{
		Methods: []cashurpc.MethodType{cashurpc.MethodType_BOLT11, cashurpc.MethodType_SAT},
	}
	nuts[5] = &cashurpc.NutDetails{
		Methods: []cashurpc.MethodType{cashurpc.MethodType_BOLT11, cashurpc.MethodType_SAT},
	}
	mintInfo.Nuts = nuts
	return &mintInfo, nil
}
