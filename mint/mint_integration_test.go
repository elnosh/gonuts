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
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
	"github.com/elnosh/gonuts/cashu/nuts/nut12"
	"github.com/elnosh/gonuts/cashu/nuts/nut14"
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
	mintQuoteRequest := nut04.PostMintQuoteBolt11Request{Amount: mintAmount, Unit: cashu.Sat.String()}
	_, err := testMint.RequestMintQuote(mintQuoteRequest)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	// test invalid unit
	mintQuoteRequest = nut04.PostMintQuoteBolt11Request{Amount: mintAmount, Unit: "eth"}
	_, err = testMint.RequestMintQuote(mintQuoteRequest)
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
	mintQuoteRequest := nut04.PostMintQuoteBolt11Request{Amount: mintAmount, Unit: cashu.Sat.String()}
	mintQuoteResponse, err := testMint.RequestMintQuote(mintQuoteRequest)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	keyset := testMint.GetActiveKeyset()

	// test invalid quote
	_, err = testMint.GetMintQuoteState("mintquote1234")
	if !errors.Is(err, cashu.QuoteNotExistErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.QuoteNotExistErr, err)
	}

	// test quote state before paying invoice
	quoteStateResponse, err := testMint.GetMintQuoteState(mintQuoteResponse.Id)
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
	quoteStateResponse, err = testMint.GetMintQuoteState(mintQuoteResponse.Id)
	if err != nil {
		t.Fatalf("unexpected error getting quote state: %v", err)
	}
	if quoteStateResponse.State != nut04.Paid {
		t.Fatalf("expected quote state '%s' but got '%s' instead", nut04.Paid, quoteStateResponse.State)
	}

	blindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, keyset)

	// mint tokens
	mintTokensRequest := nut04.PostMintBolt11Request{Quote: mintQuoteResponse.Id, Outputs: blindedMessages}
	_, err = testMint.MintTokens(mintTokensRequest)
	if err != nil {
		t.Fatalf("got unexpected error minting tokens: %v", err)
	}

	// test quote state after minting tokens
	quoteStateResponse, err = testMint.GetMintQuoteState(mintQuoteResponse.Id)
	if err != nil {
		t.Fatalf("unexpected error getting quote state: %v", err)
	}
	if quoteStateResponse.State != nut04.Issued {
		t.Fatalf("expected quote state '%s' but got '%s' instead", nut04.Issued, quoteStateResponse.State)
	}

}

func TestMintTokens(t *testing.T) {
	var mintAmount uint64 = 42000
	mintQuoteRequest := nut04.PostMintQuoteBolt11Request{Amount: mintAmount, Unit: cashu.Sat.String()}
	mintQuoteResponse, err := testMint.RequestMintQuote(mintQuoteRequest)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}

	keyset := testMint.GetActiveKeyset()
	blindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, keyset)

	// test without paying invoice
	mintTokensRequest := nut04.PostMintBolt11Request{Quote: mintQuoteResponse.Id, Outputs: blindedMessages}
	_, err = testMint.MintTokens(mintTokensRequest)
	if !errors.Is(err, cashu.MintQuoteRequestNotPaid) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MintQuoteRequestNotPaid, err)
	}

	// test invalid quote
	mintTokensRequest = nut04.PostMintBolt11Request{Quote: "mintquote1234", Outputs: blindedMessages}
	_, err = testMint.MintTokens(mintTokensRequest)
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
	mintTokensRequest = nut04.PostMintBolt11Request{Quote: mintQuoteResponse.Id, Outputs: overBlindedMessages}
	_, err = testMint.MintTokens(mintTokensRequest)
	if !errors.Is(err, cashu.OutputsOverQuoteAmountErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.OutputsOverQuoteAmountErr, err)
	}

	// test with invalid keyset in blinded messages
	invalidKeyset := crypto.MintKeyset{Id: "0192384aa"}
	invalidKeysetMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, invalidKeyset)
	mintTokensRequest = nut04.PostMintBolt11Request{Quote: mintQuoteResponse.Id, Outputs: invalidKeysetMessages}
	_, err = testMint.MintTokens(mintTokensRequest)
	if !errors.Is(err, cashu.UnknownKeysetErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.UnknownKeysetErr, err)
	}

	mintTokensRequest = nut04.PostMintBolt11Request{Quote: mintQuoteResponse.Id, Outputs: blindedMessages}
	_, err = testMint.MintTokens(mintTokensRequest)
	if err != nil {
		t.Fatalf("got unexpected error minting tokens: %v", err)
	}

	// test already minted tokens
	_, err = testMint.MintTokens(mintTokensRequest)
	if !errors.Is(err, cashu.MintQuoteAlreadyIssued) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MintQuoteAlreadyIssued, err)
	}

	// test mint with blinded messages already signed
	mintQuoteRequest = nut04.PostMintQuoteBolt11Request{Amount: mintAmount, Unit: cashu.Sat.String()}
	mintQuoteResponse, err = testMint.RequestMintQuote(mintQuoteRequest)
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

	mintTokensRequest = nut04.PostMintBolt11Request{Quote: mintQuoteResponse.Id, Outputs: blindedMessages}
	_, err = testMint.MintTokens(mintTokensRequest)
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
	paymentRequest := addInvoiceResponse.PaymentRequest

	// test invalid unit
	meltQuoteRequest := nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: "eth"}
	_, err = testMint.RequestMeltQuote(meltQuoteRequest)
	cashuErr, ok := err.(*cashu.Error)
	if !ok {
		t.Fatalf("got unexpected non-Cashu error: %v", err)
	}
	if cashuErr.Code != cashu.UnitErrCode {
		t.Fatalf("expected cashu error code '%v' but got '%v' instead", cashu.UnitErrCode, cashuErr.Code)
	}

	// test invalid invoice
	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{Request: "invoice1111", Unit: cashu.Sat.String()}
	_, err = testMint.RequestMeltQuote(meltQuoteRequest)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	_, err = testMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	// trying to create another melt quote with same invoice should throw error
	_, err = testMint.RequestMeltQuote(meltQuoteRequest)
	if !errors.Is(err, cashu.MeltQuoteForRequestExists) {
		//if !errors.Is(err, cashu.PaymentMethodNotSupportedErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MeltQuoteForRequestExists, err)
	}
}

