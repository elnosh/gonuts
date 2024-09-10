package wallet

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut03"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut07"
	"github.com/elnosh/gonuts/cashu/nuts/nut09"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
	"github.com/elnosh/gonuts/cashu/nuts/nut13"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/wallet/storage"
	"github.com/tyler-smith/go-bip39"

	decodepay "github.com/nbd-wtf/ln-decodepay"
)

var (
	ErrMintNotExist            = errors.New("mint does not exist")
	ErrInsufficientMintBalance = errors.New("not enough funds in selected mint")
)

type Wallet struct {
	db        storage.DB
	masterKey *hdkeychain.ExtendedKey

	// key to receive locked ecash
	privateKey *btcec.PrivateKey

	// default mint
	currentMint *walletMint
	// list of mints that have been trusted
	mints map[string]walletMint
}

type walletMint struct {
	mintURL string
	// active keysets from mint
	activeKeysets map[string]crypto.WalletKeyset
	// list of inactive keysets (if any) from mint
	inactiveKeysets map[string]crypto.WalletKeyset
}

func InitStorage(path string) (storage.DB, error) {
	// bolt db atm
	return storage.InitBolt(path)
}

func LoadWallet(config Config) (*Wallet, error) {
	db, err := InitStorage(config.WalletPath)
	if err != nil {
		return nil, fmt.Errorf("InitStorage: %v", err)
	}

	// create new seed if none exists
	seed := db.GetSeed()
	if len(seed) == 0 {
		// create and save new seed
		entropy, err := bip39.NewEntropy(128)
		if err != nil {
			return nil, fmt.Errorf("error generating seed: %v", err)
		}

		mnemonic, err := bip39.NewMnemonic(entropy)
		if err != nil {
			return nil, fmt.Errorf("error generating seed: %v", err)
		}

		seed = bip39.NewSeed(mnemonic, "")
		db.SaveMnemonicSeed(mnemonic, seed)
	}

	// TODO: what's the point of chain params here?
	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, err
	}

	privateKey, err := DeriveP2PK(masterKey)
	if err != nil {
		return nil, err
	}

	wallet := &Wallet{db: db, masterKey: masterKey, privateKey: privateKey}
	wallet.mints, err = wallet.getWalletMints()
	if err != nil {
		return nil, err
	}
	url, err := url.Parse(config.CurrentMintURL)
	if err != nil {
		return nil, fmt.Errorf("invalid mint url: %v", err)
	}
	mintURL := url.String()

	// if mint is new, add it
	walletMint, ok := wallet.mints[mintURL]
	if !ok {
		mint, err := wallet.addMint(mintURL)
		if err != nil {
			return nil, fmt.Errorf("error adding new mint: %v", err)
		}
		wallet.currentMint = mint
	} else { // if mint is already known, check if active keyset has changed
		// get last stored active sat keyset
		var lastActiveSatKeyset crypto.WalletKeyset
		for _, keyset := range walletMint.activeKeysets {
			_, err := hex.DecodeString(keyset.Id)
			if keyset.Unit == "sat" && err == nil {
				lastActiveSatKeyset = keyset
				break
			}
		}

		keysetsResponse, err := GetAllKeysets(walletMint.mintURL)
		if err != nil {
			return nil, fmt.Errorf("error getting keysets from mint: %v", err)
		}

		activeKeysetChanged := false
		for _, keyset := range keysetsResponse.Keysets {
			// check if last active recorded keyset is not active anymore
			if keyset.Id == lastActiveSatKeyset.Id && !keyset.Active {
				activeKeysetChanged = true

				// there is new keyset, change last active to inactive
				lastActiveSatKeyset.Active = false
				wallet.db.SaveKeyset(&lastActiveSatKeyset)
				break
			}
		}

		// if active keyset changed, save accordingly
		if activeKeysetChanged {
			activeKeysets, err := GetMintActiveKeysets(walletMint.mintURL)
			if err != nil {
				return nil, fmt.Errorf("error getting keyset from mint: %v", err)
			}

			for i, keyset := range activeKeysets {
				storedKeyset := db.GetKeyset(keyset.Id)
				// save new keyset
				if storedKeyset == nil {
					db.SaveKeyset(&keyset)
				} else { // if not new, change active to true
					keyset.Active = true
					keyset.Counter = storedKeyset.Counter
					activeKeysets[i] = keyset
					wallet.db.SaveKeyset(&keyset)
				}
			}
			walletMint.activeKeysets = activeKeysets
		}
		wallet.currentMint = &walletMint
	}

	return wallet, nil
}

// get mint keysets
func mintInfo(mintURL string) (*walletMint, error) {
	activeKeysets, err := GetMintActiveKeysets(mintURL)
	if err != nil {
		return nil, err
	}

	inactiveKeysets, err := GetMintInactiveKeysets(mintURL)
	if err != nil {
		return nil, err
	}

	return &walletMint{mintURL, activeKeysets, inactiveKeysets}, nil
}

// addMint adds the mint to the list of mints trusted by the wallet
func (w *Wallet) addMint(mint string) (*walletMint, error) {
	url, err := url.Parse(mint)
	if err != nil {
		return nil, fmt.Errorf("invalid mint url: %v", err)
	}
	mintURL := url.String()

	mintInfo, err := mintInfo(mintURL)
	if err != nil {
		return nil, err
	}

	for _, keyset := range mintInfo.activeKeysets {

		w.db.SaveKeyset(&keyset)
	}
	for _, keyset := range mintInfo.inactiveKeysets {
		w.db.SaveKeyset(&keyset)
	}
	w.mints[mintURL] = *mintInfo

	return mintInfo, nil
}

func GetMintActiveKeysets(mintURL string) (map[string]crypto.WalletKeyset, error) {
	keysets, err := GetAllKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keysets from mint: %v", err)
	}

	keysetsResponse, err := GetActiveKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keysets from mint: %v", err)
	}

	activeKeysets := make(map[string]crypto.WalletKeyset)
	for i, keyset := range keysetsResponse.Keysets {

		var inputFeePpk uint
		for _, response := range keysets.Keysets {
			if response.Id == keyset.Id {
				inputFeePpk = response.InputFeePpk
			}
		}

		_, err := hex.DecodeString(keyset.Id)
		if keyset.Unit == "sat" && err == nil {
			keys, err := crypto.MapPubKeys(keysetsResponse.Keysets[i].Keys)
			if err != nil {
				return nil, err
			}
			id := crypto.DeriveKeysetId(keys)
			if id != keyset.Id {
				return nil, fmt.Errorf("Got invalid keyset. Derived id: '%v' but got '%v' from mint", id, keyset.Id)
			}

			activeKeyset := crypto.WalletKeyset{
				Id:          id,
				MintURL:     mintURL,
				Unit:        keyset.Unit,
				Active:      true,
				PublicKeys:  keys,
				InputFeePpk: inputFeePpk,
			}
			activeKeysets[id] = activeKeyset
		}
	}

	return activeKeysets, nil
}

