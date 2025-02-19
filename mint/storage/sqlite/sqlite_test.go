package sqlite

import (
	"bytes"
	"encoding/hex"
	"log"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/storage"
)

var (
	db *SQLiteDB
)

func TestMain(m *testing.M) {
	code, err := testMain(m)
	if err != nil {
		log.Println(err)
	}
	os.Exit(code)
}

func testMain(m *testing.M) (int, error) {
	dbpath := "./testsqlite"
	err := os.MkdirAll(dbpath, 0750)
	if err != nil {
		return 1, err
	}

	db, err = InitSQLite(dbpath)
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(dbpath)

	return m.Run(), nil
}

func TestProofs(t *testing.T) {
	proofs := generateRandomProofs(50)

	if err := db.SaveProofs(proofs); err != nil {
		t.Fatalf("error saving proofs: %v", err)
	}

	Ys := make([]string, 20)
	expectedProofs := make([]storage.DBProof, 20)
	for i := 0; i < 20; i++ {
		Y, _ := crypto.HashToCurve([]byte(proofs[i].Secret))
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		Ys[i] = Yhex
		expectedProofs[i] = toDBProof(proofs[i], Yhex, "")
	}

	dbProofs, err := db.GetProofsUsed(Ys)
	if err != nil {
		t.Fatalf("error getting used proofs: %v", err)
	}

	if len(dbProofs) != 20 {
		t.Fatalf("got incorrect number of proofs from db. Expected %v but got %v", 20, len(dbProofs))
	}

	sortDBProofs(expectedProofs)
	sortDBProofs(dbProofs)

	if !reflect.DeepEqual(dbProofs, expectedProofs) {
		t.Fatal("proofs from db do not match generated ones saved to db")
	}
}

func TestPendingProofs(t *testing.T) {
	quoteId := "quoteid12345"
	proofs := generateRandomProofs(50)

	if err := db.AddPendingProofs(proofs, quoteId); err != nil {
		t.Fatalf("error saving pending proofs: %v", err)
	}

	Ys := make([]string, 20)
	expectedProofs := make([]storage.DBProof, 20)
	for i := 0; i < 20; i++ {
		Y, _ := crypto.HashToCurve([]byte(proofs[i].Secret))
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		Ys[i] = Yhex
		expectedProofs[i] = toDBProof(proofs[i], Yhex, quoteId)
	}

	pendingProofs, err := db.GetPendingProofs(Ys)
	if err != nil {
		t.Fatalf("error getting pending proofs: %v", err)
	}

	if len(pendingProofs) != 20 {
		t.Fatalf("got incorrect number of pending proofs from db. Expected %v but got %v",
			20, len(pendingProofs))
	}

	sortDBProofs(expectedProofs)
	sortDBProofs(pendingProofs)

	if !reflect.DeepEqual(pendingProofs, expectedProofs) {
		t.Fatal("pending proofs from db do not match generated ones saved to db")
	}

	proofs2 := generateRandomProofs(100)
	if err := db.AddPendingProofs(proofs2, "anotherquoteid"); err != nil {
		t.Fatalf("error saving pending proofs: %v", err)
	}

	expectedProofs = make([]storage.DBProof, 50)
	for i, proof := range proofs {
		Y, _ := crypto.HashToCurve([]byte(proof.Secret))
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		expectedProofs[i] = toDBProof(proof, Yhex, "")
	}

	pendingProofsByQuote, err := db.GetPendingProofsByQuote(quoteId)
	if err != nil {
		t.Fatalf("error getting pending proofs for quote id '%v': %v", quoteId, err)
	}

	if len(pendingProofsByQuote) != 50 {
		t.Fatalf("got incorrect number of pending proofs from db. Expected %v but got %v",
			50, len(pendingProofsByQuote))
	}

	sortDBProofs(expectedProofs)
	sortDBProofs(pendingProofsByQuote)

	if !reflect.DeepEqual(pendingProofsByQuote, expectedProofs) {
		t.Fatal("pending proofs from db do not match generated ones saved to db")
	}

	if err := db.RemovePendingProofs(Ys); err != nil {
		t.Fatalf("error deleting pending proofs: %v", err)
	}

	pendingProofs, err = db.GetPendingProofs(Ys)
	if err != nil {
		t.Fatalf("error getting pending proofs: %v", err)
	}

	if len(pendingProofs) != 0 {
		t.Fatalf("expected no pending proofs but got %v", len(pendingProofs))
	}

}

