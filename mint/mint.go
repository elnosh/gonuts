package mint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/elnosh/gonuts/cashurpc"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
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
	path := setMintDBPath()
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

func (m *Mint) RequestMintQuote(method string, amount uint64, unit cashurpc.UnitType) (*cashurpc.PostMintQuoteBolt11Response, error) {
	if method != "bolt11" {
		return nil, cashu.PaymentMethodNotSupportedErr
	}
	if unit != cashurpc.UnitType_UNIT_TYPE_SAT {
		return nil, cashu.UnitNotSupportedErr
	}

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

func (m *Mint) GetMintQuoteState(method, quoteId string) (*cashurpc.PostMintQuoteBolt11Response, error) {
	if method != "bolt11" {
		return nil, cashu.PaymentMethodNotSupportedErr
	}

	invoice := m.db.GetInvoice(quoteId)
	if invoice == nil {
		return nil, cashu.InvoiceNotExistErr
	}

	settled := m.LightningClient.InvoiceSettled(invoice.PaymentHash)
	if settled != invoice.Settled {
		invoice.Settled = settled
		m.db.SaveInvoice(*invoice)
	}

	quoteState := &cashurpc.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: settled, Expiry: invoice.Expiry}
	return quoteState, nil
}

// id - quote id to lookup invoice
func (m *Mint) MintTokens(method, id string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	if method != "bolt11" {
		return nil, cashu.PaymentMethodNotSupportedErr
	}

	invoice := m.db.GetInvoice(id)
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
		m.db.SaveInvoice(*invoice)
	} else {
		return nil, cashu.InvoiceNotPaidErr
	}

	return blindedSignatures, nil
}

func (m *Mint) Swap(proofs []*cashurpc.Proof, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
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
		m.db.SaveProof(proof)
	}

	return blindedSignatures, nil
}

type MeltQuote struct {
	Id             string
	InvoiceRequest string
	*cashurpc.PostMeltQuoteBolt11Response
}

func (m *Mint) MeltRequest(method, request string, unit cashurpc.UnitType) (MeltQuote, error) {
	if method != "bolt11" {
		return MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}
	if unit.String() != "sat" {
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

	var inputsAmount uint64 = 0
	for _, input := range proofs {
		inputsAmount += input.Amount
	}

	if inputsAmount < meltQuote.Amount+meltQuote.FeeReserve {
		return nil, cashu.InsufficientProofsAmount
	}

	preimage, err := m.LightningClient.SendPayment(meltQuote.InvoiceRequest)
	if err != nil {
		return nil, err
	}
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
		dbProof := m.db.GetProof(proof.Secret)
		if dbProof != nil {
			return false, cashu.ProofAlreadyUsedErr
		}

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
			log.Fatal(err)
		}
		B_, err := btcec.ParsePubKey(B_bytes)
		if err != nil {
			return nil, err
		}

		C_ := crypto.SignBlindedMessage(B_, k)
		C_hex := hex.EncodeToString(C_.SerializeCompressed())

		blindedSignature := &cashurpc.BlindedSignature{Amount: msg.Amount,
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
