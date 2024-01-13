package mint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/config"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	bolt "go.etcd.io/bbolt"
)

const (
	QuoteExpiryMins = 10
)

type Mint struct {
	db *bolt.DB

	// active keysets
	ActiveKeysets []crypto.Keyset

	// list of all keysets (both active and inactive)
	Keysets []crypto.Keyset

	LightningClient lightning.Client
}

func LoadMint(config config.Config) (*Mint, error) {
	path := setMintDBPath()
	db, err := bolt.Open(filepath.Join(path, "mint.db"), 0600, nil)
	if err != nil {
		log.Fatalf("error starting mint: %v", err)
	}

	keyset := crypto.GenerateKeyset(config.PrivateKey, config.DerivationPath)
	mint := &Mint{db: db, ActiveKeysets: []crypto.Keyset{*keyset}}
	err = mint.initMintBuckets()
	if err != nil {
		return nil, fmt.Errorf("error setting up db: %v", err)
	}
	mint.Keysets = mint.GetKeysets()
	mint.LightningClient = lightning.NewLightningClient()

	return mint, nil
}

func setMintDBPath() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	path := filepath.Join(homedir, ".gonuts", "mint")
	err = os.MkdirAll(path, 0700)
	if err != nil {
		log.Fatal(err)
	}
	return path
}

func (m *Mint) KeysetList() []string {
	keysetIds := make([]string, len(m.Keysets))

	for i, keyset := range m.Keysets {
		keysetIds[i] = keyset.Id
	}
	return keysetIds
}

// creates lightning invoice and saves it in db
func (m *Mint) RequestInvoice(amount uint64) (*lightning.Invoice, error) {
	invoice, err := m.LightningClient.CreateInvoice(amount)
	if err != nil {
		return nil, fmt.Errorf("error creating invoice: %v", err)
	}

	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		return nil, fmt.Errorf("error creating invoice: %v", err)
	}

	hash := sha256.Sum256(randomBytes)

	invoice.Id = hex.EncodeToString(hash[:])
	err = m.SaveInvoice(invoice)
	if err != nil {
		return nil, fmt.Errorf("error creating invoice: %v", err)
	}

	return &invoice, nil
}

// id - quote id to lookup invoice
func (m *Mint) MintTokens(id string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	invoice := m.GetInvoice(id)
	if invoice == nil {
		return nil, cashu.InvoiceNotExistErr
	}

	var blindedSignatures cashu.BlindedSignatures

	settled := m.LightningClient.InvoiceSettled(invoice.PaymentHash)
	if settled {
		if invoice.Redeemed {
			return nil, cashu.InvoiceTokensIssuedErr
		}

		var totalAmount uint64 = 0
		for _, message := range blindedMessages {
			totalAmount += message.Amount
		}

		if totalAmount > invoice.Amount {
			return nil, cashu.OutputsOverInvoiceErr
		}

		var err error
		blindedSignatures, err = cashu.SignBlindedMessages(blindedMessages, &m.ActiveKeysets[0])
		if err != nil {
			return nil, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}

		invoice.Settled = true
		invoice.Redeemed = true
		m.SaveInvoice(*invoice)
	} else {
		return nil, cashu.InvoiceNotPaidErr
	}

	return blindedSignatures, nil
}

func (m *Mint) Swap(proofs cashu.Proofs, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {

	var proofsAmount uint64 = 0
	var blindedMessagesAmount uint64 = 0

	for _, proof := range proofs {
		proofsAmount += proof.Amount
	}

	for _, msg := range blindedMessages {
		blindedMessagesAmount += msg.Amount
	}

	if proofsAmount != blindedMessagesAmount {
		return nil, cashu.AmountsDoNotMatch
	}

	valid, err := m.VerifyProofs(proofs)
	if err != nil || !valid {
		return nil, err
	}

	// if verification complete, sign blinded messages and add used proofs to db
	blindedSignatures, err := cashu.SignBlindedMessages(blindedMessages, &m.ActiveKeysets[0])
	if err != nil {
		cashuErr := cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		return nil, cashuErr
	}

	for _, proof := range proofs {
		m.SaveProof(proof)
	}

	return blindedSignatures, nil
}

type MeltQuote struct {
	Id             string
	InvoiceRequest string
	Amount         uint64
	FeeReserve     uint64
	Paid           bool
	Expiry         int64
}

func (m *Mint) MeltRequest(request *nut05.PostMeltQuoteBolt11Request) (*MeltQuote, error) {
	if request.Unit != "sat" {
		return nil, errors.New("unit nut supported")
	}

	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return nil, fmt.Errorf("melt request error: %v", err)
	}

	hash := sha256.Sum256(randomBytes)

	amount, fee, err := m.LightningClient.FeeReserve(request.Request)
	if err != nil {
		return nil, fmt.Errorf("error getting fee: %v", err)
	}
	expiry := time.Now().Add(time.Minute * QuoteExpiryMins).Unix()

	meltQuote := MeltQuote{
		Id:             hex.EncodeToString(hash[:]),
		InvoiceRequest: request.Request,
		Amount:         amount,
		FeeReserve:     fee,
		Paid:           false,
		Expiry:         expiry,
	}

	m.SaveMeltQuote(meltQuote)
	return &meltQuote, nil
}

// when melting, need to check that proofs being used to peg out
// are of the unit in the request. Meaning, the keyset id specified in the proof
// is for unit supported (sats)
func (m *Mint) Melt() {
}

func (m *Mint) VerifyProofs(proofs cashu.Proofs) (bool, error) {
	for _, proof := range proofs {
		dbProof := m.GetProof(proof.Secret)
		if dbProof != nil {
			return false, cashu.ProofAlreadyUsedErr
		}

		secret, err := hex.DecodeString(proof.Secret)
		if err != nil {
			cashuErr := cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
			return false, cashuErr
		}

		var privateKey []byte
		for _, kp := range m.ActiveKeysets[0].KeyPairs {
			if kp.Amount == proof.Amount {
				privateKey = kp.PrivateKey
			}
		}
		k := secp256k1.PrivKeyFromBytes(privateKey)

		Cbytes, err := hex.DecodeString(proof.C)
		if err != nil {
			cashuErr := cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
			return false, cashuErr
		}

		C, err := secp256k1.ParsePubKey(Cbytes)
		if err != nil {
			cashuErr := cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
			return false, cashuErr
		}

		if !crypto.Verify(secret, k, C) {
			return false, cashu.InvalidProofErr
		}
	}
	return true, nil
}
