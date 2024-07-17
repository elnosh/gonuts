package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/wallet"
	"github.com/joho/godotenv"
	decodepay "github.com/nbd-wtf/ln-decodepay"
	"github.com/urfave/cli/v2"
)

var nutw *wallet.Wallet

func walletConfig() wallet.Config {
	path := setWalletPath()
	// default config
	config := wallet.Config{WalletPath: path, CurrentMintURL: "http://127.0.0.1:3338"}

	envPath := filepath.Join(path, ".env")
	if _, err := os.Stat(envPath); err != nil {
		wd, err := os.Getwd()
		if err != nil {
			envPath = ""
		} else {
			envPath = filepath.Join(wd, ".env")
		}
	}

	if len(envPath) > 0 {
		err := godotenv.Load(envPath)
		if err == nil {
			config.CurrentMintURL = getMintURL()
		}
	}

	return config
}

func setWalletPath() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	path := filepath.Join(homedir, ".gonuts", "wallet")
	err = os.MkdirAll(path, 0700)
	if err != nil {
		log.Fatal(err)
	}
	return path
}

func getMintURL() string {
	mintUrl := os.Getenv("MINT_URL")
	if len(mintUrl) > 0 {
		return mintUrl
	} else {
		mintHost := os.Getenv("MINT_HOST")
		mintPort := os.Getenv("MINT_PORT")
		if len(mintHost) == 0 || len(mintPort) == 0 {
			return "http://127.0.0.1:3338"
		}

		url := &url.URL{
			Scheme: "http",
			Host:   mintHost + ":" + mintPort,
		}
		mintUrl = url.String()
	}
	return mintUrl
}

func setupWallet(ctx *cli.Context) error {
	config := walletConfig()

	var err error
	nutw, err = wallet.LoadWallet(config)
	if err != nil {
		printErr(err)
	}
	return nil
}

func main() {
	app := &cli.App{
		Name:  "nutw",
		Usage: "cashu wallet",
		Commands: []*cli.Command{
			balanceCmd,
			mintCmd,
			sendCmd,
			receiveCmd,
			payCmd,
			p2pkLockCmd,
			mnemonicCmd,
			restoreCmd,
			decodeCmd,
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

var balanceCmd = &cli.Command{
	Name:   "balance",
	Usage:  "Wallet balance",
	Before: setupWallet,
	Action: getBalance,
}

func getBalance(ctx *cli.Context) error {
	balanceByMints := nutw.GetBalanceByMints()
	fmt.Printf("Balance by mint:\n\n")
	totalBalance := uint64(0)

	mints := nutw.TrustedMints()
	slices.Sort(mints)

	for i, mint := range mints {
		balance := balanceByMints[mint]
		fmt.Printf("Mint %v: %v ---- balance: %v sats\n", i+1, mint, balance)
		totalBalance += balance
	}

	fmt.Printf("\nTotal balance: %v sats\n", totalBalance)
	return nil
}

var receiveCmd = &cli.Command{
	Name:      "receive",
	Usage:     "Receive token",
	ArgsUsage: "[TOKEN]",
	Before:    setupWallet,
	Action:    receive,
}

func receive(ctx *cli.Context) error {
	args := ctx.Args()
	if args.Len() < 1 {
		printErr(errors.New("token not provided"))
	}
	serializedToken := args.First()

	token, err := cashu.DecodeToken(serializedToken)
	if err != nil {
		printErr(err)
	}

	swap := true
	trustedMints := nutw.TrustedMints()
	mintURL := token.Token[0].Mint

	isTrusted := slices.Contains(trustedMints, mintURL)
	if !isTrusted {
		fmt.Printf("Token received comes from an untrusted mint: %v. Do you wish to trust this mint? (y/n) ", mintURL)

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			log.Fatal("error reading input, please try again")
		}

		input = strings.ToLower(strings.TrimSpace(input))
		if input == "y" || input == "yes" {
			fmt.Println("Token from unknown mint will be added")
			swap = false
		} else {
			fmt.Println("Token will be swapped to your default trusted mint")
		}
	} else {
		// if it comes from an already trusted mint, do not swap
		swap = false
	}

	receivedAmount, err := nutw.Receive(*token, swap)
	if err != nil {
		printErr(err)
	}

	fmt.Printf("%v sats received\n", receivedAmount)
	return nil
}

const invoiceFlag = "invoice"

var mintCmd = &cli.Command{
	Name:      "mint",
	Usage:     "Request mint quote. It will return a lightning invoice to be paid",
	ArgsUsage: "[AMOUNT]",
	Before:    setupWallet,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  invoiceFlag,
			Usage: "Specify paid invoice to mint tokens",
		},
	},
	Action: mint,
}

