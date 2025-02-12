package storage

import (
	"bytes"
	"encoding/hex"
	"log"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/crypto"
)

var (
	db *BoltDB
)

func TestMain(m *testing.M) {
	code, err := testMain(m)
	if err != nil {
		log.Println(err)
	}
	os.Exit(code)
}

func testMain(m *testing.M) (int, error) {
	dbpath := "./testdbbolt"
	err := os.MkdirAll(dbpath, 0750)
	if err != nil {
		return 1, err
	}
	db, err = InitBolt(dbpath)
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(dbpath)

	return m.Run(), nil
}

func TestProofs(t *testing.T) {
	keysetId1 := "keysetId12345"
	numProofsKeysetId1 := 50
	randomProofs1 := generateRandomProofs(keysetId1, numProofsKeysetId1)

	if err := db.SaveProofs(randomProofs1); err != nil {
		t.Fatalf("error saving proofs: %v", err)
	}

	proofs := db.GetProofs()
	if len(proofs) != numProofsKeysetId1 {
		t.Fatalf("expected '%v' proofs from db but got '%v'", numProofsKeysetId1, len(proofs))
	}

	keysetId2 := "someotherKeysetId123"
	numProofsKeysetId2 := 100
	randomProofs2 := generateRandomProofs(keysetId2, numProofsKeysetId2)

	if err := db.SaveProofs(randomProofs2); err != nil {
		t.Fatalf("error saving proofs: %v", err)
	}

	proofsById := db.GetProofsByKeysetId(keysetId1)
	if len(proofsById) != numProofsKeysetId1 {
		t.Fatalf("expected '%v' proofs from db for keyset '%v' but got '%v'",
			numProofsKeysetId1, keysetId1, len(proofsById))
	}

	sortProofs(randomProofs1)
	sortProofs(proofsById)
	if !reflect.DeepEqual(randomProofs1, proofsById) {
		t.Fatal("proofs from db do not match randomly generated ones saved to db")
	}

	// delete proofs from db and check correct response
	numToDelete := 3
	for i := 0; i < numToDelete; i++ {
		if err := db.DeleteProof(randomProofs1[i].Secret); err != nil {
			t.Fatalf("error deleting proof: %v", err)
		}
	}

	proofsById = db.GetProofsByKeysetId(keysetId1)
	expectedNumProofs := numProofsKeysetId1 - numToDelete
	if len(proofsById) != expectedNumProofs {
		t.Fatalf("expected '%v' proofs from db for keyset '%v' but got '%v'",
			expectedNumProofs, keysetId1, len(proofsById))
	}
}

func TestPendingProofs(t *testing.T) {
	keysetId1 := "keysetId12345"
	numProofsKeysetId1 := 50
	randomProofs1 := generateRandomProofs(keysetId1, numProofsKeysetId1)

	if err := db.AddPendingProofs(randomProofs1); err != nil {
		t.Fatalf("error saving pending proofs: %v", err)
	}

	pendingProofs := db.GetPendingProofs()
	if len(pendingProofs) != numProofsKeysetId1 {
		t.Fatalf("expected '%v' pending proofs from db but got '%v'",
			numProofsKeysetId1, len(pendingProofs))
	}

	// convert from cashu.Proofs to []DBProof to compare them to
	// response from db
	randomProofsToDB := toDBProofs(randomProofs1, "")
	sortDBProofs(randomProofsToDB)
	sortDBProofs(pendingProofs)
	if !reflect.DeepEqual(randomProofsToDB, pendingProofs) {
		t.Fatal("pending proofs from db do not match randomly generated ones saved to db")
	}

	// delete pending proofs and check correct response
	numToDelete := 3
	YsToDelete := make([]string, numToDelete)
	for i := 0; i < numToDelete; i++ {
		YsToDelete[i] = pendingProofs[i].Y
	}
	if err := db.DeletePendingProofs(YsToDelete); err != nil {
		t.Fatalf("error deleting pending proofs: %v", err)
	}
	pendingProofs = db.GetPendingProofs()
	if len(pendingProofs) != numProofsKeysetId1-numToDelete {
		t.Fatalf("expected '%v' pending proofs from db but got '%v'",
			numProofsKeysetId1-numToDelete, len(pendingProofs))
	}

	// add pending proofs tied to a quote id
	quoteId := "quoteId12345"
	numProofsQuoteId := 25
	randomProofs1 = generateRandomProofs(keysetId1, numProofsQuoteId)
	if err := db.AddPendingProofsByQuoteId(randomProofs1, quoteId); err != nil {
		t.Fatalf("error saving pending proofs by quote id: %v", err)
	}

	// check only returns pending proofs for the quote id
	proofsByQuoteId := db.GetPendingProofsByQuoteId(quoteId)
	if len(proofsByQuoteId) != numProofsQuoteId {
		t.Fatalf("expected '%v' pending proofs from db but got '%v' for quote id '%v'",
			numProofsKeysetId1, len(proofsByQuoteId), quoteId)
	}

	randomProofsToDB = toDBProofs(randomProofs1, quoteId)
	sortDBProofs(randomProofsToDB)
	sortDBProofs(proofsByQuoteId)
	if !reflect.DeepEqual(randomProofsToDB, proofsByQuoteId) {
		t.Fatalf("pending proofs for quote id '%v' from db do not match randomly generated ones saved to db",
			quoteId)
	}

	// check proofs correctly deleted for quote id
	if err := db.DeletePendingProofsByQuoteId(quoteId); err != nil {
		t.Fatalf("error deleting pending proofs by quote id: %v", err)
	}

	proofsByQuoteId = db.GetPendingProofsByQuoteId(quoteId)
	if len(proofsByQuoteId) != 0 {
		t.Fatalf("expected 0 pending proofs from db but got '%v' for quote id '%v'",
			len(proofsByQuoteId), quoteId)
	}
}