func GetMintInactiveKeysets(mintURL string) (map[string]crypto.WalletKeyset, error) {
	keysetsResponse, err := GetAllKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting keysets from mint: %v", err)
	}

	inactiveKeysets := make(map[string]crypto.WalletKeyset)
	for _, keysetRes := range keysetsResponse.Keysets {
		_, err := hex.DecodeString(keysetRes.Id)
		if !keysetRes.Active && keysetRes.Unit == "sat" && err == nil {
			keyset := crypto.WalletKeyset{
				Id:          keysetRes.Id,
				MintURL:     mintURL,
				Unit:        keysetRes.Unit,
				Active:      keysetRes.Active,
				InputFeePpk: keysetRes.InputFeePpk,
			}
			inactiveKeysets[keyset.Id] = keyset
		}
	}
	return inactiveKeysets, nil
}

// GetBalance returns the total balance aggregated from all proofs
func (w *Wallet) GetBalance() uint64 {
	return w.db.GetProofs().Amount()
}

// GetBalanceByMints returns a map of string mint
// and a uint64 that represents the balance for that mint
func (w *Wallet) GetBalanceByMints() map[string]uint64 {
	mintsBalances := make(map[string]uint64)

	for _, mint := range w.mints {
		var mintBalance uint64 = 0

		for _, keyset := range mint.activeKeysets {
			proofs := w.db.GetProofsByKeysetId(keyset.Id)
			mintBalance += proofs.Amount()
		}
		for _, keyset := range mint.inactiveKeysets {
			proofs := w.db.GetProofsByKeysetId(keyset.Id)
			mintBalance += proofs.Amount()
		}

		mintsBalances[mint.mintURL] = mintBalance
	}

	return mintsBalances
}

// RequestMint requests a mint quote to the wallet's current mint
// for the specified amount
func (w *Wallet) RequestMint(amount uint64) (*nut04.PostMintQuoteBolt11Response, error) {
	mintRequest := nut04.PostMintQuoteBolt11Request{Amount: amount, Unit: "sat"}
	mintResponse, err := PostMintQuoteBolt11(w.currentMint.mintURL, mintRequest)
	if err != nil {
		return nil, err
	}

	bolt11, err := decodepay.Decodepay(mintResponse.Request)
	if err != nil {
		return nil, fmt.Errorf("error decoding bolt11 invoice: %v", err)
	}

	invoice := storage.Invoice{
		TransactionType: storage.Mint,
		QuoteAmount:     amount,
		Id:              mintResponse.Quote,
		PaymentRequest:  mintResponse.Request,
		PaymentHash:     bolt11.PaymentHash,
		CreatedAt:       int64(bolt11.CreatedAt),
		Paid:            false,
		InvoiceAmount:   uint64(bolt11.MSatoshi / 1000),
		QuoteExpiry:     mintResponse.Expiry,
	}

	err = w.db.SaveInvoice(invoice)
	if err != nil {
		return nil, err
	}

	return mintResponse, nil
}

func (w *Wallet) MintQuoteState(quoteId string) (*nut04.PostMintQuoteBolt11Response, error) {
	return GetMintQuoteState(w.currentMint.mintURL, quoteId)
}

// MintTokens will check whether if the mint quote has been paid.
// If yes, it will create blinded messages that will send to the mint
// to get the blinded signatures.
// If successful, it will unblind the signatures to generate proofs
// and store the proofs in the db.
func (w *Wallet) MintTokens(quoteId string) (cashu.Proofs, error) {
	mintQuote, err := w.MintQuoteState(quoteId)
	if err != nil {
		return nil, err
	}
	// TODO: remove usage of 'Paid' field after mints have upgraded
	if !mintQuote.Paid || mintQuote.State == nut04.Unpaid {
		return nil, errors.New("invoice not paid")
	}
	if mintQuote.State == nut04.Issued {
		return nil, errors.New("quote has already been issued")
	}

	invoice, err := w.GetInvoiceByPaymentRequest(mintQuote.Request)
	if err != nil {
		return nil, err
	}
	if invoice == nil {
		return nil, errors.New("invoice not found")
	}

	activeKeyset, err := w.getActiveSatKeyset(w.currentMint.mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active sat keyset: %v", err)
	}
	// get counter for keyset
	counter := w.counterForKeyset(activeKeyset.Id)

	split := w.splitWalletTarget(invoice.QuoteAmount, w.currentMint.mintURL)
	blindedMessages, secrets, rs, err := w.createBlindedMessages(split, activeKeyset.Id, &counter)
	if err != nil {
		return nil, fmt.Errorf("error creating blinded messages: %v", err)
	}

	// request mint to sign the blinded messages
	postMintRequest := nut04.PostMintBolt11Request{Quote: quoteId, Outputs: blindedMessages}
	mintResponse, err := PostMintBolt11(w.currentMint.mintURL, postMintRequest)
	if err != nil {
		return nil, err
	}

	// unblind the signatures from the promises and build the proofs
	proofs, err := constructProofs(mintResponse.Signatures, secrets, rs, activeKeyset)
	if err != nil {
		return nil, fmt.Errorf("error constructing proofs: %v", err)
	}

	// store proofs in db
	err = w.saveProofs(proofs)
	if err != nil {
		return nil, fmt.Errorf("error storing proofs: %v", err)
	}

	// only increase counter if mint was successful
	err = w.incrementKeysetCounter(activeKeyset.Id, uint32(len(blindedMessages)))
	if err != nil {
		return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
	}

	// mark invoice as redeemed
	invoice.Paid = true
	invoice.SettledAt = time.Now().Unix()
	err = w.db.SaveInvoice(*invoice)
	if err != nil {
		return nil, err
	}

	return proofs, nil
}

