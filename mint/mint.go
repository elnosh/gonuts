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
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut06"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	decodepay "github.com/nbd-wtf/ln-decodepay"
)

const (
	QuoteExpiryMins = 10
	BOLT11_METHOD   = "bolt11"
	SAT_UNIT        = "sat"
)

type Mint struct {
	db *BoltDB

	// active keysets
	ActiveKeysets map[string]crypto.Keyset

	// map of all keysets (both active and inactive)
	Keysets map[string]crypto.Keyset

	LightningClient lightning.Client
	MintInfo        *nut06.MintInfo
}

func LoadMint(config Config) (*Mint, error) {
	path := config.DBPath
	if len(path) == 0 {
		path = mintPath()
	}

	db, err := InitBolt(path)
	if err != nil {
		log.Fatalf("error starting mint: %v", err)
	}

	activeKeyset := crypto.GenerateKeyset(config.PrivateKey, config.DerivationPath)
	mint := &Mint{db: db, ActiveKeysets: map[string]crypto.Keyset{activeKeyset.Id: *activeKeyset}}

	mint.db.SaveKeyset(activeKeyset)
	mint.Keysets = mint.db.GetKeysets()
	mint.Keysets[activeKeyset.Id] = *activeKeyset
	mint.LightningClient = lightning.NewLightningClient()
	mint.MintInfo, err = getMintInfo()
	if err != nil {
		return nil, err
	}

	for i, keyset := range mint.Keysets {
		if keyset.Id != activeKeyset.Id && keyset.Active {
			keyset.Active = false
			mint.db.SaveKeyset(&keyset)
			mint.Keysets[i] = keyset
		}
	}

	return mint, nil
}