func TestKeysets(t *testing.T) {
	keyset1 := generateKeyset("http://localhost:3338")
	keyset2 := generateKeyset("http://localhost:3338")
	keyset3 := generateKeyset("http://localhost:8888")

	if err := db.SaveKeyset(&keyset1); err != nil {
		t.Fatalf("error saving keyset: %v", err)
	}
	if err := db.SaveKeyset(&keyset2); err != nil {
		t.Fatalf("error saving keyset: %v", err)
	}
	if err := db.SaveKeyset(&keyset3); err != nil {
		t.Fatalf("error saving keyset: %v", err)
	}

	keysetsMap := db.GetKeysets()
	// length should be 2 because the map keys are the different mints
	if len(keysetsMap) != 2 {
		t.Fatalf("expected keyset map of length 2 but got %v", len(keysetsMap))
	}

	keysetFromDb := db.GetKeyset(keyset1.Id)
	if !reflect.DeepEqual(keyset1, *keysetFromDb) {
		t.Fatalf("keyset '%v' from db does not match '%v'", *keysetFromDb, keyset1)
	}

	var incrementBy uint32 = 5
	if err := db.IncrementKeysetCounter(keyset2.Id, incrementBy); err != nil {
		t.Fatalf("error updating keyset counter: %v", err)
	}

	counter := db.GetKeysetCounter(keyset1.Id)
	if counter != 0 {
		t.Fatalf("expected counter for keyset '%v' to 0 but got %v", keyset1.Id, counter)
	}

	counter = db.GetKeysetCounter(keyset2.Id)
	if counter != incrementBy {
		t.Fatalf("expected counter for keyset '%v' to %v but got %v", keyset1.Id, incrementBy, counter)
	}

	if err := db.IncrementKeysetCounter(keyset2.Id, 3); err != nil {
		t.Fatalf("error updating keyset counter: %v", err)
	}

	counter = db.GetKeysetCounter(keyset2.Id)
	if counter != incrementBy+3 {
		t.Fatalf("expected counter for keyset '%v' to %v but got %v", keyset1.Id, incrementBy+3, counter)
	}
}

func TestMintQuotes(t *testing.T) {
	quoteId := "quoteId1"
	mintQuote := generateMintQuote(quoteId, false)
	if err := db.SaveMintQuote(mintQuote); err != nil {
		t.Fatalf("error saving mint quote: %v", err)
	}

	mintQuotes := generateRandomMintQuotes(50, false)
	for _, quote := range mintQuotes {
		if err := db.SaveMintQuote(quote); err != nil {
			t.Fatalf("error saving mint quote: %v", err)
		}
	}

	// find quote by id
	quoteById := db.GetMintQuoteById(quoteId)
	if quoteById == nil {
		t.Fatal("expected valid quote but got nil")
	}
	if !reflect.DeepEqual(mintQuote, *quoteById) {
		t.Fatal("mint quote from db does not match generated one")
	}
	if quoteById.PrivateKey != nil {
		t.Fatalf("expected nil private key but got %v", quoteById.PrivateKey)
	}

	quotesFromDb := db.GetMintQuotes()
	expectedNumQuotes := 51
	if len(quotesFromDb) != expectedNumQuotes {
		t.Fatalf("expected '%v' mint quotes but got '%v' ", expectedNumQuotes, len(quotesFromDb))
	}

	// test mint quote with private key
	quoteId = "quote-with-privatekey"
	mintQuote = generateMintQuote(quoteId, true)
	if err := db.SaveMintQuote(mintQuote); err != nil {
		t.Fatalf("error saving mint quote: %v", err)
	}
	quoteById = db.GetMintQuoteById(quoteId)
	if quoteById == nil {
		t.Fatal("expected valid quote but got nil")
	}
	if !reflect.DeepEqual(mintQuote, *quoteById) {
		t.Fatal("mint quote from db does not match generated one")
	}
	if quoteById.PrivateKey == nil {
		t.Fatal("expected private key but got nil")
	}

	expectedKey := mintQuote.PrivateKey.Serialize()
	if bytes.Compare(expectedKey, quoteById.PrivateKey.Serialize()) != 0 {
		t.Fatalf("expected key '%v' but got '%v'", expectedKey, quoteById.PrivateKey.Serialize())
	}

}