// Send will return a cashu token with proofs for the given amount
func (w *Wallet) Send(amount uint64, mintURL string, includeFees bool) (cashu.Proofs, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, ErrMintNotExist
	}

	proofsToSend, err := w.getProofsForAmount(amount, &selectedMint, nil, includeFees)
	if err != nil {
		return nil, err
	}

	return proofsToSend, nil
}

// SendToPubkey returns a cashu token with proofs that are locked to
// the passed pubkey
func (w *Wallet) SendToPubkey(
	amount uint64,
	mintURL string,
	pubkey *btcec.PublicKey,
	includeFees bool,
) (cashu.Proofs, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, ErrMintNotExist
	}

	// check first if mint supports P2PK NUT
	mintInfo, err := GetMintInfo(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting info from mint: %v", err)
	}
	nut11 := mintInfo.Nuts[11].(map[string]interface{})
	if nut11["supported"] != true {
		return nil, errors.New("mint does not support Pay to Public Key")
	}

	lockedProofs, err := w.getProofsForAmount(amount, &selectedMint, pubkey, includeFees)
	if err != nil {
		return nil, err
	}

	return lockedProofs, nil
}

// Receives Cashu token. If swap is true, it will swap the funds to the configured default mint.
// If false, it will add the proofs from the mint and add that mint to the list of trusted mints.
func (w *Wallet) Receive(token cashu.Token, swapToTrusted bool) (uint64, error) {
	proofsToSwap := token.Proofs()
	tokenMint := token.Mint()

	if swapToTrusted {
		trustedMintProofs, err := w.swapToTrusted(token)
		if err != nil {
			return 0, fmt.Errorf("error swapping token to trusted mint: %v", err)
		}
		return trustedMintProofs.Amount(), nil
	} else {
		// only add mint if not previously trusted
		_, ok := w.mints[tokenMint]
		if !ok {
			_, err := w.addMint(tokenMint)
			if err != nil {
				return 0, err
			}
		}

		proofs, err := w.swap(proofsToSwap, tokenMint)
		if err != nil {
			return 0, err
		}
		w.saveProofs(proofs)

		return proofs.Amount(), nil
	}
}

