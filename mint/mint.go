package mint

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	"github.com/elnosh/gonuts/cashu/nuts/nut14"
	"github.com/elnosh/gonuts/cashu/nuts/nut17"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/mint/pubsub"
	"github.com/elnosh/gonuts/mint/storage"
	"github.com/elnosh/gonuts/mint/storage/sqlite"
	decodepay "github.com/nbd-wtf/ln-decodepay"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	QuoteExpiryMins = 10
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
	logger          *slog.Logger
	mppEnabled      bool

	publisher *pubsub.PubSub
}

func LoadMint(config Config) (*Mint, error) {
	path := config.MintPath
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}

	logger, err := setupLogger(path, config.LogLevel)
	if err != nil {
		return nil, err
	}

	db, err := sqlite.InitSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("error setting up sqlite: %v", err)
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
	logger.Info(fmt.Sprintf("setting active keyset '%v' with fee %v", activeKeyset.Id, activeKeyset.InputFeePpk))

	mint := &Mint{
		db:            db,
		activeKeysets: map[string]crypto.MintKeyset{activeKeyset.Id: *activeKeyset},
		limits:        config.Limits,
		logger:        logger,
		mppEnabled:    config.EnableMPP,
		publisher:     pubsub.NewPubSub(),
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
			mint.db.UpdateKeysetActive(activeKeyset.Id, true)
		}
		keyset, err := crypto.GenerateKeyset(master, dbkeyset.DerivationPathIdx, dbkeyset.InputFeePpk)
		if err != nil {
			return nil, err
		}
		keyset.Active = dbkeyset.Active
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

	if err := config.LightningClient.ConnectionStatus(); err != nil {
		return nil, fmt.Errorf("can't connect to lightning backend: %v", err)
	}
	mint.lightningClient = config.LightningClient
	mint.SetMintInfo(config.MintInfo)

	for _, keyset := range mint.keysets {
		if keyset.Id != activeKeyset.Id && keyset.Active {
			mint.logger.Info(fmt.Sprintf("setting keyset '%v' to inactive", keyset.Id))
			keyset.Active = false
			mint.db.UpdateKeysetActive(keyset.Id, false)
			mint.keysets[keyset.Id] = keyset
		}
	}

	return mint, nil
}

func setupLogger(mintPath string, logLevel LogLevel) (*slog.Logger, error) {
	replacer := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.SourceKey {
			source := a.Value.Any().(*slog.Source)
			source.File = filepath.Base(source.File)
		}
		if a.Key == slog.TimeKey {
			a.Value = slog.StringValue(time.Now().Truncate(time.Second * 2).Format(time.DateTime))
		}
		return a
	}

	logFile, err := os.OpenFile(filepath.Join(mintPath, "mint.log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("error opening log file: %v", err)
	}

	logWriter := io.MultiWriter(os.Stdout, logFile)
	level := slog.LevelInfo
	switch logLevel {
	case Debug:
		level = slog.LevelDebug
	case Disable:
		logWriter = io.Discard
	}

	return slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		AddSource:   true,
		Level:       level,
		ReplaceAttr: replacer,
	})), nil
}

// logInfof formats the strings with args and preserves the source position
// from where this method is called for the log msg. Otherwise all messages would be logged with
// source line of this log method and not the original caller
func (m *Mint) logInfof(format string, args ...any) {
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	r := slog.NewRecord(time.Now(), slog.LevelInfo, fmt.Sprintf(format, args...), pcs[0])
	_ = m.logger.Handler().Handle(context.Background(), r)
}

func (m *Mint) logErrorf(format string, args ...any) {
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	r := slog.NewRecord(time.Now(), slog.LevelError, fmt.Sprintf(format, args...), pcs[0])
	_ = m.logger.Handler().Handle(context.Background(), r)
}

func (m *Mint) logDebugf(format string, args ...any) {
	if !m.logger.Enabled(context.Background(), slog.LevelDebug) {
		return
	}

	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	r := slog.NewRecord(time.Now(), slog.LevelDebug, fmt.Sprintf(format, args...), pcs[0])
	_ = m.logger.Handler().Handle(context.Background(), r)
}

