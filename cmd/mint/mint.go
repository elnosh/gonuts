package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/elnosh/gonuts/cashu/nuts/nut06"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/joho/godotenv"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

func configFromEnv() (*mint.Config, error) {
	var inputFeePpk uint = 0
	if inputFeeEnv, ok := os.LookupEnv("INPUT_FEE_PPK"); ok {
		fee, err := strconv.ParseUint(inputFeeEnv, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid INPUT_FEE_PPK: %v", err)
		}
		inputFeePpk = uint(fee)
	}

	derivationPathIdx, err := strconv.ParseUint(os.Getenv("DERIVATION_PATH_IDX"), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid DERIVATION_PATH_IDX: %v", err)
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

	mintLimits := mint.MintLimits{}
	if maxBalanceEnv, ok := os.LookupEnv("MAX_BALANCE"); ok {
		maxBalance, err := strconv.ParseUint(maxBalanceEnv, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_BALANCE: %v", err)
		}
		mintLimits.MaxBalance = maxBalance
	}

	if maxMintEnv, ok := os.LookupEnv("MINTING_MAX_AMOUNT"); ok {
		maxMint, err := strconv.ParseUint(maxMintEnv, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid MINTING_MAX_AMOUNT: %v", err)
		}
		mintLimits.MintingSettings = mint.MintMethodSettings{MaxAmount: maxMint}
	}

	if maxMeltEnv, ok := os.LookupEnv("MELTING_MAX_AMOUNT"); ok {
		maxMelt, err := strconv.ParseUint(maxMeltEnv, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid MELTING_MAX_AMOUNT: %v", err)
		}
		mintLimits.MeltingSettings = mint.MeltMethodSettings{MaxAmount: maxMelt}
	}

	mintInfo := mint.MintInfo{
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
			return nil, fmt.Errorf("error parsing contact info: %v", err)
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
			return nil, fmt.Errorf("invalid icon url: %v", err)
		}
		mintInfo.IconURL = iconURL.String()
	}

	urls := os.Getenv("MINT_URLS")
	if len(urls) > 0 {
		urlList := []string{}
		if err := json.Unmarshal([]byte(urls), &urlList); err != nil {
			return nil, fmt.Errorf("error parsing list of URLs: %v", err)
		}
		for _, urlString := range urlList {
			mintURL, err := url.Parse(urlString)
			if err != nil {
				return nil, fmt.Errorf("invalid url: %v", err)
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
			return nil, err
		}

		macaroonBytes, err := os.ReadFile(macaroonPath)
		if err != nil {
			return nil, fmt.Errorf("error reading macaroon: os.ReadFile %v", err)
		}

		macaroon := &macaroon.Macaroon{}
		if err = macaroon.UnmarshalBinary(macaroonBytes); err != nil {
			return nil, fmt.Errorf("unable to decode macaroon: %v", err)
		}
		macarooncreds, err := macaroons.NewMacaroonCredential(macaroon)
		if err != nil {
			return nil, fmt.Errorf("error setting macaroon creds: %v", err)
		}
		lndConfig := lightning.LndConfig{
			GRPCHost: host,
			Cert:     creds,
			Macaroon: macarooncreds,
		}

		lightningClient, err = lightning.SetupLndClient(lndConfig)
		if err != nil {
			return nil, fmt.Errorf("error setting LND client: %v", err)
		}
	case "FakeBackend":
		lightningClient = &lightning.FakeBackend{}
	default:
		return nil, errors.New("invalid lightning backend")
	}

	logLevel := mint.Info
	if strings.ToLower(os.Getenv("LOG")) == "debug" {
		logLevel = mint.Debug
	}

	return &mint.Config{
		DerivationPathIdx: uint32(derivationPathIdx),
		Port:              port,
		MintPath:          mintPath,
		InputFeePpk:       inputFeePpk,
		MintInfo:          mintInfo,
		Limits:            mintLimits,
		LightningClient:   lightningClient,
		LogLevel:          logLevel,
	}, nil
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("error loading .env file")
	}
	mintConfig, err := configFromEnv()
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
