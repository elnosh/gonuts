package mint

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut06"
	"github.com/elnosh/gonuts/cashu/nuts/nut07"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
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
	activeKeysets map[string]crypto.MintKeyset

	// map of all keysets (both active and inactive)
	keysets map[string]crypto.MintKeyset

	lightningClient lightning.Client
	mintInfo        nut06.MintInfo
	limits          MintLimits
}

func LoadMint(config Config) (*Mint, error) {
	path := config.MintPath
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

	mint := &Mint{
		db:            db,
		activeKeysets: map[string]crypto.MintKeyset{activeKeyset.Id: *activeKeyset},
		limits:        config.Limits,
	}

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
	mint.keysets = mintKeysets
	mint.keysets[activeKeyset.Id] = *activeKeyset
	if config.LightningClient == nil {
		return nil, errors.New("invalid lightning client")
	}
	mint.lightningClient = config.LightningClient

	err = mint.SetMintInfo(config.MintInfo)
	if err != nil {
		return nil, fmt.Errorf("error setting mint info: %v", err)
	}

	for _, keyset := range mint.keysets {
		if keyset.Id != activeKeyset.Id && keyset.Active {
			keyset.Active = false
			mint.db.UpdateKeysetActive(keyset.Id, false)
			mint.keysets[keyset.Id] = keyset
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

	// check limits
	if m.limits.MintingSettings.MaxAmount > 0 {
		if amount > m.limits.MintingSettings.MaxAmount {
			return storage.MintQuote{}, cashu.MintAmountExceededErr
		}
	}
	if m.limits.MaxBalance > 0 {
		balance, err := m.db.GetBalance()
		if err != nil {
			return storage.MintQuote{}, err
		}
		if balance+amount > m.limits.MaxBalance {
			return storage.MintQuote{}, cashu.MintingDisabled
		}
	}

	// get an invoice from the lightning backend
	invoice, err := m.requestInvoice(amount)
	if err != nil {
		msg := fmt.Sprintf("error generating payment request: %v", err)
		return storage.MintQuote{}, cashu.BuildCashuError(msg, cashu.LightningBackendErrCode)
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
		return storage.MintQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
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

	// if previously unpaid, check if invoice has been paid
	if mintQuote.State == nut04.Unpaid {
		status, err := m.lightningClient.InvoiceStatus(mintQuote.PaymentHash)
		if err != nil {
			msg := fmt.Sprintf("error getting invoice status: %v", err)
			return storage.MintQuote{}, cashu.BuildCashuError(msg, cashu.LightningBackendErrCode)
		}

		if status.Settled {
			mintQuote.State = nut04.Paid
			err := m.db.UpdateMintQuoteState(mintQuote.Id, mintQuote.State)
			if err != nil {
				msg := fmt.Sprintf("error getting quote state: %v", err)
				return storage.MintQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
			}
		}
	}

	return mintQuote, nil
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

	invoicePaid := false
	if mintQuote.State == nut04.Unpaid {
		invoiceStatus, err := m.lightningClient.InvoiceStatus(mintQuote.PaymentHash)
		if err != nil {
			msg := fmt.Sprintf("error getting invoice status: %v", err)
			return nil, cashu.BuildCashuError(msg, cashu.LightningBackendErrCode)
		}
		if invoiceStatus.Settled {
			invoicePaid = true
		}
	} else {
		invoicePaid = true
	}

	if invoicePaid {
		if mintQuote.State == nut04.Issued {
			return nil, cashu.MintQuoteAlreadyIssued
		}

		var blindedMessagesAmount uint64
		B_s := make([]string, len(blindedMessages))
		for i, bm := range blindedMessages {
			blindedMessagesAmount += bm.Amount
			B_s[i] = bm.B_
		}

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
			return nil, cashu.OutputsOverQuoteAmountErr
		}

		sigs, err := m.db.GetBlindSignatures(B_s)
		if err != nil {
			msg := fmt.Sprintf("could not get signatures from db: %v", err)
			return nil, cashu.BuildCashuError(msg, cashu.DBErrCode)
		}
		if len(sigs) > 0 {
			return nil, cashu.BlindedMessageAlreadySigned
		}

		blindedSignatures, err = m.signBlindedMessages(blindedMessages)
		if err != nil {
			return nil, err
		}

		// mark quote as issued after signing the blinded messages
		err = m.db.UpdateMintQuoteState(mintQuote.Id, nut04.Issued)
		if err != nil {
			msg := fmt.Sprintf("error getting quote state: %v", err)
			return nil, cashu.BuildCashuError(msg, cashu.DBErrCode)
		}
	} else {
		return nil, cashu.MintQuoteRequestNotPaid
	}

	return blindedSignatures, nil
}

// Swap will process a request to swap tokens.
// A swap requires a set of valid proofs and blinded messages.
// If valid, the mint will sign the blindedMessages and invalidate
// the proofs that were used as input.
// It returns the BlindedSignatures.
func (m *Mint) Swap(proofs cashu.Proofs, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	var proofsAmount uint64
	Ys := make([]string, len(proofs))
	for i, proof := range proofs {
		proofsAmount += proof.Amount

		Y, err := crypto.HashToCurve([]byte(proof.Secret))
		if err != nil {
			return nil, cashu.InvalidProofErr
		}
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		Ys[i] = Yhex
	}

	var blindedMessagesAmount uint64
	B_s := make([]string, len(blindedMessages))
	for i, bm := range blindedMessages {
		blindedMessagesAmount += bm.Amount
		B_s[i] = bm.B_
	}

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

	err := m.verifyProofs(proofs, Ys)
	if err != nil {
		return nil, err
	}

	sigs, err := m.db.GetBlindSignatures(B_s)
	if err != nil {
		msg := fmt.Sprintf("could not get signatures from db: %v", err)
		return nil, cashu.BuildCashuError(msg, cashu.DBErrCode)
	}
	if len(sigs) > 0 {
		return nil, cashu.BlindedMessageAlreadySigned
	}

	// if sig all, verify signatures in blinded messages
	if nut11.ProofsSigAll(proofs) {
		if err := verifyP2PKBlindedMessages(proofs, blindedMessages); err != nil {
			return nil, err
		}
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
		return nil, cashu.BuildCashuError(msg, cashu.DBErrCode)
	}

	return blindedSignatures, nil
}

// RequestMeltQuote will process a request to melt tokens and return a MeltQuote.
// A melt is requested by a wallet to request the mint to pay an invoice.
func (m *Mint) RequestMeltQuote(method, request, unit string) (storage.MeltQuote, error) {
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
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.StandardErrCode)
	}
	if bolt11.MSatoshi == 0 {
		return storage.MeltQuote{}, cashu.BuildCashuError("invoice has no amount", cashu.StandardErrCode)
	}
	satAmount := uint64(bolt11.MSatoshi) / 1000

	// check melt limit
	if m.limits.MeltingSettings.MaxAmount > 0 {
		if satAmount > m.limits.MeltingSettings.MaxAmount {
			return storage.MeltQuote{}, cashu.MeltAmountExceededErr
		}
	}

	quoteId, err := cashu.GenerateRandomQuoteId()
	if err != nil {
		return storage.MeltQuote{}, cashu.StandardErr
	}
	// Fee reserve that is required by the mint
	fee := m.lightningClient.FeeReserve(satAmount)

	meltQuote := storage.MeltQuote{
		Id:             quoteId,
		InvoiceRequest: request,
		PaymentHash:    bolt11.PaymentHash,
		Amount:         satAmount,
		FeeReserve:     fee,
		State:          nut05.Unpaid,
		Expiry:         uint64(time.Now().Add(time.Minute * QuoteExpiryMins).Unix()),
	}

	// check if a mint quote exists with the same invoice.
	// if mint quote exists with same invoice, it can be
	// settled internally so set the fee to 0
	mintQuote, err := m.db.GetMintQuoteByPaymentHash(bolt11.PaymentHash)
	if err == nil {
		meltQuote.InvoiceRequest = mintQuote.PaymentRequest
		meltQuote.PaymentHash = mintQuote.PaymentHash
		meltQuote.FeeReserve = 0
	}

	if err := m.db.SaveMeltQuote(meltQuote); err != nil {
		msg := fmt.Sprintf("error saving melt quote to db: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
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

	return meltQuote, nil
}

// MeltTokens verifies whether proofs provided are valid
// and proceeds to attempt payment.
func (m *Mint) MeltTokens(method, quoteId string, proofs cashu.Proofs) (storage.MeltQuote, error) {
	var proofsAmount uint64
	Ys := make([]string, len(proofs))
	for i, proof := range proofs {
		proofsAmount += proof.Amount

		Y, err := crypto.HashToCurve([]byte(proof.Secret))
		if err != nil {
			return storage.MeltQuote{}, cashu.InvalidProofErr
		}
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		Ys[i] = Yhex
	}

	if method != BOLT11_METHOD {
		return storage.MeltQuote{}, cashu.PaymentMethodNotSupportedErr
	}

	meltQuote, err := m.db.GetMeltQuote(quoteId)
	if err != nil {
		return storage.MeltQuote{}, cashu.QuoteNotExistErr
	}
	if meltQuote.State == nut05.Paid {
		return storage.MeltQuote{}, cashu.MeltQuoteAlreadyPaid
	}

	err = m.verifyProofs(proofs, Ys)
	if err != nil {
		return storage.MeltQuote{}, err
	}

	fees := m.TransactionFees(proofs)
	// checks if amount in proofs is enough
	if proofsAmount < meltQuote.Amount+meltQuote.FeeReserve+uint64(fees) {
		return storage.MeltQuote{}, cashu.InsufficientProofsAmount
	}

	if nut11.ProofsSigAll(proofs) {
		return storage.MeltQuote{}, nut11.SigAllOnlySwap
	}

	// set proofs as pending before trying to make payment
	err = m.db.AddPendingProofs(proofs, meltQuote.Id)
	if err != nil {
		msg := fmt.Sprintf("error setting proofs as pending in db: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
	}

	// before asking backend to send payment, check if quotes can be settled
	// internally (i.e mint and melt quotes exist with the same invoice)
	mintQuote, err := m.db.GetMintQuoteByPaymentHash(meltQuote.PaymentHash)
	if err == nil {
		meltQuote, err = m.settleQuotesInternally(mintQuote, meltQuote)
		if err != nil {
			return storage.MeltQuote{}, err
		}
	} else {
		// if quote can't be settled internally, ask backend to make payment
		preimage, err := m.lightningClient.SendPayment(meltQuote.InvoiceRequest, meltQuote.Amount)
		if err != nil {
			return storage.MeltQuote{}, cashu.BuildCashuError(err.Error(), cashu.LightningBackendErrCode)
		}

		// if payment succeeded, mark melt quote as paid
		meltQuote.State = nut05.Paid
		meltQuote.Preimage = preimage
		err = m.db.UpdateMeltQuote(meltQuote.Id, meltQuote.Preimage, meltQuote.State)
		if err != nil {
			msg := fmt.Sprintf("error updating melt quote state: %v", err)
			return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
		}
	}

	// if quote is paid, unset pending proofs and mark them as
	// used by adding them to db
	err = m.db.RemovePendingProofs(Ys)
	if err != nil {
		msg := fmt.Sprintf("error removing proofs from pending: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
	}
	err = m.db.SaveProofs(proofs)
	if err != nil {
		msg := fmt.Sprintf("error invalidating proofs. Could not save proofs to db: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
	}

	return meltQuote, nil
}

// if a pair of mint and melt quotes have the same invoice,
// settle them internally and update in db
func (m *Mint) settleQuotesInternally(
	mintQuote storage.MintQuote,
	meltQuote storage.MeltQuote,
) (storage.MeltQuote, error) {
	// need to get the invoice from the backend first to get the preimage
	invoice, err := m.lightningClient.InvoiceStatus(mintQuote.PaymentHash)
	if err != nil {
		msg := fmt.Sprintf("error getting invoice status from lightning backend: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.LightningBackendErrCode)
	}

	meltQuote.State = nut05.Paid
	meltQuote.Preimage = invoice.Preimage
	err = m.db.UpdateMeltQuote(meltQuote.Id, meltQuote.Preimage, meltQuote.State)
	if err != nil {
		msg := fmt.Sprintf("error updating melt quote state: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
	}

	// mark mint quote request as paid
	mintQuote.State = nut04.Paid
	err = m.db.UpdateMintQuoteState(mintQuote.Id, mintQuote.State)
	if err != nil {
		msg := fmt.Sprintf("error updating mint quote state: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
	}

	return meltQuote, nil
}

func (m *Mint) ProofsStateCheck(Ys []string) ([]nut07.ProofState, error) {
	usedProofs, err := m.db.GetProofsUsed(Ys)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			msg := fmt.Sprintf("could not get used proofs from db: %v", err)
			return nil, cashu.BuildCashuError(msg, cashu.DBErrCode)
		}
	}

	proofStates := make([]nut07.ProofState, len(Ys))
	for i, y := range Ys {
		state := nut07.Unspent

		YSpent := slices.ContainsFunc(usedProofs, func(proof storage.DBProof) bool {
			return proof.Y == y
		})
		if YSpent {
			state = nut07.Spent
		}

		proofStates[i] = nut07.ProofState{Y: y, State: state}
	}

	return proofStates, nil
}

func (m *Mint) RestoreSignatures(blindedMessages cashu.BlindedMessages) (cashu.BlindedMessages, cashu.BlindedSignatures, error) {
	outputs := make(cashu.BlindedMessages, 0, len(blindedMessages))
	signatures := make(cashu.BlindedSignatures, 0, len(blindedMessages))

	for _, bm := range blindedMessages {
		sig, err := m.db.GetBlindSignature(bm.B_)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			msg := fmt.Sprintf("could not get signature from db: %v", err)
			return nil, nil, cashu.BuildCashuError(msg, cashu.DBErrCode)
		}

		outputs = append(outputs, bm)
		signatures = append(signatures, sig)
	}

	return outputs, signatures, nil
}

func (m *Mint) verifyProofs(proofs cashu.Proofs, Ys []string) error {
	if len(proofs) == 0 {
		return cashu.NoProofsProvided
	}

	// check if proofs are either pending or already spent
	pendingProofs, err := m.db.GetPendingProofs(Ys)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			msg := fmt.Sprintf("could not get pending proofs from db: %v", err)
			return cashu.BuildCashuError(msg, cashu.DBErrCode)
		}
	}
	if len(pendingProofs) != 0 {
		return cashu.ProofPendingErr
	}

	usedProofs, err := m.db.GetProofsUsed(Ys)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			msg := fmt.Sprintf("could not get used proofs from db: %v", err)
			return cashu.BuildCashuError(msg, cashu.DBErrCode)
		}
	}
	if len(usedProofs) != 0 {
		return cashu.ProofAlreadyUsedErr
	}

	// check duplicte proofs
	if cashu.CheckDuplicateProofs(proofs) {
		return cashu.DuplicateProofs
	}

	for _, proof := range proofs {
		// check that id in the proof matches id of any
		// of the mint's keyset
		var k *secp256k1.PrivateKey
		if keyset, ok := m.keysets[proof.Id]; !ok {
			return cashu.UnknownKeysetErr
		} else {
			if key, ok := keyset.Keys[proof.Amount]; ok {
				k = key.PrivateKey
			} else {
				return cashu.InvalidProofErr
			}
		}

		// if P2PK locked proof, verify valid witness
		if nut11.IsSecretP2PK(proof) {
			if err := verifyP2PKLockedProof(proof); err != nil {
				return err
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

func verifyP2PKLockedProof(proof cashu.Proof) error {
	p2pkWellKnownSecret, err := nut10.DeserializeSecret(proof.Secret)
	if err != nil {
		return cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
	}

	var p2pkWitness nut11.P2PKWitness
	err = json.Unmarshal([]byte(proof.Witness), &p2pkWitness)
	if err != nil {
		p2pkWitness.Signatures = []string{}
	}

	p2pkTags, err := nut11.ParseP2PKTags(p2pkWellKnownSecret.Tags)
	if err != nil {
		return err
	}

	signaturesRequired := 1
	// if locktime is expired and there is no refund pubkey, treat as anyone can spend
	// if refund pubkey present, check signature
	if p2pkTags.Locktime > 0 && time.Now().Local().Unix() > p2pkTags.Locktime {
		if len(p2pkTags.Refund) == 0 {
			return nil
		} else {
			hash := sha256.Sum256([]byte(proof.Secret))
			if len(p2pkWitness.Signatures) < 1 {
				return nut11.InvalidWitness
			}
			if !nut11.HasValidSignatures(hash[:], p2pkWitness, signaturesRequired, p2pkTags.Refund) {
				return nut11.NotEnoughSignaturesErr
			}
		}
	} else {
		pubkey, err := nut11.ParsePublicKey(p2pkWellKnownSecret.Data)
		if err != nil {
			return err
		}
		keys := []*btcec.PublicKey{pubkey}
		// message to sign
		hash := sha256.Sum256([]byte(proof.Secret))

		if p2pkTags.NSigs > 0 {
			signaturesRequired = p2pkTags.NSigs
			if len(p2pkTags.Pubkeys) == 0 {
				return nut11.EmptyPubkeysErr
			}
			keys = append(keys, p2pkTags.Pubkeys...)
		}

		if len(p2pkWitness.Signatures) < 1 {
			return nut11.InvalidWitness
		}
		if !nut11.HasValidSignatures(hash[:], p2pkWitness, signaturesRequired, keys) {
			return nut11.NotEnoughSignaturesErr
		}
	}
	return nil
}

func verifyP2PKBlindedMessages(proofs cashu.Proofs, blindedMessages cashu.BlindedMessages) error {
	secret, err := nut10.DeserializeSecret(proofs[0].Secret)
	if err != nil {
		return cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
	}
	pubkeys, err := nut11.PublicKeys(secret)
	if err != nil {
		return err
	}

	signaturesRequired := 1
	p2pkTags, err := nut11.ParseP2PKTags(secret.Tags)
	if err != nil {
		return err
	}
	if p2pkTags.NSigs > 0 {
		signaturesRequired = p2pkTags.NSigs
	}

	// Check that the conditions across all proofs are the same
	for _, proof := range proofs {
		secret, err := nut10.DeserializeSecret(proof.Secret)
		if err != nil {
			return cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}
		// all flags need to be SIG_ALL
		if !nut11.IsSigAll(secret) {
			return nut11.AllSigAllFlagsErr
		}

		currentSignaturesRequired := 1
		p2pkTags, err := nut11.ParseP2PKTags(secret.Tags)
		if err != nil {
			return err
		}
		if p2pkTags.NSigs > 0 {
			currentSignaturesRequired = p2pkTags.NSigs
		}

		currentKeys, err := nut11.PublicKeys(secret)
		if err != nil {
			return err
		}

		// list of valid keys should be the same
		// across all proofs
		if !reflect.DeepEqual(pubkeys, currentKeys) {
			return nut11.SigAllKeysMustBeEqualErr
		}

		// all n_sigs must be same
		if signaturesRequired != currentSignaturesRequired {
			return nut11.NSigsMustBeEqualErr
		}
	}

	for _, bm := range blindedMessages {
		B_bytes, err := hex.DecodeString(bm.B_)
		if err != nil {
			return cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}
		hash := sha256.Sum256(B_bytes)

		var witness nut11.P2PKWitness
		err = json.Unmarshal([]byte(bm.Witness), &witness)
		if err != nil || len(witness.Signatures) < 1 {
			return nut11.InvalidWitness
		}

		if !nut11.HasValidSignatures(hash[:], witness, signaturesRequired, pubkeys) {
			return nut11.NotEnoughSignaturesErr
		}
	}

	return nil
}

// signBlindedMessages will sign the blindedMessages and
// return the blindedSignatures
func (m *Mint) signBlindedMessages(blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	blindedSignatures := make(cashu.BlindedSignatures, len(blindedMessages))

	for i, msg := range blindedMessages {
		if _, ok := m.keysets[msg.Id]; !ok {
			return nil, cashu.UnknownKeysetErr
		}
		var k *secp256k1.PrivateKey
		keyset, ok := m.activeKeysets[msg.Id]
		if !ok {
			return nil, cashu.InactiveKeysetSignatureRequest
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

		// DLEQ proof
		e, s := crypto.GenerateDLEQ(k, B_, C_)

		blindedSignature := cashu.BlindedSignature{
			Amount: msg.Amount,
			C_:     C_hex,
			Id:     keyset.Id,
			DLEQ: &cashu.DLEQProof{
				E: hex.EncodeToString(e.Serialize()),
				S: hex.EncodeToString(s.Serialize()),
			},
		}

		blindedSignatures[i] = blindedSignature

		if err := m.db.SaveBlindSignature(msg.B_, blindedSignature); err != nil {
			msg := fmt.Sprintf("error saving signatures: %v", err)
			return nil, cashu.BuildCashuError(msg, cashu.DBErrCode)
		}
	}

	return blindedSignatures, nil
}

// requestInvoice requests an invoice from the Lightning backend
// for the given amount
func (m *Mint) requestInvoice(amount uint64) (*lightning.Invoice, error) {
	invoice, err := m.lightningClient.CreateInvoice(amount)
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
		fees += m.keysets[proof.Id].InputFeePpk
	}
	return (fees + 999) / 1000
}

func (m *Mint) GetActiveKeyset() crypto.MintKeyset {
	var keyset crypto.MintKeyset
	for _, k := range m.activeKeysets {
		keyset = k
		break
	}
	return keyset
}

func (m *Mint) SetMintInfo(mintInfo MintInfo) error {
	nuts := nut06.NutsMap{
		4: nut06.NutSetting{
			Methods: []nut06.MethodSetting{
				{
					Method:    BOLT11_METHOD,
					Unit:      SAT_UNIT,
					MinAmount: m.limits.MintingSettings.MinAmount,
					MaxAmount: m.limits.MintingSettings.MaxAmount,
				},
			},
			Disabled: false,
		},
		5: nut06.NutSetting{
			Methods: []nut06.MethodSetting{
				{
					Method:    BOLT11_METHOD,
					Unit:      SAT_UNIT,
					MinAmount: m.limits.MeltingSettings.MinAmount,
					MaxAmount: m.limits.MeltingSettings.MaxAmount,
				},
			},
			Disabled: false,
		},
		7:  map[string]bool{"supported": true},
		8:  map[string]bool{"supported": false},
		9:  map[string]bool{"supported": true},
		10: map[string]bool{"supported": true},
		11: map[string]bool{"supported": true},
		12: map[string]bool{"supported": true},
	}

	info := nut06.MintInfo{
		Name:            mintInfo.Name,
		Version:         "gonuts/0.2.0",
		Description:     mintInfo.Description,
		LongDescription: mintInfo.LongDescription,
		Contact:         mintInfo.Contact,
		Motd:            mintInfo.Motd,
		Nuts:            nuts,
	}
	m.mintInfo = info
	return nil
}

func (m *Mint) RetrieveMintInfo() (nut06.MintInfo, error) {
	seed, err := m.db.GetSeed()
	if err != nil {
		return nut06.MintInfo{}, err
	}
	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nut06.MintInfo{}, err
	}
	publicKey, err := master.ECPubKey()
	if err != nil {
		return nut06.MintInfo{}, err
	}

	mintingDisabled := false
	mintBalance, err := m.db.GetBalance()
	if err != nil {
		msg := fmt.Sprintf("error getting mint balance: %v", err)
		return nut06.MintInfo{}, cashu.BuildCashuError(msg, cashu.DBErrCode)
	}

	if m.limits.MaxBalance > 0 {
		if mintBalance >= m.limits.MaxBalance {
			mintingDisabled = true
		}
	}
	nut04 := m.mintInfo.Nuts[4].(nut06.NutSetting)
	nut04.Disabled = mintingDisabled
	m.mintInfo.Nuts[4] = nut04
	m.mintInfo.Pubkey = hex.EncodeToString(publicKey.SerializeCompressed())

	return m.mintInfo, nil
}
