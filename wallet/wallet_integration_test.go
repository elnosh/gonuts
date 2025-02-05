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
	"strconv"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
	"github.com/elnosh/gonuts/cashu/nuts/nut12"
	"github.com/elnosh/gonuts/cashu/nuts/nut15"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/testutils"
	"github.com/elnosh/gonuts/wallet"
	"github.com/lightningnetwork/lnd/lnrpc"
)

var (
	ctx          context.Context
	nutshellMint *testutils.NutshellMintContainer

	mintURL1        = "http://127.0.0.1:3338"
	mintURL2        = "http://127.0.0.1:3339"
	mintWithFeesURL = "http://127.0.0.1:8080"
)

func TestMain(m *testing.M) {
	code, err := testMain(m)
	if err != nil {
		log.Println(err)
	}
	os.Exit(code)
}

func testMain(m *testing.M) (int, error) {
	flag.Parse()
	ctx = context.Background()

	errChan := make(chan error)

	testMintPath := filepath.Join(".", "testmint1")
	fakeBackend := &lightning.FakeBackend{}
	testMint, err := testutils.CreateTestMintServer(fakeBackend, 3338, 0, testMintPath, 0)
	if err != nil {
		return 1, err
	}
	go func() {
		if err := testMint.Start(); err != nil {
			errChan <- err
		}
	}()
	defer os.RemoveAll(testMintPath)

	testMintPath2 := filepath.Join(".", "testmint2")
	fakeBackend2 := &lightning.FakeBackend{}
	testMint2, err := testutils.CreateTestMintServer(fakeBackend2, 3339, 0, testMintPath2, 0)
	if err != nil {
		return 1, err
	}
	go func() {
		if err := testMint2.Start(); err != nil {
			errChan <- err
		}
	}()
	defer os.RemoveAll(testMintPath2)

	mintFeesPath := filepath.Join(".", "testmintwithfees")
	fakeBackend3 := &lightning.FakeBackend{}
	mintWithFees, err := testutils.CreateTestMintServer(fakeBackend3, 8080, 0, mintFeesPath, 100)
	if err != nil {
		return 1, err
	}
	go func() {
		if err := mintWithFees.Start(); err != nil {
			errChan <- err
		}
	}()
	defer os.RemoveAll(mintFeesPath)

	nutshellMint, err = testutils.CreateNutshellMintContainer(ctx, 0, nil)
	if err != nil {
		log.Fatalf("error starting nutshell mint: %v", err)
	}
	defer nutshellMint.Terminate(ctx)

	select {
	case err := <-errChan:
		return 1, err
	case <-time.After(time.Millisecond * 500):
		break
	}

	return m.Run(), nil
}

func TestMintTokens(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testmintwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	var mintAmount uint64 = 30000
	mintRes, err := testWallet.RequestMint(mintAmount, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("error requesting mint: %v", err)
	}

	quote, err := testWallet.GetMintQuoteByPaymentRequest(mintRes.Request)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	mintedAmount, err := testWallet.MintTokens(quote.QuoteId)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	if mintedAmount != mintAmount {
		t.Fatalf("expected proofs amount of '%v' but got '%v' instead", mintAmount, mintedAmount)
	}

	// non-existent quote
	_, err = testWallet.MintTokens("id198274")
	if !errors.Is(err, wallet.ErrQuoteNotFound) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrQuoteNotFound, err)
	}
}

