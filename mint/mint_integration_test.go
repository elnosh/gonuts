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
	_, err := testMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	// test invalid method
	_, err = testMint.RequestMintQuote("strike", mintAmount, testutils.SAT_UNIT)
	if !errors.Is(err, cashu.PaymentMethodNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test invalid unit
	_, err = testMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, "eth")
	if !errors.Is(err, cashu.UnitNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.UnitNotSupportedErr, err)
	}
}

func TestMintTokens(t *testing.T) {
	var mintAmount uint64 = 42000
	mintQuoteResponse, err := testMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, testutils.SAT_UNIT)
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
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Quote, blindedMessages)
	if !errors.Is(err, cashu.InvoiceNotPaidErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvoiceNotPaidErr, err)
	}

	// test invalid quote
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, "mintquote1234", blindedMessages)
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
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Quote, overBlindedMessages)
	if !errors.Is(err, cashu.OutputsOverInvoiceErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.OutputsOverInvoiceErr, err)
	}

	// test with invalid keyset in blinded messages
	invalidKeyset := crypto.GenerateKeyset("seed", "path")
	invalidKeysetMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, *invalidKeyset)
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Quote, invalidKeysetMessages)
	if !errors.Is(err, cashu.InvalidSignatureRequest) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvalidSignatureRequest, err)
	}

	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Quote, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error minting tokens: %v", err)
	}

	// test already minted tokens
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Quote, blindedMessages)
	if !errors.Is(err, cashu.InvoiceTokensIssuedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvoiceTokensIssuedErr, err)
	}
}

func TestSwap(t *testing.T) {
	var amount uint64 = 10000
	proofs, err := testutils.GetValidProofsForAmount(amount, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	var keyset crypto.Keyset
	for _, k := range testMint.ActiveKeysets {
		keyset = k
		break
	}

	newBlindedMessages, _, _, err := testutils.CreateBlindedMessages(amount, keyset)
	overBlindedMessages, _, _, err := testutils.CreateBlindedMessages(amount+200, keyset)

	// test blinded messages over proofs amount
	_, err = testMint.Swap(proofs, overBlindedMessages)
	if !errors.Is(err, cashu.InputsBelowOutputs) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.OutputsOverInvoiceErr, err)
	}

	_, err = testMint.Swap(proofs, newBlindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error in swap: %v", err)
	}

	// test already used proofs
	_, err = testMint.Swap(proofs, newBlindedMessages)
	if !errors.Is(err, cashu.ProofAlreadyUsedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.ProofAlreadyUsedErr, err)
	}
}

func TestMeltRequest(t *testing.T) {
	invoice := lnrpc.Invoice{Value: 10000}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	// test invalid method
	_, err = testMint.MeltRequest("strike", addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if !errors.Is(err, cashu.PaymentMethodNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test invalid unit
	_, err = testMint.MeltRequest(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, "eth")
	if !errors.Is(err, cashu.UnitNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.UnitNotSupportedErr, err)
	}

	// test invalid invoice
	_, err = testMint.MeltRequest(testutils.BOLT11_METHOD, "invoice1111", testutils.SAT_UNIT)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	_, err = testMint.MeltRequest(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

}

func TestMelt(t *testing.T) {
	var amount uint64 = 1000
	underProofs, err := testutils.GetValidProofsForAmount(amount, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	invoice := lnrpc.Invoice{Value: 6000}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	meltQuote, err := testMint.MeltRequest(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	// test proofs amount under melt amount
	_, err = testMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, underProofs)
	if !errors.Is(err, cashu.InsufficientProofsAmount) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	validProofs, err := testutils.GetValidProofsForAmount(6500, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}
	validSecret := validProofs[0].Secret

	// test invalid proofs
	validProofs[0].Secret = "some invalid secret"
	_, err = testMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if !errors.Is(err, cashu.InvalidProofErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvalidProofErr, err)
	}

	validProofs[0].Secret = validSecret
	melt, err := testMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if !melt.Paid {
		t.Fatal("got unexpected unpaid melt quote")
	}

	// test already used proofs
	_, err = testMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if !errors.Is(err, cashu.QuoteAlreadyPaid) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.QuoteAlreadyPaid, err)
	}

}
