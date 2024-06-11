//go:build integration

package wallet_test

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"slices"
	"testing"

	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/testutils"
	"github.com/elnosh/gonuts/wallet"
	"github.com/lightningnetwork/lnd/lnrpc"
)

var (
	ctx      context.Context
	bitcoind *btcdocker.Bitcoind
	lnd1     *btcdocker.Lnd
	lnd2     *btcdocker.Lnd
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
	testMint, err := testutils.CreateTestMintServer(lnd1, "mykey", "3338", testMintPath)
	if err != nil {
		log.Println(err)
		return 1
	}
	defer func() {
		os.RemoveAll(testMintPath)
	}()
	go mint.StartMintServer(testMint)

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
	token, err := testWallet.Send(sendAmount, mintURL)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	if token.TotalAmount() != sendAmount {
		t.Fatalf("expected token amount of '%v' but got '%v' instead", sendAmount, token.TotalAmount())
	}

	// test with invalid mint
	_, err = testWallet.Send(sendAmount, "http://nonexistent.mint")
	if !errors.Is(err, wallet.ErrMintNotExist) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrMintNotExist, err)
	}

	// insufficient balance in wallet
	_, err = testWallet.Send(2000000, mintURL)
	if !errors.Is(err, wallet.ErrInsufficientMintBalance) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrInsufficientMintBalance, err)
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
	testMint, err := testutils.CreateTestMintServer(lnd2, "mykey", "3339", testMintPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testMintPath)
	}()
	go mint.StartMintServer(testMint)

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

	token, err := testWallet2.Send(1500, mint2URL)
	if err != nil {
		t.Fatalf("got unexpected error in send: %v", err)
	}

	// test receive swap == true
	_, err = testWallet.Receive(*token, true)
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

	token2, err := testWallet2.Send(1500, mint2URL)
	if err != nil {
		t.Fatalf("got unexpected error in send: %v", err)
	}

	// test receive swap == false
	_, err = testWallet.Receive(*token2, false)
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
	_, err = balanceTestWallet.Send(sendAmount, mintURL)
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
	// doing self-payment so this should make melt request fail
	_, err = balanceTestWallet.Melt(addInvoiceResponse.PaymentRequest, mintURL)
	if err == nil {
		t.Fatal("expected error in melt request but got nil")
	}

	// check balance is same after failed melt
	if balanceTestWallet.GetBalance() != balanceBeforeMelt {
		t.Fatalf("expected balance of '%v' but got '%v' instead", balanceBeforeMelt, balanceTestWallet.GetBalance())
	}
}