func TestMeltQuotes(t *testing.T) {
	quoteId := "quoteId1"
	quote := generateMeltQuote(quoteId)
	if err := db.SaveMeltQuote(quote); err != nil {
		t.Fatalf("error saving melt quote: %v", err)
	}

	quotes := generateRandomMeltQuotes(50)
	for _, quote := range quotes {
		if err := db.SaveMeltQuote(quote); err != nil {
			t.Fatalf("error saving melt quote: %v", err)
		}
	}

	// find quote by id
	quoteById := db.GetMeltQuoteById(quoteId)
	if quoteById == nil {
		t.Fatal("expected valid quote but got nil")
	}

	if !reflect.DeepEqual(quote, *quoteById) {
		t.Fatal("melt quote from db does not match generated one")
	}

	quotesFromDb := db.GetMeltQuotes()
	expectedNumQuotes := 51
	if len(quotesFromDb) != expectedNumQuotes {
		t.Fatalf("expected '%v' melt quotes but got '%v' ", expectedNumQuotes, len(quotesFromDb))
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

func generateRandomProofs(keysetId string, num int) cashu.Proofs {
	proofs := make(cashu.Proofs, num)

	for i := 0; i < num; i++ {
		proof := cashu.Proof{
			Amount: 21,
			Id:     keysetId,
			Secret: generateRandomString(64),
			C:      generateRandomString(64),
		}
		proofs[i] = proof
	}

	return proofs
}

func toDBProofs(proofs cashu.Proofs, quoteId string) []DBProof {
	dbProofs := make([]DBProof, len(proofs))

	for i, proof := range proofs {
		Y, _ := crypto.HashToCurve([]byte(proof.Secret))
		Yhex := hex.EncodeToString(Y.SerializeCompressed())

		dbProof := DBProof{
			Y:           Yhex,
			Amount:      proof.Amount,
			Id:          proof.Id,
			Secret:      proof.Secret,
			C:           proof.C,
			DLEQ:        proof.DLEQ,
			MeltQuoteId: quoteId,
		}
		dbProofs[i] = dbProof
	}

	return dbProofs
}

func sortProofs(proofs cashu.Proofs) {
	slices.SortFunc(proofs, func(a, b cashu.Proof) int {
		return strings.Compare(a.Secret, b.Secret)
	})
}

func sortDBProofs(proofs []DBProof) {
	slices.SortFunc(proofs, func(a, b DBProof) int {
		return strings.Compare(a.Secret, b.Secret)
	})
}

func generateKeyset(mint string) crypto.WalletKeyset {
	return crypto.WalletKeyset{
		Id:          generateRandomString(32),
		MintURL:     mint,
		Unit:        cashu.Sat.String(),
		Active:      true,
		PublicKeys:  make(map[uint64]*secp256k1.PublicKey),
		InputFeePpk: 100,
	}
}

func generateMintQuote(id string, privateKey bool) MintQuote {
	mintQuote := MintQuote{
		QuoteId: id,
		Mint:    "http://localhost:3338",
		Method:  "bolt11",
		State:   nut04.Unpaid,
		Amount:  21,
	}
	if privateKey {
		pk, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			panic(err)
		}
		mintQuote.PrivateKey = pk
	}

	return mintQuote
}

func generateRandomMintQuotes(num int, privateKey bool) []MintQuote {
	quotes := make([]MintQuote, num)
	for i := 0; i < num; i++ {
		id := generateRandomString(32)
		quote := generateMintQuote(id, privateKey)
		quotes[i] = quote
	}
	return quotes
}

func generateMeltQuote(id string) MeltQuote {
	return MeltQuote{
		QuoteId: id,
		Mint:    "http://localhost:3338",
		Method:  "bolt11",
		State:   nut05.Unpaid,
		Amount:  21,
	}
}

func generateRandomMeltQuotes(num int) []MeltQuote {
	quotes := make([]MeltQuote, num)
	for i := 0; i < num; i++ {
		id := generateRandomString(32)
		quote := generateMeltQuote(id)
		quotes[i] = quote
	}
	return quotes
}