func mint(ctx *cli.Context) error {
	// if paid invoice was passed, request tokens from mint
	if ctx.IsSet(invoiceFlag) {
		err := mintTokens(ctx.String(invoiceFlag))
		if err != nil {
			printErr(err)
		}
		return nil
	}

	args := ctx.Args()
	if args.Len() < 1 {
		printErr(errors.New("specify an amount to mint"))
	}
	amountStr := args.First()
	err := requestMint(amountStr)
	if err != nil {
		printErr(err)
	}

	return nil
}

func requestMint(amountStr string) error {
	amount, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil {
		return errors.New("invalid amount")
	}

	mintResponse, err := nutw.RequestMint(amount)
	if err != nil {
		return err
	}

	fmt.Printf("invoice: %v\n\n", mintResponse.Request)
	fmt.Println("after paying the invoice you can redeem the ecash using the --invoice flag")
	return nil
}

func mintTokens(paymentRequest string) error {
	invoice, err := nutw.GetInvoiceByPaymentRequest(paymentRequest)
	if err != nil {
		return err
	}
	if invoice == nil {
		return errors.New("invoice not found")
	}

	proofs, err := nutw.MintTokens(invoice.Id)
	if err != nil {
		return err
	}

	fmt.Printf("%v sats successfully minted\n", proofs.Amount())
	return nil
}

const lockFlag = "lock"

var sendCmd = &cli.Command{
	Name:      "send",
	Usage:     "Generates token to be sent for the specified amount",
	ArgsUsage: "[AMOUNT]",
	Before:    setupWallet,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  lockFlag,
			Usage: "generate ecash locked to a public key",
		},
	},
	Action: send,
}

func send(ctx *cli.Context) error {
	args := ctx.Args()
	if args.Len() < 1 {
		printErr(errors.New("specify an amount to send"))
	}
	amountStr := args.First()
	sendAmount, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil {
		printErr(err)
	}

	selectedMint := promptMintSelection("send")

	var token *cashu.Token
	// if lock flag is set, get ecash locked to the pubkey
	if ctx.IsSet(lockFlag) {
		lockpubkey := ctx.String(lockFlag)
		lockbytes, err := hex.DecodeString(lockpubkey)
		if err != nil {
			printErr(err)
		}
		pubkey, err := btcec.ParsePubKey(lockbytes)
		if err != nil {
			printErr(err)
		}

		token, err = nutw.SendToPubkey(sendAmount, selectedMint, pubkey, true)
		if err != nil {
			printErr(err)
		}
	} else {
		token, err = nutw.Send(sendAmount, selectedMint, true)
		if err != nil {
			printErr(err)
		}
	}

	fmt.Printf("%v\n", token.ToString())
	return nil
}

var payCmd = &cli.Command{
	Name:      "pay",
	Usage:     "Pay a lightning invoice",
	ArgsUsage: "[INVOICE]",
	Before:    setupWallet,
	Action:    pay,
}

