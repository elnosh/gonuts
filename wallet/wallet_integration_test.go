//go:build integration

package wallet_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"slices"
	"testing"

	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut12"
	"github.com/elnosh/gonuts/testutils"
	"github.com/elnosh/gonuts/wallet"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/invoicesrpc"
)

var (
	ctx             context.Context
	bitcoind        *btcdocker.Bitcoind
	lnd1            *btcdocker.Lnd
	lnd2            *btcdocker.Lnd
	dbMigrationPath = "../mint/storage/sqlite/migrations"
	nutshellMint    *testutils.NutshellMintContainer
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
	testMint, err := testutils.CreateTestMintServer(lnd1, "3338", testMintPath, dbMigrationPath, 0)
	if err != nil {
		log.Println(err)
		return 1
	}
	defer func() {
		os.RemoveAll(testMintPath)
	}()
	go func() {
		log.Fatal(testMint.Start())
	}()

	mintPath := filepath.Join(".", "testmintwithfees")
	mintWithFees, err := testutils.CreateTestMintServer(lnd1, "8888", mintPath, dbMigrationPath, 100)
	if err != nil {
		log.Println(err)
		return 1
	}
	defer func() {
		os.RemoveAll(mintPath)
	}()
	go func() {
		log.Fatal(mintWithFees.Start())
	}()

	nutshellMint, err = testutils.CreateNutshellMintContainer(ctx, 0)
	if err != nil {
		log.Fatalf("error starting nutshell mint: %v", err)
	}
	defer nutshellMint.Terminate(ctx)

	return m.Run()
}

func TestMintTokens(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testmintwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, "http://127.0.0.1:3338")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	var mintAmount uint64 = 30000
	// check no err
	mintRes, err := testWallet.RequestMint(mintAmount)
	if err != nil {
		t.Fatalf("error requesting mint: %v", err)
	}

	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintRes.Request,
	}
	response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		t.Fatalf("error paying invoice: %v", response.PaymentError)
	}

	mintInvoice, _ := testWallet.GetInvoiceByPaymentRequest(mintRes.Request)
	if mintInvoice == nil {
		t.Fatal("got unexpected nil invoice")
	}

	proofs, err := testWallet.MintTokens(mintInvoice.Id)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	if proofs.Amount() != mintAmount {
		t.Fatalf("expected proofs amount of '%v' but got '%v' instead", mintAmount, proofs.Amount())
	}

	// non-existent quote
	_, err = testWallet.MintTokens("id198274")
	if err == nil {
		t.Fatalf("expected error but got nil")
	}
}

func TestSend(t *testing.T) {
	mintURL := "http://127.0.0.1:3338"
	testWalletPath := filepath.Join(".", "/testsendwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	err = testutils.FundCashuWallet(ctx, testWallet, lnd2, 30000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	var sendAmount uint64 = 4200
	proofsToSend, err := testWallet.Send(sendAmount, mintURL, true)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	if proofsToSend.Amount() != sendAmount {
		t.Fatalf("expected token amount of '%v' but got '%v' instead", sendAmount, proofsToSend.Amount())
	}

	// test with invalid mint
	_, err = testWallet.Send(sendAmount, "http://nonexistent.mint", true)
	if !errors.Is(err, wallet.ErrMintNotExist) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrMintNotExist, err)
	}

	// insufficient balance in wallet
	_, err = testWallet.Send(2000000, mintURL, true)
	if !errors.Is(err, wallet.ErrInsufficientMintBalance) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrInsufficientMintBalance, err)
	}

	// test mint with fees
	mintWithFeesURL := "http://127.0.0.1:8888"
	feesWalletPath := filepath.Join(".", "/testsendwalletfees")
	feesWallet, err := testutils.CreateTestWallet(feesWalletPath, mintWithFeesURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(feesWalletPath)
	}()

	err = testutils.FundCashuWallet(ctx, feesWallet, lnd2, 10000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	sendAmount = 2000
	proofsToSend, err = feesWallet.Send(sendAmount, mintWithFeesURL, true)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	fees, err := testutils.Fees(proofsToSend, mintWithFeesURL)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	if proofsToSend.Amount() != sendAmount+uint64(fees) {
		t.Fatalf("expected token amount of '%v' but got '%v' instead", sendAmount+uint64(fees), proofsToSend.Amount())
	}

	// send without fees to receive
	proofsToSend, err = feesWallet.Send(sendAmount, mintWithFeesURL, false)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	if proofsToSend.Amount() != sendAmount {
		t.Fatalf("expected token amount of '%v' but got '%v' instead", sendAmount+uint64(fees), proofsToSend.Amount())
	}
}