// swap to be used when receiving
func (w *Wallet) swap(proofsToSwap cashu.Proofs, mintURL string) (cashu.Proofs, error) {
	var nut10secret nut10.WellKnownSecret
	// if P2PK, add signature to Witness in the proofs
	if nut11.IsSecretP2PK(proofsToSwap[0]) {
		var err error
		nut10secret, err = nut10.DeserializeSecret(proofsToSwap[0].Secret)
		if err != nil {
			return nil, err
		}
		// check that public key in data is one wallet can sign for
		if !nut11.CanSign(nut10secret, w.privateKey) {
			return nil, fmt.Errorf("cannot sign locked proofs")
		}

		proofsToSwap, err = nut11.AddSignatureToInputs(proofsToSwap, w.privateKey)
		if err != nil {
			return nil, fmt.Errorf("error signing inputs: %v", err)
		}
	}

	var activeSatKeyset *crypto.WalletKeyset
	var counter *uint32 = nil
	mint, trustedMint := w.mints[mintURL]
	if !trustedMint {
		// get keys if mint not trusted
		var err error
		activeKeysets, err := GetMintActiveKeysets(mintURL)
		if err != nil {
			return nil, err
		}
		for _, keyset := range activeKeysets {
			activeSatKeyset = &keyset
		}
		mint = walletMint{mintURL: mintURL, activeKeysets: activeKeysets}
	} else {
		var err error
		activeSatKeyset, err = w.getActiveSatKeyset(mintURL)
		if err != nil {
			return nil, fmt.Errorf("error getting active sat keyset: %v", err)
		}
		keysetCounter := w.counterForKeyset(activeSatKeyset.Id)
		counter = &keysetCounter
	}

	fees := w.fees(proofsToSwap, &mint)
	split := w.splitWalletTarget(proofsToSwap.Amount()-uint64(fees), mintURL)
	outputs, secrets, rs, err := w.createBlindedMessages(split, activeSatKeyset.Id, counter)
	if err != nil {
		return nil, fmt.Errorf("createBlindedMessages: %v", err)
	}

	// if P2PK locked ecash has `SIG_ALL` flag, sign outputs
	if nut11.IsSecretP2PK(proofsToSwap[0]) && nut11.IsSigAll(nut10secret) {
		outputs, err = nut11.AddSignatureToOutputs(outputs, w.privateKey)
		if err != nil {
			return nil, fmt.Errorf("error signing outputs: %v", err)
		}
	}

	// make swap request to mint
	swapRequest := nut03.PostSwapRequest{Inputs: proofsToSwap, Outputs: outputs}
	swapResponse, err := PostSwap(mintURL, swapRequest)
	if err != nil {
		return nil, err
	}

	// unblind signatures to get proofs and save them to db
	proofs, err := constructProofs(swapResponse.Signatures, secrets, rs, activeSatKeyset)
	if err != nil {
		return nil, fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	// only increment the counter if mint was from trusted list
	if trustedMint {
		err = w.incrementKeysetCounter(activeSatKeyset.Id, uint32(len(outputs)))
		if err != nil {
			return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
		}
	}

	return proofs, nil
}

// swapToTrusted will swap the proofs from mint in the token
// to the wallet's configured default mint
func (w *Wallet) swapToTrusted(token cashu.Token) (cashu.Proofs, error) {
	invoicePct := 0.99
	tokenAmount := token.Amount()
	amount := float64(tokenAmount) * invoicePct
	tokenMintURL := token.Mint()
	proofsToSwap := token.Proofs()

	var mintResponse *nut04.PostMintQuoteBolt11Response
	var meltQuoteResponse *nut05.PostMeltQuoteBolt11Response
	var err error

	activeKeysets, err := GetMintActiveKeysets(tokenMintURL)
	if err != nil {
		return nil, err
	}
	mint := &walletMint{mintURL: tokenMintURL, activeKeysets: activeKeysets}

	fees := uint64(w.fees(proofsToSwap, mint))
	// if proofs are P2PK locked, sign appropriately
	if nut11.IsSecretP2PK(proofsToSwap[0]) {
		nut10secret, err := nut10.DeserializeSecret(proofsToSwap[0].Secret)
		if err != nil {
			return nil, err
		}
		// if sig all, swap them first and then melt
		// increase fees since extra swap will incur fees
		if nut11.IsSigAll(nut10secret) {
			proofsToSwap, err = w.swap(proofsToSwap, tokenMintURL)
			if err != nil {
				return nil, err
			}
			fees += fees
		} else {
			// if not sig all, can just sign inputs and no need to do a swap first
			if !nut11.CanSign(nut10secret, w.privateKey) {
				return nil, fmt.Errorf("cannot sign locked proofs")
			}

			proofsToSwap, err = nut11.AddSignatureToInputs(proofsToSwap, w.privateKey)
			if err != nil {
				return nil, fmt.Errorf("error signing inputs: %v", err)
			}
		}
	}

	for {
		// request a mint quote from the configured default mint
		// this will generate an invoice from the trusted mint
		mintAmountRequest := uint64(amount) - fees
		mintResponse, err = w.RequestMint(mintAmountRequest)
		if err != nil {
			return nil, fmt.Errorf("error requesting mint: %v", err)
		}

		// request melt quote from untrusted mint which will
		// request mint to pay invoice generated from trusted mint in previous mint request
		meltRequest := nut05.PostMeltQuoteBolt11Request{Request: mintResponse.Request, Unit: "sat"}
		meltQuoteResponse, err = PostMeltQuoteBolt11(tokenMintURL, meltRequest)
		if err != nil {
			return nil, fmt.Errorf("error with melt request: %v", err)
		}

		// if amount in token is less than amount asked from mint in melt request,
		// lower the amount for mint request
		if meltQuoteResponse.Amount+meltQuoteResponse.FeeReserve+fees > tokenAmount {
			invoicePct -= 0.01
			amount *= invoicePct
		} else {
			break
		}
	}

	// request untrusted mint to pay invoice generated from trusted mint
	meltBolt11Request := nut05.PostMeltBolt11Request{Quote: meltQuoteResponse.Quote, Inputs: proofsToSwap}
	meltBolt11Response, err := PostMeltBolt11(tokenMintURL, meltBolt11Request)
	if err != nil {
		return nil, fmt.Errorf("error melting token: %v", err)
	}

	// if melt request was successful and untrusted mint paid the invoice,
	// make mint request to trusted mint to get valid proofs
	if meltBolt11Response.Paid {
		proofs, err := w.MintTokens(mintResponse.Quote)
		if err != nil {
			return nil, fmt.Errorf("error minting tokens: %v", err)
		}
		return proofs, nil
	} else {
		return nil, errors.New("mint could not pay lightning invoice")
	}
}

// Melt will request the mint to pay the given invoice
func (w *Wallet) Melt(invoice string, mintURL string) (*nut05.PostMeltQuoteBolt11Response, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, ErrMintNotExist
	}

	meltRequest := nut05.PostMeltQuoteBolt11Request{Request: invoice, Unit: "sat"}
	meltQuoteResponse, err := PostMeltQuoteBolt11(mintURL, meltRequest)
	if err != nil {
		return nil, err
	}

	amountNeeded := meltQuoteResponse.Amount + meltQuoteResponse.FeeReserve
	proofs, err := w.getProofsForAmount(amountNeeded, &selectedMint, nil, true)
	if err != nil {
		return nil, err
	}

	meltBolt11Request := nut05.PostMeltBolt11Request{Quote: meltQuoteResponse.Quote, Inputs: proofs}
	meltBolt11Response, err := PostMeltBolt11(mintURL, meltBolt11Request)
	if err != nil {
		w.saveProofs(proofs)
		return nil, err
	}

	// TODO: deprecate paid field and only use State
	// TODO: check for PENDING as well
	paid := meltBolt11Response.Paid
	// if state field is present, use that instead of paid
	if meltBolt11Response.State != nut05.Unknown {
		paid = meltBolt11Response.State == nut05.Paid
	} else {
		if paid {
			meltBolt11Response.State = nut05.Paid
		} else {
			meltBolt11Response.State = nut05.Unpaid
		}
	}
	if !paid {
		// save proofs if invoice was not paid
		w.saveProofs(proofs)
	} else {
		bolt11, err := decodepay.Decodepay(invoice)
		if err != nil {
			return nil, fmt.Errorf("error decoding bolt11 invoice: %v", err)
		}

		// save invoice to db
		invoice := storage.Invoice{
			TransactionType: storage.Melt,
			QuoteAmount:     amountNeeded,
			Id:              meltQuoteResponse.Quote,
			PaymentRequest:  invoice,
			PaymentHash:     bolt11.PaymentHash,
			Preimage:        meltBolt11Response.Preimage,
			CreatedAt:       int64(bolt11.CreatedAt),
			Paid:            true,
			SettledAt:       time.Now().Unix(),
			InvoiceAmount:   uint64(bolt11.MSatoshi / 1000),
			QuoteExpiry:     meltQuoteResponse.Expiry,
		}

		err = w.db.SaveInvoice(invoice)
		if err != nil {
			return nil, err
		}
	}
	return meltBolt11Response, err
}

func (w *Wallet) getProofsFromMint(mintURL string) cashu.Proofs {
	proofs := w.getInactiveProofsByMint(mintURL)
	proofs = append(proofs, w.getActiveProofsByMint(mintURL)...)
	return proofs
}

func (w *Wallet) getInactiveProofsByMint(mintURL string) cashu.Proofs {
	selectedMint := w.mints[mintURL]

	proofs := cashu.Proofs{}
	for _, keyset := range selectedMint.inactiveKeysets {
		keysetProofs := w.db.GetProofsByKeysetId(keyset.Id)
		proofs = append(proofs, keysetProofs...)
	}

	return proofs
}

func (w *Wallet) getActiveProofsByMint(mintURL string) cashu.Proofs {
	selectedMint := w.mints[mintURL]

	proofs := cashu.Proofs{}
	for _, keyset := range selectedMint.activeKeysets {
		keysetProofs := w.db.GetProofsByKeysetId(keyset.Id)
		proofs = append(proofs, keysetProofs...)
	}

	return proofs
}

