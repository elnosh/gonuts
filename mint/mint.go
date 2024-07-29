package mint

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut06"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/mint/storage"
	"github.com/elnosh/gonuts/mint/storage/sqlite"
	decodepay "github.com/nbd-wtf/ln-decodepay"
)

const (
	QuoteExpiryMins = 10
	BOLT11_METHOD   = "bolt11"
	SAT_UNIT        = "sat"
)

type Mint struct {
	db storage.MintDB

	// active keysets
	ActiveKeysets map[string]crypto.MintKeyset

	// map of all keysets (both active and inactive)
	Keysets map[string]crypto.MintKeyset

	LightningClient lightning.Client
	MintInfo        *nut06.MintInfo
}

func LoadMint(config Config) (*Mint, error) {
	path := config.DBPath
	if len(path) == 0 {
		path = mintPath()
	}

	db, err := sqlite.InitSQLite(path, config.DBMigrationPath)
	if err != nil {
		log.Fatalf("error starting mint: %v", err)
	}

	seed, err := db.GetSeed()
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// generate new seed
			for {
				seed, err = hdkeychain.GenerateSeed(32)
				if err == nil {
					err = db.SaveSeed(seed)
					if err != nil {
						return nil, err
					}
					break
				}
			}
		} else {
			return nil, err
		}
	}

	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, err
	}

	activeKeyset, err := crypto.GenerateKeyset(master, config.DerivationPathIdx, config.InputFeePpk)
	if err != nil {
		return nil, err
	}
	mint := &Mint{db: db, ActiveKeysets: map[string]crypto.MintKeyset{activeKeyset.Id: *activeKeyset}}

	dbKeysets, err := mint.db.GetKeysets()
	if err != nil {
		return nil, fmt.Errorf("error reading keysets from db: %v", err)
	}

	activeKeysetNew := true
	mintKeysets := make(map[string]crypto.MintKeyset)
	for _, dbkeyset := range dbKeysets {
		seed, err := hex.DecodeString(dbkeyset.Seed)
		if err != nil {
			return nil, err
		}

		master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
		if err != nil {
			return nil, err
		}

		if dbkeyset.Id == activeKeyset.Id {
			activeKeysetNew = false
		}
		keyset, err := crypto.GenerateKeyset(master, dbkeyset.DerivationPathIdx, dbkeyset.InputFeePpk)
		if err != nil {
			return nil, err
		}
		mintKeysets[keyset.Id] = *keyset
	}

	// save active keyset if new
	if activeKeysetNew {
		hexseed := hex.EncodeToString(seed)
		activeDbKeyset := storage.DBKeyset{
			Id:                activeKeyset.Id,
			Unit:              activeKeyset.Unit,
			Active:            true,
			Seed:              hexseed,
			DerivationPathIdx: activeKeyset.DerivationPathIdx,
			InputFeePpk:       activeKeyset.InputFeePpk,
		}
		err := mint.db.SaveKeyset(activeDbKeyset)
		if err != nil {
			return nil, fmt.Errorf("error saving new active keyset: %v", err)
		}
	}

	mint.Keysets = mintKeysets
	mint.Keysets[activeKeyset.Id] = *activeKeyset
	mint.LightningClient = lightning.NewLightningClient()
	mint.MintInfo, err = mint.getMintInfo()
	if err != nil {
		return nil, err
	}

	for _, keyset := range mint.Keysets {
		if keyset.Id != activeKeyset.Id && keyset.Active {
			keyset.Active = false
			mint.db.UpdateKeysetActive(keyset.Id, false)
			mint.Keysets[keyset.Id] = keyset
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
// and returns a mint quote or an error.
// The request to mint a token is explained in
// NUT-04 here: https://github.com/cashubtc/nuts/blob/main/04.md.
func (m *Mint) RequestMintQuote(method string, amount uint64, unit string) (storage.MintQuote, error) {
	// only support bolt11
	if method != BOLT11_METHOD {
		return storage.MintQuote{}, cashu.PaymentMethodNotSupportedErr
	}
	// only support sat unit
	if unit != SAT_UNIT {
		return storage.MintQuote{}, cashu.UnitNotSupportedErr
	}

	// get an invoice from the lightning backend
	invoice, err := m.requestInvoice(amount)
	if err != nil {
		msg := fmt.Sprintf("error generating payment request: %v", err)
		return storage.MintQuote{}, cashu.BuildCashuError(msg, cashu.InvoiceErrCode)
	}

	quoteId, err := cashu.GenerateRandomQuoteId()
	if err != nil {
		return storage.MintQuote{}, err
	}
	mintQuote := storage.MintQuote{
		Id:             quoteId,
		Amount:         amount,
		PaymentRequest: invoice.PaymentRequest,
		PaymentHash:    invoice.PaymentHash,
		State:          nut04.Unpaid,
		Expiry:         invoice.Expiry,
	}

	err = m.db.SaveMintQuote(mintQuote)
	if err != nil {
		msg := fmt.Sprintf("error saving mint quote to db: %v", err)
		return storage.MintQuote{}, cashu.BuildCashuError(msg, cashu.DBErrorCode)
	}

	return mintQuote, nil
}

// GetMintQuoteState returns the state of a mint quote.
// Used to check whether a mint quote has been paid.
func (m *Mint) GetMintQuoteState(method, quoteId string) (storage.MintQuote, error) {
	if method != BOLT11_METHOD {
		return storage.MintQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	mintQuote, err := m.db.GetMintQuote(quoteId)
	if err != nil {
		return storage.MintQuote{}, cashu.QuoteNotExistErr
	}

	// check if the invoice has been paid
	status, err := m.LightningClient.InvoiceStatus(mintQuote.PaymentHash)
	if err != nil {
		msg := fmt.Sprintf("error getting status of payment request: %v", err)
		return storage.MintQuote{}, cashu.BuildCashuError(msg, cashu.InvoiceErrCode)
	}

	if status.Settled && mintQuote.State == nut04.Unpaid {
		mintQuote.State = nut04.Paid
		err := m.db.UpdateMintQuoteState(mintQuote.Id, mintQuote.State)
		if err != nil {
			msg := fmt.Sprintf("error getting quote state: %v", err)
			return storage.MintQuote{}, cashu.BuildCashuError(msg, cashu.DBErrorCode)
		}
	}

	return *mintQuote, nil
}

// MintTokens verifies whether the mint quote with id has been paid and proceeds to
// sign the blindedMessages and return the BlindedSignatures if it was paid.
func (m *Mint) MintTokens(method, id string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	if method != BOLT11_METHOD {
		return nil, cashu.PaymentMethodNotSupportedErr
	}

	mintQuote, err := m.db.GetMintQuote(id)
	if err != nil {
		return nil, cashu.QuoteNotExistErr
	}
	var blindedSignatures cashu.BlindedSignatures

	status, err := m.LightningClient.InvoiceStatus(mintQuote.PaymentHash)
	if err != nil {
		msg := fmt.Sprintf("error getting status of payment request: %v", err)
		return nil, cashu.BuildCashuError(msg, cashu.InvoiceErrCode)
	}
	if status.Settled {
		if mintQuote.State == nut04.Issued {
			return nil, cashu.InvoiceTokensIssuedErr
		}

		blindedMessagesAmount := blindedMessages.Amount()
		// check overflow
		if len(blindedMessages) > 0 {
			for _, msg := range blindedMessages {
				if blindedMessagesAmount < msg.Amount {
					return nil, cashu.InvalidBlindedMessageAmount
				}
			}
		}

		// verify that amount from blinded messages is less
		// than quote amount
		if blindedMessagesAmount > mintQuote.Amount {
			return nil, cashu.OutputsOverInvoiceErr
		}

		var err error
		blindedSignatures, err = m.signBlindedMessages(blindedMessages)
		if err != nil {
			return nil, err
		}

		// mark quote as issued after signing the blinded messages
		err = m.db.UpdateMintQuoteState(mintQuote.Id, nut04.Issued)
		if err != nil {
			msg := fmt.Sprintf("error getting quote state: %v", err)
			return nil, cashu.BuildCashuError(msg, cashu.DBErrorCode)
		}
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
	proofsLen := len(proofs)
	if proofsLen == 0 {
		return nil, cashu.NoProofsProvided
	}

	var proofsAmount uint64
	Ys := make([]string, proofsLen)
	for i, proof := range proofs {
		proofsAmount += proof.Amount

		Y, err := crypto.HashToCurve([]byte(proof.Secret))
		if err != nil {
			return nil, cashu.InvalidProofErr
		}
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		Ys[i] = Yhex
	}

	blindedMessagesAmount := blindedMessages.Amount()
	// check overflow
	if len(blindedMessages) > 0 {
		for _, msg := range blindedMessages {
			if blindedMessagesAmount < msg.Amount {
				return nil, cashu.InvalidBlindedMessageAmount
			}
		}
	}
	fees := m.TransactionFees(proofs)
	if proofsAmount-uint64(fees) < blindedMessagesAmount {
		return nil, cashu.InsufficientProofsAmount
	}

	// check if proofs were alredy used
	usedProofs, err := m.db.GetProofsUsed(Ys)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			msg := fmt.Sprintf("could not get used proofs from db: %v", err)
			return nil, cashu.BuildCashuError(msg, cashu.DBErrorCode)
		}
	}
	if len(usedProofs) != 0 {
		return nil, cashu.ProofAlreadyUsedErr
	}

	err = m.verifyProofs(proofs)
	if err != nil {
		return nil, err
	}

	// if verification complete, sign blinded messages
	blindedSignatures, err := m.signBlindedMessages(blindedMessages)
	if err != nil {
		return nil, err
	}

	// invalidate proofs after signing blinded messages
	err = m.db.SaveProofs(proofs)
	if err != nil {
		msg := fmt.Sprintf("error invalidating proofs. Could not save proofs to db: %v", err)
		return nil, cashu.BuildCashuError(msg, cashu.DBErrorCode)
	}

	return blindedSignatures, nil
}

// MeltRequest will process a request to melt tokens and return a MeltQuote.
// A melt is requested by a wallet to request the mint to pay an invoice.
func (m *Mint) MeltRequest(method, request, unit string) (storage.MeltQuote, error) {
	if method != BOLT11_METHOD {
		return storage.MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}
	if unit != SAT_UNIT {
		return storage.MeltQuote{}, cashu.UnitNotSupportedErr
	}

	// check invoice passed is valid
	bolt11, err := decodepay.Decodepay(request)
	if err != nil {
		msg := fmt.Sprintf("invalid invoice: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.InvoiceErrCode)
	}

	quoteId, err := cashu.GenerateRandomQuoteId()
	if err != nil {
		return storage.MeltQuote{}, cashu.StandardErr
	}

	satAmount := uint64(bolt11.MSatoshi) / 1000
	// Fee reserve that is required by the mint
	fee := m.LightningClient.FeeReserve(satAmount)
	expiry := uint64(time.Now().Add(time.Minute * QuoteExpiryMins).Unix())

	meltQuote := storage.MeltQuote{
		Id:             quoteId,
		InvoiceRequest: request,
		PaymentHash:    bolt11.PaymentHash,
		Amount:         satAmount,
		FeeReserve:     fee,
		State:          nut05.Unpaid,
		Expiry:         expiry,
	}
	if err := m.db.SaveMeltQuote(meltQuote); err != nil {
		msg := fmt.Sprintf("error saving melt quote to db: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrorCode)
	}

	return meltQuote, nil
}

// GetMeltQuoteState returns the state of a melt quote.
// Used to check whether a melt quote has been paid.
func (m *Mint) GetMeltQuoteState(method, quoteId string) (storage.MeltQuote, error) {
	if method != BOLT11_METHOD {
		return storage.MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote, err := m.db.GetMeltQuote(quoteId)
	if err != nil {
		return storage.MeltQuote{}, cashu.QuoteNotExistErr
	}

	return *meltQuote, nil
}

// MeltTokens verifies whether proofs provided are valid
// and proceeds to attempt payment.
func (m *Mint) MeltTokens(method, quoteId string, proofs cashu.Proofs) (storage.MeltQuote, error) {
	if method != BOLT11_METHOD {
		return storage.MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote, err := m.db.GetMeltQuote(quoteId)
	if err != nil {
		return storage.MeltQuote{}, cashu.QuoteNotExistErr
	}
	if meltQuote.State == nut05.Paid {
		return storage.MeltQuote{}, cashu.QuoteAlreadyPaid
	}

	err = m.verifyProofs(proofs)
	if err != nil {
		return storage.MeltQuote{}, err
	}

	proofsAmount := proofs.Amount()
	fees := m.TransactionFees(proofs)
	// checks if amount in proofs is enough
	if proofsAmount < meltQuote.Amount+meltQuote.FeeReserve+uint64(fees) {
		return storage.MeltQuote{}, cashu.InsufficientProofsAmount
	}

	// if proofs are valid, ask the lightning backend
	// to make the payment
	preimage, err := m.LightningClient.SendPayment(meltQuote.InvoiceRequest, meltQuote.Amount)
	if err != nil {
		return storage.MeltQuote{}, cashu.BuildCashuError(err.Error(), cashu.InvoiceErrCode)
	}

	// if payment succeeded, mark melt quote as paid
	// and invalidate proofs
	meltQuote.State = nut05.Paid
	meltQuote.Preimage = preimage
	err = m.db.UpdateMeltQuote(meltQuote.Id, meltQuote.Preimage, meltQuote.State)
	if err != nil {
		msg := fmt.Sprintf("error getting quote state: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrorCode)
	}

	err = m.db.SaveProofs(proofs)
	if err != nil {
		msg := fmt.Sprintf("error invalidating proofs. Could not save proofs to db: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrorCode)
	}

	return *meltQuote, nil
}

func (m *Mint) verifyProofs(proofs cashu.Proofs) error {
	if len(proofs) == 0 {
		return cashu.EmptyInputsErr
	}

	// check duplicte proofs
	if cashu.CheckDuplicateProofs(proofs) {
		return cashu.DuplicateProofs
	}

	for _, proof := range proofs {
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
	return &invoice, nil
}

func (m *Mint) TransactionFees(inputs cashu.Proofs) uint {
	var fees uint = 0
	for _, proof := range inputs {
		// note: not checking that proof id is from valid keyset
		// because already doing that in call to verifyProofs
		fees += m.Keysets[proof.Id].InputFeePpk
	}
	return (fees + 999) / 1000
}