func TestReceive(t *testing.T) {
	mintURL := "http://127.0.0.1:3338"
	testWalletPath := filepath.Join(".", "/testreceivewallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	err = testutils.FundCashuWallet(ctx, testWallet, lnd2, 30000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	testMintPath := filepath.Join(".", "testmint2")
	testMint, err := testutils.CreateTestMintServer(lnd2, "3339", testMintPath, dbMigrationPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testMintPath)
	}()
	go func() {
		t.Fatal(testMint.Start())
	}()

	mint2URL := "http://127.0.0.1:3339"
	testWalletPath2 := filepath.Join(".", "/testreceivewallet2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mint2URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath2)
	}()

	err = testutils.FundCashuWallet(ctx, testWallet2, lnd1, 15000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	proofsToSend, err := testWallet2.Send(1500, mint2URL, true)
	if err != nil {
		t.Fatalf("got unexpected error in send: %v", err)
	}
	token, _ := cashu.NewTokenV4(proofsToSend, mint2URL, testutils.SAT_UNIT, false)

	// test receive swap == true
	_, err = testWallet.Receive(token, true)
	if err != nil {
		t.Fatalf("got unexpected error in receive: %v", err)
	}
	trustedMints := testWallet.TrustedMints()
	// there should only be 1 trusted mint since it was swapped to the default mint
	if len(trustedMints) != 1 {
		t.Fatalf("expected len of trusted mints '%v' but got '%v' instead", 1, len(trustedMints))
	}
	defaultMint := "http://127.0.0.1:3338"
	if !slices.Contains(trustedMints, defaultMint) {
		t.Fatalf("expected '%v' in list of trusted of trusted mints", defaultMint)
	}

	proofsToSend, err = testWallet2.Send(1500, mint2URL, true)
	if err != nil {
		t.Fatalf("got unexpected error in send: %v", err)
	}
	token, _ = cashu.NewTokenV4(proofsToSend, mint2URL, testutils.SAT_UNIT, false)

	// test receive swap == false
	_, err = testWallet.Receive(token, false)
	if err != nil {
		t.Fatalf("got unexpected error in receive: %v", err)
	}

	trustedMints = testWallet.TrustedMints()
	// mint from received token should be added to trusted mint if swap is false
	if len(trustedMints) != 2 {
		t.Fatalf("expected len of trusted mints '%v' but got '%v' instead", 2, len(trustedMints))
	}
	if !slices.Contains(trustedMints, mint2URL) {
		t.Fatalf("expected '%v' in list of trusted of trusted mints", mint2URL)
	}
}

func TestReceiveFees(t *testing.T) {
	// mint with fees url
	mintURL := "http://127.0.0.1:8888"
	testWalletPath := filepath.Join(".", "/testreceivefees")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	err = testutils.FundCashuWallet(ctx, testWallet, lnd2, 30000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	testWalletPath2 := filepath.Join(".", "/testreceivefees2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath2)
	}()

	var sendAmount uint64 = 2000
	proofsToSend, err := testWallet.Send(sendAmount, mintURL, true)
	if err != nil {
		t.Fatalf("got unexpected error in send: %v", err)
	}
	token, _ := cashu.NewTokenV4(proofsToSend, mintURL, testutils.SAT_UNIT, false)

	amountReceived, err := testWallet2.Receive(token, false)
	if err != nil {
		t.Fatalf("got unexpected error in receive: %v", err)
	}

	fees, err := testutils.Fees(proofsToSend, mintURL)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	if amountReceived != proofsToSend.Amount()-uint64(fees) {
		t.Fatalf("expected received amount of '%v' but got '%v' instead", proofsToSend.Amount()-uint64(fees), amountReceived)
	}
}

func TestMelt(t *testing.T) {
	mintURL := "http://127.0.0.1:3338"
	testWalletPath := filepath.Join(".", "/testmeltwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	err = testutils.FundCashuWallet(ctx, testWallet, lnd2, 30000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	// create invoice for melt request
	invoice := lnrpc.Invoice{Value: 10000}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	meltResponse, err := testWallet.Melt(addInvoiceResponse.PaymentRequest, mintURL)
	if err != nil {
		t.Fatalf("got unexpected melt error: %v", err)
	}
	if !meltResponse.Paid {
		t.Fatalf("expected paid melt")
	}

	// try melt for invoice over balance
	invoice = lnrpc.Invoice{Value: 6000000}
	addInvoiceResponse, err = lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}
	_, err = testWallet.Melt(addInvoiceResponse.PaymentRequest, mintURL)
	if !errors.Is(err, wallet.ErrInsufficientMintBalance) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrInsufficientMintBalance, err)
	}

	_, err = testWallet.Melt(addInvoiceResponse.PaymentRequest, "http://nonexistent.mint")
	if !errors.Is(err, wallet.ErrMintNotExist) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrMintNotExist, err)
	}

	// test melt with fees
	mintWithFeesURL := "http://127.0.0.1:8888"
	feesWalletPath := filepath.Join(".", "/testsendwalletfees")
	feesWallet, err := testutils.CreateTestWallet(feesWalletPath, mintWithFeesURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(feesWalletPath)
	}()

	err = testutils.FundCashuWallet(ctx, feesWallet, lnd2, 10000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	// create invoice for melt request
	invoice = lnrpc.Invoice{Value: 5000}
	addInvoiceResponse, err = lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	meltResponse, err = feesWallet.Melt(addInvoiceResponse.PaymentRequest, mintWithFeesURL)
	if err != nil {
		t.Fatalf("got unexpected melt error: %v", err)
	}
	if !meltResponse.Paid {
		t.Fatalf("expected paid melt")
	}
}