// selectProofsToSend will try to select proofs for
// amount + fees (if includeFees is true)
func (w *Wallet) selectProofsToSend(
	proofs cashu.Proofs,
	amount uint64,
	mint *walletMint,
	includeFees bool,
) (cashu.Proofs, error) {
	proofsSum := proofs.Amount()
	if proofsSum < amount {
		return nil, ErrInsufficientMintBalance
	}

	var selectedProofs cashu.Proofs
	sort.Slice(proofs, func(i, j int) bool { return proofs[i].Amount < proofs[j].Amount })

	var smallerProofs, biggerProofs cashu.Proofs
	for _, proof := range proofs {
		if proof.Amount <= amount {
			smallerProofs = append(smallerProofs, proof)
		} else {
			biggerProofs = append(biggerProofs, proof)
		}
	}

	remainingAmount := amount
	var selectedProofsSum uint64 = 0
	for remainingAmount > 0 {
		sort.Slice(smallerProofs, func(i, j int) bool { return smallerProofs[i].Amount > smallerProofs[j].Amount })

		var selectedProof cashu.Proof
		if len(smallerProofs) > 0 {
			selectedProof = smallerProofs[0]
			smallerProofs = smallerProofs[1:]
		} else if len(biggerProofs) > 0 {
			selectedProof = biggerProofs[0]
			biggerProofs = biggerProofs[1:]
		} else {
			break
		}

		selectedProofs = append(selectedProofs, selectedProof)
		selectedProofsSum += selectedProof.Amount

		var fees uint64 = 0
		if includeFees {
			fees = uint64(w.fees(selectedProofs, mint))
		}

		if selectedProof.Amount >= remainingAmount+fees {
			break
		}

		remainingAmount = amount + fees - selectedProofsSum
		var tempSmaller cashu.Proofs
		for _, small := range smallerProofs {
			if small.Amount <= remainingAmount {
				tempSmaller = append(tempSmaller, small)
			} else {
				biggerProofs = slices.Insert(biggerProofs, 0, small)
			}
		}

		smallerProofs = tempSmaller
	}

	var fees uint64 = 0
	if includeFees {
		fees = uint64(w.fees(selectedProofs, mint))
	}

	if selectedProofsSum < amount+fees {
		return nil, fmt.Errorf(
			"insufficient funds for transaction. Amount needed %v + %v(fees) = %v",
			amount, fees, amount+fees)
	}

	return selectedProofs, nil
}

