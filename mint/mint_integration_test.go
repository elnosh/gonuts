//go:build integration

package mint_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut07"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
	"github.com/elnosh/gonuts/cashu/nuts/nut12"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/testutils"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/invoicesrpc"
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
	cashuErr, ok := err.(*cashu.Error)
	if !ok {
		t.Fatalf("got unexpected non-Cashu error: %v", err)
	}
	if cashuErr.Code != cashu.UnitErrCode {
		t.Fatalf("expected cashu error code '%v' but got '%v' instead", cashu.UnitErrCode, cashuErr.Code)
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
		t.Fatalf("expected quote state '%s' but got '%s' instead", nut04.Unpaid, quoteStateResponse.State)
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
		t.Fatalf("expected quote state '%s' but got '%s' instead", nut04.Paid, quoteStateResponse.State)
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
		t.Fatalf("expected quote state '%s' but got '%s' instead", nut04.Issued, quoteStateResponse.State)
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

	// test mint with blinded messages already signed
	mintQuoteResponse, err = testMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	sendPaymentRequest = lnrpc.SendRequest{
		PaymentRequest: mintQuoteResponse.PaymentRequest,
	}
	response, _ = lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		t.Fatalf("error paying invoice: %v", response.PaymentError)
	}

	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, blindedMessages)
	if !errors.Is(err, cashu.BlindedMessageAlreadySigned) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.BlindedMessageAlreadySigned, err)
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

	proofs, err = testutils.GetValidProofsForAmount(amount, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	// test with blinded messages already signed
	_, err = testMint.Swap(proofs, newBlindedMessages)
	if !errors.Is(err, cashu.BlindedMessageAlreadySigned) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.BlindedMessageAlreadySigned, err)
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
	cashuErr, ok := err.(*cashu.Error)
	if !ok {
		t.Fatalf("got unexpected non-Cashu error: %v", err)
	}
	if cashuErr.Code != cashu.UnitErrCode {
		t.Fatalf("expected cashu error code '%v' but got '%v' instead", cashu.UnitErrCode, cashuErr.Code)
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
	_, err = testMint.GetMeltQuoteState(ctx, "strike", meltRequest.Id)
	if !errors.Is(err, cashu.PaymentMethodNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test invalid quote id
	_, err = testMint.GetMeltQuoteState(ctx, testutils.BOLT11_METHOD, "quote1234")
	if !errors.Is(err, cashu.QuoteNotExistErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test before paying melt
	meltQuote, err := testMint.GetMeltQuoteState(ctx, testutils.BOLT11_METHOD, meltRequest.Id)
	if err != nil {
		t.Fatalf("unexpected error getting melt quote state: %v", err)
	}
	if meltQuote.State != nut05.Unpaid {
		t.Fatalf("expected quote state '%s' but got '%s' instead", nut05.Unpaid, meltQuote.State)
	}

	// test state after melting
	validProofs, err := testutils.GetValidProofsForAmount(6500, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	_, err = testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}

	meltQuote, err = testMint.GetMeltQuoteState(ctx, testutils.BOLT11_METHOD, meltRequest.Id)
	if err != nil {
		t.Fatalf("unexpected error getting melt quote state: %v", err)
	}
	if meltQuote.State != nut05.Paid {
		t.Fatalf("expected quote state '%s' but got '%s' instead", nut05.Paid, meltQuote.State)
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
	_, err = testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, underProofs)
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
	_, err = testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if !errors.Is(err, cashu.InvalidProofErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvalidProofErr, err)
	}

	validProofs[0].Secret = validSecret

	// test with duplicates in proofs list passed
	proofsLen := len(validProofs)
	duplicateProofs := make(cashu.Proofs, proofsLen)
	copy(duplicateProofs, validProofs)
	duplicateProofs[proofsLen-2] = duplicateProofs[proofsLen-1]
	_, err = testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, duplicateProofs)
	if !errors.Is(err, cashu.DuplicateProofs) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.DuplicateProofs, err)
	}

	melt, err := testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if melt.State != nut05.Paid {
		t.Fatal("got unexpected unpaid melt quote")
	}

	// test quote already paid
	_, err = testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if !errors.Is(err, cashu.MeltQuoteAlreadyPaid) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MeltQuoteAlreadyPaid, err)
	}

	// test already used proofs
	newQuote, err := testMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}
	_, err = testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, newQuote.Id, validProofs)
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
	_, err = mintFees.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, underProofs)
	if !errors.Is(err, cashu.InsufficientProofsAmount) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InsufficientProofsAmount, err)
	}

	// test valid proofs accounting for fees
	melt, err = mintFees.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, validProofsWithFees)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if melt.State != nut05.Paid {
		t.Fatal("got unexpected unpaid melt quote")
	}

	// test failed lightning payment
	lnd3, err := btcdocker.NewLnd(ctx, bitcoind)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		lnd3.Terminate(ctx)
	}()
	// create invoice from node for which there is no route so payment fails
	noRoute := lnrpc.Invoice{Value: 2000}
	noRouteInvoice, err := lnd3.Client.AddInvoice(ctx, &noRoute)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	meltQuote, err = testMint.RequestMeltQuote(testutils.BOLT11_METHOD, noRouteInvoice.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	validProofs, err = testutils.GetValidProofsForAmount(6500, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	meltResponse, err := testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if meltResponse.State != nut05.Unpaid {
		// expecting unpaid since payment should have failed
		t.Fatalf("expected melt quote with state of '%s' but got '%s' instead", nut05.Unpaid, meltResponse.State)
	}

	// test internal quotes (mint and melt quotes with same invoice)
	var mintAmount uint64 = 42000
	mintQuoteResponse, err := testMint.RequestMintQuote(testutils.BOLT11_METHOD, mintAmount, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}
	keyset := testMint.GetActiveKeyset()
	blindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, keyset)

	proofs, err := testutils.GetValidProofsForAmount(mintAmount, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	meltQuote, err = testMint.RequestMeltQuote(testutils.BOLT11_METHOD, mintQuoteResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}
	if meltQuote.FeeReserve != 0 {
		t.Fatal("RequestMeltQuote did not return fee reserve of 0 for internal quote")
	}

	melt, err = testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, proofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if len(melt.Preimage) == 0 {
		t.Fatal("melt returned empty preimage")
	}

	// now mint should work because quote was settled internally
	_, err = testMint.MintTokens(testutils.BOLT11_METHOD, mintQuoteResponse.Id, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error in mint: %v", err)
	}
}

func TestPendingProofs(t *testing.T) {
	// use hodl invoice to cause payment to stuck and put quote and proofs in state of pending
	preimage, _ := testutils.GenerateRandomBytes()
	hash := sha256.Sum256(preimage)
	hodlInvoice := invoicesrpc.AddHoldInvoiceRequest{Hash: hash[:], Value: 2100}
	addHodlInvoiceRes, err := lnd2.InvoicesClient.AddHoldInvoice(ctx, &hodlInvoice)
	if err != nil {
		t.Fatalf("error creating hodl invoice: %v", err)
	}

	meltQuote, err := testMint.RequestMeltQuote(testutils.BOLT11_METHOD, addHodlInvoiceRes.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	validProofs, err := testutils.GetValidProofsForAmount(2200, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	// custom context just for this melt call to timeout after 5s and return pending
	// for the stuck hodl invoice
	meltContext, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	melt, err := testMint.MeltTokens(meltContext, testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if melt.State != nut05.Pending {
		t.Fatalf("expected melt quote with state of '%s' but got '%s' instead", nut05.Pending, melt.State)
	}

	meltQuote, err = testMint.GetMeltQuoteState(meltContext, testutils.BOLT11_METHOD, meltQuote.Id)
	if err != nil {
		t.Fatalf("unexpected error getting melt quote state: %v", err)
	}
	if meltQuote.State != nut05.Pending {
		t.Fatalf("expected melt quote with state of '%s' but got '%s' instead", nut05.Pending, melt.State)
	}

	Ys := make([]string, len(validProofs))
	for i, proof := range validProofs {
		Y, _ := crypto.HashToCurve([]byte(proof.Secret))
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		Ys[i] = Yhex
	}

	states, err := testMint.ProofsStateCheck(Ys)
	if err != nil {
		t.Fatalf("unexpected error checking states of proofs: %v", err)
	}
	for _, proofState := range states {
		if proofState.State != nut07.Pending {
			t.Fatalf("expected pending proof but got '%s' instead", proofState.State)
		}
	}

	_, err = testMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if !errors.Is(err, cashu.MeltQuotePending) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MeltQuotePending, err)
	}

	// try to use currently pending proofs in another op.
	// swap should return err saying proofs are pending
	blindedMessages, _, _, _ := testutils.CreateBlindedMessages(validProofs.Amount(), testMint.GetActiveKeyset())
	_, err = testMint.Swap(validProofs, blindedMessages)
	if !errors.Is(err, cashu.ProofPendingErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.ProofPendingErr, err)
	}

	settleHodlInvoice := invoicesrpc.SettleInvoiceMsg{Preimage: preimage}
	_, err = lnd2.InvoicesClient.SettleInvoice(ctx, &settleHodlInvoice)
	if err != nil {
		t.Fatalf("error settling hodl invoice: %v", err)
	}

	meltQuote, err = testMint.GetMeltQuoteState(ctx, testutils.BOLT11_METHOD, melt.Id)
	if err != nil {
		t.Fatalf("unexpected error getting melt quote state: %v", err)
	}
	if meltQuote.State != nut05.Paid {
		t.Fatalf("expected melt quote with state of '%s' but got '%s' instead", nut05.Paid, meltQuote.State)
	}

	expectedPreimage := hex.EncodeToString(preimage)
	if meltQuote.Preimage != expectedPreimage {
		t.Fatalf("expected melt quote with preimage of '%v' but got '%v' instead", preimage, meltQuote.Preimage)
	}

	states, err = testMint.ProofsStateCheck(Ys)
	if err != nil {
		t.Fatalf("unexpected error checking states of proofs: %v", err)
	}

	for _, proofState := range states {
		if proofState.State != nut07.Spent {
			t.Fatalf("expected spent proof but got '%s' instead", proofState.State)
		}
	}
}

func TestProofsStateCheck(t *testing.T) {
	validProofs, err := testutils.GetValidProofsForAmount(5000, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	Ys := make([]string, len(validProofs))
	for i, proof := range validProofs {
		Y, _ := crypto.HashToCurve([]byte(proof.Secret))
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		Ys[i] = Yhex
	}

	proofStates, err := testMint.ProofsStateCheck(Ys)
	if err != nil {
		t.Fatalf("unexpected error checking proof states: %v", err)
	}

	// proofs should be unspent here
	for _, proofState := range proofStates {
		if proofState.State != nut07.Unspent {
			t.Fatalf("expected proof state '%s' but got '%s'", nut07.Unspent, proofState.State)
		}
	}

	// spend proofs and check spent state in response from mint
	proofsToSpend := cashu.Proofs{}
	numProofs := len(validProofs) / 2
	Ys = make([]string, numProofs)
	for i := 0; i < numProofs; i++ {
		proofsToSpend = append(proofsToSpend, validProofs[i])
		Y, _ := crypto.HashToCurve([]byte(validProofs[i].Secret))
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		Ys[i] = Yhex
	}

	blindedMessages, _, _, _ := testutils.CreateBlindedMessages(proofsToSpend.Amount(), testMint.GetActiveKeyset())
	_, err = testMint.Swap(proofsToSpend, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	proofStates, err = testMint.ProofsStateCheck(Ys)
	if err != nil {
		t.Fatalf("unexpected error checking proof states: %v", err)
	}

	for _, proofState := range proofStates {
		if proofState.State != nut07.Spent {
			t.Fatalf("expected proof state '%s' but got '%s'", nut07.Spent, proofState.State)
		}
	}
}

func TestRestoreSignatures(t *testing.T) {
	// create blinded messages
	blindedMessages, _, _, blindedSignatures, err := testutils.GetBlindedSignatures(5000, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating blinded signatures: %v", err)
	}

	outputs, signatures, err := testMint.RestoreSignatures(blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error restoring signatures: %v\n", err)
	}

	if len(outputs) != len(signatures) {
		t.Fatalf("length of ouputs '%v' does not match length of signatures '%v'\n", len(outputs), len(signatures))
	}
	if !reflect.DeepEqual(blindedMessages, outputs) {
		t.Fatal("outputs in request do not match outputs from mint response")
	}
	if !reflect.DeepEqual(blindedSignatures, signatures) {
		t.Fatal("blinded signatures do not match signatures from mint response")
	}

	// test with blinded messages that have not been previously signed
	unsigned, _, _, _ := testutils.CreateBlindedMessages(4200, testMint.GetActiveKeyset())
	outputs, signatures, err = testMint.RestoreSignatures(unsigned)
	if err != nil {
		t.Fatalf("unexpected error restoring signatures: %v\n", err)
	}

	// response should be empty
	if len(outputs) != 0 && len(signatures) != 0 {
		t.Fatalf("expected empty outputs and signatures but got %v and %v\n", len(outputs), len(signatures))
	}

	// test with only a portion of blinded messages signed
	partial := append(blindedMessages, unsigned...)
	outputs, signatures, err = testMint.RestoreSignatures(partial)
	if err != nil {
		t.Fatalf("unexpected error restoring signatures: %v\n", err)
	}

	if len(outputs) != len(signatures) {
		t.Fatalf("length of ouputs '%v' does not match length of signatures '%v'\n", len(outputs), len(signatures))
	}
	if !reflect.DeepEqual(blindedMessages, outputs) {
		t.Fatal("outputs in request do not match outputs from mint response")
	}
	if !reflect.DeepEqual(blindedSignatures, signatures) {
		t.Fatal("blinded signatures do not match signatures from mint response")
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
	_, err = limitsMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, validProofs)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}

	// this should be within max balance now
	mintQuoteResponse, err = limitsMint.RequestMintQuote(testutils.BOLT11_METHOD, 9000, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error requesting mint quote: %v", err)
	}
}

func TestNUT11P2PK(t *testing.T) {
	lock, _ := btcec.NewPrivateKey()

	p2pkMintPath := filepath.Join(".", "p2pkmint")
	p2pkMint, err := testutils.CreateTestMint(lnd1, p2pkMintPath, dbMigrationPath, 0, mint.MintLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(p2pkMintPath)
	}()

	keyset := p2pkMint.GetActiveKeyset()

	var mintAmount uint64 = 1500
	lockedProofs, err := testutils.GetProofsWithLock(mintAmount, lock.PubKey(), nut11.P2PKTags{}, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	blindedMessages, _, _, _ := testutils.CreateBlindedMessages(mintAmount, keyset)

	// swap with proofs that do not have valid witness
	_, err = p2pkMint.Swap(lockedProofs, blindedMessages)
	if !errors.Is(err, nut11.InvalidWitness) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.InvalidWitness, err)
	}

	// invalid proofs signed with another key
	anotherKey, _ := btcec.NewPrivateKey()
	invalidProofs, _ := nut11.AddSignatureToInputs(lockedProofs, anotherKey)
	_, err = p2pkMint.Swap(invalidProofs, blindedMessages)
	if !errors.Is(err, nut11.NotEnoughSignaturesErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.NotEnoughSignaturesErr, err)
	}

	// valid signed proofs
	signedProofs, _ := nut11.AddSignatureToInputs(lockedProofs, lock)
	_, err = p2pkMint.Swap(signedProofs, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	// test multisig
	key1, _ := btcec.NewPrivateKey()
	key2, _ := btcec.NewPrivateKey()
	multisigKeys := []*btcec.PublicKey{key1.PubKey(), key2.PubKey()}
	tags := nut11.P2PKTags{
		Sigflag: nut11.SIGALL,
		NSigs:   2,
		Pubkeys: multisigKeys,
	}
	multisigProofs, err := testutils.GetProofsWithLock(mintAmount, lock.PubKey(), tags, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}

	// proofs with only 1 signature but require 2
	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)
	notEnoughSigsProofs, _ := nut11.AddSignatureToInputs(multisigProofs, lock)
	_, err = p2pkMint.Swap(notEnoughSigsProofs, blindedMessages)
	if !errors.Is(err, nut11.NotEnoughSignaturesErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.NotEnoughSignaturesErr, err)
	}

	signingKeys := []*btcec.PrivateKey{key1, key2}
	// enough signatures but blinded messages not signed
	signedProofs, _ = testutils.AddSignaturesToInputs(multisigProofs, signingKeys)
	_, err = p2pkMint.Swap(signedProofs, blindedMessages)
	if !errors.Is(err, nut11.InvalidWitness) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.InvalidWitness, err)
	}

	// inputs and outputs with valid signatures
	signedBlindedMessages, _ := testutils.AddSignaturesToOutputs(blindedMessages, signingKeys)
	_, err = p2pkMint.Swap(signedProofs, signedBlindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	// test with locktime
	tags = nut11.P2PKTags{
		Locktime: time.Now().Add(time.Minute * 1).Unix(),
	}
	locktimeProofs, err := testutils.GetProofsWithLock(mintAmount, lock.PubKey(), tags, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)
	// unsigned proofs
	_, err = p2pkMint.Swap(locktimeProofs, blindedMessages)
	if !errors.Is(err, nut11.InvalidWitness) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.InvalidWitness, err)
	}

	signedProofs, _ = nut11.AddSignatureToInputs(locktimeProofs, lock)
	_, err = p2pkMint.Swap(signedProofs, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	tags = nut11.P2PKTags{
		Locktime: time.Now().Add(-(time.Minute * 10)).Unix(),
	}
	locktimeProofs, err = testutils.GetProofsWithLock(mintAmount, lock.PubKey(), tags, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}

	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)
	// locktime expired so spendable without signature
	_, err = p2pkMint.Swap(locktimeProofs, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	// test locktime expired but with refund keys
	tags = nut11.P2PKTags{
		Locktime: time.Now().Add(-(time.Minute * 10)).Unix(),
		Refund:   []*btcec.PublicKey{key1.PubKey()},
	}
	locktimeProofs, err = testutils.GetProofsWithLock(mintAmount, lock.PubKey(), tags, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	// unsigned proofs should fail because there were refund pubkeys in the tags
	_, err = p2pkMint.Swap(locktimeProofs, blindedMessages)
	if err == nil {
		t.Fatal("expected error but got 'nil' instead")
	}

	// sign with refund pubkey
	signedProofs, _ = testutils.AddSignaturesToInputs(locktimeProofs, []*btcec.PrivateKey{key1})
	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)
	_, err = p2pkMint.Swap(signedProofs, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	// get locked proofs for melting
	lockedProofs, err = testutils.GetProofsWithLock(mintAmount, lock.PubKey(), nut11.P2PKTags{}, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}

	invoice := lnrpc.Invoice{Value: 500}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	meltQuote, err := p2pkMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	_, err = p2pkMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, lockedProofs)
	if !errors.Is(err, nut11.InvalidWitness) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.InvalidWitness, err)
	}

	signedProofs, _ = testutils.AddSignaturesToInputs(lockedProofs, []*btcec.PrivateKey{lock})
	_, err = p2pkMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, lockedProofs)
	if err != nil {
		t.Fatalf("unexpected error melting: %v", err)
	}

	// test melt with SIG_ALL fails
	tags = nut11.P2PKTags{
		Sigflag: nut11.SIGALL,
	}
	lockedProofs, err = testutils.GetProofsWithLock(mintAmount, lock.PubKey(), tags, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	signedProofs, _ = testutils.AddSignaturesToInputs(lockedProofs, []*btcec.PrivateKey{lock})

	invoice = lnrpc.Invoice{Value: 500}
	addInvoiceResponse, err = lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	meltQuote, err = p2pkMint.RequestMeltQuote(testutils.BOLT11_METHOD, addInvoiceResponse.PaymentRequest, testutils.SAT_UNIT)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}
	_, err = p2pkMint.MeltTokens(ctx, testutils.BOLT11_METHOD, meltQuote.Id, lockedProofs)
	if !errors.Is(err, nut11.SigAllOnlySwap) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.SigAllOnlySwap, err)
	}
}

func TestDLEQProofs(t *testing.T) {
	var amount uint64 = 5000
	proofs, err := testutils.GetValidProofsForAmount(amount, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	keyset := testMint.GetActiveKeyset()

	// check proofs minted from testMint have valid DLEQ proofs
	for _, proof := range proofs {
		if proof.DLEQ == nil {
			t.Fatal("mint returned nil DLEQ proof")
		}

		if !nut12.VerifyProofDLEQ(proof, keyset.Keys[proof.Amount].PublicKey) {
			t.Fatal("generated invalid DLEQ proof from MintTokens")
		}
	}

	blindedMessages, _, _, err := testutils.CreateBlindedMessages(amount, keyset)
	blindSignatures, err := testMint.Swap(proofs, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	for i, sig := range blindSignatures {
		if sig.DLEQ == nil {
			t.Fatal("mint returned nil DLEQ proof from swap")
		}
		if !nut12.VerifyBlindSignatureDLEQ(
			*sig.DLEQ,
			keyset.Keys[sig.Amount].PublicKey,
			blindedMessages[i].B_,
			sig.C_,
		) {
			t.Fatal("mint generated invalid DLEQ proof in swap")
		}
	}
}