// check balance is correct after certain operations
func TestWalletBalance(t *testing.T) {
	mintURL := "http://127.0.0.1:3338"
	testWalletPath := filepath.Join(".", "/testwalletbalance")
	balanceTestWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	// test balance after mint request
	var mintAmount uint64 = 20000
	mintRequest, err := balanceTestWallet.RequestMint(mintAmount)
	if err != nil {
		t.Fatalf("unexpected error in mint request: %v", err)
	}

	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintRequest.Request,
	}
	response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		t.Fatalf("error paying invoice: %v", response.PaymentError)
	}
	_, err = balanceTestWallet.MintTokens(mintRequest.Quote)
	if err != nil {
		t.Fatalf("unexpected error in mint tokens: %v", err)
	}

	if balanceTestWallet.GetBalance() != mintAmount {
		t.Fatalf("expected balance of '%v' but got '%v' instead", mintAmount, balanceTestWallet.GetBalance())
	}
	mintBalance := balanceTestWallet.GetBalanceByMints()[mintURL]
	if mintBalance != mintAmount {
		t.Fatalf("expected mint balance of '%v' but got '%v' instead", mintAmount, mintBalance)
	}

	balance := balanceTestWallet.GetBalance()
	// test balance after send
	var sendAmount uint64 = 1200
	_, err = balanceTestWallet.Send(sendAmount, mintURL, true)
	if err != nil {
		t.Fatalf("unexpected error in send: %v", err)
	}
	if balanceTestWallet.GetBalance() != balance-sendAmount {
		t.Fatalf("expected balance of '%v' but got '%v' instead", balance-sendAmount, balanceTestWallet.GetBalance())
	}

	// test balance is same after failed melt request
	invoice := lnrpc.Invoice{Value: 5000}
	addInvoiceResponse, err := lnd1.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	balanceBeforeMelt := balanceTestWallet.GetBalance()
	// doing self-payment so this should make melt return unpaid
	meltresponse, err := balanceTestWallet.Melt(addInvoiceResponse.PaymentRequest, mintURL)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if meltresponse.State != nut05.Unpaid {
		t.Fatalf("expected melt with unpaid state but got '%v'", meltresponse.State.String())
	}

	// check balance is same after failed melt
	if balanceTestWallet.GetBalance() != balanceBeforeMelt {
		t.Fatalf("expected balance of '%v' but got '%v' instead", balanceBeforeMelt, balanceTestWallet.GetBalance())
	}
}