func (w *Wallet) swapToSend(
	amount uint64,
	mint *walletMint,
	pubkeyLock *btcec.PublicKey,
	includeFees bool,
) (cashu.Proofs, error) {
	activeSatKeyset, err := w.getActiveSatKeyset(mint.mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active sat keyset: %v", err)
	}

	splitForSendAmount := cashu.AmountSplit(amount)
	var feesToReceive uint = 0
	if includeFees {
		feesToReceive = feesForCount(len(splitForSendAmount)+1, activeSatKeyset)
		amount += uint64(feesToReceive)
	}

	proofs := w.getProofsFromMint(mint.mintURL)
	proofsToSwap, err := w.selectProofsToSend(proofs, amount, mint, true)
	if err != nil {
		return nil, err
	}

	var send, change cashu.BlindedMessages
	var secrets, changeSecrets []string
	var rs, changeRs []*secp256k1.PrivateKey
	var counter, incrementCounterBy uint32

	split := append(splitForSendAmount, cashu.AmountSplit(uint64(feesToReceive))...)
	slices.Sort(split)
	if pubkeyLock == nil {
		counter = w.counterForKeyset(activeSatKeyset.Id)
		// blinded messages for send amount from counter
		send, secrets, rs, err = w.createBlindedMessages(split, activeSatKeyset.Id, &counter)
		if err != nil {
			return nil, err
		}
		incrementCounterBy += uint32(len(send))
	} else {
		// if pubkey to lock ecash is present, generate blinded messages
		// with secrets locking the ecash
		send, secrets, rs, err = blindedMessagesFromLock(split, activeSatKeyset.Id, pubkeyLock)
		if err != nil {
			return nil, err
		}
		counter = w.counterForKeyset(activeSatKeyset.Id)
	}

	proofsAmount := proofsToSwap.Amount()
	fees := w.fees(proofsToSwap, mint)
	// blinded messages for change amount
	if proofsAmount-amount-uint64(fees) > 0 {
		changeAmount := proofsAmount - amount - uint64(fees)
		changeSplit := w.splitWalletTarget(changeAmount, mint.mintURL)
		change, changeSecrets, changeRs, err = w.createBlindedMessages(changeSplit, activeSatKeyset.Id, &counter)
		if err != nil {
			return nil, err
		}
		incrementCounterBy += uint32(len(change))
	}

	blindedMessages := make(cashu.BlindedMessages, len(send))
	copy(blindedMessages, send)
	blindedMessages = append(blindedMessages, change...)
	secrets = append(secrets, changeSecrets...)
	rs = append(rs, changeRs...)

	cashu.SortBlindedMessages(blindedMessages, secrets, rs)

	// create outputs from splitWalletTarget
	// call swap endpoint
	swapRequest := nut03.PostSwapRequest{Inputs: proofsToSwap, Outputs: blindedMessages}
	swapResponse, err := PostSwap(mint.mintURL, swapRequest)
	if err != nil {
		return nil, err
	}

	for _, proof := range proofsToSwap {
		w.db.DeleteProof(proof.Secret)
	}

	proofsFromSwap, err := constructProofs(swapResponse.Signatures, secrets, rs, activeSatKeyset)
	if err != nil {
		return nil, fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	proofsToSend := make(cashu.Proofs, len(send))
	for i, sendmsg := range send {
		for j, proof := range proofsFromSwap {
			if sendmsg.Amount == proof.Amount {
				proofsToSend[i] = proof
				proofsFromSwap = slices.Delete(proofsFromSwap, j, j+1)
				break
			}
		}
	}

	// remaining proofs are change proofs to save to db
	w.saveProofs(proofsFromSwap)

	err = w.incrementKeysetCounter(activeSatKeyset.Id, incrementCounterBy)
	if err != nil {
		return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
	}

	return proofsToSend, nil
}

// getProofsForAmount will return proofs from mint for the give amount.
// if pubkeyLock is present it will generate proofs locked to the public key.
// It returns error if wallet does not have enough proofs to fulfill amount
func (w *Wallet) getProofsForAmount(
	amount uint64,
	mint *walletMint,
	pubkeyLock *btcec.PublicKey,
	includeFees bool,
) (cashu.Proofs, error) {
	// TODO: need to check first if 'input_fee_ppk' for keyset has changed
	mintProofs := w.getProofsFromMint(mint.mintURL)
	selectedProofs, err := w.selectProofsToSend(mintProofs, amount, mint, includeFees)
	if err != nil {
		return nil, err
	}

	var fees uint64 = 0
	if includeFees {
		fees = uint64(w.fees(selectedProofs, mint))
	}
	totalAmount := amount + uint64(fees)

	// only try selecting offline if lock is not specified
	// if lock is specified, need to do swap first to create locked proofs
	if pubkeyLock == nil {
		// check if offline selection worked (i.e by checking that amount + fees add up)
		// if proofs stored fulfill amount, delete them from db and return them
		if selectedProofs.Amount() == totalAmount {
			for _, proof := range selectedProofs {
				w.db.DeleteProof(proof.Secret)
			}
			return selectedProofs, nil
		}
	}

	// if offline selection did not work or needed to do swap
	// to lock the ecash, swap proofs to then send
	proofsToSend, err := w.swapToSend(amount, mint, pubkeyLock, includeFees)
	if err != nil {
		return nil, err
	}

	return proofsToSend, nil
}

// splitWalletTarget returns a split for an amount.
// creates the split based on the state of the wallet.
// it has a defautl target of 3 coins of each amount
func (w *Wallet) splitWalletTarget(amountToSplit uint64, mint string) []uint64 {
	target := 3
	proofs := w.getProofsFromMint(mint)

	// amounts that are in wallet
	amountsInWallet := make([]uint64, len(proofs))
	for i, proof := range proofs {
		amountsInWallet[i] = proof.Amount
	}
	slices.Sort(amountsInWallet)

	allPosibleAmounts := make([]uint64, crypto.MAX_ORDER)
	for i := 0; i < crypto.MAX_ORDER; i++ {
		amount := uint64(math.Pow(2, float64(i)))
		allPosibleAmounts[i] = amount
	}

	// based on amounts that are already in the wallet
	// define what amounts wanted to reach target
	var neededAmounts []uint64
	for _, amount := range allPosibleAmounts {
		count := cashu.Count(amountsInWallet, amount)
		timesToAdd := cashu.Max(0, uint64(target)-uint64(count))
		for i := 0; i < int(timesToAdd); i++ {
			neededAmounts = append(neededAmounts, amount)
		}
	}
	slices.Sort(neededAmounts)

	// fill in based on the needed amounts
	// that are below the amount passed (amountToSplit)
	var amounts []uint64
	var amountsSum uint64 = 0
	for amountsSum < amountToSplit {
		if len(neededAmounts) > 0 {
			if amountsSum+neededAmounts[0] > amountToSplit {
				break
			}
			amounts = append(amounts, neededAmounts[0])
			amountsSum += neededAmounts[0]
			neededAmounts = slices.Delete(neededAmounts, 0, 1)
		} else {
			break
		}
	}

	remainingAmount := amountToSplit - amountsSum
	if remainingAmount > 0 {
		amounts = append(amounts, cashu.AmountSplit(remainingAmount)...)
	}

	return amounts
}

func (w *Wallet) fees(proofs cashu.Proofs, mint *walletMint) uint {
	var fees uint = 0
	for _, proof := range proofs {
		if keyset, ok := mint.activeKeysets[proof.Id]; ok {
			fees += keyset.InputFeePpk
			continue
		}

		if keyset, ok := mint.inactiveKeysets[proof.Id]; ok {
			fees += keyset.InputFeePpk
		}
	}
	return (fees + 999) / 1000
}

func feesForCount(count int, keyset *crypto.WalletKeyset) uint {
	var fees uint = 0
	for i := 0; i < count; i++ {
		fees += keyset.InputFeePpk
	}
	return (fees + 999) / 1000
}

// returns Blinded messages, secrets - [][]byte, and list of r
// if counter is nil, it generates random secrets
// if counter is non-nil, it will generate secrets deterministically
func (w *Wallet) createBlindedMessages(
	splitAmounts []uint64,
	keysetId string,
	counter *uint32,
) (cashu.BlindedMessages, []string, []*secp256k1.PrivateKey, error) {
	splitLen := len(splitAmounts)
	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)

	keysetDerivationPath, err := nut13.DeriveKeysetPath(w.masterKey, keysetId)
	if err != nil {
		return nil, nil, nil, err
	}

	for i, amt := range splitAmounts {
		var secret string
		var r *secp256k1.PrivateKey
		if counter == nil {
			secret, r, err = generateRandomSecret()
			if err != nil {
				return nil, nil, nil, err
			}
		} else {
			secret, r, err = generateDeterministicSecret(keysetDerivationPath, *counter)
			if err != nil {
				return nil, nil, nil, err
			}
			*counter++
		}

		B_, r, err := crypto.BlindMessage(secret, r)
		if err != nil {
			return nil, nil, nil, err
		}

		blindedMessages[i] = cashu.NewBlindedMessage(keysetId, amt, B_)
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}

func generateRandomSecret() (string, *secp256k1.PrivateKey, error) {
	r, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return "", nil, err
	}

	secretBytes := make([]byte, 32)
	_, err = rand.Read(secretBytes)
	if err != nil {
		return "", nil, err
	}
	secret := hex.EncodeToString(secretBytes)

	return secret, r, nil
}

func generateDeterministicSecret(path *hdkeychain.ExtendedKey, counter uint32) (
	string,
	*secp256k1.PrivateKey,
	error,
) {
	r, err := nut13.DeriveBlindingFactor(path, counter)
	if err != nil {
		return "", nil, err
	}

	secret, err := nut13.DeriveSecret(path, counter)
	if err != nil {
		return "", nil, err
	}

	return secret, r, nil
}

func blindedMessagesFromLock(splitAmounts []uint64, keysetId string, lockPubkey *btcec.PublicKey) (
	cashu.BlindedMessages,
	[]string,
	[]*secp256k1.PrivateKey,
	error,
) {
	serialized := lockPubkey.SerializeCompressed()
	pubkey := hex.EncodeToString(serialized)

	splitLen := len(splitAmounts)
	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)
	for i, amt := range splitAmounts {
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		secret, err := nut11.P2PKSecret(pubkey, nut11.P2PKTags{})
		if err != nil {
			return nil, nil, nil, err
		}

		B_, r, err := crypto.BlindMessage(secret, r)
		if err != nil {
			return nil, nil, nil, err
		}

		blindedMessages[i] = cashu.NewBlindedMessage(keysetId, amt, B_)
		secrets[i] = string(secret)
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}