func TestSend(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testsendwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	if err := testutils.FundCashuWallet(ctx, testWallet, nil, 30000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	var sendAmount uint64 = 4200
	proofsToSend, err := testWallet.Send(sendAmount, testWallet.CurrentMint(), true)
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
	_, err = testWallet.Send(2000000, testWallet.CurrentMint(), true)
	if !errors.Is(err, wallet.ErrInsufficientMintBalance) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrInsufficientMintBalance, err)
	}

	// test mint with fees
	feesWalletPath := filepath.Join(".", "/testsendwalletfees")
	feesWallet, err := testutils.CreateTestWallet(feesWalletPath, mintWithFeesURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(feesWalletPath)

	if err := testutils.FundCashuWallet(ctx, feesWallet, nil, 10000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	sendAmount = 2000
	proofsToSend, err = feesWallet.Send(sendAmount, feesWallet.CurrentMint(), true)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	fees, err := testutils.Fees(proofsToSend, feesWallet.CurrentMint())
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	if proofsToSend.Amount() != sendAmount+uint64(fees) {
		t.Fatalf("expected token amount of '%v' but got '%v' instead", sendAmount+uint64(fees), proofsToSend.Amount())
	}

	// send without fees to receive
	proofsToSend, err = feesWallet.Send(sendAmount, feesWallet.CurrentMint(), false)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	if proofsToSend.Amount() != sendAmount {
		t.Fatalf("expected token amount of '%v' but got '%v' instead", sendAmount+uint64(fees), proofsToSend.Amount())
	}
}

func TestReceive(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testreceivewallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	if err := testutils.FundCashuWallet(ctx, testWallet, nil, 30000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	testWalletPath2 := filepath.Join(".", "/testreceivewallet2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL2)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	if err := testutils.FundCashuWallet(ctx, testWallet2, nil, 15000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	proofsToSend, err := testWallet2.Send(1500, mintURL2, true)
	if err != nil {
		t.Fatalf("got unexpected error in send: %v", err)
	}
	token, _ := cashu.NewTokenV4(proofsToSend, mintURL2, cashu.Sat, false)

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
	if trustedMints[0] != defaultMint {
		t.Fatalf("expected '%v' in list of trusted of trusted mints", defaultMint)
	}

	proofsToSend, err = testWallet2.Send(1500, mintURL2, true)
	if err != nil {
		t.Fatalf("got unexpected error in send: %v", err)
	}
	token, _ = cashu.NewTokenV4(proofsToSend, mintURL2, cashu.Sat, false)

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
	if !slices.Contains(trustedMints, mintURL2) {
		t.Fatalf("expected '%v' in list of trusted of trusted mints", mintURL2)
	}
}

func TestReceiveFees(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testreceivefees")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintWithFeesURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	if err := testutils.FundCashuWallet(ctx, testWallet, nil, 30000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	testWalletPath2 := filepath.Join(".", "/testreceivefees2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintWithFeesURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	var sendAmount uint64 = 2000
	proofsToSend, err := testWallet.Send(sendAmount, testWallet.CurrentMint(), true)
	if err != nil {
		t.Fatalf("got unexpected error in send: %v", err)
	}
	token, _ := cashu.NewTokenV4(proofsToSend, testWallet.CurrentMint(), cashu.Sat, false)

	amountReceived, err := testWallet2.Receive(token, false)
	if err != nil {
		t.Fatalf("got unexpected error in receive: %v", err)
	}

	fees, err := testutils.Fees(proofsToSend, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	if amountReceived != proofsToSend.Amount()-uint64(fees) {
		t.Fatalf("expected received amount of '%v' but got '%v' instead", proofsToSend.Amount()-uint64(fees), amountReceived)
	}
}

func TestMelt(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testmeltwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	if err := testutils.FundCashuWallet(ctx, testWallet, nil, 30000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	bolt11, _, _, _ := lightning.CreateFakeInvoice(30000, false)
	meltQuote, err := testWallet.RequestMeltQuote(bolt11, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting melt quote: %v", err)
	}
	meltResponse, err := testWallet.Melt(meltQuote.Quote)
	if err != nil {
		t.Fatalf("got unexpected melt error: %v", err)
	}
	if meltResponse.State != nut05.Paid {
		t.Fatalf("expected paid melt")
	}

	// try melt for invoice over balance
	bolt11, _, _, _ = lightning.CreateFakeInvoice(600000, false)
	meltQuote, err = testWallet.RequestMeltQuote(bolt11, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting melt quote: %v", err)
	}
	_, err = testWallet.Melt(meltQuote.Quote)
	if !errors.Is(err, wallet.ErrInsufficientMintBalance) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrInsufficientMintBalance, err)
	}

	bolt11, _, _, _ = lightning.CreateFakeInvoice(600000, false)
	_, err = testWallet.RequestMeltQuote(bolt11, "http://nonexistent.mint")
	if !errors.Is(err, wallet.ErrMintNotExist) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrMintNotExist, err)
	}

	// test melt with fees
	feesWalletPath := filepath.Join(".", "/testsendwalletfees")
	feesWallet, err := testutils.CreateTestWallet(feesWalletPath, mintWithFeesURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(feesWalletPath)

	if err := testutils.FundCashuWallet(ctx, feesWallet, nil, 10000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	bolt11, _, _, _ = lightning.CreateFakeInvoice(5000, false)
	meltQuote, err = feesWallet.RequestMeltQuote(bolt11, feesWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting melt quote: %v", err)
	}
	meltResponse, err = feesWallet.Melt(meltQuote.Quote)
	if err != nil {
		t.Fatalf("got unexpected melt error: %v", err)
	}
	if meltResponse.State != nut05.Paid {
		t.Fatalf("expected paid melt")
	}
}

func TestMintSwap(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testmintswapwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	var amountToSwap uint64 = 1000
	_, err = testWallet.MintSwap(amountToSwap, testWallet.CurrentMint(), mintURL2)
	if !errors.Is(err, wallet.ErrMintNotExist) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrMintNotExist, err)
	}

	_, err = testWallet.AddMint(mintURL2)
	if err != nil {
		t.Fatalf("unexpected error adding mint to wallet: %v", err)
	}

	_, err = testWallet.MintSwap(amountToSwap, testWallet.CurrentMint(), mintURL2)
	if !errors.Is(err, wallet.ErrInsufficientMintBalance) {
		t.Fatalf("expected error '%v' but got error '%v'", wallet.ErrInsufficientMintBalance, err)
	}

	var fundAmount uint64 = 21000
	if err := testutils.FundCashuWallet(ctx, testWallet, nil, fundAmount); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}
	amountSwapped, err := testWallet.MintSwap(amountToSwap, testWallet.CurrentMint(), mintURL2)
	if err != nil {
		t.Fatalf("unexpected error doing mint swap: %v", err)
	}

	balanceByMints := testWallet.GetBalanceByMints()
	mint1Balance := balanceByMints[testWallet.CurrentMint()]
	expectedBalance := fundAmount - amountToSwap
	if mint1Balance != expectedBalance {
		t.Fatalf("expected balance '%v' but got '%v'", expectedBalance, mint1Balance)
	}

	mint2Balance := balanceByMints[mintURL2]
	if mint2Balance != amountSwapped {
		t.Fatalf("expected balance '%v' but got '%v'", amountSwapped, mint2Balance)
	}
}