// check balance is correct after ops with fees
func TestWalletBalanceFees(t *testing.T) {
	mintURL := "http://127.0.0.1:8888"
	testWalletPath := filepath.Join(".", "/testwalletbalancefees")
	balanceTestWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	err = testutils.FundCashuWallet(ctx, balanceTestWallet, lnd2, 30000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	testWalletPath2 := filepath.Join(".", "/testreceivefees2")
	balanceTestWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath2)
	}()

	sendAmounts := []uint64{1200, 2000, 5000}

	for _, sendAmount := range sendAmounts {
		proofsToSend, err := balanceTestWallet.Send(sendAmount, mintURL, true)
		if err != nil {
			t.Fatalf("unexpected error in send: %v", err)
		}
		token, _ := cashu.NewTokenV4(proofsToSend, mintURL, testutils.SAT_UNIT, false)

		// test balance in receiving wallet
		balanceBeforeReceive := balanceTestWallet2.GetBalance()
		_, err = balanceTestWallet2.Receive(token, false)
		if err != nil {
			t.Fatalf("got unexpected error: %v", err)
		}
		expectedBalance := balanceBeforeReceive + sendAmount
		if balanceTestWallet2.GetBalance() != expectedBalance {
			t.Fatalf("expected balance of '%v' but got '%v' instead", expectedBalance, balanceTestWallet2.GetBalance())
		}
	}

	// test without including fees in send
	for _, sendAmount := range sendAmounts {
		proofsToSend, err := balanceTestWallet.Send(sendAmount, mintURL, false)
		if err != nil {
			t.Fatalf("unexpected error in send: %v", err)
		}
		token, _ := cashu.NewTokenV4(proofsToSend, mintURL, testutils.SAT_UNIT, false)

		fees, err := testutils.Fees(proofsToSend, mintURL)
		if err != nil {
			t.Fatalf("got unexpected error: %v", err)
		}

		// test balance in receiving wallet
		balanceBeforeReceive := balanceTestWallet2.GetBalance()
		_, err = balanceTestWallet2.Receive(token, false)
		if err != nil {
			t.Fatalf("got unexpected error: %v", err)
		}
		// expected balance should be the sending amount minus fees
		// since those were not included
		expectedBalance := balanceBeforeReceive + sendAmount - uint64(fees)
		if balanceTestWallet2.GetBalance() != expectedBalance {
			t.Fatalf("expected balance of '%v' but got '%v' instead", expectedBalance, balanceTestWallet2.GetBalance())
		}
	}
}