// RequestMintQuote will process a request to mint tokens
// and returns a mint quote or an error.
// The request to mint a token is explained in
// NUT-04 here: https://github.com/cashubtc/nuts/blob/main/04.md.
func (m *Mint) RequestMintQuote(mintQuoteRequest nut04.PostMintQuoteBolt11Request) (storage.MintQuote, error) {
	// only support sat unit
	if mintQuoteRequest.Unit != cashu.Sat.String() {
		errmsg := fmt.Sprintf("unit '%v' not supported", mintQuoteRequest.Unit)
		return storage.MintQuote{}, cashu.BuildCashuError(errmsg, cashu.UnitErrCode)
	}

	// check limits
	requestAmount := mintQuoteRequest.Amount
	if m.limits.MintingSettings.MaxAmount > 0 {
		if requestAmount > m.limits.MintingSettings.MaxAmount {
			return storage.MintQuote{}, cashu.MintAmountExceededErr
		}
	}
	if m.limits.MaxBalance > 0 {
		balance, err := m.db.GetBalance()
		if err != nil {
			errmsg := fmt.Sprintf("could not get mint balance from db: %v", err)
			return storage.MintQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
		}
		if balance+requestAmount > m.limits.MaxBalance {
			return storage.MintQuote{}, cashu.MintingDisabled
		}
	}

	// get an invoice from the lightning backend
	m.logInfof("requesting invoice from lightning backend for %v sats", requestAmount)
	invoice, err := m.requestInvoice(requestAmount)
	if err != nil {
		errmsg := fmt.Sprintf("could not generate invoice: %v", err)
		return storage.MintQuote{}, cashu.BuildCashuError(errmsg, cashu.LightningBackendErrCode)
	}

	quoteId, err := cashu.GenerateRandomQuoteId()
	if err != nil {
		m.logErrorf("error generating random quote id: %v", err)
		return storage.MintQuote{}, cashu.StandardErr
	}
	mintQuote := storage.MintQuote{
		Id:             quoteId,
		Amount:         requestAmount,
		PaymentRequest: invoice.PaymentRequest,
		PaymentHash:    invoice.PaymentHash,
		State:          nut04.Unpaid,
		Expiry:         uint64(time.Now().Add(time.Second * time.Duration(invoice.Expiry)).Unix()),
	}

	err = m.db.SaveMintQuote(mintQuote)
	if err != nil {
		errmsg := fmt.Sprintf("error saving mint quote to db: %v", err)
		return storage.MintQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}

	// goroutine to check in the background when invoice gets paid and update db if so
	go m.checkInvoicePaid(quoteId)

	return mintQuote, nil
}

// GetMintQuoteState returns the state of a mint quote.
func (m *Mint) GetMintQuoteState(quoteId string) (storage.MintQuote, error) {
	mintQuote, err := m.db.GetMintQuote(quoteId)
	if err != nil {
		return storage.MintQuote{}, cashu.QuoteNotExistErr
	}

	// if previously unpaid, check if invoice has been paid
	if mintQuote.State == nut04.Unpaid {
		m.logDebugf("checking status of invoice with hash '%v'", mintQuote.PaymentHash)
		status, err := m.lightningClient.InvoiceStatus(mintQuote.PaymentHash)
		if err != nil {
			errmsg := fmt.Sprintf("error getting invoice status: %v", err)
			return storage.MintQuote{}, cashu.BuildCashuError(errmsg, cashu.LightningBackendErrCode)
		}

		if status.Settled {
			m.logInfof("mint quote '%v' with invoice payment hash '%v' was paid", mintQuote.Id, mintQuote.PaymentHash)
			mintQuote.State = nut04.Paid
			err := m.db.UpdateMintQuoteState(mintQuote.Id, mintQuote.State)
			if err != nil {
				errmsg := fmt.Sprintf("error updating mint quote in db: %v", err)
				return storage.MintQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}

			jsonQuote, _ := json.Marshal(mintQuote)
			m.publisher.Publish(BOLT11_MINT_QUOTE_TOPIC, jsonQuote)
		}
	}

	return mintQuote, nil
}

