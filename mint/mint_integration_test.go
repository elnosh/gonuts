//go:build integration

package mint_test

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"testing"

	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/testutils"
	"github.com/lightningnetwork/lnd/lnrpc"
)

var (
	ctx             context.Context
	bitcoind        *btcdocker.Bitcoind
	lnd1            *btcdocker.Lnd
	lnd2            *btcdocker.Lnd
	testMint        *mint.Mint
	dbMigrationPath = "./storage/sqlite/migrations"
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
	testMint, err = testutils.CreateTestMint(lnd1, testMintPath, dbMigrationPath, 0, mint.MintLimits{})
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

func TestMintQuoteState(t *testing.T) {
	var mintAmount uint64 = 42000
	mintQuoteResponse, err := testMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	keyset := testMint.GetActiveKeyset()

	// test invalid method
	_, err = testMint.GetMintQuoteState("strike", mintQuoteResponse.Id)
	if !errors.Is(err, cashu.PaymentMethodNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test invalid quote
	_, err = testMint.GetMintQuoteState(testutils.BOLT11_METHOD, "mintquote1234")
	if !errors.Is(err, cashu.QuoteNotExistErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.QuoteNotExistErr, err)
	}

	// test quote state before paying invoice
	quoteStateResponse, err := testMint.GetMintQuoteState(testutils.BOLT11_METHOD, mintQuoteResponse.Id)
	if err != nil {
		t.Fatalf("unexpected error getting quote state: %v", err)
	}
	if quoteStateResponse.State != nut04.Unpaid {
		t.Fatalf("expected quote state '%v' but got '%v' instead", nut04.Unpaid.String(), quoteStateResponse.State.String())
	}

	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintQuoteResponse.PaymentRequest,
	}
	response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		t.Fatalf("error paying invoice: %v", response.PaymentError)
	}

	// test quote state after paying invoice
	quoteStateResponse, err = testMint.GetMintQuoteState(testutils.BOLT11_METHOD, mintQuoteResponse.Id)
	if err != nil {
		t.Fatalf("unexpected error getting quote state: %v", err)
	}
	if quoteStateResponse.State != nut04.Paid {
		t.Fatalf("expected quote state '%v' but got '%v' instead", nut04.Paid.String(), quoteStateResponse.State.String())
	}

	blindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, keyset)

	// mint tokens
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error minting tokens: %v", err)
	}

	// test quote state after minting tokens
	quoteStateResponse, err = testMint.GetMintQuoteState(testutils.BOLT11_METHOD, mintQuoteResponse.Id)
	if err != nil {
		t.Fatalf("unexpected error getting quote state: %v", err)
	}
	if quoteStateResponse.State != nut04.Issued {
		t.Fatalf("expected quote state '%v' but got '%v' instead", nut04.Issued.String(), quoteStateResponse.State.String())
	}

}

func TestMintTokens(t *testing.T) {
	var mintAmount uint64 = 42000
	mintQuoteResponse, err := testMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	keyset := testMint.GetActiveKeyset()

	blindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, keyset)

	// test without paying invoice
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, blindedMessages)
	if !errors.Is(err, cashu.MintQuoteRequestNotPaid) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MintQuoteRequestNotPaid, err)
	}

	// test invalid quote
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, "mintquote1234", blindedMessages)
	if !errors.Is(err, cashu.QuoteNotExistErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.QuoteNotExistErr, err)
	}

	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintQuoteResponse.PaymentRequest,
	}
	response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		t.Fatalf("error paying invoice: %v", response.PaymentError)
	}

	// test with blinded messages over request mint amount
	overBlindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount+100, keyset)
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, overBlindedMessages)
	if !errors.Is(err, cashu.OutputsOverQuoteAmountErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.OutputsOverQuoteAmountErr, err)
	}

	// test with invalid keyset in blinded messages
	invalidKeyset := crypto.MintKeyset{Id: "0192384aa"}
	invalidKeysetMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, invalidKeyset)
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, invalidKeysetMessages)
	if !errors.Is(err, cashu.UnknownKeysetErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.UnknownKeysetErr, err)
	}

	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error minting tokens: %v", err)
	}

	// test already minted tokens
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, blindedMessages)
	if !errors.Is(err, cashu.MintQuoteAlreadyIssued) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MintQuoteAlreadyIssued, err)
	}
}

