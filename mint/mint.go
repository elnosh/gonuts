package mint

import (
	cashurpc "buf.build/gen/go/cashu/rpc/protocolbuffers/go"
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
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	decodepay "github.com/nbd-wtf/ln-decodepay"
)

const (
	QuoteExpiryMins = 10
)

type Mint struct {
	db *BoltDB

	// map of all keysets (both active and inactive)
	Keysets map[string]crypto.Keyset

	LightningClient lightning.Client
	MintInfo        *cashurpc.InfoResponse
}

func LoadMint(config Config) (*Mint, error) {
	path := mintPath()
	db, err := InitBolt(path)
	if err != nil {
		log.Fatalf("error starting mint: %v", err)
	}

	activeKeyset := crypto.GenerateKeyset(config.PrivateKey, config.DerivationPath)
	mint := &Mint{db: db}

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

func (m *Mint) KeysetList() []string {
	keysetIds := make([]string, len(m.Keysets))

	i := 0
	for k := range m.Keysets {
		keysetIds[i] = k
		i++
	}
	return keysetIds
}

// RequestMintQuote will process a request to mint tokens
// and returns a mint quote response or an error.
// The request to mint a token is explained in
// NUT-04 here: https://github.com/cashubtc/nuts/blob/main/04.md.
func (m *Mint) RequestMintQuote(method string, amount uint64, unit string) (*cashurpc.PostMintQuoteBolt11Response, error) {
	if method != "bolt11" {
		return nil, cashu.PaymentMethodNotSupportedErr
	}
	// only support sat unit
	if unit != "sat" {
		return nil, cashu.UnitNotSupportedErr
	}

	// get an invoice from the lightning backend
	invoice, err := m.requestInvoice(amount)
	if err != nil {
		return nil, err
	}

	err = m.db.SaveInvoice(*invoice)
	if err != nil {
		return nil, err
	}

	reqMintQuoteResponse := cashurpc.PostMintQuoteBolt11Response{
		Quote:   invoice.Id,
		Request: invoice.PaymentRequest,
		Paid:    invoice.Settled,
	}

	return &reqMintQuoteResponse, nil
}

// GetMintQuoteState returns the state of a mint quote.
// Used to check whether a mint quote has been paid.
func (m *Mint) GetMintQuoteState(method, quoteId string) (*cashurpc.PostMintQuoteBolt11Response, error) {
	if method != "bolt11" {
		return nil, cashu.PaymentMethodNotSupportedErr
	}

	invoice := m.db.GetInvoice(quoteId)
	if invoice == nil {
		return nil, cashu.InvoiceNotExistErr
	}

	// check if the invoice has been paid
	settled, _ := m.LightningClient.InvoiceSettled(invoice.PaymentHash)
	if settled != invoice.Settled {
		invoice.Settled = settled
		m.db.SaveInvoice(*invoice)
	}

	quoteState := &cashurpc.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: settled, Expiry: invoice.Expiry}
	return quoteState, nil
}