// MintTokens verifies whether the mint quote with id has been paid and proceeds to
// sign the blindedMessages and return the BlindedSignatures if it was paid.
func (m *Mint) MintTokens(mintTokensRequest nut04.PostMintBolt11Request) (cashu.BlindedSignatures, error) {
	mintQuote, err := m.GetMintQuoteState(mintTokensRequest.Quote)
	if err != nil {
		return nil, err
	}

	var blindedSignatures cashu.BlindedSignatures

	switch mintQuote.State {
	case nut04.Unpaid:
		return nil, cashu.MintQuoteRequestNotPaid
	case nut04.Issued:
		return nil, cashu.MintQuoteAlreadyIssued
	case nut04.Pending:
		return nil, cashu.QuotePending
	case nut04.Paid:
		err := func() error {
			// set quote as pending while validating blinded messages and signing
			err = m.db.UpdateMintQuoteState(mintQuote.Id, nut04.Pending)
			if err != nil {
				errmsg := fmt.Sprintf("error mint quote state: %v", err)
				return cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}

			blindedMessages := mintTokensRequest.Outputs
			var blindedMessagesAmount uint64
			B_s := make([]string, len(blindedMessages))
			for i, bm := range blindedMessages {
				blindedMessagesAmount += bm.Amount
				B_s[i] = bm.B_
			}

			if len(blindedMessages) > 0 {
				for _, msg := range blindedMessages {
					if blindedMessagesAmount < msg.Amount {
						return cashu.InvalidBlindedMessageAmount
					}
				}
			}

			// verify that amount from blinded messages is less
			// than quote amount
			if blindedMessagesAmount > mintQuote.Amount {
				return cashu.OutputsOverQuoteAmountErr
			}

			sigs, err := m.db.GetBlindSignatures(B_s)
			if err != nil {
				errmsg := fmt.Sprintf("error getting blind signatures from db: %v", err)
				return cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}

			if len(sigs) > 0 {
				return cashu.BlindedMessageAlreadySigned
			}

			blindedSignatures, err = m.signBlindedMessages(blindedMessages)
			if err != nil {
				return err
			}

			// mark quote as issued after signing the blinded messages
			mintQuote.State = nut04.Issued
			err = m.db.UpdateMintQuoteState(mintQuote.Id, nut04.Issued)
			if err != nil {
				errmsg := fmt.Sprintf("error mint quote state: %v", err)
				return cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}
			jsonQuote, _ := json.Marshal(mintQuote)
			m.publisher.Publish(BOLT11_MINT_QUOTE_TOPIC, jsonQuote)
			return nil
		}()

		// update mint quote to previous state if there was an error
		if err != nil {
			if err := m.db.UpdateMintQuoteState(mintQuote.Id, mintQuote.State); err != nil {
				return nil, err
			}
			return nil, err
		}
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
		errmsg := fmt.Sprintf("error getting blind signatures from db: %v", err)
		return nil, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}
	if len(sigs) > 0 {
		return nil, cashu.BlindedMessageAlreadySigned
	}

	// if sig all, verify signatures in blinded messages
	if nut11.ProofsSigAll(proofs) {
		m.logDebugf("locked proofs have SIG_ALL flag. Verifying blinded messages")
		if err := verifyBlindedMessages(proofs, blindedMessages); err != nil {
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
		errmsg := fmt.Sprintf("error invalidating proofs. Could not save proofs to db: %v", err)
		return nil, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}
	m.publishProofsStateChanges(proofs, nut07.Spent)

	return blindedSignatures, nil
}

// RequestMeltQuote will process a request to melt tokens and return a MeltQuote.
// A melt is requested by a wallet to request the mint to pay an invoice.
func (m *Mint) RequestMeltQuote(meltQuoteRequest nut05.PostMeltQuoteBolt11Request) (storage.MeltQuote, error) {
	if meltQuoteRequest.Unit != cashu.Sat.String() {
		errmsg := fmt.Sprintf("unit '%v' not supported", meltQuoteRequest.Unit)
		return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.UnitErrCode)
	}

	// check invoice passed is valid
	request := meltQuoteRequest.Request
	bolt11, err := decodepay.Decodepay(request)
	if err != nil {
		errmsg := fmt.Sprintf("invalid invoice: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.MeltQuoteErrCode)
	}
	if bolt11.MSatoshi == 0 {
		return storage.MeltQuote{}, cashu.BuildCashuError("invoice has no amount", cashu.MeltQuoteErrCode)
	}
	invoiceSatAmount := uint64(bolt11.MSatoshi) / 1000
	quoteAmount := invoiceSatAmount

	// check if a mint quote exists with the same invoice.
	_, err = m.db.GetMintQuoteByPaymentHash(bolt11.PaymentHash)
	isInternal := false
	if err == nil {
		isInternal = true
	}

	isMpp := false
	var amountMsat uint64 = 0
	// check mpp option
	if len(meltQuoteRequest.Options) > 0 {
		mpp, ok := meltQuoteRequest.Options["mpp"]
		if ok {
			if m.mppEnabled {
				// if this is an internal invoice, reject MPP request
				if isInternal {
					return storage.MeltQuote{},
						cashu.BuildCashuError("mpp for internal invoice is not allowed", cashu.MeltQuoteErrCode)
				}

				// check mpp msat amount is less than invoice amount
				if mpp.AmountMsat >= uint64(bolt11.MSatoshi) {
					return storage.MeltQuote{},
						cashu.BuildCashuError("mpp amount is not less than amount in invoice",
							cashu.MeltQuoteErrCode)
				}
				isMpp = true
				amountMsat = mpp.AmountMsat
				quoteAmount = amountMsat / 1000
				m.logInfof("got melt quote request to pay partial amount '%v' of invoice with amount '%v'",
					quoteAmount, invoiceSatAmount)
			} else {
				return storage.MeltQuote{},
					cashu.BuildCashuError("MPP is not supported", cashu.MeltQuoteErrCode)
			}
		}
	}

	// check melt limit
	if m.limits.MeltingSettings.MaxAmount > 0 {
		if quoteAmount > m.limits.MeltingSettings.MaxAmount {
			return storage.MeltQuote{}, cashu.MeltAmountExceededErr
		}
	}

	// check if a melt quote for the invoice already exists
	quote, _ := m.db.GetMeltQuoteByPaymentRequest(request)
	if quote != nil {
		return storage.MeltQuote{}, cashu.MeltQuoteForRequestExists
	}

	quoteId, err := cashu.GenerateRandomQuoteId()
	if err != nil {
		m.logErrorf("error generating random quote id: %v", err)
		return storage.MeltQuote{}, cashu.StandardErr
	}
	// Fee reserve that is required by the mint
	fee := m.lightningClient.FeeReserve(quoteAmount)
	// if mint quote exists with same invoice, it can be
	// settled internally so set the fee to 0
	if isInternal {
		m.logDebugf(`in melt quote request found mint quote with same invoice. 
		Setting fee reserve to 0 because quotes can be settled internally.`)
		fee = 0
	}
	meltQuote := storage.MeltQuote{
		Id:             quoteId,
		InvoiceRequest: request,
		PaymentHash:    bolt11.PaymentHash,
		Amount:         quoteAmount,
		FeeReserve:     fee,
		State:          nut05.Unpaid,
		Expiry:         uint64(time.Now().Add(time.Minute * QuoteExpiryMins).Unix()),
		IsMpp:          isMpp,
		AmountMsat:     amountMsat,
	}

	m.logInfof("got melt quote request for invoice of amount '%v'. Setting fee reserve to %v",
		invoiceSatAmount, meltQuote.FeeReserve)

	if err := m.db.SaveMeltQuote(meltQuote); err != nil {
		errmsg := fmt.Sprintf("error saving melt quote to db: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}

	return meltQuote, nil
}

// GetMeltQuoteState returns the state of a melt quote.
// Used to check whether a melt quote has been paid.
func (m *Mint) GetMeltQuoteState(ctx context.Context, quoteId string) (storage.MeltQuote, error) {
	meltQuote, err := m.db.GetMeltQuote(quoteId)
	if err != nil {
		return storage.MeltQuote{}, cashu.QuoteNotExistErr
	}

	// if quote is pending, check with backend if status of payment has changed
	if meltQuote.State == nut05.Pending {
		m.logDebugf("checking status of payment with hash '%v' for melt quote '%v'",
			meltQuote.PaymentHash, meltQuote.Id)

		paymentStatus, err := m.lightningClient.OutgoingPaymentStatus(ctx, meltQuote.PaymentHash)
		if err != nil {
			m.logErrorf(`error checking outgoing payment status: %v. Leaving proofs for quote '%v' as pending`,
				err, meltQuote.Id)
			return meltQuote, nil
		}

		switch paymentStatus.PaymentStatus {
		// settle proofs (remove pending, and add to used)
		// mark quote as paid and set preimage
		case lightning.Succeeded:
			m.logInfof("payment %v succeded. setting melt quote '%v' to paid and invalidating proofs",
				meltQuote.PaymentHash, meltQuote.Id)

			proofs, err := m.removePendingProofsForQuote(meltQuote.Id)
			if err != nil {
				errmsg := fmt.Sprintf("error removing pending proofs for quote: %v", err)
				return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}
			err = m.db.SaveProofs(proofs)
			if err != nil {
				errmsg := fmt.Sprintf("error invalidating proofs. Could not save proofs to db: %v", err)
				return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}

			meltQuote.State = nut05.Paid
			meltQuote.Preimage = paymentStatus.Preimage
			err = m.db.UpdateMeltQuote(meltQuote.Id, paymentStatus.Preimage, nut05.Paid)
			if err != nil {
				errmsg := fmt.Sprintf("error updating melt quote state: %v", err)
				return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}
			m.publishProofsStateChanges(proofs, nut07.Spent)

		case lightning.Failed:
			m.logInfof("payment %v failed with error: %v. Setting melt quote '%v' to unpaid and removing proofs from pending",
				meltQuote.PaymentHash, paymentStatus.PaymentFailureReason, meltQuote.Id)

			meltQuote.State = nut05.Unpaid
			err = m.db.UpdateMeltQuote(meltQuote.Id, "", meltQuote.State)
			if err != nil {
				errmsg := fmt.Sprintf("error updating melt quote state: %v", err)
				return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}
			_, err = m.removePendingProofsForQuote(meltQuote.Id)
			if err != nil {
				errmsg := fmt.Sprintf("error removing pending proofs for quote: %v", err)
				return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}
		}
	}

	return meltQuote, nil
}

func (m *Mint) removePendingProofsForQuote(quoteId string) (cashu.Proofs, error) {
	dbproofs, err := m.db.GetPendingProofsByQuote(quoteId)
	if err != nil {
		return nil, err
	}

	proofs := make(cashu.Proofs, len(dbproofs))
	Ys := make([]string, len(dbproofs))
	for i, dbproof := range dbproofs {
		Ys[i] = dbproof.Y

		proof := cashu.Proof{
			Amount:  dbproof.Amount,
			Id:      dbproof.Id,
			Secret:  dbproof.Secret,
			C:       dbproof.C,
			Witness: dbproof.Witness,
		}
		proofs[i] = proof
	}

	err = m.db.RemovePendingProofs(Ys)
	if err != nil {
		return nil, err
	}

	return proofs, nil
}

// MeltTokens verifies whether proofs provided are valid
// and proceeds to attempt payment.
func (m *Mint) MeltTokens(ctx context.Context, meltTokensRequest nut05.PostMeltBolt11Request) (storage.MeltQuote, error) {
	proofs := meltTokensRequest.Inputs

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

	meltQuote, err := m.db.GetMeltQuote(meltTokensRequest.Quote)
	if err != nil {
		return storage.MeltQuote{}, cashu.QuoteNotExistErr
	}
	if meltQuote.State == nut05.Paid {
		return storage.MeltQuote{}, cashu.MeltQuoteAlreadyPaid
	}
	if meltQuote.State == nut05.Pending {
		return storage.MeltQuote{}, cashu.QuotePending
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

	m.logInfof("verified proofs in melt tokens request. Setting proofs as pending before attempting payment.")
	// set proofs as pending before trying to make payment
	err = m.db.AddPendingProofs(proofs, meltQuote.Id)
	if err != nil {
		errmsg := fmt.Sprintf("error setting proofs as pending in db: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}
	meltQuote.State = nut05.Pending
	err = m.db.UpdateMeltQuote(meltQuote.Id, "", nut05.Pending)
	if err != nil {
		errmsg := fmt.Sprintf("error updating melt quote state: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}

	// before asking backend to send payment, check if quotes can be settled
	// internally (i.e mint and melt quotes exist with the same invoice)
	mintQuote, err := m.db.GetMintQuoteByPaymentHash(meltQuote.PaymentHash)
	if err == nil {
		m.logDebugf("quotes '%v' and '%v' have same invoice so settling them internally", meltQuote.Id, mintQuote.Id)
		meltQuote, err = m.settleQuotesInternally(mintQuote, meltQuote)
		if err != nil {
			return storage.MeltQuote{}, err
		}
		err := m.db.RemovePendingProofs(Ys)
		if err != nil {
			errmsg := fmt.Sprintf("error removing pending proofs: %v", err)
			return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
		}
		err = m.db.SaveProofs(proofs)
		if err != nil {
			errmsg := fmt.Sprintf("error invalidating proofs. Could not save proofs to db: %v", err)
			return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
		}
		m.publishProofsStateChanges(proofs, nut07.Spent)
	} else {
		var sendPaymentResponse lightning.PaymentStatus
		// if melt is MPP, pay partial amount. If not, send full payment
		if meltQuote.IsMpp {
			m.logInfof("attempting MPP payment of amount '%v' for invoice '%v'",
				meltQuote.Amount, meltQuote.InvoiceRequest)
			sendPaymentResponse, err = m.lightningClient.PayPartialAmount(
				ctx,
				meltQuote.InvoiceRequest,
				meltQuote.AmountMsat,
				m.lightningClient.FeeReserve(meltQuote.AmountMsat/1000),
			)
		} else {
			m.logInfof("attempting to pay invoice: %v", meltQuote.InvoiceRequest)
			sendPaymentResponse, err = m.lightningClient.SendPayment(ctx, meltQuote.InvoiceRequest, meltQuote.Amount)
		}
		if err != nil {
			// if SendPayment failed do not return yet, an extra check will be done
			sendPaymentResponse.PaymentStatus = lightning.Failed
			m.logDebugf("Payment failed with error: %v. Will do extra check", err)
		}

		switch sendPaymentResponse.PaymentStatus {
		case lightning.Succeeded:
			m.logInfof("succesfully paid invoice with hash '%v' for melt quote '%v'", meltQuote.PaymentHash, meltQuote.Id)
			// if payment succeeded:
			// - unset pending proofs and mark them as spent by adding them to the db
			// - mark melt quote as paid
			meltQuote.State = nut05.Paid
			meltQuote.Preimage = sendPaymentResponse.Preimage
			err = m.settleProofs(Ys, proofs)
			if err != nil {
				return storage.MeltQuote{}, err
			}
			err = m.db.UpdateMeltQuote(meltQuote.Id, sendPaymentResponse.Preimage, nut05.Paid)
			if err != nil {
				errmsg := fmt.Sprintf("error updating melt quote state: %v", err)
				return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
			}

		case lightning.Pending:
			// if payment is pending, leave quote and proofs as pending and return
			m.logInfof("outgoing payment for quote '%v' is pending.", meltQuote.Id)
			return meltQuote, nil

		case lightning.Failed:
			// if got failed from SendPayment
			// do additional check by calling to get outgoing payment status
			paymentStatus, err := m.lightningClient.OutgoingPaymentStatus(ctx, meltQuote.PaymentHash)
			if status.Code(err) == codes.NotFound {
				m.logInfof("no outgoing payment found with hash: %v. Removing pending proofs and marking quote '%v' as unpaid",
					meltQuote.PaymentHash, meltQuote.Id)

				meltQuote.State = nut05.Unpaid
				err = m.db.UpdateMeltQuote(meltQuote.Id, "", meltQuote.State)
				if err != nil {
					errmsg := fmt.Sprintf("error updating melt quote state: %v", err)
					return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
				}
				err = m.db.RemovePendingProofs(Ys)
				if err != nil {
					errmsg := fmt.Sprintf("error removing proofs from pending: %v", err)
					return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
				}
				return meltQuote, nil
			}
			if err != nil {
				m.logErrorf(`error checking outgoing payment status: %v. Leaving proofs for quote '%v' as pending`, err, meltQuote.Id)
				return meltQuote, nil
			}

			switch paymentStatus.PaymentStatus {
			// only set quote to unpaid and remove pending proofs if OutgoingPaymentStatus
			// returned a nil err (meaning it was actually able to check the status)
			// and payment status was failed
			case lightning.Failed:
				m.logInfof("payment failed with error: %v. Removing pending proofs and marking quote '%v' as unpaid",
					paymentStatus.PaymentFailureReason, meltQuote.Id)

				meltQuote.State = nut05.Unpaid
				err = m.db.UpdateMeltQuote(meltQuote.Id, "", meltQuote.State)
				if err != nil {
					errmsg := fmt.Sprintf("error updating melt quote state: %v", err)
					return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
				}
				err = m.db.RemovePendingProofs(Ys)
				if err != nil {
					errmsg := fmt.Sprintf("error removing proofs from pending: %v", err)
					return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
				}
				return meltQuote, nil
			case lightning.Succeeded:
				m.logInfof("succesfully paid invoice with hash '%v' for melt quote '%v'", meltQuote.PaymentHash, meltQuote.Id)
				err = m.settleProofs(Ys, proofs)
				if err != nil {
					return storage.MeltQuote{}, err
				}
				meltQuote.State = nut05.Paid
				meltQuote.Preimage = paymentStatus.Preimage
				err = m.db.UpdateMeltQuote(meltQuote.Id, paymentStatus.Preimage, nut05.Paid)
				if err != nil {
					errmsg := fmt.Sprintf("error updating melt quote state: %v", err)
					return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
				}
			}
		}
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
		errmsg := fmt.Sprintf("error getting invoice status from lightning backend: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.LightningBackendErrCode)
	}

	meltQuote.State = nut05.Paid
	meltQuote.Preimage = invoice.Preimage
	err = m.db.UpdateMeltQuote(meltQuote.Id, meltQuote.Preimage, meltQuote.State)
	if err != nil {
		errmsg := fmt.Sprintf("error updating melt quote state: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}

	// mark mint quote request as paid
	mintQuote.State = nut04.Paid
	err = m.db.UpdateMintQuoteState(mintQuote.Id, mintQuote.State)
	if err != nil {
		errmsg := fmt.Sprintf("error updating mint quote state: %v", err)
		return storage.MeltQuote{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}
	jsonQuote, _ := json.Marshal(mintQuote)
	m.publisher.Publish(BOLT11_MINT_QUOTE_TOPIC, jsonQuote)

	return meltQuote, nil
}

// settleProofs will remove the proofs from the pending table
// and mark them as spent by adding them to the used proofs table
func (m *Mint) settleProofs(Ys []string, proofs cashu.Proofs) error {
	err := m.db.RemovePendingProofs(Ys)
	if err != nil {
		errmsg := fmt.Sprintf("error removing pending proofs: %v", err)
		return cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}
	err = m.db.SaveProofs(proofs)
	if err != nil {
		errmsg := fmt.Sprintf("error invalidating proofs. Could not save proofs to db: %v", err)
		return cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}
	m.publishProofsStateChanges(proofs, nut07.Spent)

	return nil
}

func (m *Mint) ProofsStateCheck(Ys []string) ([]nut07.ProofState, error) {
	// status of proofs that are pending due to an in-flight lightning payment
	// could have changed so need to check with the lightning backend the status
	// of the payment
	pendingProofs, err := m.db.GetPendingProofs(Ys)
	if err != nil {
		errmsg := fmt.Sprintf("could not get pending proofs from db: %v", err)
		return nil, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}

	pendingQuotes := make(map[string]bool)
	for _, pendingProof := range pendingProofs {
		if !pendingQuotes[pendingProof.MeltQuoteId] {
			pendingQuotes[pendingProof.MeltQuoteId] = true
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	m.logDebugf("checking if status of pending proofs has changed")
	for quoteId := range pendingQuotes {
		// GetMeltQuoteState will check the status of the quote
		// and update the db tables (pending proofs, used proofs) appropriately
		// if the status has changed
		_, err := m.GetMeltQuoteState(ctx, quoteId)
		if err != nil {
			return nil, err
		}
	}

	// get pending proofs from db since they could have changed
	// from checking the quote state
	pendingProofs, err = m.db.GetPendingProofs(Ys)
	if err != nil {
		errmsg := fmt.Sprintf("could not get pending proofs from db: %v", err)
		return nil, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}

	usedProofs, err := m.db.GetProofsUsed(Ys)
	if err != nil {
		errmsg := fmt.Sprintf("could not get used proofs from db: %v", err)
		return nil, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}

	proofStates := make([]nut07.ProofState, len(Ys))
	for i, y := range Ys {
		state := nut07.Unspent

		YSpentIdx := slices.IndexFunc(usedProofs, func(proof storage.DBProof) bool {
			return proof.Y == y
		})
		YPendingIdx := slices.IndexFunc(pendingProofs, func(proof storage.DBProof) bool {
			return proof.Y == y
		})

		var witness string
		if YSpentIdx >= 0 {
			state = nut07.Spent
			witness = usedProofs[YSpentIdx].Witness
		} else if YPendingIdx >= 0 {
			state = nut07.Pending
			witness = pendingProofs[YPendingIdx].Witness
		}

		proofStates[i] = nut07.ProofState{Y: y, State: state, Witness: witness}
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
			errmsg := fmt.Sprintf("could not get signature from db: %v", err)
			return nil, nil, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
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
			errmsg := fmt.Sprintf("could not get pending proofs from db: %v", err)
			return cashu.BuildCashuError(errmsg, cashu.DBErrCode)
		}
	}
	if len(pendingProofs) != 0 {
		return cashu.ProofPendingErr
	}

	usedProofs, err := m.db.GetProofsUsed(Ys)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			errmsg := fmt.Sprintf("could not get used proofs from db: %v", err)
			return cashu.BuildCashuError(errmsg, cashu.DBErrCode)
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
		nut10Secret, err := nut10.DeserializeSecret(proof.Secret)
		if err == nil {
			if nut10Secret.Kind == nut10.P2PK {
				if err := verifyP2PKLockedProof(proof, nut10Secret); err != nil {
					return err
				}
				m.logDebugf("verified P2PK locked proof")
			} else if nut10Secret.Kind == nut10.HTLC {
				if err := verifyHTLCProof(proof, nut10Secret); err != nil {
					return err
				}
				m.logDebugf("verified HTLC proof")
			}
		}

		Cbytes, err := hex.DecodeString(proof.C)
		if err != nil {
			errmsg := fmt.Sprintf("invalid C: %v", err)
			return cashu.BuildCashuError(errmsg, cashu.StandardErrCode)
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

func verifyP2PKLockedProof(proof cashu.Proof, proofSecret nut10.WellKnownSecret) error {
	var p2pkWitness nut11.P2PKWitness
	json.Unmarshal([]byte(proof.Witness), &p2pkWitness)

	p2pkTags, err := nut11.ParseP2PKTags(proofSecret.Data.Tags)
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
			if !nut11.HasValidSignatures(hash[:], p2pkWitness.Signatures, signaturesRequired, p2pkTags.Refund) {
				return nut11.NotEnoughSignaturesErr
			}
		}
	} else {
		pubkey, err := nut11.ParsePublicKey(proofSecret.Data.Data)
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

		if nut11.DuplicateSignatures(p2pkWitness.Signatures) {
			return nut11.DuplicateSignaturesErr
		}

		if !nut11.HasValidSignatures(hash[:], p2pkWitness.Signatures, signaturesRequired, keys) {
			return nut11.NotEnoughSignaturesErr
		}
	}
	return nil
}

func verifyHTLCProof(proof cashu.Proof, proofSecret nut10.WellKnownSecret) error {
	var htlcWitness nut14.HTLCWitness
	json.Unmarshal([]byte(proof.Witness), &htlcWitness)

	p2pkTags, err := nut11.ParseP2PKTags(proofSecret.Data.Tags)
	if err != nil {
		return err
	}

	// if locktime is expired and there is no refund pubkey, treat as anyone can spend
	// if refund pubkey present, check signature
	if p2pkTags.Locktime > 0 && time.Now().Local().Unix() > p2pkTags.Locktime {
		if len(p2pkTags.Refund) == 0 {
			return nil
		} else {
			hash := sha256.Sum256([]byte(proof.Secret))
			if len(htlcWitness.Signatures) < 1 {
				return nut11.InvalidWitness
			}
			if !nut11.HasValidSignatures(hash[:], htlcWitness.Signatures, 1, p2pkTags.Refund) {
				return nut11.NotEnoughSignaturesErr
			}
		}
		return nil
	}

	// verify valid preimage
	preimageBytes, err := hex.DecodeString(htlcWitness.Preimage)
	if err != nil {
		return nut14.InvalidPreimageErr
	}
	hashBytes := sha256.Sum256(preimageBytes)
	hash := hex.EncodeToString(hashBytes[:])

	if len(proofSecret.Data.Data) != 64 {
		return nut14.InvalidHashErr
	}
	if hash != proofSecret.Data.Data {
		return nut14.InvalidPreimageErr
	}

	// if n_sigs flag present, verify signatures
	if p2pkTags.NSigs > 0 {
		if len(htlcWitness.Signatures) < 1 {
			return nut11.NoSignaturesErr
		}

		hash := sha256.Sum256([]byte(proof.Secret))

		if nut11.DuplicateSignatures(htlcWitness.Signatures) {
			return nut11.DuplicateSignaturesErr
		}

		if !nut11.HasValidSignatures(hash[:], htlcWitness.Signatures, p2pkTags.NSigs, p2pkTags.Pubkeys) {
			return nut11.NotEnoughSignaturesErr
		}
	}

	return nil
}

// verifyBlindedMessages used to verify blinded messages are signed when SIG_ALL flag
// is present in either a P2PK or HTLC locked proofs
func verifyBlindedMessages(proofs cashu.Proofs, blindedMessages cashu.BlindedMessages) error {
	secret, err := nut10.DeserializeSecret(proofs[0].Secret)
	if err != nil {
		return cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
	}

	// pubkeys will hold list of public keys that can sign
	pubkeys, err := nut11.PublicKeys(secret)
	if err != nil {
		return err
	}

	signaturesRequired := 1
	p2pkTags, err := nut11.ParseP2PKTags(secret.Data.Tags)
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
		p2pkTags, err := nut11.ParseP2PKTags(secret.Data.Tags)
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

		var signatures []string
		switch secret.Kind {
		case nut10.P2PK:
			var witness nut11.P2PKWitness
			if err := json.Unmarshal([]byte(bm.Witness), &witness); err != nil {
				return nut11.InvalidWitness
			}
			signatures = witness.Signatures
		case nut10.HTLC:
			var witness nut14.HTLCWitness
			if err := json.Unmarshal([]byte(bm.Witness), &witness); err != nil {
				return nut11.InvalidWitness
			}

			// verify valid preimage
			preimageBytes, err := hex.DecodeString(witness.Preimage)
			if err != nil {
				return nut14.InvalidPreimageErr
			}
			hashBytes := sha256.Sum256(preimageBytes)
			hash := hex.EncodeToString(hashBytes[:])

			if len(secret.Data.Data) != 64 {
				return nut14.InvalidHashErr
			}
			if hash != secret.Data.Data {
				return nut14.InvalidPreimageErr
			}
			signatures = witness.Signatures
		default:
			return nut11.InvalidKindErr
		}

		if nut11.DuplicateSignatures(signatures) {
			return nut11.DuplicateSignaturesErr
		}
		if !nut11.HasValidSignatures(hash[:], signatures, signaturesRequired, pubkeys) {
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
			errmsg := fmt.Sprintf("invalid B_: %v", err)
			return nil, cashu.BuildCashuError(errmsg, cashu.StandardErrCode)
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
			errmsg := fmt.Sprintf("error saving blind signatures: %v", err)
			return nil, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
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

func (m *Mint) SetMintInfo(mintInfo MintInfo) {
	nuts := nut06.Nuts{
		Nut04: nut06.NutSetting{
			Methods: []nut06.MethodSetting{
				{
					Method:    cashu.BOLT11_METHOD,
					Unit:      cashu.Sat.String(),
					MinAmount: m.limits.MintingSettings.MinAmount,
					MaxAmount: m.limits.MintingSettings.MaxAmount,
				},
			},
			Disabled: false,
		},
		Nut05: nut06.NutSetting{
			Methods: []nut06.MethodSetting{
				{
					Method:    cashu.BOLT11_METHOD,
					Unit:      cashu.Sat.String(),
					MinAmount: m.limits.MeltingSettings.MinAmount,
					MaxAmount: m.limits.MeltingSettings.MaxAmount,
				},
			},
			Disabled: false,
		},
		Nut07: nut06.Supported{Supported: true},
		Nut08: nut06.Supported{Supported: false},
		Nut09: nut06.Supported{Supported: true},
		Nut10: nut06.Supported{Supported: true},
		Nut11: nut06.Supported{Supported: true},
		Nut12: nut06.Supported{Supported: true},
		Nut14: nut06.Supported{Supported: true},
		Nut17: nut17.InfoSetting{
			Supported: []nut17.SupportedMethod{
				{
					Method: cashu.BOLT11_METHOD,
					Unit:   cashu.Sat.String(),
					Commands: []string{
						nut17.Bolt11MintQuote.String(),
					},
				},
			},
		},
	}

	if m.mppEnabled {
		nuts.Nut15 = &nut06.NutSetting{
			Methods: []nut06.MethodSetting{
				{Method: cashu.BOLT11_METHOD, Unit: cashu.Sat.String()},
			},
		}
	}

	info := nut06.MintInfo{
		Name:            mintInfo.Name,
		Version:         "gonuts/0.4.0",
		Description:     mintInfo.Description,
		LongDescription: mintInfo.LongDescription,
		Contact:         mintInfo.Contact,
		Motd:            mintInfo.Motd,
		IconURL:         mintInfo.IconURL,
		URLs:            mintInfo.URLs,
		Time:            time.Now().Unix(),
		Nuts:            nuts,
	}
	m.mintInfo = info
}

func (m Mint) RetrieveMintInfo() (nut06.MintInfo, error) {
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
		errmsg := fmt.Sprintf("error getting mint balance: %v", err)
		return nut06.MintInfo{}, cashu.BuildCashuError(errmsg, cashu.DBErrCode)
	}

	if m.limits.MaxBalance > 0 {
		if mintBalance >= m.limits.MaxBalance {
			mintingDisabled = true
		}
	}
	nut04 := m.mintInfo.Nuts.Nut04
	nut04.Disabled = mintingDisabled
	m.mintInfo.Nuts.Nut04 = nut04
	m.mintInfo.Pubkey = hex.EncodeToString(publicKey.SerializeCompressed())

	return m.mintInfo, nil
}

func (m *Mint) publishProofsStateChanges(proofs cashu.Proofs, state nut07.State) {
	proofStates := make([]nut07.ProofState, len(proofs))

	for i, proof := range proofs {
		Y, _ := crypto.HashToCurve([]byte(proof.Secret))
		Yhex := hex.EncodeToString(Y.SerializeCompressed())
		proofStates[i] = nut07.ProofState{Y: Yhex, State: state, Witness: proof.Witness}
	}

	stateResponse := nut07.PostCheckStateResponse{
		States: proofStates,
	}

	proofStatesJson, _ := json.Marshal(&stateResponse)
	m.publisher.Publish(PROOF_STATE_TOPIC, proofStatesJson)
}