// constructProofs unblinds the blindedSignatures and returns the proofs
func constructProofs(
	blindedSignatures cashu.BlindedSignatures,
	secrets []string,
	rs []*secp256k1.PrivateKey,
	keyset *crypto.WalletKeyset,
) (cashu.Proofs, error) {

	if len(blindedSignatures) != len(secrets) || len(blindedSignatures) != len(rs) {
		return nil, errors.New("lengths do not match")
	}

	proofs := make(cashu.Proofs, len(blindedSignatures))
	for i, blindedSignature := range blindedSignatures {
		pubkey, ok := keyset.PublicKeys[blindedSignature.Amount]
		if !ok {
			return nil, errors.New("key not found")
		}

		C, err := unblindSignature(blindedSignature.C_, rs[i], pubkey)
		if err != nil {
			return nil, err
		}

		proof := cashu.Proof{
			Amount: blindedSignature.Amount,
			Secret: secrets[i],
			C:      C,
			Id:     blindedSignature.Id,
		}
		proofs[i] = proof
	}

	return proofs, nil
}

func unblindSignature(C_str string, r *secp256k1.PrivateKey, key *secp256k1.PublicKey) (
	string,
	error,
) {
	C_bytes, err := hex.DecodeString(C_str)
	if err != nil {
		return "", err
	}
	C_, err := secp256k1.ParsePubKey(C_bytes)
	if err != nil {
		return "", err
	}

	C := crypto.UnblindSignature(C_, r, key)
	Cstr := hex.EncodeToString(C.SerializeCompressed())
	return Cstr, nil
}

func (w *Wallet) incrementKeysetCounter(keysetId string, num uint32) error {
	err := w.db.IncrementKeysetCounter(keysetId, num)
	if err != nil {
		return err
	}
	return nil
}

// keyset passed should exist in wallet
func (w *Wallet) counterForKeyset(keysetId string) uint32 {
	return w.db.GetKeysetCounter(keysetId)
}

// getActiveSatKeyset returns the active sat keyset for the mint passed.
// if mint passed is known and the latest active sat keyset has changed,
// it will inactivate the previous active and save new active to db
func (w *Wallet) getActiveSatKeyset(mintURL string) (*crypto.WalletKeyset, error) {
	mint, ok := w.mints[mintURL]
	// if mint is not known, get active sat keyset from calling mint
	if !ok {
		activeKeysets, err := GetMintActiveKeysets(mintURL)
		if err != nil {
			return nil, err
		}
		for _, keyset := range activeKeysets {
			return &keyset, nil
		}
	}

	// get the latest active keyset
	var activeKeyset crypto.WalletKeyset
	for _, keyset := range mint.activeKeysets {
		activeKeyset = keyset
		break
	}

	allKeysets, err := GetAllKeysets(mintURL)
	if err != nil {
		return nil, err
	}

	// check if there is new active keyset
	activeChanged := true
	for _, keyset := range allKeysets.Keysets {
		if keyset.Active && keyset.Id == activeKeyset.Id {
			activeChanged = false
			break
		}
	}

	// if new active, save it to db and inactivate previous
	if activeChanged {
		// inactivate previous active
		activeKeyset.Active = false
		w.db.SaveKeyset(&activeKeyset)
		w.mints[mintURL].inactiveKeysets[activeKeyset.Id] = activeKeyset
		delete(w.mints[mintURL].activeKeysets, activeKeyset.Id)

		for _, keyset := range allKeysets.Keysets {
			_, err = hex.DecodeString(keyset.Id)
			if keyset.Active && keyset.Unit == "sat" && err == nil {
				keysetKeys, err := GetKeysetById(mintURL, keyset.Id)
				if err != nil {
					return nil, err
				}

				keys, err := crypto.MapPubKeys(keysetKeys.Keysets[0].Keys)
				if err != nil {
					return nil, err
				}

				activeKeyset = crypto.WalletKeyset{
					Id:          keyset.Id,
					MintURL:     mintURL,
					Unit:        keyset.Unit,
					Active:      true,
					PublicKeys:  keys,
					InputFeePpk: keyset.InputFeePpk,
				}

				w.db.SaveKeyset(&activeKeyset)
				w.mints[mintURL].activeKeysets[keyset.Id] = activeKeyset
			}
		}
	}

	return &activeKeyset, nil
}

func (w *Wallet) getWalletMints() (map[string]walletMint, error) {
	walletMints := make(map[string]walletMint)

	keysets := w.db.GetKeysets()
	for k, mintKeysets := range keysets {
		activeKeysets := make(map[string]crypto.WalletKeyset)
		inactiveKeysets := make(map[string]crypto.WalletKeyset)
		for _, keyset := range mintKeysets {
			// ignore keysets with non-hex id
			_, err := hex.DecodeString(keyset.Id)
			if err != nil {
				continue
			}

			if len(keyset.PublicKeys) == 0 {
				publicKeys, err := getKeysetKeys(keyset.MintURL, keyset.Id)
				if err != nil {
					return nil, err
				}
				keyset.PublicKeys = publicKeys
				w.db.SaveKeyset(&keyset)
			}

			if keyset.Active {
				activeKeysets[keyset.Id] = keyset
			} else {
				inactiveKeysets[keyset.Id] = keyset
			}
		}

		walletMints[k] = walletMint{
			mintURL:         k,
			activeKeysets:   activeKeysets,
			inactiveKeysets: inactiveKeysets,
		}
	}

	return walletMints, nil
}

func getKeysetKeys(mintURL, id string) (map[uint64]*secp256k1.PublicKey, error) {
	keysetsResponse, err := GetKeysetById(mintURL, id)
	if err != nil {
		return nil, fmt.Errorf("error getting keyset from mint: %v", err)
	}

	var keys map[uint64]*secp256k1.PublicKey
	if len(keysetsResponse.Keysets) > 0 && keysetsResponse.Keysets[0].Unit == "sat" {
		var err error
		keys, err = crypto.MapPubKeys(keysetsResponse.Keysets[0].Keys)
		if err != nil {
			return nil, err
		}
	}

	return keys, nil
}

// CurrentMint returns the current mint url
func (w *Wallet) CurrentMint() string {
	return w.currentMint.mintURL
}

func (w *Wallet) TrustedMints() []string {
	trustedMints := make([]string, len(w.mints))

	i := 0
	for mintURL := range w.mints {
		trustedMints[i] = mintURL
		i++
	}
	return trustedMints
}