// check balance is correct after certain operations
func TestWalletBalance(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testwalletbalance")
	balanceTestWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	// test balance after mint request
	var mintAmount uint64 = 20000
	mintRequest, err := balanceTestWallet.RequestMint(mintAmount, balanceTestWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error in mint request: %v", err)
	}

	_, err = balanceTestWallet.MintTokens(mintRequest.Quote)
	if err != nil {
		t.Fatalf("unexpected error in mint tokens: %v", err)
	}

	if balanceTestWallet.GetBalance() != mintAmount {
		t.Fatalf("expected balance of '%v' but got '%v' instead", mintAmount, balanceTestWallet.GetBalance())
	}
	mintBalance := balanceTestWallet.GetBalanceByMints()[mintURL1]
	if mintBalance != mintAmount {
		t.Fatalf("expected mint balance of '%v' but got '%v' instead", mintAmount, mintBalance)
	}

	balance := balanceTestWallet.GetBalance()
	// test balance after send
	var sendAmount uint64 = 1200
	_, err = balanceTestWallet.Send(sendAmount, balanceTestWallet.CurrentMint(), true)
	if err != nil {
		t.Fatalf("unexpected error in send: %v", err)
	}
	if balanceTestWallet.GetBalance() != balance-sendAmount {
		t.Fatalf("expected balance of '%v' but got '%v' instead", balance-sendAmount, balanceTestWallet.GetBalance())
	}

	// test balance is same after failed melt request
	failPayment := true
	// this will make the payment fail
	bolt11, _, _, err := lightning.CreateFakeInvoice(5000, failPayment)
	if err != nil {
		t.Fatal(err)
	}
	balanceBeforeMelt := balanceTestWallet.GetBalance()

	meltQuote, err := balanceTestWallet.RequestMeltQuote(bolt11, balanceTestWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting melt quote: %v", err)
	}
	meltresponse, err := balanceTestWallet.Melt(meltQuote.Quote)
	if err != nil {
		t.Fatalf("got unexpected error in melt: %v", err)
	}
	if meltresponse.State != nut05.Unpaid {
		t.Fatalf("expected melt with unpaid state but got '%v'", meltresponse.State.String())
	}

	if balanceTestWallet.GetBalance() != balanceBeforeMelt {
		t.Fatalf("expected balance of '%v' but got '%v' instead", balanceBeforeMelt, balanceTestWallet.GetBalance())
	}
}