// mintPath returns the mint's path
// at $HOME/.gonuts/mint
func mintPath() string {
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

// RequestMintQuote will process a request to mint tokens
// and returns a mint quote response or an error.
// The request to mint a token is explained in
// NUT-04 here: https://github.com/cashubtc/nuts/blob/main/04.md.
func (m *Mint) RequestMintQuote(method string, amount uint64, unit string) (nut04.PostMintQuoteBolt11Response, error) {
	// only support bolt11
	if method != BOLT11_METHOD {
		return nut04.PostMintQuoteBolt11Response{}, cashu.PaymentMethodNotSupportedErr
	}
	// only support sat unit
	if unit != SAT_UNIT {
		return nut04.PostMintQuoteBolt11Response{}, cashu.UnitNotSupportedErr
	}

	// get an invoice from the lightning backend
	invoice, err := m.requestInvoice(amount)
	if err != nil {
		msg := fmt.Sprintf("error generating invoice: %v", err)
		return nut04.PostMintQuoteBolt11Response{}, cashu.BuildCashuError(msg, cashu.InvoiceErrCode)
	}

	err = m.db.SaveInvoice(*invoice)
	if err != nil {
		return nut04.PostMintQuoteBolt11Response{}, cashu.StandardErr
	}

	reqMintQuoteResponse := nut04.PostMintQuoteBolt11Response{
		Quote:   invoice.Id,
		Request: invoice.PaymentRequest,
		State:   nut04.Unpaid,
		Paid:    invoice.Settled, // DEPRECATED: remove after wallets have upgraded
		Expiry:  invoice.Expiry,
	}

	return reqMintQuoteResponse, nil
}

// GetMintQuoteState returns the state of a mint quote.
// Used to check whether a mint quote has been paid.
func (m *Mint) GetMintQuoteState(method, quoteId string) (nut04.PostMintQuoteBolt11Response, error) {
	if method != BOLT11_METHOD {
		return nut04.PostMintQuoteBolt11Response{}, cashu.PaymentMethodNotSupportedErr
	}

	invoice := m.db.GetInvoice(quoteId)
	if invoice == nil {
		return nut04.PostMintQuoteBolt11Response{}, cashu.QuoteNotExistErr
	}

	// check if the invoice has been paid
	status, err := m.LightningClient.InvoiceStatus(invoice.PaymentHash)
	if err != nil {
		msg := fmt.Sprintf("error getting invoice status: %v", err)
		return nut04.PostMintQuoteBolt11Response{}, cashu.BuildCashuError(msg, cashu.InvoiceErrCode)
	}

	state := nut04.Unpaid
	if status.Settled {
		invoice.Settled = status.Settled
		state = nut04.Paid
		if invoice.Redeemed {
			state = nut04.Issued
		}
		m.db.SaveInvoice(*invoice)
	}

	quoteState := nut04.PostMintQuoteBolt11Response{
		Quote:   invoice.Id,
		Request: invoice.PaymentRequest,
		State:   state,
		Paid:    invoice.Settled, // DEPRECATED: remove after wallets have upgraded
		Expiry:  invoice.Expiry,
	}
	return quoteState, nil
}

// MintTokens verifies whether the mint quote with id has been paid and proceeds to
// sign the blindedMessages and return the BlindedSignatures if it was paid.
func (m *Mint) MintTokens(method, id string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	if method != BOLT11_METHOD {
		return nil, cashu.PaymentMethodNotSupportedErr
	}

	invoice := m.db.GetInvoice(id)
	if invoice == nil {
		return nil, cashu.InvoiceNotExistErr
	}

	var blindedSignatures cashu.BlindedSignatures

	status, err := m.LightningClient.InvoiceStatus(invoice.PaymentHash)
	if err != nil {
		msg := fmt.Sprintf("error getting invoice status: %v", err)
		return nil, cashu.BuildCashuError(msg, cashu.InvoiceErrCode)
	}
	if status.Settled {
		if invoice.Redeemed {
			return nil, cashu.InvoiceTokensIssuedErr
		}

		var totalAmount uint64 = 0
		for _, message := range blindedMessages {
			totalAmount += message.Amount
		}

		// verify that amount from invoice is less than the amount
		// from the blinded messages
		if totalAmount > invoice.Amount {
			return nil, cashu.OutputsOverInvoiceErr
		}

		var err error
		blindedSignatures, err = m.signBlindedMessages(blindedMessages)
		if err != nil {
			return nil, err
		}

		// mark invoice as redeemed after signing the blinded messages
		invoice.Settled = true
		invoice.Redeemed = true
		m.db.SaveInvoice(*invoice)
	} else {
		return nil, cashu.InvoiceNotPaidErr
	}

	return blindedSignatures, nil
}

// Swap will process a request to swap tokens.
// A swap requires a set of valid proofs and blinded messages.
// If valid, the mint will sign the blindedMessages and invalidate
// the proofs that were used as input.
// It returns the BlindedSignatures.
func (m *Mint) Swap(proofs cashu.Proofs, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	var blindedMessagesAmount uint64 = 0
	proofsAmount := proofs.Amount()

	for _, msg := range blindedMessages {
		blindedMessagesAmount += msg.Amount
	}

	if proofsAmount < blindedMessagesAmount {
		return nil, cashu.InputsBelowOutputs
	}

	err := m.verifyProofs(proofs)
	if err != nil {
		return nil, err
	}

	// if verification complete, sign blinded messages and invalidate used proofs
	// by adding them to the db
	blindedSignatures, err := m.signBlindedMessages(blindedMessages)
	if err != nil {
		return nil, err
	}

	for _, proof := range proofs {
		m.db.SaveProof(proof)
	}

	return blindedSignatures, nil
}

type MeltQuote struct {
	Id             string
	InvoiceRequest string
	PaymentHash    string
	Amount         uint64
	FeeReserve     uint64
	State          nut05.State
	Paid           bool // DEPRECATED: use state instead
	Expiry         int64
	Preimage       string
}

// MeltRequest will process a request to melt tokens and return a MeltQuote.
// A melt is requested by a wallet to request the mint to pay an invoice.
func (m *Mint) MeltRequest(method, request, unit string) (MeltQuote, error) {
	if method != BOLT11_METHOD {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}
	if unit != SAT_UNIT {
		return MeltQuote{}, cashu.UnitNotSupportedErr
	}

	// check invoice passed is valid
	bolt11, err := decodepay.Decodepay(request)
	if err != nil {
		msg := fmt.Sprintf("invalid invoice: %v", err)
		return MeltQuote{}, cashu.BuildCashuError(msg, cashu.InvoiceErrCode)
	}

	// generate random id for melt quote
	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		return MeltQuote{}, cashu.StandardErr
	}
	hash := sha256.Sum256(randomBytes)

	satAmount := uint64(bolt11.MSatoshi) / 1000
	// Fee reserve that is required by the mint
	fee := m.LightningClient.FeeReserve(satAmount)
	expiry := time.Now().Add(time.Minute * QuoteExpiryMins).Unix()

	meltQuote := MeltQuote{
		Id:             hex.EncodeToString(hash[:]),
		InvoiceRequest: request,
		PaymentHash:    bolt11.PaymentHash,
		Amount:         satAmount,
		FeeReserve:     fee,
		State:          nut05.Unpaid,
		Paid:           false,
		Expiry:         expiry,
	}
	m.db.SaveMeltQuote(meltQuote)

	return meltQuote, nil
}