// MintTokens verifies whether the mint quote with id has been paid and proceeds to
// sign the blindedMessages and return the BlindedSignatures if it was paid.
func (m *Mint) MintTokens(method, id string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	if method != "bolt11" {
		return nil, cashu.PaymentMethodNotSupportedErr
	}

	invoice := m.db.GetInvoice(id)
	if invoice == nil {
		return nil, cashu.InvoiceNotExistErr
	}

	var blindedSignatures cashu.BlindedSignatures

	settled, _ := m.LightningClient.InvoiceSettled(invoice.PaymentHash)
	if settled {
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
			return nil, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
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
func (m *Mint) Swap(proofs []*cashurpc.Proof, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	var blindedMessagesAmount uint64 = 0
	proofsAmount := cashu.Amount(proofs)

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

	// if verification complete, sign blinded messages and invalidate used proofs
	// by adding them to the db
	blindedSignatures, err := m.signBlindedMessages(blindedMessages)
	if err != nil {
		return nil, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
	}

	for _, proof := range proofs {
		m.db.SaveProof(proof)
	}

	return blindedSignatures, nil
}

type MeltQuote struct {
	Id             string
	InvoiceRequest string
	*cashurpc.PostMeltQuoteBolt11Response
}

// MeltRequest will process a request to melt tokens and return a MeltQuote.
// A melt is requested by a wallet to request the mint to pay an invoice.
func (m *Mint) MeltRequest(method, request, unit string) (MeltQuote, error) {
	if method != "bolt11" {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}
	if unit != "sat" {
		return MeltQuote{}, cashu.UnitNotSupportedErr
	}

	// check invoice passed is valid
	_, err := decodepay.Decodepay(request)
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

	// Fee reserved that is required by the mint
	amount, fee, err := m.LightningClient.FeeReserve(request)
	if err != nil {
		msg := fmt.Sprintf("melt request error: %v", err)
		return MeltQuote{}, cashu.BuildCashuError(msg, cashu.StandardErrCode)
	}
	expiry := time.Now().Add(time.Minute * QuoteExpiryMins).Unix()

	meltQuote := MeltQuote{
		Id: hex.EncodeToString(hash[:]),
		PostMeltQuoteBolt11Response: &cashurpc.PostMeltQuoteBolt11Response{
			Amount:     amount,
			FeeReserve: fee,
			Paid:       false,
			Expiry:     expiry,
		},
		InvoiceRequest: request,
	}
	m.db.SaveMeltQuote(meltQuote)

	return meltQuote, nil
}

// GetMeltQuoteState returns the state of a melt quote.
// Used to check whether a melt quote has been paid.
func (m *Mint) GetMeltQuoteState(method, quoteId string) (MeltQuote, error) {
	if method != "bolt11" {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote := m.db.GetMeltQuote(quoteId)
	if meltQuote == nil {
		return MeltQuote{}, cashu.MeltQuoteNotExistErr
	}

	return *meltQuote, nil
}

// MeltTokens verifies whether proofs provided are valid
// and proceeds to attempt payment.
func (m *Mint) MeltTokens(method, quoteId string, proofs []*cashurpc.Proof) (*cashurpc.PostMeltBolt11Response, error) {
	if method != "bolt11" {
		return nil, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote := m.db.GetMeltQuote(quoteId)
	if meltQuote == nil {
		return nil, cashu.MeltQuoteNotExistErr
	}

	valid, err := m.VerifyProofs(proofs)
	if err != nil || !valid {
		return nil, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
	}

	inputsAmount := cashu.Amount(proofs)

	if inputsAmount < meltQuote.Amount+meltQuote.FeeReserve {
		return nil, cashu.InsufficientProofsAmount
	}

	// if proofs are valid, ask the lightning backend
	// to make the payment
	preimage, err := m.LightningClient.SendPayment(meltQuote.InvoiceRequest)
	if err != nil {
		return nil, err
	}

	// if payment succeeded, mark melt quote as paid
	// and invalidate proofs
	meltQuote.Paid = true

	for _, proof := range proofs {
		m.db.SaveProof(proof)
	}

	return &cashurpc.PostMeltBolt11Response{
		PaymentPreimage: preimage,
		Paid:            meltQuote.Paid,
	}, nil
}

func (m *Mint) VerifyProofs(proofs []*cashurpc.Proof) (bool, error) {
	for _, proof := range proofs {
		// if proof is already in db, it means it was already used
		dbProof := m.db.GetProof(proof.Secret)
		if dbProof != nil {
			return false, cashu.ProofAlreadyUsedErr
		}

		// check that id in the proof matches id of any
		// of the mint's keyset
		var k *secp256k1.PrivateKey
		if keyset, ok := m.Keysets[proof.Id]; !ok {
			return false, cashu.InvalidKeysetProof
		} else {
			if key, ok := keyset.Keys[proof.Amount]; ok {
				k = key.PrivateKey
			} else {
				return false, cashu.InvalidProofErr
			}
		}

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

// signBlindedMessages will sign the blindedMessages and
// return the blindedSignatures
func (m *Mint) signBlindedMessages(blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	blindedSignatures := make(cashu.BlindedSignatures, len(blindedMessages))

	for i, msg := range blindedMessages {
		var k *secp256k1.PrivateKey
		keyset, ok := m.Keysets[msg.Id]
		if !ok {
			return nil, cashu.InvalidSignatureRequest
		} else if keyset.Active {
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

		blindedSignature := &cashurpc.BlindedSignature{Amount: msg.Amount,
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