// check balance is correct after ops with fees
func TestWalletBalanceFees(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testwalletbalancefees")
	balanceTestWallet, err := testutils.CreateTestWallet(testWalletPath, mintWithFeesURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	err = testutils.FundCashuWallet(ctx, balanceTestWallet, nil, 30000)
	if err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	testWalletPath2 := filepath.Join(".", "/testreceivefees2")
	balanceTestWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintWithFeesURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	sendAmounts := []uint64{1200, 2000, 5000}

	for _, sendAmount := range sendAmounts {
		proofsToSend, err := balanceTestWallet.Send(sendAmount, balanceTestWallet.CurrentMint(), true)
		if err != nil {
			t.Fatalf("unexpected error in send: %v", err)
		}
		token, _ := cashu.NewTokenV4(proofsToSend, balanceTestWallet.CurrentMint(), cashu.Sat, false)

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
		proofsToSend, err := balanceTestWallet.Send(sendAmount, balanceTestWallet.CurrentMint(), false)
		if err != nil {
			t.Fatalf("unexpected error in send: %v", err)
		}
		token, _ := cashu.NewTokenV4(proofsToSend, balanceTestWallet.CurrentMint(), cashu.Sat, false)

		fees, err := testutils.Fees(proofsToSend, balanceTestWallet.CurrentMint())
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
	port, _ := testutils.GetAvailablePort()
	mintURL := "http://127.0.0.1:" + strconv.Itoa(port)

	testMintPath := filepath.Join(".", "testmint2")
	// Setting delay so that it marks payments as pending
	fakeBackend := &lightning.FakeBackend{PaymentDelay: int64(time.Minute) * 2}
	testMint, err := testutils.CreateTestMintServer(fakeBackend, port, 0, testMintPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testMintPath)
	go func() {
		t.Fatal(testMint.Start())
	}()

	testWalletPath := filepath.Join(".", "/testpendingwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	var fundingBalance uint64 = 15000
	if err := testutils.FundCashuWallet(ctx, testWallet, nil, fundingBalance); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	// fake backend has payment delay set so this invoice will return pending
	bolt11, _, paymentHash, err := lightning.CreateFakeInvoice(2100, false)
	meltQuote, err := testWallet.RequestMeltQuote(bolt11, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting melt quote: %v", err)
	}

	meltResponse, err := testWallet.Melt(meltQuote.Quote)
	if err != nil {
		t.Fatalf("unexpected error in melt: %v", err)
	}
	if meltResponse.State != nut05.Pending {
		t.Fatalf("expected quote state of '%s' but got '%s' instead", nut05.Pending, meltQuote.State)
	}

	// check pending balance is same as quote amount
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

	// check no pending balance after settling payment and checking melt quote state
	fakeBackend.SetInvoiceStatus(paymentHash, lightning.Succeeded)

	meltQuoteStateResponse, err := testWallet.CheckMeltQuoteState(meltQuote.Quote)
	if err != nil {
		t.Fatalf("unexpected error checking melt quote state: %v", err)
	}
	if meltQuoteStateResponse.State != nut05.Paid {
		t.Fatalf("expected quote state of '%s' but got '%s' instead",
			nut05.Paid, meltQuoteStateResponse.State)
	}
	if testWallet.PendingBalance() != 0 {
		t.Fatalf("expected no pending balance but got '%v' instead", pendingBalance)
	}

	// check no pending melt quotes
	pendingMeltQuotes = testWallet.GetPendingMeltQuotes()
	if len(pendingMeltQuotes) != 0 {
		t.Fatalf("expected no pending quotes but got '%v' instead", len(pendingMeltQuotes))
	}

	// test pending payment and then cancel it
	bolt11, _, paymentHash, err = lightning.CreateFakeInvoice(2100, false)
	meltQuote, err = testWallet.RequestMeltQuote(bolt11, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting melt quote: %v", err)
	}

	meltResponse, err = testWallet.Melt(meltQuote.Quote)
	if err != nil {
		t.Fatalf("unexpected error in melt: %v", err)
	}
	if meltResponse.State != nut05.Pending {
		t.Fatalf("expected quote state of '%s' but got '%s' instead", nut05.Pending, meltQuote.State)
	}

	pendingBalance = testWallet.PendingBalance()
	expectedPendingBalance = meltQuote.Amount + meltQuote.FeeReserve
	if testWallet.PendingBalance() != expectedPendingBalance {
		t.Fatalf("expected pending balance of '%v' but got '%v' instead",
			expectedPendingBalance, pendingBalance)
	}
	pendingMeltQuotes = testWallet.GetPendingMeltQuotes()
	if len(pendingMeltQuotes) != 1 {
		t.Fatalf("expected '%v' pending quote but got '%v' instead", 1, len(pendingMeltQuotes))
	}

	fakeBackend.SetInvoiceStatus(paymentHash, lightning.Failed)
	meltQuoteStateResponse, err = testWallet.CheckMeltQuoteState(meltQuote.Quote)
	if err != nil {
		t.Fatalf("unexpected error checking melt quote state: %v", err)
	}
	if meltQuoteStateResponse.State != nut05.Unpaid {
		t.Fatalf("expected quote state of '%s' but got '%s' instead",
			nut05.Unpaid, meltQuoteStateResponse.State)
	}

	// check no pending balance after canceling and checking melt quote state
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

	proofsToSend1, _ := testWallet.Send(100, testWallet.CurrentMint(), false)
	proofsToSend2, _ := testWallet.Send(21, testWallet.CurrentMint(), false)

	pendingBalance = testWallet.PendingBalance()
	expectedPending := proofsToSend1.Amount() + proofsToSend2.Amount()
	if pendingBalance != expectedPending {
		t.Fatalf("expected pending balance of '%v' but got '%v' instead", expectedPending, pendingBalance)
	}

	testWalletPath2 := filepath.Join(".", "/testpendingwallet2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	// redeem proofs from other wallet to mark them spent
	token, _ := cashu.NewTokenV4(proofsToSend2, testWallet.CurrentMint(), cashu.Sat, false)
	_, _ = testWallet2.Receive(token, false)

	if err := testWallet.RemoveSpentProofs(); err != nil {
		t.Fatalf("unexpected error in removing spent proofs: %v", err)
	}

	// after RemoveSpentProofs call, pending balance should decrease by amount of redeemed proofs
	pendingBalance = testWallet.PendingBalance()
	expectedPending = proofsToSend1.Amount()
	if pendingBalance != expectedPending {
		t.Fatalf("expected pending balance of '%v' but got '%v' instead", expectedPending, pendingBalance)
	}

	balanceBeforeReclaiming := testWallet.GetBalance()

	amountReclaimed, err := testWallet.ReclaimUnspentProofs()
	if err != nil {
		t.Fatalf("unexpected error in reclaiming unspent proofs: %v", err)
	}

	expectedAmount := proofsToSend1.Amount()
	if amountReclaimed != expectedAmount {
		t.Fatalf("expected reclaimed amount of '%v' but got '%v' instead", expectedAmount, amountReclaimed)
	}

	// pending balance should now be 0
	pendingBalance = testWallet.PendingBalance()
	if pendingBalance != 0 {
		t.Fatalf("expected no pending balance but got '%v' instead", pendingBalance)
	}

	// wallet balance should have added reclaimed proofs
	expectedWalletBalance = balanceBeforeReclaiming + amountReclaimed
	if expectedWalletBalance != testWallet.GetBalance() {
		t.Fatalf("expected wallet balance of '%v' but got '%v' instead", expectedWalletBalance, testWallet.GetBalance())
	}

}

// Test wallet operations work after mint rotates to new keyset
func TestKeysetRotations(t *testing.T) {
	port, _ := testutils.GetAvailablePort()
	mintURL := "http://127.0.0.1:" + strconv.Itoa(port)

	testMintPath := filepath.Join(".", "testmintkeysetrotation")
	var keysetDerivationIdx uint32 = 0
	fakeBackend := &lightning.FakeBackend{}
	testMint, err := testutils.CreateTestMintServer(fakeBackend, port, keysetDerivationIdx, testMintPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testMintPath)
	go func() {
		if err := testMint.Start(); err != nil {
			t.Fatal(err)
		}
	}()

	var bumpKeyset = func(mint *mint.MintServer) *mint.MintServer {
		testMint.Shutdown()
		keysetDerivationIdx++
		testMint, err := testutils.CreateTestMintServer(fakeBackend, port, keysetDerivationIdx, testMintPath, 0)
		if err != nil {
			t.Fatal(err)
		}
		return testMint
	}

	testWalletPath := filepath.Join(".", "/testkeysetrotationwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	testWalletPath2 := filepath.Join(".", "/testkeysetrotationwallet2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	var mintAmount uint64 = 30000
	mintRes, err := testWallet.RequestMint(mintAmount, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("error requesting mint: %v", err)
	}

	testMint = bumpKeyset(testMint)
	go func() {
		if err := testMint.Start(); err != nil {
			t.Fatal(err)
		}
	}()
	time.Sleep(time.Millisecond * 500)

	_, err = testWallet.MintTokens(mintRes.Quote)
	if err != nil {
		t.Fatalf("got unexpected error minting tokens: %v", err)
	}

	testMint = bumpKeyset(testMint)
	go func() {
		if err := testMint.Start(); err != nil {
			t.Fatal(err)
		}
	}()
	time.Sleep(time.Millisecond * 500)

	activeKeyset, _ := wallet.GetMintActiveKeyset(mintURL, cashu.Sat)
	// SendToPubkey would require a swap so new proofs should have id from new keyset
	lockedProofs, err := testWallet.SendToPubkey(210, mintURL, testWallet.GetReceivePubkey(), nil, false)
	if err != nil {
		t.Fatalf("unexpected getting locked proofs: %v", err)
	}
	if lockedProofs[0].Id != activeKeyset.Id {
		t.Fatalf("expected proofs with id '%v' but got '%v'", activeKeyset.Id, lockedProofs[0].Id)
	}
	token, _ := cashu.NewTokenV4(lockedProofs, mintURL, cashu.Sat, false)

	testMint = bumpKeyset(testMint)
	go func() {
		if err := testMint.Start(); err != nil {
			t.Fatal(err)
		}
	}()
	time.Sleep(time.Millisecond * 500)
	_, err = testWallet2.Receive(token, false)
}

func TestWalletRestore(t *testing.T) {
	testWalletPath := filepath.Join(".", "/testrestorewallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	testWalletPath2 := filepath.Join(".", "/testrestorewallet2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	testWalletRestore(t, testWallet, testWallet2, testWalletPath)
}

func testWalletRestore(
	t *testing.T,
	testWallet *wallet.Wallet,
	testWallet2 *wallet.Wallet,
	restorePath string,
) {
	mintURL := testWallet.CurrentMint()

	var mintAmount uint64 = 20000
	mintRequest, err := testWallet.RequestMint(mintAmount, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error in mint request: %v", err)
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
	token, _ := cashu.NewTokenV4(proofsToSend, mintURL, cashu.Sat, false)

	_, err = testWallet2.Receive(token, false)
	if err != nil {
		t.Fatalf("got unexpected error in receive: %v", err)
	}

	var sendAmount2 uint64 = 1000
	proofsToSend, err = testWallet.Send(sendAmount2, mintURL, true)
	if err != nil {
		t.Fatalf("unexpected error in send: %v", err)
	}
	token, _ = cashu.NewTokenV4(proofsToSend, mintURL, cashu.Sat, false)

	_, err = testWallet2.Receive(token, false)
	if err != nil {
		t.Fatalf("got unexpected error in receive: %v", err)
	}

	mnemonic := testWallet.Mnemonic()

	// delete wallet db to restore
	os.RemoveAll(filepath.Join(restorePath, "wallet.db"))

	amountRestored, err := wallet.Restore(restorePath, mnemonic, []string{mintURL})
	if err != nil {
		t.Fatalf("error restoring wallet: %v\n", err)
	}

	expectedAmount := mintAmount - sendAmount1 - sendAmount2
	if amountRestored != expectedAmount {
		t.Fatalf("restored amount '%v' does not match expected amount '%v'", amountRestored, expectedAmount)
	}
}

func TestHTLC(t *testing.T) {
	htlcMintURL := mintURL1

	testWalletPath := filepath.Join(".", "/testwallethtlc")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, htlcMintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	testWalletPath2 := filepath.Join(".", "/testwallethtlc2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, htlcMintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	if err := testutils.FundCashuWallet(ctx, testWallet, nil, 30000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	preimage := "aaaaaa"
	htlcLockedProofs, err := testWallet.HTLCLockedProofs(1000, testWallet.CurrentMint(), preimage, nil, false)
	if err != nil {
		t.Fatalf("unexpected error generating ecash HTLC: %v", err)
	}
	lockedEcash, _ := cashu.NewTokenV4(htlcLockedProofs, testWallet.CurrentMint(), cashu.Sat, false)

	amountReceived, err := testWallet2.ReceiveHTLC(lockedEcash, preimage)
	if err != nil {
		t.Fatalf("unexpected error receiving HTLC: %v", err)
	}

	balance := testWallet2.GetBalance()
	if balance != amountReceived {
		t.Fatalf("expected balance of '%v' but got '%v' instead", amountReceived, balance)
	}

	// test HTLC that requires signature
	tags := nut11.P2PKTags{
		NSigs:   1,
		Pubkeys: []*btcec.PublicKey{testWallet2.GetReceivePubkey()},
	}
	htlcLockedProofs, err = testWallet.HTLCLockedProofs(1000, testWallet.CurrentMint(), preimage, &tags, false)
	if err != nil {
		t.Fatalf("unexpected error generating ecash HTLC: %v", err)
	}
	lockedEcash, _ = cashu.NewTokenV4(htlcLockedProofs, testWallet.CurrentMint(), cashu.Sat, false)

	amountReceived, err = testWallet2.ReceiveHTLC(lockedEcash, preimage)
	if err != nil {
		t.Fatalf("unexpected error receiving HTLC: %v", err)
	}

	expectedBalance := balance + amountReceived
	walletBalance := testWallet2.GetBalance()
	if walletBalance != expectedBalance {
		t.Fatalf("expected balance of '%v' but got '%v' instead", expectedBalance, walletBalance)
	}
}

func TestSendToPubkey(t *testing.T) {
	port, _ := testutils.GetAvailablePort()
	p2pkMintURL := "http://127.0.0.1:" + strconv.Itoa(port)

	p2pkMintPath := filepath.Join(".", "p2pkmint1")
	fakeBackend := &lightning.FakeBackend{}
	p2pkMint, err := testutils.CreateTestMintServer(fakeBackend, port, 0, p2pkMintPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(p2pkMintPath)
	go func() {
		t.Fatal(p2pkMint.Start())
	}()

	port2, _ := testutils.GetAvailablePort()
	p2pkMintURL2 := "http://127.0.0.1:" + strconv.Itoa(port2)
	p2pkMintPath2 := filepath.Join(".", "p2pkmint2")
	fakeBackend2 := &lightning.FakeBackend{}
	p2pkMint2, err := testutils.CreateTestMintServer(fakeBackend2, port2, 0, p2pkMintPath2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(p2pkMintPath2)
	go func() {
		t.Fatal(p2pkMint2.Start())
	}()

	testWalletPath := filepath.Join(".", "/testwalletp2pk")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, p2pkMintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	testWalletPath2 := filepath.Join(".", "/testwalletp2pk2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, p2pkMintURL2)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	testP2PK(t, testWallet, testWallet2)
}

func testP2PK(
	t *testing.T,
	testWallet *wallet.Wallet,
	testWallet2 *wallet.Wallet,
) {
	mintRequest, err := testWallet.RequestMint(20000, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error in mint request: %v", err)
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
	lockedEcash, _ := cashu.NewTokenV4(lockedProofs, testWallet.CurrentMint(), cashu.Sat, false)

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
	lockedEcash, _ = cashu.NewTokenV4(lockedProofs, testWallet.CurrentMint(), cashu.Sat, false)

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
	testWalletPath := filepath.Join(".", "/testdleqwallet")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	testDLEQ(t, testWallet)
}

func testDLEQ(t *testing.T, testWallet *wallet.Wallet) {
	mintURL := testWallet.CurrentMint()
	keyset, err := wallet.GetMintActiveKeyset(mintURL, cashu.Sat)
	if err != nil {
		t.Fatalf("unexpected error getting keysets: %v", err)
	}

	mintRes, err := testWallet.RequestMint(10000, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting mint: %v", err)
	}

	_, err = testWallet.MintTokens(mintRes.Quote)
	if err != nil {
		t.Fatalf("unexpected error minting tokens: %v", err)
	}

	proofsToSend, err := testWallet.Send(2100, mintURL, false)
	if err != nil {
		t.Fatalf("unexpected error in Send: %v", err)
	}
	for _, proof := range proofsToSend {
		if proof.DLEQ == nil {
			t.Fatal("got nil DLEQ proof from Send")
		}

		pubkey := keyset.PublicKeys[proof.Amount]
		if !nut12.VerifyProofDLEQ(proof, pubkey) {
			t.Fatal("invalid DLEQ proof returned from Send")
		}
	}
}

func TestMultimintPayment(t *testing.T) {
	// setup bitcoind and lnd nodes
	bitcoind, err := btcdocker.NewBitcoind(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = bitcoind.Client.CreateWallet("")
	if err != nil {
		t.Fatal(err)
	}

	lnd1, err := btcdocker.NewLnd(ctx, bitcoind)
	if err != nil {
		t.Fatal(err)
	}
	lnd2, err := btcdocker.NewLnd(ctx, bitcoind)
	if err != nil {
		t.Fatal(err)
	}
	lnd3, err := btcdocker.NewLnd(ctx, bitcoind)
	if err != nil {
		t.Fatal(err)
	}
	lnd4, err := btcdocker.NewLnd(ctx, bitcoind)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		bitcoind.Terminate(ctx)
		lnd1.Terminate(ctx)
		lnd2.Terminate(ctx)
		lnd3.Terminate(ctx)
		lnd4.Terminate(ctx)
	}()

	if err := testutils.FundLndNode(ctx, bitcoind, lnd1); err != nil {
		t.Fatal(err)
	}
	if err := testutils.FundLndNode(ctx, bitcoind, lnd2); err != nil {
		t.Fatal(err)
	}
	if err := testutils.FundLndNode(ctx, bitcoind, lnd3); err != nil {
		t.Fatal(err)
	}

	if err := testutils.OpenChannel(ctx, bitcoind, lnd1, lnd2, 1500000); err != nil {
		t.Fatal(err)
	}
	if err := testutils.OpenChannel(ctx, bitcoind, lnd2, lnd3, 1500000); err != nil {
		t.Fatal(err)
	}
	if err := testutils.OpenChannel(ctx, bitcoind, lnd3, lnd1, 1500000); err != nil {
		t.Fatal(err)
	}

	port, _ := testutils.GetAvailablePort()
	mint1URL := "http://127.0.0.1:" + strconv.Itoa(port)
	testMintPath := filepath.Join(".", "testmppmint1")
	lndClient1, err := testutils.LndClient(lnd1, testMintPath)
	if err != nil {
		t.Fatal(err)
	}
	testMint, err := testutils.CreateTestMintServer(lndClient1, port, 0, testMintPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		log.Fatal(testMint.Start())
	}()

	port2, _ := testutils.GetAvailablePort()
	mint2URL := "http://127.0.0.1:" + strconv.Itoa(port2)
	testMintPath2 := filepath.Join(".", "testmppmint2")
	lndClient2, err := testutils.LndClient(lnd2, testMintPath2)
	if err != nil {
		t.Fatal(err)
	}
	testMint2, err := testutils.CreateTestMintServer(lndClient2, port2, 0, testMintPath2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.RemoveAll(testMintPath)
		os.RemoveAll(testMintPath2)
	}()
	go func() {
		log.Fatal(testMint2.Start())
	}()

	// add both mints to wallet
	testWalletPath := filepath.Join(".", "/testwalletmultimintpayment")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mint1URL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	// fund wallet from both mints
	if err := testutils.FundCashuWallet(ctx, testWallet, lnd3, 21000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}
	testWallet.Shutdown()

	walletConfig := wallet.Config{WalletPath: testWalletPath, CurrentMintURL: mint2URL}
	testWallet, err = wallet.LoadWallet(walletConfig)
	if err != nil {
		t.Fatalf("error loading wallet: %v", err)
	}

	if err := testutils.FundCashuWallet(ctx, testWallet, lnd3, 21000); err != nil {
		t.Fatalf("error funding wallet: %v", err)
	}

	// test MPP with gonuts mint
	testMultimintPayment(t, testWallet, lnd3, lnd4)

	// NOTE: comment out tests with nutshell for now until it updates
	// to use msat for MPP

	// setup to test MPP with nutshell mint
	// nutshellMint1, err := testutils.CreateNutshellMintContainer(ctx, 100, lnd1)
	// if err != nil {
	// 	t.Fatalf("error starting nutshell mint: %v", err)
	// }
	// defer nutshellMint1.Terminate(ctx)
	// nutshellURL := nutshellMint1.Host
	//
	// nutshellMint2, err := testutils.CreateNutshellMintContainer(ctx, 100, lnd2)
	// if err != nil {
	// 	t.Fatalf("error starting nutshell mint: %v", err)
	// }
	// defer nutshellMint2.Terminate(ctx)
	// nutshellURL2 := nutshellMint2.Host
	//
	// testNutshellWalletPath := filepath.Join(".", "/nutshellwalletmultimint")
	// testNutshellWallet, err := testutils.CreateTestWallet(testNutshellWalletPath, nutshellURL)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// defer os.RemoveAll(testNutshellWalletPath)
	//
	// if err := testutils.FundCashuWallet(ctx, testNutshellWallet, lnd3, 21000); err != nil {
	// 	t.Fatalf("error funding wallet: %v", err)
	// }
	// testNutshellWallet.Shutdown()
	//
	// walletConfig = wallet.Config{WalletPath: testNutshellWalletPath, CurrentMintURL: nutshellURL2}
	// testNutshellWallet, err = wallet.LoadWallet(walletConfig)
	// if err != nil {
	// 	t.Fatalf("error loading wallet: %v", err)
	// }
	// if err := testutils.FundCashuWallet(ctx, testNutshellWallet, lnd3, 21000); err != nil {
	// 	t.Fatalf("error funding wallet: %v", err)
	// }
	//
	// // test MPP with nutshell mint
	// testMultimintPayment(t, testNutshellWallet, lnd3, lnd4)
}

func testMultimintPayment(
	t *testing.T,
	testWallet *wallet.Wallet,
	routeNode *btcdocker.Lnd,
	noRouteNode *btcdocker.Lnd,
) {
	// create invoice from lnd3
	invoice := lnrpc.Invoice{Value: 10000}
	addInvoiceResponse, err := routeNode.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	mints := testWallet.TrustedMints()
	mint1 := mints[0]
	mint2 := mints[1]

	balanceBeforeMultiPayment := testWallet.GetBalanceByMints()
	// try multimint from wallet using funds from 2 mints
	multimintPaymentSplit := map[string]uint64{
		mint1: 5000 * 1000,
		mint2: 5000 * 1000,
	}
	meltResponses, err := testWallet.MultiMintPayment(addInvoiceResponse.PaymentRequest, multimintPaymentSplit)
	if err != nil {
		t.Fatalf("error doing multimint payment: %v", err)
	}

	for _, melt := range meltResponses {
		if melt.State != nut05.Paid {
			t.Fatalf("quote '%v' does not have PAID state", melt.Quote)
		}
	}

	balanceAfterMultiPayment := testWallet.GetBalanceByMints()
	if balanceAfterMultiPayment[mint1]+5000 > balanceBeforeMultiPayment[mint1] {
		t.Fatalf(`balance not affected after successful multimint payment.
		Balance before payment for mint '%v' was '%v'. Balance after payment '%v'.`,
			mint1, balanceBeforeMultiPayment[mint1], balanceAfterMultiPayment[mint1])
	}
	if balanceAfterMultiPayment[mint2]+5000 > balanceBeforeMultiPayment[mint2] {
		t.Fatalf(`balance not affected after successful multimint payment.
		Balance before payment for mint '%v' was '%v'. Balance after payment '%v'.`,
			mint2, balanceBeforeMultiPayment[mint2], balanceAfterMultiPayment[mint2])
	}

	// this should fail as there is no route for the payment
	invoice = lnrpc.Invoice{Value: 10000}
	addInvoiceResponse, err = noRouteNode.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	// test split with only one mint
	multimintPaymentSplit = map[string]uint64{mint1: 5000 * 1000}
	meltResponses, err = testWallet.MultiMintPayment(addInvoiceResponse.PaymentRequest, multimintPaymentSplit)
	if !errors.Is(err, nut15.ErrSplitTooShort) {
		t.Fatalf("expected err '%v' but got '%v'", nut15.ErrSplitTooShort, err)
	}

	// test split does not add up to invoice amount
	multimintPaymentSplit = map[string]uint64{
		mint1: 5000 * 1000,
		mint2: 3000 * 1000,
	}
	meltResponses, err = testWallet.MultiMintPayment(addInvoiceResponse.PaymentRequest, multimintPaymentSplit)
	splitSumErrString := "sum of split amounts '8000' does not equal invoice amount of '10000'"
	if err.Error() != splitSumErrString {
		t.Fatalf("expected err '%v' but got '%v'", splitSumErrString, err)
	}

	previousBalance := testWallet.GetBalance()
	// expecting error because payment will fail
	meltResponses, err = testWallet.MultiMintPayment(addInvoiceResponse.PaymentRequest, multimintPaymentSplit)
	if err == nil {
		t.Fatalf("expected nil err but got '%v'", err)
	}

	balanceAfterFailure := testWallet.GetBalance()
	// balance should stay the same since multimint payment failed
	if previousBalance != balanceAfterFailure {
		t.Fatalf(`balance before failed multimint payment '%v' does not match balance after '%v'`,
			previousBalance, balanceAfterFailure)
	}

	// test with split where there aren't enough funds in
	// one of the selected mints in the split
	invoice = lnrpc.Invoice{Value: 21000}
	addInvoiceResponse, err = routeNode.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		t.Fatalf("error creating invoice: %v", err)
	}

	// split with not enough funds in one mint
	split := map[string]uint64{
		mint1: 4000 * 1000,
		// this mint has balance of 16000
		mint2: 17000 * 1000,
	}
	previousBalance = testWallet.GetBalance()
	meltResponses, err = testWallet.MultiMintPayment(addInvoiceResponse.PaymentRequest, split)
	if err == nil {
		t.Fatalf("expected nil err but got '%v'", err)
	}

	balanceAfterFailure = testWallet.GetBalance()
	// balance should stay the same since one of the melts should have failed
	if previousBalance != balanceAfterFailure {
		t.Fatalf(`balance before failed multimint payment '%v' does not match balance after '%v'`,
			previousBalance, balanceAfterFailure)
	}
}

// TESTS AGAINST NUTSHELL MINT

// test regular wallet ops against Nutshell
func TestNutshell(t *testing.T) {
	nutshellMint, err := testutils.CreateNutshellMintContainer(ctx, 100, nil)
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
	defer os.RemoveAll(testWalletPath)

	mintRes, err := testWallet.RequestMint(10000, testWallet.CurrentMint())
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
	token, _ := cashu.NewTokenV4(proofsToSend, nutshellURL, cashu.Sat, false)

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
	defer os.RemoveAll(testWalletPath)

	mintRes, err := testWallet.RequestMint(10000, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting mint: %v", err)
	}

	_, err = testWallet.MintTokens(mintRes.Quote)
	if err != nil {
		t.Fatalf("unexpected error minting tokens: %v", err)
	}

	var invoiceAmount uint64 = 2000
	bolt11 := "lnbcrt20u1pnn00ztpp5h6frn7fk93jurxpygwnkck2u7dc05c2he7l7amgna7ngteeynk2qdqqcqzzsxqyz5vqsp5s6fw9g7twqcv5h9pv74vutwj7v3f4xy8jgtwww05mt0lp0sl8zsq9qyyssqt9khadm8v7mzc7z7rkuah4xqncrsjfxueqjfv2enze7vvha478asgztpfdw9c6redv2zr4xru7t6k6epfsw50tguzc08g88up0ct08gpalvp8d"

	// TODO: this invoice was being rejected by nutshell
	//bolt11, _, _, _ := lightning.CreateFakeInvoice(invoiceAmount, false)

	balanceBeforeMelt := testWallet.GetBalance()

	meltQuote, err := testWallet.RequestMeltQuote(bolt11, testWallet.CurrentMint())
	if err != nil {
		t.Fatalf("unexpected error requesting melt quote: %v", err)
	}

	meltResponse, err := testWallet.Melt(meltQuote.Quote)
	if err != nil {
		t.Fatalf("got unexpected melt error: %v", err)
	}
	change := len(meltResponse.Change)
	if change < 1 {
		t.Fatalf("expected change")
	}

	// actual lightning fee paid
	lightningFee := meltResponse.FeeReserve - meltResponse.Change.Amount()
	expectedBalance := balanceBeforeMelt - invoiceAmount - lightningFee
	if testWallet.GetBalance() != expectedBalance {
		t.Fatalf("expected balance of '%v' but got '%v' instead", expectedBalance, testWallet.GetBalance())
	}

	// do extra ops after melting to check counter for blinded messages
	// was incremented correctly
	mintRes, err = testWallet.RequestMint(5000, testWallet.CurrentMint())
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
	token, _ := cashu.NewTokenV4(proofsToSend, nutshellURL, cashu.Sat, false)
	_, err = testWallet.Receive(token, false)
	if err != nil {
		t.Fatalf("unexpected error receiving: %v", err)
	}
}

func TestSendToPubkeyNutshell(t *testing.T) {
	nutshellURL := nutshellMint.Host

	nutshellMint2, err := testutils.CreateNutshellMintContainer(ctx, 0, nil)
	if err != nil {
		t.Fatalf("error starting nutshell mint: %v", err)
	}
	defer nutshellMint2.Terminate(ctx)

	testWalletPath := filepath.Join(".", "/testwalletp2pk")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, nutshellURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	testWalletPath2 := filepath.Join(".", "/testwalletp2pk2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, nutshellMint2.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	testP2PK(t, testWallet, testWallet2)
}

func TestDLEQProofsNutshell(t *testing.T) {
	nutshellURL := nutshellMint.Host

	testWalletPath := filepath.Join(".", "/testwalletdleqnutshell")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, nutshellURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	testDLEQ(t, testWallet)
}

func TestWalletRestoreNutshell(t *testing.T) {
	mintURL := nutshellMint.Host

	testWalletPath := filepath.Join(".", "/testrestorewalletnutshell")
	testWallet, err := testutils.CreateTestWallet(testWalletPath, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath)

	testWalletPath2 := filepath.Join(".", "/testrestorewalletnutshell2")
	testWallet2, err := testutils.CreateTestWallet(testWalletPath2, mintURL)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testWalletPath2)

	testWalletRestore(t, testWallet, testWallet2, testWalletPath)
}
