package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

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
		},
		Action: func(*cli.Context) error {
			fmt.Println("hey! I'm nutw")
			return nil
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

var mintCmd = &cli.Command{
	Name:   "mint",
	Before: SetupWallet,
	Action: requestMint,
}

func requestMint(ctx *cli.Context) error {
	args := ctx.Args()
	amountStr := args.First()

	amount, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil {
		printErr(errors.New("invalid amount"))
	}

	mintResponse, err := nutw.RequestMint(amount)
	if err != nil {
		printErr(err)
	}

	invoice := lightning.Invoice{Id: mintResponse.Hash,
		PaymentRequest: mintResponse.PaymentRequest, Amount: amount}

	err = nutw.SaveInvoice(invoice)
	if err != nil {
		printErr(err)
	}

	fmt.Printf("invoice: %v\n", mintResponse.PaymentRequest)
	return nil
}

func printErr(msg error) {
	fmt.Println(msg.Error())
	os.Exit(0)
}