func TestPendingProofs(t *testing.T) {
	mintURL := "http://127.0.0.1:3338"
	testWalletPath := filepath.Join(".", "/testpendingwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	var fundingBalance uint64 = 15000
	if err := testutils.FundCashuWallet(ctx, testWallet, lnd2, fundingBalance); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	// use hodl invoice to cause melt to get stuck in pending
	preimage, _ := testutils.GenerateRandomBytes()
	hash := sha256.Sum256(preimage)
	hodlInvoice := invoicesrpc.AddHoldInvoiceRequest{Hash: hash[:], Value: 2100}
	addHodlInvoiceRes, err := lnd2.InvoicesClient.AddHoldInvoice(ctx, &hodlInvoice)
	if err != nil {
		t.Fatalf("error creating hodl invoice: %v", err)
	}

	meltQuote, err := testWallet.Melt(addHodlInvoiceRes.PaymentRequest, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error in melt: %v", err)
	}
	if meltQuote.State != nut05.Pending {
		t.Fatalf("expected quote state of '%s' but got '%s' instead", nut05.Pending, meltQuote.State)
	}

	// check amount of pending proofs is same as quote amount
	pendingProofsAmount := wallet.Amount(testWallet.GetPendingProofs())
	expectedAmount := meltQuote.Amount + meltQuote.FeeReserve
	if pendingProofsAmount != expectedAmount {
		t.Fatalf("expected amount of pending proofs of '%v' but got '%v' instead",
			expectedAmount, pendingProofsAmount)
	}
	pendingBalance := testWallet.PendingBalance()
	expectedPendingBalance := meltQuote.Amount + meltQuote.FeeReserve
	if pendingBalance != expectedPendingBalance {
		t.Fatalf("expected pending balance of '%v' but got '%v' instead",
			expectedPendingBalance, pendingBalance)
	}

	// there should be 1 pending quote
	pendingMeltQuotes := testWallet.GetPendingMeltQuotes()
	if len(pendingMeltQuotes) != 1 {
		t.Fatalf("expected '%v' pending quote but got '%v' instead", 1, len(pendingMeltQuotes))
	}
	if pendingMeltQuotes[0] != meltQuote.Quote {
		t.Fatalf("expected pending quote with id '%v' but got '%v' instead",
			meltQuote.Quote, pendingMeltQuotes[0])
	}

	// settle hodl invoice and test that there are no pending proofs now
	settleHodlInvoice := invoicesrpc.SettleInvoiceMsg{Preimage: preimage}
	_, err = lnd2.InvoicesClient.SettleInvoice(ctx, &settleHodlInvoice)
	if err != nil {
		t.Fatalf("error settling hodl invoice: %v", err)
	}

	meltQuoteStateResponse, err := testWallet.CheckMeltQuoteState(meltQuote.Quote)
	if err != nil {
		t.Fatalf("unexpected error checking melt quote state: %v", err)
	}
	if meltQuoteStateResponse.State != nut05.Paid {
		t.Fatalf("expected quote state of '%s' but got '%s' instead",
			nut05.Paid, meltQuoteStateResponse.State)
	}

	// check no pending proofs or pending balance after settling and checking melt quote state
	pendingProofsAmount = wallet.Amount(testWallet.GetPendingProofs())
	if pendingProofsAmount != 0 {
		t.Fatalf("expected no pending proofs amount but got '%v' instead", pendingProofsAmount)
	}
	pendingBalance = testWallet.PendingBalance()
	if pendingBalance != 0 {
		t.Fatalf("expected no pending balance but got '%v' instead", pendingBalance)
	}

	// check no pending melt quotes
	pendingMeltQuotes = testWallet.GetPendingMeltQuotes()
	if len(pendingMeltQuotes) != 0 {
		t.Fatalf("expected no pending quotes but got '%v' instead", len(pendingMeltQuotes))
	}

	// test hodl invoice to cause melt to get stuck in pending and then cancel it
	preimage, _ = testutils.GenerateRandomBytes()
	hash = sha256.Sum256(preimage)
	hodlInvoice = invoicesrpc.AddHoldInvoiceRequest{Hash: hash[:], Value: 2100}
	addHodlInvoiceRes, err = lnd2.InvoicesClient.AddHoldInvoice(ctx, &hodlInvoice)
	if err != nil {
		t.Fatalf("error creating hodl invoice: %v", err)
	}

	meltQuote, err = testWallet.Melt(addHodlInvoiceRes.PaymentRequest, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error in melt: %v", err)
	}
	if meltQuote.State != nut05.Pending {
		t.Fatalf("expected quote state of '%s' but got '%s' instead", nut05.Pending, meltQuote.State)
	}
	pendingProofsAmount = wallet.Amount(testWallet.GetPendingProofs())
	expectedAmount = meltQuote.Amount + meltQuote.FeeReserve
	if pendingProofsAmount != expectedAmount {
		t.Fatalf("expected amount of pending proofs of '%v' but got '%v' instead",
			expectedAmount, pendingProofsAmount)
	}
	pendingMeltQuotes = testWallet.GetPendingMeltQuotes()
	if len(pendingMeltQuotes) != 1 {
		t.Fatalf("expected '%v' pending quote but got '%v' instead", 1, len(pendingMeltQuotes))
	}

	cancelInvoice := invoicesrpc.CancelInvoiceMsg{PaymentHash: hash[:]}
	_, err = lnd2.InvoicesClient.CancelInvoice(ctx, &cancelInvoice)
	if err != nil {
		t.Fatalf("error canceling hodl invoice: %v", err)
	}

	meltQuoteStateResponse, err = testWallet.CheckMeltQuoteState(meltQuote.Quote)
	if err != nil {
		t.Fatalf("unexpected error checking melt quote state: %v", err)
	}
	if meltQuoteStateResponse.State != nut05.Unpaid {
		t.Fatalf("expected quote state of '%s' but got '%s' instead",
			nut05.Unpaid, meltQuoteStateResponse.State)
	}

	// check no pending proofs or pending balance after canceling and checking melt quote state
	pendingProofsAmount = wallet.Amount(testWallet.GetPendingProofs())
	if pendingProofsAmount != 0 {
		t.Fatalf("expected no pending proofs amount but got '%v' instead", pendingProofsAmount)
	}
	pendingBalance = testWallet.PendingBalance()
	if pendingBalance != 0 {
		t.Fatalf("expected no pending balance but got '%v' instead", pendingBalance)
	}
	// check no pending melt quotes
	pendingMeltQuotes = testWallet.GetPendingMeltQuotes()
	if len(pendingMeltQuotes) != 0 {
		t.Fatalf("expected no pending quotes but got '%v' instead", len(pendingMeltQuotes))
	}

	// check proofs that were pending were added back to wallet balance
	// so wallet balance at this point should be fundingWalletAmount - firstSuccessfulMeltAmount
	walletBalance := testWallet.GetBalance()
	expectedWalletBalance := fundingBalance - meltQuote.Amount - meltQuote.FeeReserve
	if walletBalance != expectedWalletBalance {
		t.Fatalf("expected wallet balance of '%v' but got '%v' instead",
			expectedWalletBalance, walletBalance)
	}
}

func TestWalletRestore(t *testing.T) {
	mintURL := "http://127.0.0.1:3338"

	testWalletPath := filepath.Join(".", "/testrestorewallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	testWalletPath2 := filepath.Join(".", "/testrestorewallet2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath2)
	}()

	testWalletRestore(t, testWallet, testWallet2, testWalletPath, false)
}

