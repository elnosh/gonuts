//go:build integration

package wallet

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"testing"

	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/testutils"
	"github.com/lightningnetwork/lnd/lnrpc"
)

var (
	ctx        context.Context
	bitcoind   *btcdocker.Bitcoind
	lnd1       *btcdocker.Lnd
	lnd2       *btcdocker.Lnd
	testWallet *Wallet
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

	mintConfig := mint.Config{
		PrivateKey:     "mykey",
		DerivationPath: "0/0/0",
	}
	nodeDir := lnd1.LndDir

	os.Setenv("LIGHTNING_BACKEND", "Lnd")
	os.Setenv("LND_REST_HOST", "https://"+lnd1.Host+":"+lnd1.RestPort)
	os.Setenv("LND_CERT_PATH", filepath.Join(nodeDir, "/tls.cert"))
	os.Setenv("LND_MACAROON_PATH", filepath.Join(nodeDir, "/data/chain/bitcoin/regtest/admin.macaroon"))

	mintServer, err := mint.SetupMintServer(mintConfig)
	if err != nil {
		log.Println(err)
		return 1
	}

	go mint.StartMintServer(mintServer)

	testWalletDir := filepath.Join(".", "/testwalletdb")
	if err = os.MkdirAll(testWalletDir, 0750); err != nil {
		log.Println(err)
		return 1
	}
	defer func() {
		os.RemoveAll(testWalletDir)
	}()

	walletConfig := Config{
		WalletPath:     testWalletDir,
		CurrentMintURL: "http://127.0.0.1:3338",
	}
	testWallet, err = LoadWallet(walletConfig)
	if err != nil {
		log.Println(err)
		return 1
	}

	return m.Run()
}

func TestMintTokens(t *testing.T) {
	var mintAmount uint64 = 10000
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

	proofs, err := testWallet.MintTokens(mintRes.Quote)
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
	if !errors.Is(err, ErrMintNotExist) {
		t.Fatalf("expected error '%v' but got error '%v'", ErrMintNotExist, err)
	}

	// insufficient balance in wallet
	_, err = testWallet.Send(2000000, mintURL)
	if !errors.Is(err, ErrInsufficientMintBalance) {
		t.Fatalf("expected error '%v' but got error '%v'", ErrInsufficientMintBalance, err)
	}
}
