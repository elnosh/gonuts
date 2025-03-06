package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/mint/manager"
	"github.com/urfave/cli/v2"
)

const (
	MINT_SERVER_URL = "http://127.0.0.1:8080"
	KEYSET_FLAG     = "keyset"
)

func main() {
	app := &cli.App{
		Name:  "mint-cli",
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
				Action: getIssued,
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
				Action: getRedeemed,
			},
			{
				Name:   "totalbalance",
				Usage:  "Get total ecash in circulation",
				Action: getTotalBalance,
			},
			{
				Name:   "keysets",
				Usage:  "Get keysets",
				Action: getKeysets,
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

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func getIssued(ctx *cli.Context) error {
	url := MINT_SERVER_URL + "/issued"
	keyset := ctx.String(KEYSET_FLAG)
	if len(keyset) > 0 {
		url = url + "/" + keyset
	}

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusInternalServerError {
		printErr(errors.New(string(body)))
	}

	if len(keyset) > 0 {
		var issuedByKeysetResponse manager.KeysetIssued
		if err := json.Unmarshal(body, &issuedByKeysetResponse); err != nil {
			return err
		}

		fmt.Printf("Issued: %v\n", issuedByKeysetResponse.AmountIssued)
	} else {
		var issuedResponse manager.IssuedEcashResponse
		if err := json.Unmarshal(body, &issuedResponse); err != nil {
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

func getRedeemed(ctx *cli.Context) error {
	url := MINT_SERVER_URL + "/redeemed"
	keyset := ctx.String(KEYSET_FLAG)
	if len(keyset) > 0 {
		url = url + "/" + keyset
	}

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusInternalServerError {
		printErr(errors.New(string(body)))
	}

	if len(keyset) > 0 {
		var redeemedByKeyset manager.KeysetRedeemed
		if err := json.Unmarshal(body, &redeemedByKeyset); err != nil {
			return err
		}

		fmt.Printf("Redeemed: %v\n", redeemedByKeyset.AmountRedeemed)
	} else {
		var redeemedResponse manager.RedeemedEcashResponse
		if err := json.Unmarshal(body, &redeemedResponse); err != nil {
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

func getTotalBalance(ctx *cli.Context) error {
	resp, err := http.Get(MINT_SERVER_URL + "/totalbalance")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusInternalServerError {
		printErr(errors.New(string(body)))
	}

	var totalBalanceResponse manager.TotalBalanceResponse
	if err := json.Unmarshal(body, &totalBalanceResponse); err != nil {
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

func getKeysets(ctx *cli.Context) error {
	resp, err := http.Get(MINT_SERVER_URL + "/keysets")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var keysets nut02.GetKeysetsResponse
	if err := json.Unmarshal(body, &keysets); err != nil {
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
		printErr(errors.New("please specify a fee for the new keyset"))
	}
	fee := ctx.Int("fee")
	feeParam := url.Values{"fee": {strconv.Itoa(fee)}}

	rotateKeysetUrl := MINT_SERVER_URL + "/rotatekeyset"
	req, err := http.NewRequest(http.MethodPost, rotateKeysetUrl, nil)
	req.URL.RawQuery = feeParam.Encode()
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusInternalServerError {
		printErr(errors.New(string(body)))
	}

	var newKeyset nut02.Keyset
	if err := json.Unmarshal(body, &newKeyset); err != nil {
		return err
	}

	fmt.Println("New keyset: ")
	fmt.Printf("\n%v\n", newKeyset.Id)
	fmt.Printf("\tunit: %v\n", newKeyset.Unit)
	fmt.Printf("\tactive: %v\n", newKeyset.Active)
	fmt.Printf("\tfee: %v\n\n", newKeyset.InputFeePpk)

	return nil
}

func printErr(msg error) {
	fmt.Println(msg.Error())
	os.Exit(0)
}