func TestSwap(t *testing.T) {
	var amount uint64 = 10000
	proofs, err := testutils.GetValidProofsForAmount(amount, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	keyset := testMint.GetActiveKeyset()

	newBlindedMessages, _, _, err := testutils.CreateBlindedMessages(amount, keyset)
	overBlindedMessages, _, _, err := testutils.CreateBlindedMessages(amount+200, keyset)

	// test blinded messages over proofs amount
	_, err = testMint.Swap(proofs, overBlindedMessages)
	if !errors.Is(err, cashu.InsufficientProofsAmount) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InsufficientProofsAmount, err)
	}

	// test with duplicates in proofs list passed
	proofsLen := len(proofs)
	duplicateProofs := make(cashu.Proofs, proofsLen)
	copy(duplicateProofs, proofs)
	duplicateProofs[proofsLen-2] = duplicateProofs[proofsLen-1]
	_, err = testMint.Swap(duplicateProofs, newBlindedMessages)
	if !errors.Is(err, cashu.DuplicateProofs) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.DuplicateProofs, err)
	}

	// valid proofs
	_, err = testMint.Swap(proofs, newBlindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error in swap: %v", err)
	}

	// test already used proofs
	_, err = testMint.Swap(proofs, newBlindedMessages)
	if !errors.Is(err, cashu.ProofAlreadyUsedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.ProofAlreadyUsedErr, err)
	}

	// mint with fees
	mintFeesPath := filepath.Join(".", "mintfees")
	mintFees, err := testutils.CreateTestMint(lnd1, mintFeesPath, dbMigrationPath, 100, mint.MintLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(mintFeesPath)
	}()

	amount = 5000
	proofs, err = testutils.GetValidProofsForAmount(amount, mintFees, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	keyset = mintFees.GetActiveKeyset()

	fees := mintFees.TransactionFees(proofs)
	invalidAmtblindedMessages, _, _, err := testutils.CreateBlindedMessages(amount, keyset)
	validAmtBlindedMessages, _, _, err := testutils.CreateBlindedMessages(amount-uint64(fees), keyset)

	// test swap with proofs provided below amount needed + fees
	_, err = mintFees.Swap(proofs, invalidAmtblindedMessages)
	if !errors.Is(err, cashu.InsufficientProofsAmount) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InsufficientProofsAmount, err)
	}

	// swap with correct amount accounting for fees
	_, err = mintFees.Swap(proofs, validAmtBlindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error in swap: %v", err)
	}
}

func TestRequestMeltQuote(t *testing.T) {
	invoice := lnrpc.Invoice{Value: 10000}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	// test invalid method
	_, err = testMint.RequestMeltQuote("strike", addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if !errors.Is(err, cashu.PaymentMethodNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test invalid unit
	_, err = testMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, "eth")
	if !errors.Is(err, cashu.UnitNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.UnitNotSupportedErr, err)
	}

	// test invalid invoice
	_, err = testMint.RequestMeltQuote(testutils.BOLT11_METHOD, "invoice1111", testutils.SAT_UNIT)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	_, err = testMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}
}