func testWalletRestore(
	t *testing.T,
	testWallet *wallet.Wallet,
	testWallet2 *wallet.Wallet,
	restorePath string,
	fakeBackend bool,
) {
	mintURL := testWallet.CurrentMint()

	var mintAmount uint64 = 20000
	mintRequest, err := testWallet.RequestMint(mintAmount)
	if err != nil {
		t.Fatalf("unexpected error in mint request: %v", err)
	}

	if !fakeBackend {
		//pay invoice
		sendPaymentRequest := lnrpc.SendRequest{
			PaymentRequest: mintRequest.Request,
		}
		response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
		if len(response.PaymentError) > 0 {
			t.Fatalf("error paying invoice: %v", response.PaymentError)
		}
	}

	_, err = testWallet.MintTokens(mintRequest.Quote)
	if err != nil {
		t.Fatalf("unexpected error in mint tokens: %v", err)
	}

	var sendAmount1 uint64 = 5000
	proofsToSend, err := testWallet.Send(sendAmount1, mintURL, true)
	if err != nil {
		t.Fatalf("unexpected error in send: %v", err)
	}
	token, _ := cashu.NewTokenV4(proofsToSend, mintURL, testutils.SAT_UNIT, false)

	_, err = testWallet2.Receive(token, false)
	if err != nil {
		t.Fatalf("got unexpected error in receive: %v", err)
	}

	var sendAmount2 uint64 = 1000
	proofsToSend, err = testWallet.Send(sendAmount2, mintURL, true)
	if err != nil {
		t.Fatalf("unexpected error in send: %v", err)
	}
	token, _ = cashu.NewTokenV4(proofsToSend, mintURL, testutils.SAT_UNIT, false)

	_, err = testWallet2.Receive(token, false)
	if err != nil {
		t.Fatalf("got unexpected error in receive: %v", err)
	}

	mnemonic := testWallet.Mnemonic()

	// delete wallet db to restore
	os.RemoveAll(filepath.Join(restorePath, "wallet.db"))

	proofs, err := wallet.Restore(restorePath, mnemonic, []string{mintURL})
	if err != nil {
		t.Fatalf("error restoring wallet: %v\n", err)
	}

	expectedAmount := mintAmount - sendAmount1 - sendAmount2
	if proofs.Amount() != expectedAmount {
		t.Fatalf("restored proofs amount '%v' does not match to expected amount '%v'", proofs.Amount(), expectedAmount)
	}

}

func TestSendToPubkey(t *testing.T) {
	p2pkMintPath := filepath.Join(".", "p2pkmint1")
	p2pkMint, err := testutils.CreateTestMintServer(lnd1, "8889", p2pkMintPath, dbMigrationPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(p2pkMintPath)
	}()
	go func() {
		t.Fatal(p2pkMint.Start())
	}()
	p2pkMintURL := "http://127.0.0.1:8889"

	p2pkMintPath2 := filepath.Join(".", "p2pkmint2")
	p2pkMint2, err := testutils.CreateTestMintServer(lnd2, "8890", p2pkMintPath2, dbMigrationPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(p2pkMintPath2)
	}()
	go func() {
		t.Fatal(p2pkMint2.Start())
	}()
	p2pkMintURL2 := "http://127.0.0.1:8890"

	testWalletPath := filepath.Join(".", "/testwalletp2pk")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, p2pkMintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	testWalletPath2 := filepath.Join(".", "/testwalletp2pk2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, p2pkMintURL2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath2)
	}()

	testP2PK(t, testWallet, testWallet2, false)
}