func TestMeltQuoteState(t *testing.T) {
	invoice := lnrpc.Invoice{Value: 2000}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	paymentRequest := addInvoiceResponse.PaymentRequest

	lookupInvoice, err := lnd2.Client.LookupInvoice(ctx, &lnrpc.PaymentHash{RHash: addInvoiceResponse.RHash})
	if err != nil {
		t.Fatalf("error finding invoice: %v", err)
	}

	meltQuoteRequest := nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltRequest, err := testMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	// test invalid quote id
	_, err = testMint.GetMeltQuoteState(ctx, "quote1234")
	if !errors.Is(err, cashu.QuoteNotExistErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.PaymentMethodNotSupportedErr, err)
	}

	// test before paying melt
	meltQuote, err := testMint.GetMeltQuoteState(ctx, meltRequest.Id)
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

	meltTokensRequest := nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}

	meltQuote, err = testMint.GetMeltQuoteState(ctx, meltRequest.Id)
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
	paymentRequest := addInvoiceResponse.PaymentRequest

	meltQuoteRequest := nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err := testMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	// test proofs amount under melt amount
	meltTokensRequest := nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: underProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
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
	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
	if !errors.Is(err, cashu.InvalidProofErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InvalidProofErr, err)
	}

	validProofs[0].Secret = validSecret

	// test with duplicates in proofs list passed
	proofsLen := len(validProofs)
	duplicateProofs := make(cashu.Proofs, proofsLen)
	copy(duplicateProofs, validProofs)
	duplicateProofs[proofsLen-2] = duplicateProofs[proofsLen-1]
	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: duplicateProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
	if !errors.Is(err, cashu.DuplicateProofs) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.DuplicateProofs, err)
	}

	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofs}
	melt, err := testMint.MeltTokens(ctx, meltTokensRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if melt.State != nut05.Paid {
		t.Fatal("got unexpected unpaid melt quote")
	}

	// test quote already paid
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
	if !errors.Is(err, cashu.MeltQuoteAlreadyPaid) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MeltQuoteAlreadyPaid, err)
	}

	invoice = lnrpc.Invoice{Value: 6000}
	addInvoiceResponse, err = lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	paymentRequest = addInvoiceResponse.PaymentRequest

	// test already used proofs
	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	newQuote, err := testMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: newQuote.Id, Inputs: validProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
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
	paymentRequest = addInvoiceResponse.PaymentRequest

	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err = mintFees.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	// test proofs below needed amount with fees
	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: underProofs}
	_, err = mintFees.MeltTokens(ctx, meltTokensRequest)
	if !errors.Is(err, cashu.InsufficientProofsAmount) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.InsufficientProofsAmount, err)
	}

	// test valid proofs accounting for fees
	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofsWithFees}
	melt, err = mintFees.MeltTokens(ctx, meltTokensRequest)
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
	paymentRequest = noRouteInvoice.PaymentRequest

	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err = testMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	validProofs, err = testutils.GetValidProofsForAmount(6500, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofs}
	meltResponse, err := testMint.MeltTokens(ctx, meltTokensRequest)
	if meltResponse.State != nut05.Unpaid {
		// expecting unpaid since payment should have failed
		t.Fatalf("expected melt quote with state of '%s' but got '%s' instead", nut05.Unpaid, meltResponse.State)
	}

	// test internal quotes (mint and melt quotes with same invoice)
	var mintAmount uint64 = 42000
	mintQuoteRequest := nut04.PostMintQuoteBolt11Request{Amount: mintAmount, Unit: cashu.Sat.String()}
	mintQuoteResponse, err := testMint.RequestMintQuote(mintQuoteRequest)
	if err != nil {
		t.Fatalf("error requesting mint quote: %v", err)
	}
	keyset := testMint.GetActiveKeyset()
	blindedMessages, _, _, err := testutils.CreateBlindedMessages(mintAmount, keyset)

	proofs, err := testutils.GetValidProofsForAmount(mintAmount, testMint, lnd2)
	if err != nil {
		t.Fatalf("error generating valid proofs: %v", err)
	}

	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{
		Request: mintQuoteResponse.PaymentRequest,
		Unit:    cashu.Sat.String(),
	}
	meltQuote, err = testMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}
	if meltQuote.FeeReserve != 0 {
		t.Fatal("RequestMeltQuote did not return fee reserve of 0 for internal quote")
	}

	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: proofs}
	melt, err = testMint.MeltTokens(ctx, meltTokensRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if len(melt.Preimage) == 0 {
		t.Fatal("melt returned empty preimage")
	}

	// now mint should work because quote was settled internally
	mintTokensRequest := nut04.PostMintBolt11Request{Quote: mintQuoteResponse.Id, Outputs: blindedMessages}
	_, err = testMint.MintTokens(mintTokensRequest)
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
	paymentRequest := addHodlInvoiceRes.PaymentRequest

	meltQuoteRequest := nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err := testMint.RequestMeltQuote(meltQuoteRequest)
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

	meltTokensRequest := nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofs}
	melt, err := testMint.MeltTokens(meltContext, meltTokensRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if melt.State != nut05.Pending {
		t.Fatalf("expected melt quote with state of '%s' but got '%s' instead", nut05.Pending, melt.State)
	}

	meltQuote, err = testMint.GetMeltQuoteState(meltContext, meltQuote.Id)
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

	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
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

	meltQuote, err = testMint.GetMeltQuoteState(ctx, melt.Id)
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
	mintQuoteRequest := nut04.PostMintQuoteBolt11Request{Amount: mintAmount, Unit: cashu.Sat.String()}
	mintQuoteResponse, err := limitsMint.RequestMintQuote(mintQuoteRequest)
	if !errors.Is(err, cashu.MintAmountExceededErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MintAmountExceededErr, err)
	}

	// amount below max limit
	mintAmount = 9500
	mintQuoteRequest = nut04.PostMintQuoteBolt11Request{Amount: mintAmount, Unit: cashu.Sat.String()}
	mintQuoteResponse, err = limitsMint.RequestMintQuote(mintQuoteRequest)
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

	mintTokensRequest := nut04.PostMintBolt11Request{Quote: mintQuoteResponse.Id, Outputs: blindedMessages}
	blindedSignatures, err := limitsMint.MintTokens(mintTokensRequest)
	if err != nil {
		t.Fatalf("got unexpected error minting tokens: %v", err)
	}

	// test request mint that will make it go above max balance
	mintQuoteRequest = nut04.PostMintQuoteBolt11Request{Amount: 9000, Unit: cashu.Sat.String()}
	mintQuoteResponse, err = limitsMint.RequestMintQuote(mintQuoteRequest)
	if !errors.Is(err, cashu.MintingDisabled) {
		t.Fatalf("expected error '%v' but got '%v' instead", cashu.MintingDisabled, err)
	}

	// test melt with invoice over max melt amount
	invoice := lnrpc.Invoice{Value: 15000}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	paymentRequest := addInvoiceResponse.PaymentRequest

	meltQuoteRequest := nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	_, err = limitsMint.RequestMeltQuote(meltQuoteRequest)
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
	paymentRequest = addInvoiceResponse.PaymentRequest

	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err := limitsMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	meltTokensRequest := nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofs}
	_, err = limitsMint.MeltTokens(ctx, meltTokensRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}

	// this should be within max balance now
	mintQuoteRequest = nut04.PostMintQuoteBolt11Request{Amount: 9000, Unit: cashu.Sat.String()}
	mintQuoteResponse, err = limitsMint.RequestMintQuote(mintQuoteRequest)
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
	hexPubkey := hex.EncodeToString(lock.PubKey().SerializeCompressed())
	p2pkSpendingCondition := nut10.SpendingCondition{
		Kind: nut10.P2PK,
		Data: hexPubkey,
	}
	lockedProofs, err := testutils.GetProofsWithSpendingCondition(mintAmount, p2pkSpendingCondition, p2pkMint, lnd2)
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
	p2pkSpendingCondition.Tags = nut11.SerializeP2PKTags(tags)
	multisigProofs, err := testutils.GetProofsWithSpendingCondition(mintAmount, p2pkSpendingCondition, p2pkMint, lnd2)
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
	signedProofs, _ = testutils.AddP2PKWitnessToInputs(multisigProofs, signingKeys)
	_, err = p2pkMint.Swap(signedProofs, blindedMessages)
	if !errors.Is(err, nut11.InvalidWitness) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.InvalidWitness, err)
	}

	// inputs and outputs with valid signatures
	signedBlindedMessages, _ := testutils.AddP2PKWitnessToOutputs(blindedMessages, signingKeys)
	_, err = p2pkMint.Swap(signedProofs, signedBlindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	// test with locktime
	tags = nut11.P2PKTags{
		Locktime: time.Now().Add(time.Minute * 1).Unix(),
	}
	p2pkSpendingCondition.Tags = nut11.SerializeP2PKTags(tags)
	locktimeProofs, err := testutils.GetProofsWithSpendingCondition(mintAmount, p2pkSpendingCondition, p2pkMint, lnd2)
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
	p2pkSpendingCondition.Tags = nut11.SerializeP2PKTags(tags)
	locktimeProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, p2pkSpendingCondition, p2pkMint, lnd2)
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
	p2pkSpendingCondition.Tags = nut11.SerializeP2PKTags(tags)
	locktimeProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, p2pkSpendingCondition, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	// unsigned proofs should fail because there were refund pubkeys in the tags
	_, err = p2pkMint.Swap(locktimeProofs, blindedMessages)
	if err == nil {
		t.Fatal("expected error but got 'nil' instead")
	}

	// sign with refund pubkey
	signedProofs, _ = testutils.AddP2PKWitnessToInputs(locktimeProofs, []*btcec.PrivateKey{key1})
	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)
	_, err = p2pkMint.Swap(signedProofs, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	// get locked proofs for melting
	p2pkSpendingCondition.Tags = [][]string{}
	lockedProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, p2pkSpendingCondition, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}

	invoice := lnrpc.Invoice{Value: 500}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	paymentRequest := addInvoiceResponse.PaymentRequest

	meltQuoteRequest := nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err := p2pkMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	meltTokensRequest := nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: lockedProofs}
	_, err = p2pkMint.MeltTokens(ctx, meltTokensRequest)
	if !errors.Is(err, nut11.InvalidWitness) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.InvalidWitness, err)
	}

	signedProofs, _ = testutils.AddP2PKWitnessToInputs(lockedProofs, []*btcec.PrivateKey{lock})
	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: signedProofs}
	_, err = p2pkMint.MeltTokens(ctx, meltTokensRequest)
	if err != nil {
		t.Fatalf("unexpected error melting: %v", err)
	}

	// test melt with SIG_ALL fails
	tags = nut11.P2PKTags{
		Sigflag: nut11.SIGALL,
	}
	p2pkSpendingCondition.Tags = nut11.SerializeP2PKTags(tags)
	lockedProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, p2pkSpendingCondition, p2pkMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	signedProofs, _ = testutils.AddP2PKWitnessToInputs(lockedProofs, []*btcec.PrivateKey{lock})

	invoice = lnrpc.Invoice{Value: 500}
	addInvoiceResponse, err = lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	paymentRequest = addInvoiceResponse.PaymentRequest

	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err = p2pkMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: signedProofs}
	_, err = p2pkMint.MeltTokens(ctx, meltTokensRequest)
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

