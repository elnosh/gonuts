package mint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
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
	ActiveKeysets map[string]crypto.Keyset

	// map of all keysets (both active and inactive)
	Keysets map[string]crypto.Keyset

	LightningClient lightning.Client
}

func LoadMint(config Config) (*Mint, error) {
	path := setMintDBPath()
	db, err := bolt.Open(filepath.Join(path, "mint.db"), 0600, nil)
	if err != nil {
		log.Fatalf("error starting mint: %v", err)
	}

	activeKeyset := crypto.GenerateKeyset(config.PrivateKey, config.DerivationPath)
	mint := &Mint{db: db, ActiveKeysets: map[string]crypto.Keyset{activeKeyset.Id: *activeKeyset}}
	err = mint.initMintBuckets()
	if err != nil {
		return nil, fmt.Errorf("error setting up db: %v", err)
	}

	mint.SaveKeyset(*activeKeyset)
	mint.Keysets = mint.GetKeysets()
	mint.Keysets[activeKeyset.Id] = *activeKeyset
	mint.LightningClient = lightning.NewLightningClient()

	for i, keyset := range mint.Keysets {
		if keyset.Id != activeKeyset.Id && keyset.Active {
			keyset.Active = false
			mint.SaveKeyset(keyset)
			mint.Keysets[i] = keyset
		}
	}

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

	i := 0
	for k := range m.Keysets {
		keysetIds[i] = k
		i++
	}
	return keysetIds
}

func (m *Mint) RequestMintQuote(method string, amount uint64, unit string) (nut04.PostMintQuoteBolt11Response, error) {
	if method != "bolt11" {
		return nut04.PostMintQuoteBolt11Response{}, cashu.PaymentMethodNotSupportedErr
	}
	if unit != "sat" {
		return nut04.PostMintQuoteBolt11Response{}, cashu.UnitNotSupportedErr
	}

	invoice, err := m.requestInvoice(amount)
	if err != nil {
		return nut04.PostMintQuoteBolt11Response{}, err
	}

	err = m.SaveInvoice(*invoice)
	if err != nil {
		return nut04.PostMintQuoteBolt11Response{}, err
	}

	reqMintQuoteResponse := nut04.PostMintQuoteBolt11Response{
		Quote:   invoice.Id,
		Request: invoice.PaymentRequest,
		Paid:    invoice.Settled,
		Expiry:  invoice.Expiry,
	}

	return reqMintQuoteResponse, nil
}

func (m *Mint) GetMintQuoteState(method, quoteId string) (nut04.PostMintQuoteBolt11Response, error) {
	if method != "bolt11" {
		return nut04.PostMintQuoteBolt11Response{}, cashu.PaymentMethodNotSupportedErr
	}

	invoice := m.GetInvoice(quoteId)
	if invoice == nil {
		return nut04.PostMintQuoteBolt11Response{}, cashu.InvoiceNotExistErr
	}

	settled := m.LightningClient.InvoiceSettled(invoice.PaymentHash)
	if settled != invoice.Settled {
		invoice.Settled = settled
		m.SaveInvoice(*invoice)
	}

	quoteState := nut04.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: settled, Expiry: invoice.Expiry}
	return quoteState, nil
}

// id - quote id to lookup invoice
func (m *Mint) MintTokens(method, id string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	if method != "bolt11" {
		return nil, cashu.PaymentMethodNotSupportedErr
	}

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
		blindedSignatures, err = m.signBlindedMessages(blindedMessages)
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
	blindedSignatures, err := m.signBlindedMessages(blindedMessages)
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
	Preimage       string
}

func (m *Mint) MeltRequest(method, request, unit string) (MeltQuote, error) {
	if method != "bolt11" {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}
	if unit != "sat" {
		return MeltQuote{}, cashu.UnitNotSupportedErr
	}

	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return MeltQuote{}, fmt.Errorf("melt request error: %v", err)
	}

	hash := sha256.Sum256(randomBytes)

	amount, fee, err := m.LightningClient.FeeReserve(request)
	if err != nil {
		return MeltQuote{}, fmt.Errorf("error getting fee: %v", err)
	}
	expiry := time.Now().Add(time.Minute * QuoteExpiryMins).Unix()

	meltQuote := MeltQuote{
		Id:             hex.EncodeToString(hash[:]),
		InvoiceRequest: request,
		Amount:         amount,
		FeeReserve:     fee,
		Paid:           false,
		Expiry:         expiry,
	}
	m.SaveMeltQuote(meltQuote)

	return meltQuote, nil
}

