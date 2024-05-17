//go:build integration

package wallet

import (
	"context"
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
	err := testutils.FundLndNode(ctx, bitcoind, lnd1)
	if err != nil {
		t.Fatalf("error funding node: %v", err)
	}

	err = testutils.OpenChannel(ctx, bitcoind, lnd1, lnd2, 15000000)
	if err != nil {
		t.Fatalf("error opening channel: %v", err)
	}

	// check no err
	mintRes, err := testWallet.RequestMint(100)
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
	if proofs.Amount() != 100 {
		t.Fatalf("expected proofs amount of: '%v' but got '%v' instead", 100, proofs.Amount())
	}
	if testWallet.GetBalance() != 100 {
		t.Fatalf("expected wallet balance of: '%v' but got '%v' instead", 100, testWallet.GetBalance())
	}

	// Clean up the container after the test is complete
	t.Cleanup(func() {
		if err := bitcoind.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}

		if err := lnd1.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}

		if err := lnd2.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}
	})
}