func TestMintQuotes(t *testing.T) {
	mintQuotes := generateRandomMintQuotes(150, false)

	var wg sync.WaitGroup
	var mu sync.RWMutex
	errs := make([]error, 0)
	for _, quote := range mintQuotes {
		wg.Add(1)
		go func(quote storage.MintQuote) {
			if err := db.SaveMintQuote(quote); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
			wg.Done()
		}(quote)
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("error saving mint quote: %v", errs[0])
	}

	expectedQuote := mintQuotes[21]
	quote, err := db.GetMintQuote(expectedQuote.Id)
	if err != nil {
		t.Fatalf("error getting mint quote by id: %v", err)
	}
	if !reflect.DeepEqual(expectedQuote, quote) {
		t.Fatal("quote from db does not match generated one")
	}
	if quote.Pubkey != nil {
		t.Fatalf("expected nil pubkey but got '%v'", quote.Pubkey)
	}

	quote, err = db.GetMintQuoteByPaymentHash(expectedQuote.PaymentHash)
	if err != nil {
		t.Fatalf("error getting mint quote by payment hash: %v", err)
	}
	if !reflect.DeepEqual(expectedQuote, quote) {
		t.Fatal("quote from db does not match generated one")
	}
	if quote.Pubkey != nil {
		t.Fatalf("expected nil pubkey but got '%v'", quote.Pubkey)
	}

	if err := db.UpdateMintQuoteState(quote.Id, nut04.Paid); err != nil {
		t.Fatalf("error updating mint quote: %v", err)
	}

	expectedQuote.State = nut04.Paid
	quote, err = db.GetMintQuote(expectedQuote.Id)
	if err != nil {
		t.Fatalf("error getting mint quote by id: %v", err)
	}
	if !reflect.DeepEqual(expectedQuote, quote) {
		t.Fatal("quote from db does not match generated one")
	}

	if err := db.UpdateMintQuoteState(quote.Id, nut04.Issued); err != nil {
		t.Fatalf("error updating mint quote: %v", err)
	}

	expectedQuote.State = nut04.Issued
	quote, err = db.GetMintQuote(expectedQuote.Id)
	if err != nil {
		t.Fatalf("error getting mint quote by id: %v", err)
	}
	if !reflect.DeepEqual(expectedQuote, quote) {
		t.Fatal("quote from db does not match generated one")
	}

	// test mint quotes with pubkey
	mintQuotes = generateRandomMintQuotes(20, true)

	errs = make([]error, 0)
	for _, quote := range mintQuotes {
		wg.Add(1)
		go func(quote storage.MintQuote) {
			if err := db.SaveMintQuote(quote); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
			wg.Done()
		}(quote)
	}
	wg.Wait()

	expectedQuote = mintQuotes[10]
	quote, err = db.GetMintQuote(expectedQuote.Id)
	if err != nil {
		t.Fatalf("error getting mint quote by id: %v", err)
	}
	if !reflect.DeepEqual(expectedQuote, quote) {
		t.Fatal("quote from db does not match generated one")
	}
	if expectedQuote.Pubkey == nil {
		t.Fatal("expected pubkey in mint quote but got nil")
	}
	expectedPubkey := expectedQuote.Pubkey.SerializeCompressed()
	if bytes.Compare(expectedPubkey, quote.Pubkey.SerializeCompressed()) != 0 {
		t.Fatalf("expected pubkey '%v' but got '%v'", expectedPubkey, quote.Pubkey.SerializeCompressed())
	}
}

func TestMeltQuote(t *testing.T) {
	meltQuotes := generateRandomMeltQuotes(150)

	var wg sync.WaitGroup
	var mu sync.RWMutex
	errs := make([]error, 0)
	for _, quote := range meltQuotes {
		wg.Add(1)
		go func(quote storage.MeltQuote) {
			if err := db.SaveMeltQuote(quote); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
			wg.Done()
		}(quote)
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("error saving melt quote: %v", errs[0])
	}

	expectedQuote := meltQuotes[21]
	quote, err := db.GetMeltQuote(expectedQuote.Id)
	if err != nil {
		t.Fatalf("error getting melt quote by id: %v", err)
	}

	if !reflect.DeepEqual(expectedQuote, quote) {
		t.Fatal("quote from db does not match generated one")
	}

	meltQuote, err := db.GetMeltQuoteByPaymentRequest(expectedQuote.InvoiceRequest)
	if err != nil {
		t.Fatalf("error getting melt quote by payment request: %v", err)
	}

	if !reflect.DeepEqual(expectedQuote, *meltQuote) {
		t.Fatal("quote from db does not match generated one")
	}

	if err := db.UpdateMeltQuote(quote.Id, "", nut05.Pending); err != nil {
		t.Fatalf("error updating melt quote: %v", err)
	}

	expectedQuote.State = nut05.Pending
	quote, err = db.GetMeltQuote(expectedQuote.Id)
	if err != nil {
		t.Fatalf("error getting melt quote by id: %v", err)
	}
	if !reflect.DeepEqual(expectedQuote, quote) {
		t.Fatal("quote from db does not match generated one")
	}

	if err := db.UpdateMeltQuote(quote.Id, "fakepreimage", nut05.Paid); err != nil {
		t.Fatalf("error updating melt quote: %v", err)
	}

	expectedQuote.State = nut05.Paid
	expectedQuote.Preimage = "fakepreimage"
	quote, err = db.GetMeltQuote(expectedQuote.Id)
	if err != nil {
		t.Fatalf("error getting melt quote by id: %v", err)
	}
	if !reflect.DeepEqual(expectedQuote, quote) {
		t.Fatal("quote from db does not match generated one")
	}
}