func TestMeltQuoteState(t *testing.T) {
	invoice := lnrpc.Invoice{Value: 2000}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	lookupInvoice, err := lnd2.Client.LookupInvoice(ctx, &lnrpc.PaymentHash{RHash: addInvoiceResponse.RHash})
	if err != nil {
		t.Fatalf("error finding invoice: %v", err)
	}

	meltRequest, err := testMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	// test invalid method
	_, err = testMint.GetMeltQuoteState("strike", meltRequest.Id)
	if !errors.Is(err, cashu.PaymentMethodNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test invalid quote id
	_, err = testMint.GetMeltQuoteState(testutils.BOLT11_METHOD, "quote1234")
	if !errors.Is(err, cashu.QuoteNotExistErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test before paying melt
	meltQuote, err := testMint.GetMeltQuoteState(testutils.BOLT11_METHOD, meltRequest.Id)
	if err != nil {
		t.Fatalf("unexpected error getting melt quote state: %v", err)
	}
	if meltQuote.State != nut05.Unpaid {
		t.Fatalf("expected quote state '%v' but got '%v' instead", nut05.Unpaid.String(), meltQuote.State.String())
	}

	// test state after melting
	validProofs, err := testutils.GetValidProofsForAmount(6500, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	_, err = testMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}

	meltQuote, err = testMint.GetMeltQuoteState(testutils.BOLT11_METHOD, meltRequest.Id)
	if err != nil {
		t.Fatalf("unexpected error getting melt quote state: %v", err)
	}
	if meltQuote.State != nut05.Paid {
		t.Fatalf("expected quote state '%v' but got '%v' instead", nut05.Paid.String(), meltQuote.State.String())
	}
	preimageString := hex.EncodeToString(lookupInvoice.RPreimage)
	if meltQuote.Preimage != preimageString {
		t.Fatalf("expected quote preimage '%v' but got '%v' instead", preimageString, meltQuote.Preimage)
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

	meltQuote, err := testMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
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

	// test with duplicates in proofs list passed
	proofsLen := len(validProofs)
	duplicateProofs := make(cashu.Proofs, proofsLen)
	copy(duplicateProofs, validProofs)
	duplicateProofs[proofsLen-2] = duplicateProofs[proofsLen-1]
	_, err = testMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, duplicateProofs)
	if !errors.Is(err, cashu.DuplicateProofs) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.DuplicateProofs, err)
	}

	melt, err := testMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if melt.State != nut05.Paid {
		t.Fatal("got unexpected unpaid melt quote")
	}

	// test quote already paid
	_, err = testMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if !errors.Is(err, cashu.MeltQuoteAlreadyPaid) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MeltQuoteAlreadyPaid, err)
	}

	// test already used proofs
	newQuote, err := testMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}
	_, err = testMint.MeltTokens(testutils.BOLT11_METHOD, newQuote.Id, validProofs)
	if !errors.Is(err, cashu.ProofAlreadyUsedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.ProofAlreadyUsedErr, err)
	}

	// mint with fees
	mintFeesPath := filepath.Join(".", "mintfeesmelt")
	mintFees, err := testutils.CreateTestMint(lnd1, mintFeesPath, dbMigrationPath, 100, mint.MintLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(mintFeesPath)
	}()

	amount = 6000
	underProofs, err = testutils.GetValidProofsForAmount(amount, mintFees, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	amount = 6500
	validProofsWithFees, err := testutils.GetValidProofsForAmount(amount, mintFees, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	addInvoiceResponse, err = lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	meltQuote, err = mintFees.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	// test proofs below needed amount with fees
	_, err = mintFees.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, underProofs)
	if !errors.Is(err, cashu.InsufficientProofsAmount) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InsufficientProofsAmount, err)
	}

	// test valid proofs accounting for fees
	melt, err = mintFees.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, validProofsWithFees)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if melt.State != nut05.Paid {
		t.Fatal("got unexpected unpaid melt quote")
	}
	if melt.State != nut05.Paid {
		t.Fatal("got unexpected unpaid melt quote")
	}

}

func TestMintLimits(t *testing.T) {
	// setup mint with limits
	limitsMintPath := filepath.Join(".", "limitsMint")
	mintLimits := mint.MintLimits{
		MaxBalance:      15000,
		MintingSettings: mint.MintMethodSettings{MaxAmount: 10000},
		MeltingSettings: mint.MeltMethodSettings{MaxAmount: 10000},
	}

	limitsMint, err := testutils.CreateTestMint(
		lnd1,
		limitsMintPath,
		dbMigrationPath,
		100,
		mintLimits,
	)

	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(limitsMintPath)
	}()

	keyset := limitsMint.GetActiveKeyset()

	// test above mint max amount
	var mintAmount uint64 = 20000
	mintQuoteResponse, err := limitsMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, testutils.SAT_UNIT)
	if !errors.Is(err, cashu.MintAmountExceededErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MintAmountExceededErr, err)
	}

	// amount below max limit
	mintAmount = 9500
	mintQuoteResponse, err = limitsMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	blindedMessages, secrets, rs, _ := testutils.CreateBlindedMessages(mintAmount, keyset)

	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintQuoteResponse.PaymentRequest,
	}
	response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		t.Fatalf("error paying invoice: %v", response.PaymentError)
	}
	blindedSignatures, err := limitsMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error minting tokens: %v", err)
	}

	// test request mint that will make it go above max balance
	mintQuoteResponse, err = limitsMint.RequestMintQuote(testutils.BOLT11_METHOD, 9000, testutils.SAT_UNIT)
	if !errors.Is(err, cashu.MintingDisabled) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MintingDisabled, err)
	}

	// test melt with invoice over max melt amount
	invoice := lnrpc.Invoice{Value: 15000}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	_, err = limitsMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if !errors.Is(err, cashu.MeltAmountExceededErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MeltAmountExceededErr, err)
	}

	// test melt with invoice within limit
	validProofs, err := testutils.ConstructProofs(blindedSignatures, secrets, rs, &keyset)
	invoice = lnrpc.Invoice{Value: 8000}
	addInvoiceResponse, err = lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	meltQuote, err := limitsMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}
	_, err = limitsMint.MeltTokens(testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}

	// this should be within max balance now
	mintQuoteResponse, err = limitsMint.RequestMintQuote(testutils.BOLT11_METHOD, 9000, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error requesting mint quote: %v", err)
	}

}