func testP2PK(
	t *testing.T,
	testWallet *wallet.Wallet,
	testWallet2 *wallet.Wallet,
	fakeBackend bool,
) {
	mintRequest, err := testWallet.RequestMint(20000)
	if err != nil {
		t.Fatalf("unexpected error in mint request: %v", err)
	}

	if !fakeBackend {
		//pay invoice
		sendPaymentRequest := lnrpc.SendRequest{
			PaymentRequest: mintRequest.Request,
		}
		response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
		if len(response.PaymentError) > 0 {
			t.Fatalf("error paying invoice: %v", response.PaymentError)
		}
	}

	_, err = testWallet.MintTokens(mintRequest.Quote)
	if err != nil {
		t.Fatalf("unexpected error in mint tokens: %v", err)
	}

	receiverPubkey := testWallet2.GetReceivePubkey()
	lockedProofs, err := testWallet.SendToPubkey(500, testWallet.CurrentMint(), receiverPubkey, nil, true)
	if err != nil {
		t.Fatalf("unexpected error generating locked ecash: %v", err)
	}
	lockedEcash, _ := cashu.NewTokenV4(lockedProofs, testWallet.CurrentMint(), testutils.SAT_UNIT, false)

	// try receiving invalid
	_, err = testWallet.Receive(lockedEcash, true)
	if err == nil {
		t.Fatal("expected error trying to redeem locked ecash")
	}

	// this should unlock ecash and swap to trusted mint
	amountReceived, err := testWallet2.Receive(lockedEcash, true)
	if err != nil {
		t.Fatalf("unexpected error receiving locked ecash: %v", err)
	}

	trustedMints := testWallet2.TrustedMints()
	if len(trustedMints) != 1 {
		t.Fatalf("expected len of trusted mints '%v' but got '%v' instead", 1, len(trustedMints))
	}

	balance := testWallet2.GetBalance()
	if balance != amountReceived {
		t.Fatalf("expected balance of '%v' but got '%v' instead", amountReceived, balance)
	}

	lockedProofs, err = testWallet.SendToPubkey(500, testWallet.CurrentMint(), receiverPubkey, nil, true)
	if err != nil {
		t.Fatalf("unexpected error generating locked ecash: %v", err)
	}
	lockedEcash, _ = cashu.NewTokenV4(lockedProofs, testWallet.CurrentMint(), testutils.SAT_UNIT, false)

	// unlock ecash and trust mint
	amountReceived, err = testWallet2.Receive(lockedEcash, false)
	if err != nil {
		t.Fatalf("unexpected error receiving locked ecash: %v", err)
	}

	trustedMints = testWallet2.TrustedMints()
	if len(trustedMints) != 2 {
		t.Fatalf("expected len of trusted mints '%v' but got '%v' instead", 2, len(trustedMints))
	}
}

