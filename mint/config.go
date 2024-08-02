package mint

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/elnosh/gonuts/cashu/nuts/nut06"
)

type Config struct {
	DerivationPathIdx uint32
	Port              string
	DBPath            string
	DBMigrationPath   string
	InputFeePpk       uint
	Limits            MintLimits
}

type MintMethodSettings struct {
	MinAmount uint64
	MaxAmount uint64
}

type MeltMethodSettings struct {
	MinAmount uint64
	MaxAmount uint64
}

type MintLimits struct {
	MaxBalance      uint64
	MintingSettings MintMethodSettings
	MeltingSettings MeltMethodSettings
}

func GetConfig() Config {
	var inputFeePpk uint = 0
	if inputFeeEnv, ok := os.LookupEnv("INPUT_FEE_PPK"); ok {
		fee, err := strconv.ParseUint(inputFeeEnv, 10, 16)
		if err != nil {
			log.Fatalf("invalid INPUT_FEE_PPK: %v", err)
		}
		inputFeePpk = uint(fee)
	}

	derivationPathIdx, err := strconv.ParseUint(os.Getenv("DERIVATION_PATH_IDX"), 10, 32)
	if err != nil {
		log.Fatalf("invalid DERIVATION_PATH_IDX: %v", err)
	}

	mintLimits := MintLimits{}
	if maxBalanceEnv, ok := os.LookupEnv("MAX_BALANCE"); ok {
		maxBalance, err := strconv.ParseUint(maxBalanceEnv, 10, 64)
		if err != nil {
			log.Fatalf("invalid MAX_BALANCE: %v", err)
		}
		mintLimits.MaxBalance = maxBalance
	}

	if maxMintEnv, ok := os.LookupEnv("MINTING_MAX_AMOUNT"); ok {
		maxMint, err := strconv.ParseUint(maxMintEnv, 10, 64)
		if err != nil {
			log.Fatalf("invalid MINTING_MAX_AMOUNT: %v", err)
		}
		mintLimits.MintingSettings = MintMethodSettings{MaxAmount: maxMint}
	}

	if maxMeltEnv, ok := os.LookupEnv("MELTING_MAX_AMOUNT"); ok {
		maxMelt, err := strconv.ParseUint(maxMeltEnv, 10, 64)
		if err != nil {
			log.Fatalf("invalid MELTING_MAX_AMOUNT: %v", err)
		}
		mintLimits.MeltingSettings = MeltMethodSettings{MaxAmount: maxMelt}
	}

	return Config{
		DerivationPathIdx: uint32(derivationPathIdx),
		Port:              os.Getenv("MINT_PORT"),
		DBPath:            os.Getenv("MINT_DB_PATH"),
		DBMigrationPath:   "../../mint/storage/sqlite/migrations",
		InputFeePpk:       inputFeePpk,
		Limits:            mintLimits,
	}
}

// getMintInfo returns information about the mint as
// defined in NUT-06: https://github.com/cashubtc/nuts/blob/main/06.md
func (m *Mint) getMintInfo() (*nut06.MintInfo, error) {
	mintInfo := nut06.MintInfo{
		Name:        os.Getenv("MINT_NAME"),
		Version:     "gonuts/0.0.1",
		Description: os.Getenv("MINT_DESCRIPTION"),
	}

	mintInfo.LongDescription = os.Getenv("MINT_DESCRIPTION_LONG")
	mintInfo.Motd = os.Getenv("MINT_MOTD")

	seed, err := m.db.GetSeed()
	if err != nil {
		return nil, err
	}

	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, err
	}

	publicKey, err := master.ECPubKey()
	if err != nil {
		return nil, err
	}

	mintInfo.Pubkey = hex.EncodeToString(publicKey.SerializeCompressed())

	contact := os.Getenv("MINT_CONTACT_INFO")
	var mintContactInfo []nut06.ContactInfo
	if len(contact) > 0 {
		var infoArr [][]string
		err := json.Unmarshal([]byte(contact), &infoArr)
		if err != nil {
			return nil, fmt.Errorf("error parsing contact info: %v", err)
		}

		for _, info := range infoArr {
			contactInfo := nut06.ContactInfo{Method: info[0], Info: info[1]}
			mintContactInfo = append(mintContactInfo, contactInfo)
		}
	}
	mintInfo.Contact = mintContactInfo

	nuts := nut06.NutsMap{
		4: nut06.NutSetting{
			Methods: []nut06.MethodSetting{
				{
					Method:    "bolt11",
					Unit:      "sat",
					MinAmount: m.Limits.MintingSettings.MinAmount,
					MaxAmount: m.Limits.MintingSettings.MaxAmount,
				},
			},
			Disabled: false,
		},
		5: nut06.NutSetting{
			Methods: []nut06.MethodSetting{
				{
					Method:    "bolt11",
					Unit:      "sat",
					MinAmount: m.Limits.MeltingSettings.MinAmount,
					MaxAmount: m.Limits.MeltingSettings.MaxAmount,
				},
			},
			Disabled: false,
		},
		7:  map[string]bool{"supported": false},
		8:  map[string]bool{"supported": false},
		9:  map[string]bool{"supported": false},
		10: map[string]bool{"supported": false},
		11: map[string]bool{"supported": false},
		12: map[string]bool{"supported": false},
	}

	mintInfo.Nuts = nuts
	return &mintInfo, nil
}