func TestBlindSignatures(t *testing.T) {
	count := 50
	blindedMessages := generateRandomB_s(count)
	blindSignatures := generateBlindSignatures(count)

	if err := db.SaveBlindSignatures(blindedMessages, blindSignatures); err != nil {
		t.Fatalf("unexpected error saving blind signatures: %v", err)
	}

	expectedBlindSig := blindSignatures[21]
	blindSig, err := db.GetBlindSignature(blindedMessages[21])
	if err != nil {
		t.Fatalf("error getting blind signature: %v", err)
	}

	if !reflect.DeepEqual(blindSig, expectedBlindSig) {
		t.Fatal("blind signature from db does match generated one")
	}

	blindSigs, err := db.GetBlindSignatures(blindedMessages[:20])
	if err != nil {
		t.Fatalf("error getting blind signatures: %v", err)
	}

	if len(blindSigs) != 20 {
		t.Fatalf("got incorrect number of blind signatures from db. Expected %v but got %v",
			20, len(blindSigs))
	}

}

func generateRandomString(length int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = letters[rand.IntN(len(letters))]
	}
	return string(b)
}

func generateRandomProofs(num int) cashu.Proofs {
	proofs := make(cashu.Proofs, num)

	for i := 0; i < num; i++ {
		proof := cashu.Proof{
			Amount: 21,
			Id:     generateRandomString(32),
			Secret: generateRandomString(64),
			C:      generateRandomString(64),
		}
		proofs[i] = proof
	}

	return proofs
}

func toDBProof(proof cashu.Proof, Y string, quoteId string) storage.DBProof {
	return storage.DBProof{
		Y:           Y,
		Amount:      proof.Amount,
		Id:          proof.Id,
		Secret:      proof.Secret,
		C:           proof.C,
		MeltQuoteId: quoteId,
	}
}

func sortDBProofs(proofs []storage.DBProof) {
	slices.SortFunc(proofs, func(a, b storage.DBProof) int {
		return strings.Compare(a.Secret, b.Secret)
	})
}

func generateRandomMintQuotes(num int, pubkey bool) []storage.MintQuote {
	quotes := make([]storage.MintQuote, num)
	for i := 0; i < num; i++ {
		quote := storage.MintQuote{
			Id:             generateRandomString(32),
			Amount:         21,
			PaymentRequest: generateRandomString(100),
			PaymentHash:    generateRandomString(50),
			State:          nut04.Unpaid,
		}
		if pubkey {
			key, err := secp256k1.GeneratePrivateKey()
			if err != nil {
				panic(err)
			}
			quote.Pubkey = key.PubKey()
		}
		quotes[i] = quote
	}
	return quotes
}

func generateRandomMeltQuotes(num int) []storage.MeltQuote {
	quotes := make([]storage.MeltQuote, num)
	for i := 0; i < num; i++ {
		quote := storage.MeltQuote{
			Id:             generateRandomString(32),
			InvoiceRequest: generateRandomString(100),
			PaymentHash:    generateRandomString(50),
			Amount:         21,
			FeeReserve:     1,
			State:          nut05.Unpaid,
		}
		quotes[i] = quote
	}
	return quotes
}

func generateRandomB_s(num int) []string {
	B_s := make([]string, num)
	for i := 0; i < num; i++ {
		B_s[i] = generateRandomString(33)
	}
	return B_s
}

func generateBlindSignatures(num int) cashu.BlindedSignatures {
	blindSigs := make(cashu.BlindedSignatures, num)
	for i := 0; i < num; i++ {
		sig := cashu.BlindedSignature{
			C_:     generateRandomString(33),
			Id:     generateRandomString(32),
			Amount: 21,
			DLEQ: &cashu.DLEQProof{
				E: generateRandomString(33),
				S: generateRandomString(33),
			},
		}
		blindSigs[i] = sig
	}
	return blindSigs
}