// GetReceivePubkey retrieves public key to which
// the wallet can receive locked ecash
func (w *Wallet) GetReceivePubkey() *btcec.PublicKey {
	return w.privateKey.PubKey()
}

func (w *Wallet) Mnemonic() string {
	return w.db.GetMnemonic()
}

func Restore(walletPath, mnemonic string, mintsToRestore []string) (cashu.Proofs, error) {
	// check if wallet db already exists, if there is one, throw error.
	dbpath := filepath.Join(walletPath, "wallet.db")
	_, err := os.Stat(dbpath)
	if err == nil {
		return nil, errors.New("wallet already exists")
	}

	// check mnemonic is valid
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, errors.New("invalid mnemonic")
	}

	// create wallet db
	db, err := InitStorage(walletPath)
	if err != nil {
		return nil, fmt.Errorf("error restoring wallet: %v", err)
	}

	seed := bip39.NewSeed(mnemonic, "")
	// get master key from seed
	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, err
	}
	db.SaveMnemonicSeed(mnemonic, seed)

	proofsRestored := cashu.Proofs{}

	// for each mint get the keysets and do restore process for each keyset
	for _, mint := range mintsToRestore {
		mintInfo, err := GetMintInfo(mint)
		if err != nil {
			return nil, fmt.Errorf("error getting info from mint: %v", err)
		}

		nut7 := mintInfo.Nuts[7].(map[string]interface{})
		nut9 := mintInfo.Nuts[9].(map[string]interface{})
		if nut7["supported"] != true || nut9["supported"] != true {
			fmt.Println("mint does not support the necessary operations to restore wallet")
			continue
		}

		// call to get mint keysets
		keysetsResponse, err := GetAllKeysets(mint)
		if err != nil {
			return nil, err
		}

		for _, keyset := range keysetsResponse.Keysets {
			if keyset.Unit != "sat" {
				break
			}

			_, err := hex.DecodeString(keyset.Id)
			// ignore keysets with non-hex ids
			if err != nil {
				continue
			}

			var counter uint32 = 0

			keysetKeys, err := getKeysetKeys(mint, keyset.Id)
			if err != nil {
				return nil, err
			}

			walletKeyset := crypto.WalletKeyset{
				Id:         keyset.Id,
				MintURL:    mint,
				Unit:       keyset.Unit,
				Active:     keyset.Active,
				PublicKeys: keysetKeys,
				Counter:    counter,
			}

			if err := db.SaveKeyset(&walletKeyset); err != nil {
				return nil, err
			}

			keysetDerivationPath, err := nut13.DeriveKeysetPath(masterKey, keyset.Id)
			if err != nil {
				return nil, err
			}

			// stop when it reaches 3 consecutive empty batches
			emptyBatches := 0
			for emptyBatches < 3 {
				blindedMessages := make(cashu.BlindedMessages, 100)
				rs := make([]*secp256k1.PrivateKey, 100)
				secrets := make([]string, 100)

				// create batch of 100 blinded messages
				for i := 0; i < 100; i++ {
					secret, r, err := generateDeterministicSecret(keysetDerivationPath, counter)
					B_, r, err := crypto.BlindMessage(secret, r)
					if err != nil {
						return nil, err
					}

					B_str := hex.EncodeToString(B_.SerializeCompressed())
					blindedMessages[i] = cashu.BlindedMessage{B_: B_str, Id: keyset.Id}
					rs[i] = r
					secrets[i] = secret
					counter++
				}

				// if response has signatures, unblind them and check proof states
				restoreRequest := nut09.PostRestoreRequest{Outputs: blindedMessages}
				restoreResponse, err := PostRestore(mint, restoreRequest)
				if err != nil {
					return nil, fmt.Errorf("error restoring signatures from mint '%v': %v", mint, err)
				}

				if len(restoreResponse.Signatures) == 0 {
					emptyBatches++
					break
				}

				Ys := make([]string, len(restoreResponse.Signatures))
				proofs := make(map[string]cashu.Proof, len(restoreResponse.Signatures))

				// unblind signatures
				for i, signature := range restoreResponse.Signatures {
					pubkey, ok := keysetKeys[signature.Amount]
					if !ok {
						return nil, errors.New("key not found")
					}

					C, err := unblindSignature(signature.C_, rs[i], pubkey)
					if err != nil {
						return nil, err
					}

					Y, err := crypto.HashToCurve([]byte(secrets[i]))
					if err != nil {
						return nil, err
					}
					Yhex := hex.EncodeToString(Y.SerializeCompressed())
					Ys[i] = Yhex

					proof := cashu.Proof{
						Amount: signature.Amount,
						Secret: secrets[i],
						C:      C,
						Id:     signature.Id,
					}
					proofs[Yhex] = proof
				}

				proofStateRequest := nut07.PostCheckStateRequest{Ys: Ys}
				proofStateResponse, err := PostCheckProofState(mint, proofStateRequest)
				if err != nil {
					return nil, err
				}

				for _, proofState := range proofStateResponse.States {
					// NUT-07 can also respond with witness data. Since not supporting this yet, ignore proofs that have witness
					if len(proofState.Witness) > 0 {
						break
					}

					// save unspent proofs
					if proofState.State == nut07.Unspent {
						proof := proofs[proofState.Y]
						db.SaveProof(proof)
						proofsRestored = append(proofsRestored, proof)
					}
				}

				// save wallet keyset with latest counter moving forward for wallet
				if err := db.IncrementKeysetCounter(keyset.Id, counter); err != nil {
					return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
				}
				emptyBatches = 0
			}
		}
	}

	return proofsRestored, nil
}

func (w *Wallet) saveProofs(proofs cashu.Proofs) error {
	for _, proof := range proofs {
		err := w.db.SaveProof(proof)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Wallet) GetInvoiceByPaymentRequest(pr string) (*storage.Invoice, error) {
	bolt11, err := decodepay.Decodepay(pr)
	if err != nil {
		return nil, fmt.Errorf("invalid payment request: %v", err)
	}

	return w.db.GetInvoice(bolt11.PaymentHash), nil
}

func (w *Wallet) GetInvoiceByPaymentHash(hash string) *storage.Invoice {
	return w.db.GetInvoice(hash)
}

func (w *Wallet) GetAllInvoices() []storage.Invoice {
	return w.db.GetInvoices()
}
