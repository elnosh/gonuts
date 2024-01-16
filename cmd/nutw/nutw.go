package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/wallet"
	"github.com/urfave/cli/v2"
)

var nutw *wallet.Wallet

func SetupWallet(ctx *cli.Context) error {
	var err error
	nutw, err = wallet.LoadWallet()
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
	Before: SetupWallet,
	Action: getBalance,
}

func getBalance(ctx *cli.Context) error {
	balance := nutw.GetBalance()
	fmt.Printf("%v sats\n", balance)
	return nil
}

var receiveCmd = &cli.Command{
	Name:   "receive",
	Before: SetupWallet,
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

	err = nutw.Receive(*token)
	if err != nil {
		printErr(err)
	}

	fmt.Println("tokens received")
	return nil
}

const invoiceFlag = "invoice"

var mintCmd = &cli.Command{
	Name:   "mint",
	Before: SetupWallet,
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

	fmt.Printf("invoice: %v\n", invoice.PaymentRequest)
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

	blindedMessages, secrets, rs, err := cashu.CreateBlindedMessages(invoice.Amount)
	if err != nil {
		return fmt.Errorf("error creating blinded messages: %v", err)
	}

	blindedSignatures, err := nutw.MintTokens(invoice.Id, blindedMessages)
	if err != nil {
		return fmt.Errorf("error minting tokens: %v", err)
	}

	mintKeyset, err := wallet.GetMintCurrentKeyset(nutw.MintURL)
	if err != nil {
		return err
	}

	// unblind the signatures from the promises and build the proofs
	proofs, err := nutw.ConstructProofs(blindedSignatures, secrets, rs, mintKeyset)
	if err != nil {
		return fmt.Errorf("error constructing proofs: %v", err)
	}

	// store proofs in db
	err = nutw.StoreProofs(proofs)
	if err != nil {
		return fmt.Errorf("error storing proofs: %v", err)
	}

	fmt.Println("tokens successfully minted")
	return nil
}

var sendCmd = &cli.Command{
	Name:   "send",
	Before: SetupWallet,
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
	Before: SetupWallet,
	Action: pay,
}

func pay(ctx *cli.Context) error {
	args := ctx.Args()
	if args.Len() < 1 {
		printErr(errors.New("specify a lightning invoice to pay"))
	}

	invoice := args.First()
	meltRequest := nut05.PostMeltQuoteBolt11Request{Request: invoice, Unit: "sat"}
	meltResponse, err := nutw.Melt(meltRequest)
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