func pay(ctx *cli.Context) error {
	args := ctx.Args()
	if args.Len() < 1 {
		printErr(errors.New("specify a lightning invoice to pay"))
	}
	invoice := args.First()

	// check invoice passed is valid
	_, err := decodepay.Decodepay(invoice)
	if err != nil {
		printErr(fmt.Errorf("invalid invoice: %v", err))
	}
	selectedMint := promptMintSelection("pay invoice")

	meltResponse, err := nutw.Melt(invoice, selectedMint)
	if err != nil {
		printErr(err)
	}

	fmt.Printf("invoice paid: %v\n", meltResponse.Paid)
	return nil
}

var p2pkLockCmd = &cli.Command{
	Name:   "p2pk-lock",
	Usage:  "Retrieves a public key to which ecash can locked",
	Before: setupWallet,
	Action: p2pkLock,
}

func p2pkLock(ctx *cli.Context) error {
	lockpubkey := nutw.GetReceivePubkey()
	pubkey := hex.EncodeToString(lockpubkey.SerializeCompressed())

	fmt.Printf("Pay to Public Key (P2PK) lock: %v\n\n", pubkey)
	fmt.Println("You can unlock ecash locked to this public key")

	return nil
}

var mnemonicCmd = &cli.Command{
	Name:   "mnemonic",
	Usage:  "Mnemonic to restore wallet",
	Before: setupWallet,
	Action: mnemonic,
}

func mnemonic(ctx *cli.Context) error {
	mnemonic := nutw.Mnemonic()
	fmt.Printf("mnemonic: %v\n", mnemonic)
	return nil
}

var restoreCmd = &cli.Command{
	Name:   "restore",
	Usage:  "Restore wallet from mnemonic",
	Action: restore,
}

func restore(ctx *cli.Context) error {
	config := walletConfig()
	fmt.Printf("enter mnemonic: ")

	reader := bufio.NewReader(os.Stdin)
	mnemonic, err := reader.ReadString('\n')
	if err != nil {
		log.Fatal("error reading input, please try again")
	}
	mnemonic = mnemonic[:len(mnemonic)-1]

	proofs, err := wallet.Restore(config.WalletPath, mnemonic, []string{config.CurrentMintURL})
	if err != nil {
		printErr(fmt.Errorf("error restoring wallet: %v", err))
	}

	fmt.Printf("restored proofs for amount of: %v\n", proofs.Amount())
	return nil
}

var decodeCmd = &cli.Command{
	Name:      "decode",
	ArgsUsage: "[TOKEN]",
	Usage:     "Decode token",
	Action:    decode,
}

func decode(ctx *cli.Context) error {
	args := ctx.Args()
	if args.Len() < 1 {
		printErr(errors.New("token not provided"))
	}
	serializedToken := args.First()

	token, err := cashu.DecodeToken(serializedToken)
	if err != nil {
		printErr(err)
	}

	jsonToken, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		printErr(err)
	}

	fmt.Println(string(jsonToken))

	return nil
}

func promptMintSelection(action string) string {
	balanceByMints := nutw.GetBalanceByMints()
	mintsLen := len(balanceByMints)

	mints := nutw.TrustedMints()
	slices.Sort(mints)
	selectedMint := nutw.CurrentMint()
	if mintsLen > 1 {
		fmt.Printf("You have balances in %v mints: \n\n", mintsLen)

		for i, mint := range mints {
			balance := balanceByMints[mint]
			fmt.Printf("Mint %v: %v ---- balance: %v sats\n", i+1, mint, balance)
		}

		fmt.Printf("\nSelect from which mint (1-%v) you wish to %v: ", mintsLen, action)

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			log.Fatal("error reading input, please try again")
		}

		num, err := strconv.Atoi(input[:len(input)-1])
		if err != nil {
			printErr(errors.New("invalid number provided"))
		}

		if num <= 0 || num > len(mints) {
			printErr(errors.New("invalid mint selected"))
		}
		selectedMint = mints[num-1]
	}

	return selectedMint
}

func printErr(msg error) {
	fmt.Println(msg.Error())
	os.Exit(0)
}
