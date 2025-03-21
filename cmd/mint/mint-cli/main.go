package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"strconv"

	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/mint/manager"
	"github.com/urfave/cli/v2"
)

const (
	SOCKET_PATH = "/tmp/gonuts/gonuts-admin.sock"
	KEYSET_FLAG = "keyset"
)

func main() {
	app := &cli.App{
		Name:  "gonuts-cli",
		Usage: "cli to interact with the Gonuts mint",
		Commands: []*cli.Command{
			{
				Name:  "issued",
				Usage: "Get issued ecash",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  KEYSET_FLAG,
						Usage: "Issued ecash for the specified keyset",
					},
				},
				Action: issuedEcash,
			},
			{
				Name:  "redeemed",
				Usage: "Get redeemed ecash",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  KEYSET_FLAG,
						Usage: "Redeemed ecash for the specified keyset",
					},
				},
				Action: redeemedEcash,
			},
			{
				Name:   "totalbalance",
				Usage:  "Get total ecash in circulation",
				Action: totalBalance,
			},
			{
				Name:   "keysets",
				Usage:  "List keysets",
				Action: listKeysets,
			},
			{
				Name:  "rotatekeyset",
				Usage: "Rotate keyset",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "fee",
						Usage: "Fee for the new keyset",
					},
				},
				Action: rotateKeyset,
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func sendRequest(method string, params []string) (*manager.Response, error) {
	conn, err := net.Dial("unix", SOCKET_PATH)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req := manager.Request{
		JsonRPC: "2.0",
		Method:  method,
		Params:  params,
		Id:      rand.Int(),
	}

	jsonReq, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	_, err = conn.Write(jsonReq)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	var resp manager.Response
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return nil, err
	}

	if resp.Error.Code < 0 || len(resp.Error.Message) > 0 {
		return nil, errors.New(resp.Error.Message)
	}

	return &resp, nil
}

func issuedEcash(ctx *cli.Context) error {
	keyset := ctx.String(KEYSET_FLAG)
	var params []string = nil
	if len(keyset) > 0 {
		params = []string{keyset}
	}

	resp, err := sendRequest(manager.ISSUED_ECASH_REQUEST, params)
	if err != nil {
		return err
	}

	if len(keyset) > 0 {
		var issuedByKeysetResponse manager.KeysetIssued
		if err := json.Unmarshal(resp.Result, &issuedByKeysetResponse); err != nil {
			return err
		}

		fmt.Printf("Issued: %v\n", issuedByKeysetResponse.AmountIssued)
	} else {
		var issuedResponse manager.IssuedEcashResponse
		if err := json.Unmarshal(resp.Result, &issuedResponse); err != nil {
			return err
		}

		fmt.Println("Issued by keyset:")
		for _, keyset := range issuedResponse.Keysets {
			fmt.Printf("\t%v: %v\n", keyset.Id, keyset.AmountIssued)
		}
		fmt.Printf("\nTotal issued: %v\n", issuedResponse.TotalIssued)
	}

	return nil
}

func redeemedEcash(ctx *cli.Context) error {
	keyset := ctx.String(KEYSET_FLAG)
	var params []string = nil
	if len(keyset) > 0 {
		params = []string{keyset}
	}

	resp, err := sendRequest(manager.REDEEMED_ECASH_REQUEST, params)
	if err != nil {
		return err
	}

	if len(keyset) > 0 {
		var redeemedByKeyset manager.KeysetRedeemed
		if err := json.Unmarshal(resp.Result, &redeemedByKeyset); err != nil {
			return err
		}

		fmt.Printf("Redeemed: %v\n", redeemedByKeyset.AmountRedeemed)
	} else {
		var redeemedResponse manager.RedeemedEcashResponse
		if err := json.Unmarshal(resp.Result, &redeemedResponse); err != nil {
			return err
		}

		fmt.Println("Redeemed by keyset:")
		for _, keyset := range redeemedResponse.Keysets {
			fmt.Printf("\t%v: %v\n", keyset.Id, keyset.AmountRedeemed)
		}
		fmt.Printf("\nTotal redeemed: %v\n", redeemedResponse.TotalRedeemed)
	}

	return nil
}

func totalBalance(ctx *cli.Context) error {
	resp, err := sendRequest(manager.TOTAL_BALANCE, nil)
	if err != nil {
		return err
	}

	var totalBalanceResponse manager.TotalBalanceResponse
	if err := json.Unmarshal(resp.Result, &totalBalanceResponse); err != nil {
		return err
	}

	fmt.Println("Issued by keyset:")
	for _, keyset := range totalBalanceResponse.TotalIssued.Keysets {
		fmt.Printf("\t%v: %v\n", keyset.Id, keyset.AmountIssued)
	}
	fmt.Printf("Total issued: %v\n", totalBalanceResponse.TotalIssued.TotalIssued)

	fmt.Println("\nRedeemed by keyset:")
	for _, keyset := range totalBalanceResponse.TotalRedeemed.Keysets {
		fmt.Printf("\t%v: %v\n", keyset.Id, keyset.AmountRedeemed)
	}
	fmt.Printf("Total redeemed: %v\n", totalBalanceResponse.TotalRedeemed.TotalRedeemed)

	fmt.Printf("\nTotal in circulation: %v\n", totalBalanceResponse.TotalInCirculation)

	return nil
}

func listKeysets(ctx *cli.Context) error {
	resp, err := sendRequest(manager.LIST_KEYSETS, nil)
	if err != nil {
		return err
	}

	var keysets nut02.GetKeysetsResponse
	if err := json.Unmarshal(resp.Result, &keysets); err != nil {
		return err
	}

	fmt.Println("Keysets: ")

	for _, keyset := range keysets.Keysets {
		fmt.Printf("\n%v\n", keyset.Id)
		fmt.Printf("\tunit: %v\n", keyset.Unit)
		fmt.Printf("\tactive: %v\n", keyset.Active)
		fmt.Printf("\tfee: %v\n\n", keyset.InputFeePpk)
	}

	return nil
}

func rotateKeyset(ctx *cli.Context) error {
	if !ctx.IsSet("fee") {
		return errors.New("please specify a fee for the new keyset")
	}
	fee := ctx.Int("fee")

	resp, err := sendRequest(manager.ROTATE_KEYSET, []string{strconv.Itoa(fee)})
	if err != nil {
		return err
	}

	var newKeyset nut02.Keyset
	if err := json.Unmarshal(resp.Result, &newKeyset); err != nil {
		return err
	}

	fmt.Println("New keyset: ")
	fmt.Printf("\n%v\n", newKeyset.Id)
	fmt.Printf("\tunit: %v\n", newKeyset.Unit)
	fmt.Printf("\tactive: %v\n", newKeyset.Active)
	fmt.Printf("\tfee: %v\n\n", newKeyset.InputFeePpk)

	return nil
}