func (m *Mint) GetMeltQuoteState(method, quoteId string) (MeltQuote, error) {
	if method != "bolt11" {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote := m.GetMeltQuote(quoteId)
	if meltQuote == nil {
		return MeltQuote{}, cashu.MeltQuoteNotExistErr
	}

	return *meltQuote, nil
}

func (m *Mint) MeltTokens(method, quoteId string, proofs cashu.Proofs) (MeltQuote, error) {
	if method != "bolt11" {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote := m.GetMeltQuote(quoteId)
	if meltQuote == nil {
		return MeltQuote{}, cashu.MeltQuoteNotExistErr
	}

	valid, err := m.VerifyProofs(proofs)
	if err != nil || !valid {
		return MeltQuote{}, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
	}

	var inputsAmount uint64 = 0
	for _, input := range proofs {
		inputsAmount += input.Amount
	}

	if inputsAmount < meltQuote.Amount+meltQuote.FeeReserve {
		return MeltQuote{}, cashu.InsufficientProofsAmount
	}

	preimage, err := m.LightningClient.SendPayment(meltQuote.InvoiceRequest)
	if err != nil {
		return *meltQuote, nil
	}
	meltQuote.Paid = true
	meltQuote.Preimage = preimage

	for _, proof := range proofs {
		m.SaveProof(proof)
	}

	return *meltQuote, nil
}

func (m *Mint) VerifyProofs(proofs cashu.Proofs) (bool, error) {
	for _, proof := range proofs {
		dbProof := m.GetProof(proof.Secret)
		if dbProof != nil {
			return false, cashu.ProofAlreadyUsedErr
		}

		var privateKey []byte
		keyset, ok := m.Keysets[proof.Id]
		if !ok {
			return false, cashu.InvalidKeysetProof
		} else {
			for _, kp := range keyset.KeyPairs {
				if kp.Amount == proof.Amount {
					privateKey = kp.PrivateKey
				}
			}
		}
		k := secp256k1.PrivKeyFromBytes(privateKey)

		Cbytes, err := hex.DecodeString(proof.C)
		if err != nil {
			return false, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}

		C, err := secp256k1.ParsePubKey(Cbytes)
		if err != nil {
			return false, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}

		if !crypto.Verify(proof.Secret, k, C) {
			return false, cashu.InvalidProofErr
		}
	}
	return true, nil
}

func (m *Mint) signBlindedMessages(blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	blindedSignatures := make(cashu.BlindedSignatures, len(blindedMessages))

	for i, msg := range blindedMessages {
		keyset, ok := m.ActiveKeysets[msg.Id]
		if !ok {
			return nil, cashu.InvalidSignatureRequest
		}

		var privateKey []byte
		for _, kp := range keyset.KeyPairs {
			if kp.Amount == msg.Amount {
				privateKey = kp.PrivateKey
			}
		}
		privKey := secp256k1.PrivKeyFromBytes(privateKey)

		B_bytes, err := hex.DecodeString(msg.B_)
		if err != nil {
			log.Fatal(err)
		}
		B_, err := btcec.ParsePubKey(B_bytes)
		if err != nil {
			return nil, err
		}

		C_ := crypto.SignBlindedMessage(B_, privKey)
		C_hex := hex.EncodeToString(C_.SerializeCompressed())

		blindedSignature := cashu.BlindedSignature{Amount: msg.Amount,
			C_: C_hex, Id: keyset.Id}

		blindedSignatures[i] = blindedSignature
	}

	return blindedSignatures, nil
}

// creates lightning invoice
func (m *Mint) requestInvoice(amount uint64) (*lightning.Invoice, error) {
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

	return &invoice, nil
}