func TestHTLC(t *testing.T) {
	var mintAmount uint64 = 1500
	preimage := "111111"
	preimageBytes, _ := hex.DecodeString(preimage)
	hashBytes := sha256.Sum256(preimageBytes)
	hash := hex.EncodeToString(hashBytes[:])
	htlcSpendingCondition := nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
	}
	lockedProofs, err := testutils.GetProofsWithSpendingCondition(mintAmount, htlcSpendingCondition, testMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	keyset := testMint.GetActiveKeyset()
	blindedMessages, _, _, _ := testutils.CreateBlindedMessages(mintAmount, keyset)

	// test with proofs that do not have a witness
	_, err = testMint.Swap(lockedProofs, blindedMessages)
	if !errors.Is(err, nut14.InvalidPreimageErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut14.InvalidPreimageErr, err)
	}

	// test with invalid preimage to hash
	proofs, _ := testutils.AddHTLCWitnessToInputs(lockedProofs, "000000", nil)
	_, err = testMint.Swap(proofs, blindedMessages)
	if !errors.Is(err, nut14.InvalidPreimageErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut14.InvalidPreimageErr, err)
	}

	// test with valid preimage
	proofs, _ = testutils.AddHTLCWitnessToInputs(lockedProofs, preimage, nil)
	_, err = testMint.Swap(proofs, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error swapping HTLC proofs: %v", err)
	}

	// test with signature required
	signingKey, _ := btcec.NewPrivateKey()
	tags := nut11.P2PKTags{
		NSigs:   1,
		Pubkeys: []*btcec.PublicKey{signingKey.PubKey()},
	}
	htlcSpendingCondition = nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
		Tags: nut11.SerializeP2PKTags(tags),
	}
	lockedProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, htlcSpendingCondition, testMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)

	// test requiring signature but witness only has preimage
	lockedProofs, _ = testutils.AddHTLCWitnessToInputs(lockedProofs, preimage, nil)
	_, err = testMint.Swap(lockedProofs, blindedMessages)
	if !errors.Is(err, nut11.NoSignaturesErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.NoSignaturesErr, err)
	}

	// test valid preimage but with invalid signature
	anotherKey, _ := btcec.NewPrivateKey()
	invalidProofs, _ := testutils.AddHTLCWitnessToInputs(lockedProofs, preimage, anotherKey)
	_, err = testMint.Swap(invalidProofs, blindedMessages)
	if !errors.Is(err, nut11.NotEnoughSignaturesErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.NotEnoughSignaturesErr, err)
	}

	// test with valid preimage and valid signatures
	validProofs, _ := testutils.AddHTLCWitnessToInputs(lockedProofs, preimage, signingKey)
	_, err = testMint.Swap(validProofs, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error swapping HTLC proofs: %v", err)
	}

	// test multisig
	multisigKeys := []*btcec.PublicKey{signingKey.PubKey(), anotherKey.PubKey()}
	tags = nut11.P2PKTags{
		NSigs:   2,
		Pubkeys: multisigKeys,
	}
	serializedTags := nut11.SerializeP2PKTags(tags)
	htlcSpendingCondition = nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
		Tags: serializedTags,
	}
	multisigHTLC, err := testutils.GetProofsWithSpendingCondition(mintAmount, htlcSpendingCondition, testMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)

	// test with valid preimage and 1 signature but require 2
	notEnoughSigsProofs, _ := testutils.AddHTLCWitnessToInputs(multisigHTLC, preimage, signingKey)
	_, err = testMint.Swap(notEnoughSigsProofs, blindedMessages)
	if !errors.Is(err, nut11.NotEnoughSignaturesErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.NotEnoughSignaturesErr, err)
	}

	// test SIG_ALL flag
	tags = nut11.P2PKTags{
		Sigflag: nut11.SIGALL,
		NSigs:   1,
		Pubkeys: []*btcec.PublicKey{signingKey.PubKey()},
	}
	htlcSpendingCondition = nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
		Tags: nut11.SerializeP2PKTags(tags),
	}
	lockedProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, htlcSpendingCondition, testMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)

	// test only inputs signed
	proofs, _ = testutils.AddHTLCWitnessToInputs(lockedProofs, preimage, signingKey)
	blindedMessages, _ = testutils.AddHTLCWitnessToOutputs(blindedMessages, preimage, nil)
	_, err = testMint.Swap(proofs, blindedMessages)
	if !errors.Is(err, nut11.NotEnoughSignaturesErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.NotEnoughSignaturesErr, err)
	}

	// add signatures to outputs for SIG_ALL
	blindedMessages, err = testutils.AddHTLCWitnessToOutputs(blindedMessages, preimage, signingKey)
	_, err = testMint.Swap(proofs, blindedMessages)
	if err != nil {
		t.Fatalf("got unexpected error swapping HTLC proofs: %v", err)
	}

	// test with locktime
	tags = nut11.P2PKTags{
		Locktime: time.Now().Add(-(time.Minute * 10)).Unix(),
	}
	htlcSpendingCondition = nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
		Tags: nut11.SerializeP2PKTags(tags),
	}
	locktimeProofs, err := testutils.GetProofsWithSpendingCondition(mintAmount, htlcSpendingCondition, testMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}

	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)
	// locktime expired so spendable without signature
	_, err = testMint.Swap(locktimeProofs, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	// test locktime expired but with refund keys
	tags = nut11.P2PKTags{
		Locktime: time.Now().Add(-(time.Minute * 10)).Unix(),
		Refund:   []*btcec.PublicKey{signingKey.PubKey()},
	}
	htlcSpendingCondition = nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
		Tags: nut11.SerializeP2PKTags(tags),
	}
	locktimeProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, htlcSpendingCondition, testMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	// unsigned proofs should fail because there were refund pubkeys in the tags
	_, err = testMint.Swap(locktimeProofs, blindedMessages)
	if err == nil {
		t.Fatal("expected error but got 'nil' instead")
	}

	// sign with refund pubkey
	signedProofs, _ := testutils.AddHTLCWitnessToInputs(locktimeProofs, "", signingKey)
	blindedMessages, _, _, _ = testutils.CreateBlindedMessages(mintAmount, keyset)
	_, err = testMint.Swap(signedProofs, blindedMessages)
	if err != nil {
		t.Fatalf("unexpected error in swap: %v", err)
	}

	// get locked proofs for melting
	htlcSpendingCondition = nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
		Tags: [][]string{},
	}
	lockedProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, htlcSpendingCondition, testMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}

	invoice := lnrpc.Invoice{Value: 500}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	paymentRequest := addInvoiceResponse.PaymentRequest

	meltQuoteRequest := nut05.PostMeltQuoteBolt11Request{Request: paymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err := testMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	meltTokensRequest := nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: lockedProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
	if !errors.Is(err, nut14.InvalidPreimageErr) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut14.InvalidPreimageErr, err)
	}

	validProofs, _ = testutils.AddHTLCWitnessToInputs(lockedProofs, preimage, nil)
	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: validProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
	if err != nil {
		t.Fatalf("unexpected error melting: %v", err)
	}

	// test melt with SIG_ALL fails
	tags = nut11.P2PKTags{
		Sigflag: nut11.SIGALL,
	}
	htlcSpendingCondition = nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
		Tags: nut11.SerializeP2PKTags(tags),
	}
	lockedProofs, err = testutils.GetProofsWithSpendingCondition(mintAmount, htlcSpendingCondition, testMint, lnd2)
	if err != nil {
		t.Fatalf("error getting locked proofs: %v", err)
	}
	lockedProofs, _ = testutils.AddHTLCWitnessToInputs(lockedProofs, preimage, signingKey)

	invoice = lnrpc.Invoice{Value: 500}
	addInvoiceResponse, err = lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	meltQuoteRequest = nut05.PostMeltQuoteBolt11Request{Request: addInvoiceResponse.PaymentRequest, Unit: cashu.Sat.String()}
	meltQuote, err = testMint.RequestMeltQuote(meltQuoteRequest)
	if err != nil {
		t.Fatalf("got unexpected error in melt request: %v", err)
	}

	meltTokensRequest = nut05.PostMeltBolt11Request{Quote: meltQuote.Id, Inputs: lockedProofs}
	_, err = testMint.MeltTokens(ctx, meltTokensRequest)
	if !errors.Is(err, nut11.SigAllOnlySwap) {
		t.Fatalf("expected error '%v' but got '%v' instead", nut11.SigAllOnlySwap, err)
	}
}
