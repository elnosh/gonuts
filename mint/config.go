package mint

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elnosh/gonuts/cashu/nuts/nut06"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

type LogLevel int

const (
	Info LogLevel = iota
	Debug
	Disable
)

type Config struct {
	DerivationPathIdx uint32
	Port              string
	MintPath          string
	DBMigrationPath   string
	InputFeePpk       uint
	MintInfo          MintInfo
	Limits            MintLimits
	LightningClient   lightning.Client
	LogLevel          LogLevel
	// NOTE: using this value for testing
	MeltTimeout *time.Duration
}

type MintInfo struct {
	Name            string
	Description     string
	LongDescription string
	Contact         []nut06.ContactInfo
	Motd            string
	IconURL         string
	URLs            []string
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

func ConfigFromEnv() (*Config, error) {
	var inputFeePpk uint = 0
	if inputFeeEnv, ok := os.LookupEnv("INPUT_FEE_PPK"); ok {
		fee, err := strconv.ParseUint(inputFeeEnv, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid INPUT_FEE_PPK: %w", err)
		}
		inputFeePpk = uint(fee)
	}

	derivationPathIdx, err := strconv.ParseUint(os.Getenv("DERIVATION_PATH_IDX"), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid DERIVATION_PATH_IDX: %w", err)
	}

	port := os.Getenv("MINT_PORT")
	if len(port) == 0 {
		port = "3338"
	}

	mintPath := os.Getenv("MINT_DB_PATH")
	// if MINT_DB_PATH is empty, use $HOME/.gonuts/mint
	if len(mintPath) == 0 {
		homedir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		mintPath = filepath.Join(homedir, ".gonuts", "mint")
	}

	dbMigrations := os.Getenv("DB_MIGRATIONS")
	if len(dbMigrations) == 0 {
		dbMigrations = "../../mint/storage/sqlite/migrations"
	}

	mintLimits := MintLimits{}
	if maxBalanceEnv, ok := os.LookupEnv("MAX_BALANCE"); ok {
		maxBalance, err := strconv.ParseUint(maxBalanceEnv, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_BALANCE: %w", err)
		}
		mintLimits.MaxBalance = maxBalance
	}

	if maxMintEnv, ok := os.LookupEnv("MINTING_MAX_AMOUNT"); ok {
		maxMint, err := strconv.ParseUint(maxMintEnv, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid MINTING_MAX_AMOUNT: %w", err)
		}
		mintLimits.MintingSettings = MintMethodSettings{MaxAmount: maxMint}
	}

	if maxMeltEnv, ok := os.LookupEnv("MELTING_MAX_AMOUNT"); ok {
		maxMelt, err := strconv.ParseUint(maxMeltEnv, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid MELTING_MAX_AMOUNT: %w", err)
		}
		mintLimits.MeltingSettings = MeltMethodSettings{MaxAmount: maxMelt}
	}

	mintInfo := MintInfo{
		Name:            os.Getenv("MINT_NAME"),
		Description:     os.Getenv("MINT_DESCRIPTION"),
		LongDescription: os.Getenv("MINT_DESCRIPTION_LONG"),
		Motd:            os.Getenv("MINT_MOTD"),
	}

	contact := os.Getenv("MINT_CONTACT_INFO")
	var mintContactInfo []nut06.ContactInfo
	if len(contact) > 0 {
		var infoArr [][]string
		if err := json.Unmarshal([]byte(contact), &infoArr); err != nil {
			return nil, fmt.Errorf("error parsing contact info: %w", err)
		}

		for _, info := range infoArr {
			contactInfo := nut06.ContactInfo{Method: info[0], Info: info[1]}
			mintContactInfo = append(mintContactInfo, contactInfo)
		}
	}
	mintInfo.Contact = mintContactInfo

	if len(os.Getenv("MINT_ICON_URL")) > 0 {
		iconURL, err := url.Parse(os.Getenv("MINT_ICON_URL"))
		if err != nil {
			return nil, fmt.Errorf("invalid icon url: %w", err)
		}
		mintInfo.IconURL = iconURL.String()
	}

	urls := os.Getenv("MINT_URLS")
	if len(urls) > 0 {
		urlList := []string{}
		if err := json.Unmarshal([]byte(urls), &urlList); err != nil {
			return nil, fmt.Errorf("error parsing list of URLs: %w", err)
		}
		for _, urlString := range urlList {
			mintURL, err := url.Parse(urlString)
			if err != nil {
				return nil, fmt.Errorf("invalid url: %w", err)
			}
			mintInfo.URLs = append(mintInfo.URLs, mintURL.String())
		}
	}

	var lightningClient lightning.Client
	switch os.Getenv("LIGHTNING_BACKEND") {
	case "Lnd":
		// read values for setting up LND
		host := os.Getenv("LND_GRPC_HOST")
		if host == "" {
			return nil, errors.New("LND_GRPC_HOST cannot be empty")
		}
		certPath := os.Getenv("LND_CERT_PATH")
		if certPath == "" {
			return nil, errors.New("LND_CERT_PATH cannot be empty")
		}
		macaroonPath := os.Getenv("LND_MACAROON_PATH")
		if macaroonPath == "" {
			return nil, errors.New("LND_MACAROON_PATH cannot be empty")
		}

		creds, err := credentials.NewClientTLSFromFile(certPath, "")
		if err != nil {
			return nil, fmt.Errorf("error reading macaroon: %w", err)
		}

		macaroonBytes, err := os.ReadFile(macaroonPath)
		if err != nil {
			return nil, fmt.Errorf("error reading macaroon: os.ReadFile %w", err)
		}

		mac := &macaroon.Macaroon{}
		if err = mac.UnmarshalBinary(macaroonBytes); err != nil {
			return nil, fmt.Errorf("unable to decode macaroon: %w", err)
		}
		macarooncreds, err := macaroons.NewMacaroonCredential(mac)
		if err != nil {
			return nil, fmt.Errorf("error setting macaroon creds: %w", err)
		}
		lndConfig := lightning.LndConfig{
			GRPCHost: host,
			Cert:     creds,
			Macaroon: macarooncreds,
		}

		lightningClient, err = lightning.SetupLndClient(lndConfig)
		if err != nil {
			return nil, fmt.Errorf("error setting LND client: %w", err)
		}
	case "FakeBackend":
		lightningClient = &lightning.FakeBackend{}
	default:
		return nil, errors.New("invalid lightning backend")
	}

	logLevel := Info
	if strings.ToLower(os.Getenv("LOG")) == "debug" {
		logLevel = Debug
	}

	return &Config{
		DerivationPathIdx: uint32(derivationPathIdx),
		Port:              port,
		MintPath:          mintPath,
		DBMigrationPath:   dbMigrations,
		InputFeePpk:       inputFeePpk,
		MintInfo:          mintInfo,
		Limits:            mintLimits,
		LightningClient:   lightningClient,
		LogLevel:          logLevel,
	}, nil
}
