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
	"github.com/elnosh/gonuts/mint/lightning"
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
	fmt.Printf("balance in %v mints\n", len(balanceByMints))
	totalBalance := uint64(0)

	for mint, balance := range balanceByMints {
		fmt.Printf("mint: %v ---- balance: %v\n", mint, balance)
		totalBalance += balance
	}

	fmt.Printf("total balance: %v\n", totalBalance)
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
		fmt.Printf("token received comes from an untrusted mint: %v. Do you wish to trust this mint? (y/n)", mintURL)

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			log.Fatal("error reading input, please try again")
		}

		input = strings.ToLower(strings.TrimSpace(input))
		if input == "y" || input == "yes" {
			fmt.Println("token from unknown mint will be added")
			swap = false
		} else {
			fmt.Println("token will be swapped to your default trusted mint")
		}
	} else {
		// if it comes from an already trusted mint, do not swap 
		swap = false
	}


	err = nutw.Receive(*token, swap)
	if err != nil {
		printErr(err)
	}

	fmt.Printf("%v sats received\n", token.TotalAmount())
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

	invoice := lightning.Invoice{Id: mintResponse.Quote,
		PaymentRequest: mintResponse.Request, Amount: amount,
		Expiry: mintResponse.Expiry}

	err = nutw.SaveInvoice(invoice)
	if err != nil {
		return err
	}

	fmt.Printf("invoice: %v\n\n", invoice.PaymentRequest)
	fmt.Println("after paying the invoice you can redeem the ecash using the --invoice flag")
	return nil
}

func mintTokens(paymentRequest string) error {
	invoice := nutw.GetInvoice(paymentRequest)
	if invoice == nil {
		return errors.New("invoice not found")
	}

	invoicePaid := nutw.CheckQuotePaid(invoice.Id)
	if !invoicePaid {
		return errors.New("invoice has not been paid")
	}

	activeKeyset := nutw.GetActiveSatKeyset()
	blindedMessages, secrets, rs, err := nutw.CreateBlindedMessages(invoice.Amount, activeKeyset)
	if err != nil {
		return fmt.Errorf("error creating blinded messages: %v", err)
	}

	blindedSignatures, err := nutw.MintTokens(invoice.Id, blindedMessages)
	if err != nil {
		return err
	}

	// unblind the signatures from the promises and build the proofs
	proofs, err := nutw.ConstructProofs(blindedSignatures, secrets, rs, &activeKeyset)
	if err != nil {
		return fmt.Errorf("error constructing proofs: %v", err)
	}

	// store proofs in db
	err = nutw.SaveProofs(proofs)
	if err != nil {
		return fmt.Errorf("error storing proofs: %v", err)
	}

	fmt.Println("tokens successfully minted")
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

	token, err := nutw.Send(sendAmount)
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

	invoice := args.First()
	meltResponse, err := nutw.Melt(invoice)
	if err != nil {
		printErr(err)
	}

	fmt.Printf("invoice paid: %v\n", meltResponse.Paid)
	return nil
}

func printErr(msg error) {
	fmt.Println(msg.Error())
	os.Exit(0)
}
