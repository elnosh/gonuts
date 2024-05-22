//go:build integration

package mint_test

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"testing"

	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/testutils"
	"github.com/lightningnetwork/lnd/lnrpc"
)

const (
	BOLT11_METHOD = "bolt11"
	SAT_UNIT      = "sat"
)

var (
	ctx      context.Context
	bitcoind *btcdocker.Bitcoind
	lnd1     *btcdocker.Lnd
	lnd2     *btcdocker.Lnd
	testMint *mint.Mint
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	flag.Parse()

	ctx = context.Background()
	var err error
	bitcoind, err = btcdocker.NewBitcoind(ctx)
	if err != nil {
		log.Println(err)
		return 1
	}

	_, err = bitcoind.Client.CreateWallet("")
	if err != nil {
		log.Println(err)
		return 1
	}

	lnd1, err = btcdocker.NewLnd(ctx, bitcoind)
	if err != nil {
		log.Println(err)
		return 1
	}

	lnd2, err = btcdocker.NewLnd(ctx, bitcoind)
	if err != nil {
		log.Println(err)
		return 1
	}
	defer func() {
		bitcoind.Terminate(ctx)
		lnd1.Terminate(ctx)
		lnd2.Terminate(ctx)
	}()

	err = testutils.FundLndNode(ctx, bitcoind, lnd1)
	if err != nil {
		log.Println(err)
		return 1
	}

	err = testutils.OpenChannel(ctx, bitcoind, lnd1, lnd2, 15000000)
	if err != nil {
		log.Println(err)
		return 1
	}

	testMintPath := filepath.Join(".", "testmint1")
	testMint, err = testutils.CreateTestMint(lnd1, "mykey", "3338", testMintPath)
	if err != nil {
		log.Println(err)
		return 1
	}
	defer func() {
		os.RemoveAll(testMintPath)
	}()

	return m.Run()
}

func TestRequestMintQuote(t *testing.T) {
	var mintAmount uint64 = 10000
	_, err := testMint.RequestMintQuote(BOLT11_METHOD, mintAmount, SAT_UNIT)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	// test invalid method
	_, err = testMint.RequestMintQuote("strike", mintAmount, SAT_UNIT)
	if !errors.Is(err, cashu.PaymentMethodNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test invalid unit
	_, err = testMint.RequestMintQuote(BOLT11_METHOD, mintAmount, "eth")
	if !errors.Is(err, cashu.UnitNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.UnitNotSupportedErr, err)
	}
}

func TestMintTokens(t *testing.T) {
	var mintAmount uint64 = 42000
	mintQuoteResponse, err := testMint.RequestMintQuote(BOLT11_METHOD, mintAmount, SAT_UNIT)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	var keyset crypto.Keyset
	for _, k := range testMint.ActiveKeysets {
		keyset = k
		break
	}

	blindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, keyset)

	// test without paying invoice
	_, err = testMint.MintTokens(BOLT11_METHOD, mintQuoteResponse.Quote, blindedMessages)
	if !errors.Is(err, cashu.InvoiceNotPaidErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvoiceNotPaidErr, err)
	}

	// test invalid quote
	_, err = testMint.MintTokens(BOLT11_METHOD, "mintquote1234", blindedMessages)
	if !errors.Is(err, cashu.InvoiceNotExistErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvoiceNotExistErr, err)
	}

	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintQuoteResponse.Request,
	}
	response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		t.Fatalf("error paying invoice: %v", response.PaymentError)
	}

	// test with blinded messages over request mint amount
	overBlindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount+100, keyset)
	_, err = testMint.MintTokens(BOLT11_METHOD, mintQuoteResponse.Quote, overBlindedMessages)
	if !errors.Is(err, cashu.OutputsOverInvoiceErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.OutputsOverInvoiceErr, err)
	}

	// test with invalid keyset in blinded messages
	invalidKeyset := crypto.GenerateKeyset("seed", "path")
	invalidKeysetMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, *invalidKeyset)
	_, err = testMint.MintTokens(BOLT11_METHOD, mintQuoteResponse.Quote, invalidKeysetMessages)
	if !errors.Is(err, cashu.InvalidSignatureRequest) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvalidSignatureRequest, err)
	}

	_, err = testMint.MintTokens(BOLT11_METHOD, mintQuoteResponse.Quote, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	// test already minted tokens
	_, err = testMint.MintTokens(BOLT11_METHOD, mintQuoteResponse.Quote, blindedMessages)
	if !errors.Is(err, cashu.InvoiceTokensIssuedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvoiceTokensIssuedErr, err)
	}
}
