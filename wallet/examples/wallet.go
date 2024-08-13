package main

import (
	"fmt"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/wallet"
)

func main() {
	config := wallet.Config{
		WalletPath:     "./cashu",
		CurrentMintURL: "http://localhost:3338",
	}

	wallet, err := wallet.LoadWallet(config)

	// Mint tokens
	mintQuote, err := wallet.RequestMint(42)

	// Check quote state
	quoteState, err := wallet.MintQuoteState(mintQuote.Quote)
	if quoteState.State == nut04.Paid {
		// Mint tokens if invoice paid
		proofs, err := wallet.MintTokens(mintQuote.Quote)
	}

	// Send
	includeFees := true
	token, err := wallet.Send(21, "http://localhost:3338", includeFees)
	fmt.Println(token.ToString())

	// Receive
	receiveToken, err := cashu.DecodeToken("cashuAeyJ0b2tlbiI6W3sibW...")

	swapToTrustedMint := true
	amountReceived, err := wallet.Receive(*receiveToken, swapToTrustedMint)

	// Melt (pay invoice)
	meltResponse, err := wallet.Melt("lnbc100n1pja0w9pdqqx...", "http://localhost:3338")
}