func TestDLEQProofs(t *testing.T) {
	mintURL := "http://127.0.0.1:3338"
	testWalletPath := filepath.Join(".", "/testdleqwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	testDLEQ(t, testWallet, false)
}

func testDLEQ(t *testing.T, testWallet *wallet.Wallet, fakeBackend bool) {
	mintURL := testWallet.CurrentMint()
	keysets, err := wallet.GetMintActiveKeysets(mintURL)
	if err != nil {
		t.Fatalf("unexpected error getting keysets: %v", err)
	}

	mintRes, err := testWallet.RequestMint(10000)
	if err != nil {
		t.Fatalf("unexpected error requesting mint: %v", err)
	}

	if !fakeBackend {
		//pay invoice
		sendPaymentRequest := lnrpc.SendRequest{
			PaymentRequest: mintRes.Request,
		}
		response, _ := lnd2.Client.SendPaymentSync(ctx, &sendPaymentRequest)
		if len(response.PaymentError) > 0 {
			t.Fatalf("error paying invoice: %v", response.PaymentError)
		}

	}
	proofs, err := testWallet.MintTokens(mintRes.Quote)
	if err != nil {
		t.Fatalf("unexpected error minting tokens: %v", err)
	}

	for _, proof := range proofs {
		if proof.DLEQ == nil {
			t.Fatal("got nil DLEQ proof from MintTokens")
		}

		pubkey := keysets[proof.Id].PublicKeys[proof.Amount]
		if !nut12.VerifyProofDLEQ(proof, pubkey) {
			t.Fatal("invalid DLEQ proof returned from MintTokens")
		}
	}

	proofsToSend, err := testWallet.Send(2100, mintURL, false)
	if err != nil {
		t.Fatalf("unexpected error in Send: %v", err)
	}
	for _, proof := range proofsToSend {
		if proof.DLEQ == nil {
			t.Fatal("got nil DLEQ proof from Send")
		}

		pubkey := keysets[proof.Id].PublicKeys[proof.Amount]
		if !nut12.VerifyProofDLEQ(proof, pubkey) {
			t.Fatal("invalid DLEQ proof returned from Send")
		}
	}
}

// TESTS AGAINST NUTSHELL MINT

// test regular wallet ops against Nutshell
func TestNutshell(t *testing.T) {
	nutshellMint, err := testutils.CreateNutshellMintContainer(ctx, 100)
	if err != nil {
		t.Fatalf("error starting nutshell mint: %v", err)
	}
	defer nutshellMint.Terminate(ctx)
	nutshellURL := nutshellMint.Host

	// test mint with fees
	testWalletPath := filepath.Join(".", "/nutshellWallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, nutshellURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	mintRes, err := testWallet.RequestMint(10000)
	if err != nil {
		t.Fatalf("unexpected error requesting mint: %v", err)
	}

	_, err = testWallet.MintTokens(mintRes.Quote)
	if err != nil {
		t.Fatalf("unexpected error minting tokens: %v", err)
	}

	var sendAmount uint64 = 2000
	proofsToSend, err := testWallet.Send(sendAmount, nutshellURL, true)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	token, _ := cashu.NewTokenV4(proofsToSend, nutshellURL, testutils.SAT_UNIT, false)

	fees, _ := testutils.Fees(proofsToSend, nutshellURL)
	if proofsToSend.Amount() != sendAmount+uint64(fees) {
		t.Fatalf("expected token amount of '%v' but got '%v' instead", sendAmount+uint64(fees), proofsToSend.Amount())
	}

	amountReceived, err := testWallet.Receive(token, false)
	if err != nil {
		t.Fatalf("unexpected error receiving: %v", err)
	}

	fees, _ = testutils.Fees(proofsToSend, nutshellURL)
	if amountReceived != proofsToSend.Amount()-uint64(fees) {
		t.Fatalf("expected received amount of '%v' but got '%v' instead", proofsToSend.Amount()-uint64(fees), amountReceived)
	}
}

func TestOverpaidFeesChange(t *testing.T) {
	nutshellURL := nutshellMint.Host

	testWalletPath := filepath.Join(".", "/nutshellfeeschange")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, nutshellURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	mintRes, err := testWallet.RequestMint(10000)
	if err != nil {
		t.Fatalf("unexpected error requesting mint: %v", err)
	}

	_, err = testWallet.MintTokens(mintRes.Quote)
	if err != nil {
		t.Fatalf("unexpected error minting tokens: %v", err)
	}

	var invoiceAmount int64 = 2000
	invoice := lnrpc.Invoice{Value: invoiceAmount}
	addInvoiceResponse, err := lnd2.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	balanceBeforeMelt := testWallet.GetBalance()
	meltResponse, err := testWallet.Melt(addInvoiceResponse.PaymentRequest, nutshellURL)
	if err != nil {
		t.Fatalf("got unexpected melt error: %v", err)
	}
	change := len(meltResponse.Change)
	if change < 1 {
		t.Fatalf("expected change")
	}

	// actual lightning fee paid
	lightningFee := meltResponse.FeeReserve - meltResponse.Change.Amount()
	expectedBalance := balanceBeforeMelt - uint64(invoiceAmount) - lightningFee
	if testWallet.GetBalance() != expectedBalance {
		t.Fatalf("expected balance of '%v' but got '%v' instead", expectedBalance, testWallet.GetBalance())
	}

	// do extra ops after melting to check counter for blinded messages
	// was incremented correctly
	mintRes, err = testWallet.RequestMint(5000)
	if err != nil {
		t.Fatalf("unexpected error requesting mint: %v", err)
	}
	_, err = testWallet.MintTokens(mintRes.Quote)
	if err != nil {
		t.Fatalf("unexpected error minting tokens: %v", err)
	}

	var sendAmount uint64 = testWallet.GetBalance()
	proofsToSend, err := testWallet.Send(sendAmount, nutshellURL, true)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	token, _ := cashu.NewTokenV4(proofsToSend, nutshellURL, testutils.SAT_UNIT, false)
	_, err = testWallet.Receive(token, false)
	if err != nil {
		t.Fatalf("unexpected error receiving: %v", err)
	}
}

func TestSendToPubkeyNutshell(t *testing.T) {
	nutshellURL := nutshellMint.Host

	nutshellMint2, err := testutils.CreateNutshellMintContainer(ctx, 0)
	if err != nil {
		t.Fatalf("error starting nutshell mint: %v", err)
	}
	defer nutshellMint2.Terminate(ctx)

	testWalletPath := filepath.Join(".", "/testwalletp2pk")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, nutshellURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	testWalletPath2 := filepath.Join(".", "/testwalletp2pk2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, nutshellMint2.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath2)
	}()

	testP2PK(t, testWallet, testWallet2, true)
}

func TestDLEQProofsNutshell(t *testing.T) {
	nutshellURL := nutshellMint.Host

	testWalletPath := filepath.Join(".", "/testwalletdleqnutshell")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, nutshellURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	testDLEQ(t, testWallet, true)
}

func TestWalletRestoreNutshell(t *testing.T) {
	mintURL := nutshellMint.Host

	testWalletPath := filepath.Join(".", "/testrestorewalletnutshell")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath)
	}()

	testWalletPath2 := filepath.Join(".", "/testrestorewalletnutshell2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testWalletPath2)
	}()

	testWalletRestore(t, testWallet, testWallet2, testWalletPath, true)
}