// GetMeltQuoteState returns the state of a melt quote.
// Used to check whether a melt quote has been paid.
func (m *Mint) GetMeltQuoteState(method, quoteId string) (MeltQuote, error) {
	if method != BOLT11_METHOD {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote := m.db.GetMeltQuote(quoteId)
	if meltQuote == nil {
		return MeltQuote{}, cashu.QuoteNotExistErr
	}

	return *meltQuote, nil
}

// MeltTokens verifies whether proofs provided are valid
// and proceeds to attempt payment.
func (m *Mint) MeltTokens(method, quoteId string, proofs cashu.Proofs) (MeltQuote, error) {
	if method != BOLT11_METHOD {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote := m.db.GetMeltQuote(quoteId)
	if meltQuote == nil {
		return MeltQuote{}, cashu.QuoteNotExistErr
	}
	if meltQuote.State == nut05.Paid {
		return MeltQuote{}, cashu.QuoteAlreadyPaid
	}

	proofsAmount := proofs.Amount()

	// checks if amount in proofs is enough
	if proofsAmount < meltQuote.Amount+meltQuote.FeeReserve {
		return MeltQuote{}, cashu.InsufficientProofsAmount
	}

	err := m.verifyProofs(proofs)
	if err != nil {
		return MeltQuote{}, err
	}

	// if proofs are valid, ask the lightning backend
	// to make the payment
	preimage, err := m.LightningClient.SendPayment(meltQuote.InvoiceRequest, meltQuote.Amount)
	if err != nil {
		return MeltQuote{}, cashu.BuildCashuError(err.Error(), cashu.InvoiceErrCode)
	}

	// if payment succeeded, mark melt quote as paid
	// and invalidate proofs
	meltQuote.State = nut05.Paid
	// Deprecate Paid field in favor of State
	meltQuote.Paid = true
	meltQuote.Preimage = preimage
	m.db.SaveMeltQuote(*meltQuote)
	for _, proof := range proofs {
		m.db.SaveProof(proof)
	}

	return *meltQuote, nil
}

func (m *Mint) verifyProofs(proofs cashu.Proofs) error {
	for _, proof := range proofs {
		// if proof is already in db, it means it was already used
		dbProof := m.db.GetProof(proof.Secret)
		if dbProof != nil {
			return cashu.ProofAlreadyUsedErr
		}

		// check that id in the proof matches id of any
		// of the mint's keyset
		var k *secp256k1.PrivateKey
		if keyset, ok := m.Keysets[proof.Id]; !ok {
			return cashu.InvalidKeysetProof
		} else {
			if key, ok := keyset.Keys[proof.Amount]; ok {
				k = key.PrivateKey
			} else {
				return cashu.InvalidProofErr
			}
		}

		Cbytes, err := hex.DecodeString(proof.C)
		if err != nil {
			return cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}

		C, err := secp256k1.ParsePubKey(Cbytes)
		if err != nil {
			return cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}

		if !crypto.Verify(proof.Secret, k, C) {
			return cashu.InvalidProofErr
		}
	}
	return nil
}

// signBlindedMessages will sign the blindedMessages and
// return the blindedSignatures
func (m *Mint) signBlindedMessages(blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	blindedSignatures := make(cashu.BlindedSignatures, len(blindedMessages))

	for i, msg := range blindedMessages {
		var k *secp256k1.PrivateKey
		keyset, ok := m.ActiveKeysets[msg.Id]
		if !ok {
			return nil, cashu.InvalidSignatureRequest
		} else {
			if key, ok := keyset.Keys[msg.Amount]; ok {
				k = key.PrivateKey
			} else {
				return nil, cashu.InvalidBlindedMessageAmount
			}
		}

		B_bytes, err := hex.DecodeString(msg.B_)
		if err != nil {
			return nil, cashu.StandardErr
		}
		B_, err := btcec.ParsePubKey(B_bytes)
		if err != nil {
			return nil, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}

		C_ := crypto.SignBlindedMessage(B_, k)
		C_hex := hex.EncodeToString(C_.SerializeCompressed())

		blindedSignature := cashu.BlindedSignature{Amount: msg.Amount,
			C_: C_hex, Id: keyset.Id}

		blindedSignatures[i] = blindedSignature
	}

	return blindedSignatures, nil
}

// requestInvoices requests an invoice from the Lightning backend
// for the given amount
func (m *Mint) requestInvoice(amount uint64) (*lightning.Invoice, error) {
	invoice, err := m.LightningClient.CreateInvoice(amount)
	if err != nil {
		return nil, err
	}

	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(randomBytes)
	invoice.Id = hex.EncodeToString(hash[:])

	return &invoice, nil
}
