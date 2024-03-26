package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/wallet"
	"github.com/joho/godotenv"
	"github.com/urfave/cli/v2"
)

var nutw *wallet.Wallet

func walletConfig() wallet.Config {
	path := setWalletPath()
	// default config
	config := wallet.Config{WalletPath: path, CurrentMintURL: "https://8333.space:3338", DomainSeparation: false}

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

	domainSeparation, _ := strconv.ParseBool(os.Getenv("WALLET_DOMAIN_SEPARATION"))
	config.DomainSeparation = domainSeparation

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
		Usage: "cashu cli wallet",
		Commands: []*cli.Command{
			balanceCmd,
			mintCmd,
			sendCmd,
			receiveCmd,
			payCmd,
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

var balanceCmd = &cli.Command{
	Name:   "balance",
	Before: setupWallet,
	Action: getBalance,
}

func getBalance(ctx *cli.Context) error {
	balanceByMints := nutw.GetBalanceByMints()
	fmt.Printf("Balance by mint:\n\n")
	totalBalance := uint64(0)

	i := 1
	for mint, balance := range balanceByMints {
		fmt.Printf("Mint %v: %v ---- balance: %v sats\n", i, mint, balance)
		totalBalance += balance
		i++
	}

	fmt.Printf("\nTotal balance: %v sats\n", totalBalance)
	return nil
}

var receiveCmd = &cli.Command{
	Name:   "receive",
	Before: setupWallet,
	Action: receive,
}

func receive(ctx *cli.Context) error {
	args := ctx.Args()
	if args.Len() < 1 {
		printErr(errors.New("cashu token not provided"))
	}
	serializedToken := args.First()

	token, err := cashu.DecodeToken(serializedToken)
	if err != nil {
		printErr(err)
	}

	swap := true
	trustedMints := nutw.TrustedMints()
	mintURL := token.Token[0].Mint
	_, ok := trustedMints[mintURL]
	if !ok {
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
	Name:   "mint",
	Before: setupWallet,
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
	invoice := nutw.GetInvoice(paymentRequest)
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

var sendCmd = &cli.Command{
	Name:   "send",
	Before: setupWallet,
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

	token, err := nutw.Send(sendAmount, selectedMint)
	if err != nil {
		printErr(err)
	}

	fmt.Printf("%v\n", token.ToString())
	return nil
}

var payCmd = &cli.Command{
	Name:   "pay",
	Before: setupWallet,
	Action: pay,
}

func pay(ctx *cli.Context) error {
	args := ctx.Args()
	if args.Len() < 1 {
		printErr(errors.New("specify a lightning invoice to pay"))
	}

	selectedMint := promptMintSelection("pay invoice")

	invoice := args.First()
	meltResponse, err := nutw.Melt(invoice, selectedMint)
	if err != nil {
		printErr(err)
	}

	fmt.Printf("invoice paid: %v\n", meltResponse.Paid)
	return nil
}

func promptMintSelection(action string) string {
	balanceByMints := nutw.GetBalanceByMints()
	mintsLen := len(balanceByMints)

	mintsMap := make(map[int]string)
	selectedMint := nutw.CurrentMint()
	if mintsLen > 1 {
		fmt.Printf("You have balances in %v mints: \n\n", mintsLen)

		i := 1
		for mint, balance := range balanceByMints {
			fmt.Printf("Mint %v: %v ---- balance: %v sats\n", i, mint, balance)
			mintsMap[i] = mint
			i++
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
		selectedMint = mintsMap[num]
	}

	return selectedMint
}

func printErr(msg error) {
	fmt.Println(msg.Error())
	os.Exit(0)
}
