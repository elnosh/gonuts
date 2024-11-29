package wallet

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
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
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
	"github.com/elnosh/gonuts/cashu/nuts/nut12"
	"github.com/elnosh/gonuts/cashu/nuts/nut13"
	"github.com/elnosh/gonuts/cashu/nuts/nut14"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/wallet/storage"
	"github.com/tyler-smith/go-bip39"

	decodepay "github.com/nbd-wtf/ln-decodepay"
)

var (
	ErrMintNotExist            = errors.New("mint does not exist")
	ErrInsufficientMintBalance = errors.New("not enough funds in selected mint")
	ErrQuoteNotFound           = errors.New("quote not found")
)

type Wallet struct {
	db          storage.WalletDB
	unit        cashu.Unit
	defaultMint string
	masterKey   *hdkeychain.ExtendedKey

	// key to receive locked ecash
	privateKey *btcec.PrivateKey

	// list of mints that have been trusted
	mints map[string]walletMint
}

type walletMint struct {
	mintURL         string
	activeKeyset    crypto.WalletKeyset
	inactiveKeysets map[string]crypto.WalletKeyset
}

type Config struct {
	WalletPath     string
	CurrentMintURL string
}

func InitStorage(path string) (storage.WalletDB, error) {
	// bolt db atm
	return storage.InitBolt(path)
}

func LoadWallet(config Config) (*Wallet, error) {
	path := config.WalletPath
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}

	db, err := InitStorage(path)
	if err != nil {
		return nil, fmt.Errorf("InitStorage: %v", err)
	}

	seed := db.GetSeed()
	if len(seed) == 0 {
		// create and save new seed if none existed previously
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

	wallet := &Wallet{db: db, unit: cashu.Sat, masterKey: masterKey, privateKey: privateKey}
	wallet.mints, err = wallet.getWalletMints()
	if err != nil {
		return nil, err
	}
	url, err := url.Parse(config.CurrentMintURL)
	if err != nil {
		return nil, fmt.Errorf("invalid mint url: %v", err)
	}
	mintURL := url.String()
	wallet.defaultMint = mintURL

	_, ok := wallet.mints[mintURL]
	if !ok {
		// if mint is new, add it
		_, err := wallet.AddMint(mintURL)
		if err != nil {
			return nil, fmt.Errorf("error adding new mint: %v", err)
		}
	} else {
		// if mint is known, check if active keyset has changed
		_, err := wallet.getActiveKeyset(mintURL)
		if err != nil {
			return nil, err
		}
	}

	return wallet, nil
}

// AddMint adds the mint to the list of mints trusted by the wallet
func (w *Wallet) AddMint(mint string) (*walletMint, error) {
	url, err := url.Parse(mint)
	if err != nil {
		return nil, fmt.Errorf("invalid mint url: %v", err)
	}
	mintURL := url.String()

	activeKeyset, err := GetMintActiveKeyset(mintURL, w.unit)
	if err != nil {
		return nil, err
	}

	inactiveKeysets, err := GetMintInactiveKeysets(mintURL, w.unit)
	if err != nil {
		return nil, err
	}

	if err := w.db.SaveKeyset(activeKeyset); err != nil {
		return nil, err
	}
	for _, keyset := range inactiveKeysets {
		if err := w.db.SaveKeyset(&keyset); err != nil {
			return nil, err
		}
	}
	newWalletMint := walletMint{mintURL, *activeKeyset, inactiveKeysets}
	w.mints[mintURL] = newWalletMint

	return &newWalletMint, nil
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
		proofs := w.db.GetProofsByKeysetId(mint.activeKeyset.Id)
		mintBalance := proofs.Amount()

		for _, keyset := range mint.inactiveKeysets {
			proofs := w.db.GetProofsByKeysetId(keyset.Id)
			mintBalance += proofs.Amount()
		}

		mintsBalances[mint.mintURL] = mintBalance
	}

	return mintsBalances
}

func (w *Wallet) PendingBalance() uint64 {
	return amount(w.db.GetPendingProofs())
}

func amount(proofs []storage.DBProof) uint64 {
	var totalAmount uint64 = 0
	for _, proof := range proofs {
		totalAmount += proof.Amount
	}
	return totalAmount
}

// RequestMint requests a mint quote to the mint for the specified amount
func (w *Wallet) RequestMint(amount uint64, mint string) (*nut04.PostMintQuoteBolt11Response, error) {
	selectedMint, ok := w.mints[mint]
	if !ok {
		return nil, ErrMintNotExist
	}

	mintRequest := nut04.PostMintQuoteBolt11Request{Amount: amount, Unit: w.unit.String()}
	mintResponse, err := PostMintQuoteBolt11(selectedMint.mintURL, mintRequest)
	if err != nil {
		return nil, err
	}

	bolt11, err := decodepay.Decodepay(mintResponse.Request)
	if err != nil {
		return nil, fmt.Errorf("error decoding bolt11 invoice: %v", err)
	}

	quote := storage.MintQuote{
		QuoteId:        mintResponse.Quote,
		Mint:           selectedMint.mintURL,
		Method:         cashu.BOLT11_METHOD,
		State:          mintResponse.State,
		Unit:           w.unit.String(),
		Amount:         amount,
		PaymentRequest: mintResponse.Request,
		CreatedAt:      int64(bolt11.CreatedAt),
		QuoteExpiry:    mintResponse.Expiry,
	}
	if err := w.db.SaveMintQuote(quote); err != nil {
		return nil, fmt.Errorf("error saving mint quote: %v", err)
	}

	return mintResponse, nil
}

func (w *Wallet) MintQuoteState(quoteId string) (*nut04.PostMintQuoteBolt11Response, error) {
	quote := w.db.GetMintQuoteById(quoteId)
	if quote == nil {
		return nil, ErrQuoteNotFound
	}

	mint := quote.Mint
	if len(quote.Mint) == 0 {
		mint = w.defaultMint
		quote.Mint = mint
	}

	if quote.State == nut04.Issued {
		return &nut04.PostMintQuoteBolt11Response{
			Quote:   quote.QuoteId,
			Request: quote.PaymentRequest,
			State:   quote.State,
			Expiry:  quote.QuoteExpiry,
		}, nil
	}

	mintQuote, err := GetMintQuoteState(mint, quoteId)
	if err != nil {
		return nil, err
	}
	quote.State = mintQuote.State
	if mintQuote.State == nut04.Issued {
		quote.SettledAt = time.Now().Unix()
	}

	if err := w.db.SaveMintQuote(*quote); err != nil {
		return nil, fmt.Errorf("error saving mint quote: %v", err)
	}

	return mintQuote, nil
}

// MintTokens will check whether if the mint quote has been paid.
// If yes, it will create blinded messages that will send to the mint
// to get the blinded signatures.
// If successful, it will unblind the signatures to generate proofs
// and store the proofs in the db.
func (w *Wallet) MintTokens(quoteId string) (uint64, error) {
	quote := w.db.GetMintQuoteById(quoteId)
	if quote == nil {
		return 0, ErrQuoteNotFound
	}

	mint := quote.Mint
	if len(quote.Mint) == 0 {
		mint = w.defaultMint
		quote.Mint = mint
	}

	mintQuote, err := w.MintQuoteState(quoteId)
	if err != nil {
		return 0, err
	}
	if mintQuote.State == nut04.Unpaid {
		return 0, errors.New("payment request has not paid")
	}
	if mintQuote.State == nut04.Issued {
		return 0, errors.New("quote has already been issued")
	}

	activeKeyset, err := w.getActiveKeyset(mint)
	if err != nil {
		return 0, fmt.Errorf("error getting active sat keyset: %v", err)
	}
	// get counter for keyset
	counter := w.counterForKeyset(activeKeyset.Id)

	split := w.splitWalletTarget(quote.Amount, mint)
	blindedMessages, secrets, rs, err := w.createBlindedMessages(split, activeKeyset.Id, &counter)
	if err != nil {
		return 0, fmt.Errorf("error creating blinded messages: %v", err)
	}

	// request mint to sign the blinded messages
	postMintRequest := nut04.PostMintBolt11Request{Quote: quoteId, Outputs: blindedMessages}
	mintResponse, err := PostMintBolt11(mint, postMintRequest)
	if err != nil {
		return 0, err
	}

	// unblind the signatures from the promises and build the proofs
	proofs, err := constructProofs(mintResponse.Signatures, blindedMessages, secrets, rs, activeKeyset)
	if err != nil {
		return 0, fmt.Errorf("error constructing proofs: %v", err)
	}

	// store proofs in db
	if err := w.db.SaveProofs(proofs); err != nil {
		return 0, fmt.Errorf("error storing proofs: %v", err)
	}

	// only increase counter if mint was successful
	if err := w.db.IncrementKeysetCounter(activeKeyset.Id, uint32(len(blindedMessages))); err != nil {
		return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
	}

	quote.State = nut04.Issued
	quote.SettledAt = time.Now().Unix()
	if err = w.db.SaveMintQuote(*quote); err != nil {
		return 0, err
	}

	return proofs.Amount(), nil
}

// Send will return proofs for the given amount
func (w *Wallet) Send(amount uint64, mintURL string, includeFees bool) (cashu.Proofs, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, ErrMintNotExist
	}

	proofsToSend, err := w.getProofsForAmount(amount, &selectedMint, includeFees)
	if err != nil {
		return nil, err
	}

	if err := w.db.AddPendingProofs(proofsToSend); err != nil {
		return nil, fmt.Errorf("could not save proofs to pending: %v\n", err)
	}

	return proofsToSend, nil
}

// SendToPubkey returns proofs that are locked to the passed pubkey
func (w *Wallet) SendToPubkey(
	amount uint64,
	mintURL string,
	pubkey *btcec.PublicKey,
	tags *nut11.P2PKTags,
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
	nut11Info, ok := mintInfo.Nuts[11].(map[string]interface{})
	if !ok || nut11Info["supported"] != true {
		return nil, errors.New("mint does not support Pay to Public Key")
	}

	if pubkey == nil {
		return nil, errors.New("got nil pubkey")
	}
	hexPubkey := hex.EncodeToString(pubkey.SerializeCompressed())
	serializedTags := [][]string{}
	if tags != nil {
		serializedTags = nut11.SerializeP2PKTags(*tags)
	}
	p2pkSpendingCondition := nut10.SpendingCondition{
		Kind: nut10.P2PK,
		Data: hexPubkey,
		Tags: serializedTags,
	}
	lockedProofs, err := w.swapToSend(amount, &selectedMint, &p2pkSpendingCondition, includeFees)
	if err != nil {
		return nil, err
	}

	return lockedProofs, nil
}

// HTLCLockedProofs returns proofs that are locked to the hash of the preimage
func (w *Wallet) HTLCLockedProofs(
	amount uint64,
	mintURL string,
	preimage string,
	tags *nut11.P2PKTags,
	includeFees bool,
) (cashu.Proofs, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, ErrMintNotExist
	}

	// check first if mint supports HTLC NUT
	mintInfo, err := GetMintInfo(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting info from mint: %v", err)
	}
	nut14, ok := mintInfo.Nuts[14].(map[string]interface{})
	if !ok || nut14["supported"] != true {
		return nil, errors.New("mint does not support HTLCs")
	}

	preimageBytes, err := hex.DecodeString(preimage)
	if err != nil {
		return nil, fmt.Errorf("invalid preimage: %v", err)
	}
	hashBytes := sha256.Sum256(preimageBytes)
	hash := hex.EncodeToString(hashBytes[:])

	serializedTags := [][]string{}
	if tags != nil {
		serializedTags = nut11.SerializeP2PKTags(*tags)
	}
	htlcSpendingCondition := nut10.SpendingCondition{
		Kind: nut10.HTLC,
		Data: hash,
		Tags: serializedTags,
	}
	lockedProofs, err := w.swapToSend(amount, &selectedMint, &htlcSpendingCondition, includeFees)
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

	keyset, err := w.getActiveKeyset(tokenMint)
	if err != nil {
		return 0, fmt.Errorf("could not get active keyset: %v", err)
	}

	// verify DLEQ in proofs if present
	if !nut12.VerifyProofsDLEQ(proofsToSwap, *keyset) {
		return 0, errors.New("invalid DLEQ proof")
	}

	// if P2PK, add signature to Witness in the proofs
	nut10Secret, err := nut10.DeserializeSecret(proofsToSwap[0].Secret)
	if err == nil && nut10Secret.Kind == nut10.P2PK {
		// check that public key in data is one wallet can sign for
		if !nut11.CanSign(nut10Secret, w.privateKey) {
			return 0, fmt.Errorf("cannot sign locked proofs")
		}
		proofsToSwap, err = nut11.AddSignatureToInputs(proofsToSwap, w.privateKey)
		if err != nil {
			return 0, fmt.Errorf("error signing inputs: %v", err)
		}
	}

	// if mint in token is already the default mint, do not swap to trusted
	if _, ok := w.mints[tokenMint]; ok && tokenMint == w.defaultMint {
		swapToTrusted = false
	}

	if swapToTrusted {
		inactiveKeysets, err := GetMintInactiveKeysets(tokenMint, w.unit)
		if err != nil {
			return 0, err
		}
		mint := &walletMint{mintURL: tokenMint, activeKeyset: *keyset, inactiveKeysets: inactiveKeysets}
		amountSwapped, err := w.swapToTrusted(proofsToSwap, mint)
		if err != nil {
			return 0, fmt.Errorf("error swapping token to trusted mint: %v", err)
		}
		return amountSwapped, nil
	} else {
		// only add mint if not previously trusted
		mint, ok := w.mints[tokenMint]
		if !ok {
			newMint, err := w.AddMint(tokenMint)
			if err != nil {
				return 0, err
			}
			mint = *newMint
		}

		req, err := w.createSwapRequest(proofsToSwap, &mint)
		if err != nil {
			return 0, fmt.Errorf("could not create swap request: %v", err)
		}

		//if P2PK locked ecash has `SIG_ALL` flag, sign outputs
		if nut10Secret.Kind == nut10.P2PK && nut11.IsSigAll(nut10Secret) {
			req.outputs, err = nut11.AddSignatureToOutputs(req.outputs, w.privateKey)
			if err != nil {
				return 0, fmt.Errorf("error signing outputs: %v", err)
			}
		}

		newProofs, err := swap(tokenMint, req)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}

		err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))
		if err != nil {
			return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
		}

		if err := w.db.SaveProofs(newProofs); err != nil {
			return 0, fmt.Errorf("error storing proofs: %v", err)
		}
		return newProofs.Amount(), nil
	}
}

// ReceiveHTLC will add the preimage and any signatures if needed in order to redeem the
// locked ecash. If successful, it will make a swap and store the new proofs.
// It will add the mint in the token to the list of trusted mints.
func (w *Wallet) ReceiveHTLC(token cashu.Token, preimage string) (uint64, error) {
	proofs := token.Proofs()
	tokenMint := token.Mint()

	keyset, err := w.getActiveKeyset(tokenMint)
	if err != nil {
		return 0, fmt.Errorf("could not get active keyset: %v", err)
	}
	// verify DLEQ in proofs if present
	if !nut12.VerifyProofsDLEQ(proofs, *keyset) {
		return 0, errors.New("invalid DLEQ proof")
	}

	nut10Secret, err := nut10.DeserializeSecret(proofs[0].Secret)
	if err == nil && nut10Secret.Kind == nut10.HTLC {
		proofs, err = nut14.AddWitnessHTLC(proofs, nut10Secret, preimage, w.privateKey)
		if err != nil {
			return 0, fmt.Errorf("could not add HTLC witness: %v", err)
		}

		// only add mint if not previously trusted
		mint, ok := w.mints[tokenMint]
		if !ok {
			newMint, err := w.AddMint(tokenMint)
			if err != nil {
				return 0, err
			}
			mint = *newMint
		}

		req, err := w.createSwapRequest(proofs, &mint)
		if err != nil {
			return 0, fmt.Errorf("could not create swap request: %v", err)
		}

		//if `SIG_ALL` flag, sign outputs
		if nut11.IsSigAll(nut10Secret) {
			req.outputs, err = nut14.AddWitnessHTLCToOutputs(req.outputs, preimage, w.privateKey)
			if err != nil {
				return 0, fmt.Errorf("could not add HTLC witness to outputs: %v", err)
			}
		}

		newProofs, err := swap(tokenMint, req)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}

		err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))
		if err != nil {
			return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
		}

		if err := w.db.SaveProofs(newProofs); err != nil {
			return 0, fmt.Errorf("error storing proofs: %v", err)
		}
		return newProofs.Amount(), nil
	}

	return 0, errors.New("ecash does not have an HTLC spending condition")
}

type swapRequestPayload struct {
	inputs  cashu.Proofs
	outputs cashu.BlindedMessages
	secrets []string
	rs      []*secp256k1.PrivateKey
	// keyset to be used to unblind signatures after swap
	keyset *crypto.WalletKeyset
}

func (w *Wallet) createSwapRequest(proofs cashu.Proofs, mint *walletMint) (swapRequestPayload, error) {
	keysetCounter := w.counterForKeyset(mint.activeKeyset.Id)

	fees := feesForProofs(proofs, mint)
	split := w.splitWalletTarget(proofs.Amount()-uint64(fees), mint.mintURL)
	outputs, secrets, rs, err := w.createBlindedMessages(split, mint.activeKeyset.Id, &keysetCounter)
	if err != nil {
		return swapRequestPayload{}, fmt.Errorf("createBlindedMessages: %v", err)
	}

	return swapRequestPayload{
		inputs:  proofs,
		outputs: outputs,
		secrets: secrets,
		rs:      rs,
		keyset:  &mint.activeKeyset,
	}, nil
}

func swap(mint string, swapRequest swapRequestPayload) (cashu.Proofs, error) {
	request := nut03.PostSwapRequest{
		Inputs:  swapRequest.inputs,
		Outputs: swapRequest.outputs,
	}
	swapResponse, err := PostSwap(mint, request)
	if err != nil {
		return nil, err
	}

	// unblind signatures to get proofs
	proofs, err := constructProofs(
		swapResponse.Signatures,
		swapRequest.outputs,
		swapRequest.secrets,
		swapRequest.rs,
		swapRequest.keyset,
	)
	if err != nil {
		return nil, fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	return proofs, nil
}

// swapToTrusted will swap the proofs from mint
// to the wallet's configured default mint
func (w *Wallet) swapToTrusted(proofs cashu.Proofs, mint *walletMint) (uint64, error) {
	proofsToSwap := proofs

	// if proofs are P2PK locked and sig all, add signatures to swap them first and then melt
	nut10Secret, err := nut10.DeserializeSecret(proofs[0].Secret)
	if err == nil && nut10Secret.Kind == nut10.P2PK && nut11.IsSigAll(nut10Secret) {
		req, err := w.createSwapRequest(proofs, mint)
		if err != nil {
			return 0, fmt.Errorf("could not create swap request: %v", err)
		}
		req.outputs, err = nut11.AddSignatureToOutputs(req.outputs, w.privateKey)
		if err != nil {
			return 0, fmt.Errorf("error signing outputs: %v", err)
		}

		newProofs, err := swap(mint.mintURL, req)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}
		proofsToSwap = newProofs
	}

	defaultMint := w.mints[w.defaultMint]
	amountSwapped, err := w.swapProofs(proofsToSwap, mint, &defaultMint)
	if err != nil {
		return 0, err
	}

	return amountSwapped, nil
}

// RequestMeltQuote will request a melt quote to the mint for the specified request
func (w *Wallet) RequestMeltQuote(request, mint string) (*nut05.PostMeltQuoteBolt11Response, error) {
	_, ok := w.mints[mint]
	if !ok {
		return nil, ErrMintNotExist
	}

	_, err := decodepay.Decodepay(request)
	if err != nil {
		return nil, fmt.Errorf("invalid invoice: %v", err)
	}

	meltRequest := nut05.PostMeltQuoteBolt11Request{Request: request, Unit: w.unit.String()}
	meltQuoteResponse, err := PostMeltQuoteBolt11(mint, meltRequest)
	if err != nil {
		return nil, err
	}

	quote := storage.MeltQuote{
		QuoteId:        meltQuoteResponse.Quote,
		Mint:           mint,
		Method:         cashu.BOLT11_METHOD,
		Unit:           w.unit.String(),
		State:          meltQuoteResponse.State,
		PaymentRequest: request,
		Amount:         meltQuoteResponse.Amount,
		FeeReserve:     meltQuoteResponse.FeeReserve,
		CreatedAt:      time.Now().Unix(),
		QuoteExpiry:    meltQuoteResponse.Expiry,
	}
	if err := w.db.SaveMeltQuote(quote); err != nil {
		return nil, fmt.Errorf("error saving melt quote: %v", err)
	}

	return meltQuoteResponse, nil
}

func (w *Wallet) CheckMeltQuoteState(quoteId string) (*nut05.PostMeltQuoteBolt11Response, error) {
	quote := w.db.GetMeltQuoteById(quoteId)
	if quote == nil {
		return nil, ErrQuoteNotFound
	}

	quoteStateResponse, err := GetMeltQuoteState(quote.Mint, quoteId)
	if err != nil {
		return nil, err
	}

	if quote.State != nut05.Paid {
		// if quote was previously not paid and status has changed, update in db
		if quoteStateResponse.State == nut05.Paid {
			quote.Preimage = quoteStateResponse.Preimage
			quote.SettledAt = time.Now().Unix()
			if err := w.db.SaveMeltQuote(*quote); err != nil {
				return nil, err
			}

			pendingProofs := w.db.GetPendingProofsByQuoteId(quoteId)
			var keysetId string
			if len(pendingProofs) > 0 {
				keysetId = pendingProofs[0].Id
			}
			if err := w.db.DeletePendingProofsByQuoteId(quoteId); err != nil {
				return nil, fmt.Errorf("error removing pending proofs: %v", err)
			}
			change := len(quoteStateResponse.Change)
			// increment the counter if there was change from this quote
			if change > 0 {
				if err := w.db.IncrementKeysetCounter(keysetId, uint32(change)); err != nil {
					return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
				}
			}
		} else if quoteStateResponse.State == nut05.Unpaid {
			pendingProofs := w.db.GetPendingProofsByQuoteId(quoteId)
			// if there were any pending proofs tied to this quote, remove them from pending
			// and add them to available proofs for wallet to use
			pendingProofsLen := len(pendingProofs)
			if pendingProofsLen > 0 {
				proofsToSave := make(cashu.Proofs, pendingProofsLen)
				for i, pendingProof := range pendingProofs {
					proof := cashu.Proof{
						Amount: pendingProof.Amount,
						Id:     pendingProof.Id,
						Secret: pendingProof.Secret,
						C:      pendingProof.C,
						DLEQ:   pendingProof.DLEQ,
					}
					proofsToSave[i] = proof
				}

				if err := w.db.DeletePendingProofsByQuoteId(quoteId); err != nil {
					return nil, fmt.Errorf("error removing pending proofs: %v", err)
				}
				if err := w.db.SaveProofs(proofsToSave); err != nil {
					return nil, fmt.Errorf("error storing proofs: %v", err)
				}
			}

			quote.State = quoteStateResponse.State
			if err := w.db.SaveMeltQuote(*quote); err != nil {
				return nil, err
			}
		}
	}

	return quoteStateResponse, nil
}

// Melt will melt proofs by requesting the mint to pay the
// payment request from the melt quote passed
func (w *Wallet) Melt(quoteId string) (*nut05.PostMeltQuoteBolt11Response, error) {
	quote := w.db.GetMeltQuoteById(quoteId)
	if quote == nil {
		return nil, ErrQuoteNotFound
	}
	if quote.State == nut05.Paid {
		return nil, errors.New("request is already paid")
	}
	if quote.State == nut05.Pending {
		// if quote was previously pending, check if state has changed
		meltState, err := w.CheckMeltQuoteState(quoteId)
		if err != nil {
			return nil, fmt.Errorf("error checking state of quote: %v", err)
		}

		if meltState.State == nut05.Pending {
			return nil, fmt.Errorf("quote is still pending")
		} else if meltState.State == nut05.Paid {
			return nil, errors.New("request is already paid")
		}
	}

	mint := w.mints[quote.Mint]

	amountNeeded := quote.Amount + quote.FeeReserve
	proofs, err := w.getProofsForAmount(amountNeeded, &mint, true)
	if err != nil {
		return nil, err
	}

	// set proofs to pending
	if err := w.db.AddPendingProofsByQuoteId(proofs, quote.QuoteId); err != nil {
		return nil, fmt.Errorf("error saving pending proofs: %v", err)
	}

	activeKeyset, err := w.getActiveKeyset(mint.mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active sat keyset: %v", err)
	}
	counter := w.counterForKeyset(activeKeyset.Id)

	// NUT-08 include blank outputs in request for overpaid lightning fees
	numBlankOutputs := calculateBlankOutputs(quote.FeeReserve)
	split := make([]uint64, numBlankOutputs)
	outputs, outputsSecrets, outputsRs, err := w.createBlindedMessages(split, activeKeyset.Id, &counter)
	if err != nil {
		return nil, fmt.Errorf("error generating blinded messages for change: %v", err)
	}

	meltBolt11Request := nut05.PostMeltBolt11Request{
		Quote:   quote.QuoteId,
		Inputs:  proofs,
		Outputs: outputs,
	}
	meltBolt11Response, err := PostMeltBolt11(mint.mintURL, meltBolt11Request)
	if err != nil {
		// if there was error with melt, remove proofs from pending and save them for use
		if err := w.db.SaveProofs(proofs); err != nil {
			return nil, fmt.Errorf("error storing proofs: %v", err)
		}
		if err := w.db.DeletePendingProofsByQuoteId(quote.QuoteId); err != nil {
			return nil, fmt.Errorf("error removing pending proofs: %v", err)
		}
		return nil, err
	}

	switch meltBolt11Response.State {
	case nut05.Unpaid:
		// if quote is unpaid, remove proofs from pending and add them
		// to proofs available
		if err := w.db.SaveProofs(proofs); err != nil {
			return nil, fmt.Errorf("error storing proofs: %v", err)
		}
		if err := w.db.DeletePendingProofsByQuoteId(quote.QuoteId); err != nil {
			return nil, fmt.Errorf("error removing pending proofs: %v", err)
		}
	case nut05.Pending:
		quote.State = nut05.Pending
		if err := w.db.SaveMeltQuote(*quote); err != nil {
			return nil, fmt.Errorf("error updating melt quote: %v", err)
		}

	case nut05.Paid:
		// payment succeeded so remove proofs from pending
		if err := w.db.DeletePendingProofsByQuoteId(quote.QuoteId); err != nil {
			return nil, fmt.Errorf("error removing pending proofs: %v", err)
		}

		quote.Preimage = meltBolt11Response.Preimage
		quote.State = meltBolt11Response.State
		quote.SettledAt = time.Now().Unix()
		if err := w.db.SaveMeltQuote(*quote); err != nil {
			return nil, err
		}

		change := len(meltBolt11Response.Change)
		// if mint provided blind signtures for any overpaid lightning fees:
		// - unblind them and save the proofs in the db
		// - increment keyset counter in db (by the number of blind sigs provided by mint)
		if change > 0 {
			changeProofs, err := constructProofs(
				meltBolt11Response.Change,
				outputs[:change],
				outputsSecrets[:change],
				outputsRs[:change],
				activeKeyset,
			)
			if err != nil {
				return nil, fmt.Errorf("error unblinding signature from change: %v", err)
			}
			if err := w.db.SaveProofs(changeProofs); err != nil {
				return nil, fmt.Errorf("error storing change proofs: %v", err)
			}
			if err := w.db.IncrementKeysetCounter(activeKeyset.Id, uint32(change)); err != nil {
				return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
			}
		}
	}
	return meltBolt11Response, err
}

// MintSwap will swap the amount from to the specified mint
func (w *Wallet) MintSwap(amount uint64, from, to string) (uint64, error) {
	// check both mints are in list of trusted mints
	fromMint, fromOk := w.mints[from]
	toMint, toOk := w.mints[to]
	if !fromOk || !toOk {
		return 0, ErrMintNotExist
	}

	balanceByMints := w.GetBalanceByMints()
	if balanceByMints[from] < amount {
		return 0, ErrInsufficientMintBalance
	}

	proofsToSwap, err := w.getProofsForAmount(amount, &fromMint, true)
	if err != nil {
		return 0, err
	}

	amountSwapped, err := w.swapProofs(proofsToSwap, &fromMint, &toMint)
	if err != nil {
		return 0, err
	}

	return amountSwapped, nil
}

// swapProofs will swap the proofs in the from mint to specified mint
func (w *Wallet) swapProofs(proofs cashu.Proofs, from, to *walletMint) (uint64, error) {
	var mintResponse *nut04.PostMintQuoteBolt11Response
	var meltQuoteResponse *nut05.PostMeltQuoteBolt11Response
	invoicePct := 0.99
	proofsAmount := proofs.Amount()
	amount := float64(proofsAmount) * invoicePct
	fees := uint64(feesForProofs(proofs, from))
	for {
		// request mint quote to the 'to' mint
		// this will generate an invoice
		mintAmountRequest := uint64(amount) - fees
		var err error
		mintResponse, err = w.RequestMint(mintAmountRequest, to.mintURL)
		if err != nil {
			return 0, fmt.Errorf("error requesting mint quote: %v", err)
		}

		// request melt quote from the 'from' mint
		// this melt will pay the invoice generated from the previous mint quote request
		meltRequest := nut05.PostMeltQuoteBolt11Request{Request: mintResponse.Request, Unit: cashu.Sat.String()}
		meltQuoteResponse, err = PostMeltQuoteBolt11(from.mintURL, meltRequest)
		if err != nil {
			return 0, fmt.Errorf("error with melt request: %v", err)
		}

		// if amount in proofs is less than amount asked from mint in melt request,
		// lower the amount for mint request
		if meltQuoteResponse.Amount+meltQuoteResponse.FeeReserve+fees > proofsAmount {
			invoicePct -= 0.01
			amount *= invoicePct
		} else {
			break
		}
	}

	// request from mint to pay invoice from the mint quote request
	meltBolt11Request := nut05.PostMeltBolt11Request{Quote: meltQuoteResponse.Quote, Inputs: proofs}
	meltBolt11Response, err := PostMeltBolt11(from.mintURL, meltBolt11Request)
	if err != nil {
		return 0, fmt.Errorf("error melting token: %v", err)
	}

	// if melt request was successful and invoice got paid,
	// make mint request to get valid proofs
	if meltBolt11Response.State == nut05.Paid {
		mintedAmount, err := w.MintTokens(mintResponse.Quote)
		if err != nil {
			return 0, fmt.Errorf("error minting tokens: %v", err)
		}
		return mintedAmount, nil
	} else {
		return 0, errors.New("mint could not pay lightning invoice")
	}
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
	return w.db.GetProofsByKeysetId(selectedMint.activeKeyset.Id)
}

// selectProofsForAmount tries to select proofs from inactive keysets (if any) first
// and then proofs from the active keyset
func (w *Wallet) selectProofsForAmount(
	amount uint64,
	mint *walletMint,
	includeFees bool,
) (cashu.Proofs, error) {
	// TODO: need to check first if 'input_fee_ppk' for keyset has changed
	var selectedProofs cashu.Proofs
	var fees uint64 = 0

	inactiveKeysetProofs := w.getInactiveProofsByMint(mint.mintURL)
	// if there are proofs from inactive keysets, select from those first
	if len(inactiveKeysetProofs) > 0 {
		// safe to ignore error here because if proofs aren't enough for the amount
		// will add proofs from active keyset after
		selectedProofs, _ = selectProofsToSend(inactiveKeysetProofs, amount, mint, includeFees)
		if includeFees {
			fees = uint64(feesForProofs(selectedProofs, mint))
		}
	}

	totalAmountNeeded := amount + fees
	selectedAmount := selectedProofs.Amount()
	// return if amount from inactive proofs selected is already enough
	if selectedAmount >= totalAmountNeeded {
		return selectedProofs, nil
	} else {
		remainingAmount := totalAmountNeeded - selectedAmount
		activeKeysetProofs := w.getActiveProofsByMint(mint.mintURL)

		proofsForRemainingAmount, err := selectProofsToSend(activeKeysetProofs, remainingAmount, mint, includeFees)
		if err != nil {
			return nil, err
		}
		selectedProofs = append(selectedProofs, proofsForRemainingAmount...)
	}

	return selectedProofs, nil
}

// selectProofsToSend will try to select proofs for
// amount + fees (if includeFees is true)
func selectProofsToSend(
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
			fees = uint64(feesForProofs(selectedProofs, mint))
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
		fees = uint64(feesForProofs(selectedProofs, mint))
	}

	if selectedProofsSum < amount+fees {
		return nil, fmt.Errorf(
			"insufficient funds for transaction. Amount needed %v + %v(fees) = %v",
			amount, fees, amount+fees)
	}

	return selectedProofs, nil
}

// swapToSend will swap proofs from the wallet to get new proofs for the specified amount.
// If spendingCondition is passed then it creates proofs that are locked to it (P2PK or HTLC).
// If no spendingCondition specified, it returns regular proofs that can be spent by anyone.
func (w *Wallet) swapToSend(
	amount uint64,
	mint *walletMint,
	spendingCondition *nut10.SpendingCondition,
	includeFees bool,
) (cashu.Proofs, error) {
	activeSatKeyset, err := w.getActiveKeyset(mint.mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active sat keyset: %v", err)
	}

	splitForSendAmount := cashu.AmountSplit(amount)
	var feesToReceive uint = 0
	if includeFees {
		feesToReceive = feesForCount(len(splitForSendAmount)+1, activeSatKeyset)
		amount += uint64(feesToReceive)
	}

	proofsToSwap, err := w.selectProofsForAmount(amount, mint, true)
	if err != nil {
		return nil, err
	}

	var send, change cashu.BlindedMessages
	var secrets, changeSecrets []string
	var rs, changeRs []*secp256k1.PrivateKey
	var counter, incrementCounterBy uint32

	split := append(splitForSendAmount, cashu.AmountSplit(uint64(feesToReceive))...)
	slices.Sort(split)
	// if no spendingCondition passed, create blinded messages from counter
	if spendingCondition == nil {
		counter = w.counterForKeyset(activeSatKeyset.Id)
		// blinded messages for send amount from counter
		send, secrets, rs, err = w.createBlindedMessages(split, activeSatKeyset.Id, &counter)
		if err != nil {
			return nil, err
		}
		incrementCounterBy += uint32(len(send))
	} else {
		send, secrets, rs, err = blindedMessagesFromSpendingCondition(split, activeSatKeyset.Id, *spendingCondition)
		if err != nil {
			return nil, err
		}
		counter = w.counterForKeyset(activeSatKeyset.Id)
	}

	proofsAmount := proofsToSwap.Amount()
	fees := feesForProofs(proofsToSwap, mint)
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

	// call swap endpoint
	swapRequest := nut03.PostSwapRequest{Inputs: proofsToSwap, Outputs: blindedMessages}
	swapResponse, err := PostSwap(mint.mintURL, swapRequest)
	if err != nil {
		return nil, err
	}

	for _, proof := range proofsToSwap {
		w.db.DeleteProof(proof.Secret)
	}

	proofsFromSwap, err := constructProofs(swapResponse.Signatures, blindedMessages, secrets, rs, activeSatKeyset)
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
	if err := w.db.SaveProofs(proofsFromSwap); err != nil {
		return nil, fmt.Errorf("error storing proofs: %v", err)
	}

	err = w.db.IncrementKeysetCounter(activeSatKeyset.Id, incrementCounterBy)
	if err != nil {
		return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
	}

	return proofsToSend, nil
}

// getProofsForAmount will return proofs from mint for the given amount.
// It returns error if wallet does not have enough proofs to fulfill amount
func (w *Wallet) getProofsForAmount(
	amount uint64,
	mint *walletMint,
	includeFees bool,
) (cashu.Proofs, error) {
	selectedProofs, err := w.selectProofsForAmount(amount, mint, includeFees)
	if err != nil {
		return nil, err
	}

	var fees uint64 = 0
	if includeFees {
		fees = uint64(feesForProofs(selectedProofs, mint))
	}
	totalAmount := amount + uint64(fees)

	// check if offline selection worked (i.e by checking that amount + fees add up)
	// if proofs stored fulfill amount, delete them from db and return them
	if selectedProofs.Amount() == totalAmount {
		for _, proof := range selectedProofs {
			w.db.DeleteProof(proof.Secret)
		}
		return selectedProofs, nil
	}

	// if offline selection did not work, swap proofs to then send
	proofsToSend, err := w.swapToSend(amount, mint, nil, includeFees)
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
	slices.Sort(amounts)

	return amounts
}

func calculateBlankOutputs(feeReserve uint64) int {
	if feeReserve == 0 {
		return 0
	}
	return int(math.Max(math.Ceil(math.Log2(float64(feeReserve))), 1))
}

func feesForProofs(proofs cashu.Proofs, mint *walletMint) uint {
	var fees uint = 0
	for _, proof := range proofs {
		if mint.activeKeyset.Id == proof.Id {
			fees += mint.activeKeyset.InputFeePpk
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

func blindedMessagesFromSpendingCondition(
	splitAmounts []uint64,
	keysetId string,
	spendingCondition nut10.SpendingCondition,
) (
	cashu.BlindedMessages,
	[]string,
	[]*secp256k1.PrivateKey,
	error,
) {
	splitLen := len(splitAmounts)
	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)
	for i, amt := range splitAmounts {
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		secret, err := nut10.NewSecretFromSpendingCondition(spendingCondition)
		if err != nil {
			return nil, nil, nil, err
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

// constructProofs unblinds the blindedSignatures and returns the proofs
func constructProofs(
	blindedSignatures cashu.BlindedSignatures,
	blindedMessages cashu.BlindedMessages,
	secrets []string,
	rs []*secp256k1.PrivateKey,
	keyset *crypto.WalletKeyset,
) (cashu.Proofs, error) {

	sigsLenght := len(blindedSignatures)
	if sigsLenght != len(secrets) || sigsLenght != len(rs) {
		return nil, errors.New("lengths do not match")
	}

	proofs := make(cashu.Proofs, len(blindedSignatures))
	for i, blindedSignature := range blindedSignatures {
		pubkey, ok := keyset.PublicKeys[blindedSignature.Amount]
		if !ok {
			return nil, errors.New("key not found")
		}

		var dleq *cashu.DLEQProof
		// verify DLEQ if present
		if blindedSignature.DLEQ != nil {
			if !nut12.VerifyBlindSignatureDLEQ(
				*blindedSignature.DLEQ,
				pubkey,
				blindedMessages[i].B_,
				blindedSignature.C_,
			) {
				return nil, errors.New("got blinded signature with invalid DLEQ proof")
			} else {
				dleq = &cashu.DLEQProof{
					E: blindedSignature.DLEQ.E,
					S: blindedSignature.DLEQ.S,
					R: hex.EncodeToString(rs[i].Serialize()),
				}
			}
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
			DLEQ:   dleq,
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

// keyset passed should exist in wallet
func (w *Wallet) counterForKeyset(keysetId string) uint32 {
	return w.db.GetKeysetCounter(keysetId)
}

func (w *Wallet) getWalletMints() (map[string]walletMint, error) {
	walletMints := make(map[string]walletMint)

	keysets := w.db.GetKeysets()
	for k, mintKeysets := range keysets {
		var activeKeyset crypto.WalletKeyset
		inactiveKeysets := make(map[string]crypto.WalletKeyset)
		for _, keyset := range mintKeysets {
			// ignore keysets with non-hex id
			_, err := hex.DecodeString(keyset.Id)
			if err != nil {
				continue
			}

			if len(keyset.PublicKeys) == 0 {
				publicKeys, err := GetKeysetKeys(keyset.MintURL, keyset.Id)
				if err != nil {
					return nil, err
				}
				keyset.PublicKeys = publicKeys
				w.db.SaveKeyset(&keyset)
			}

			if keyset.Active {
				activeKeyset = keyset
			} else {
				inactiveKeysets[keyset.Id] = keyset
			}
		}

		walletMints[k] = walletMint{
			mintURL:         k,
			activeKeyset:    activeKeyset,
			inactiveKeysets: inactiveKeysets,
		}
	}

	return walletMints, nil
}

// CurrentMint returns the current mint url
func (w *Wallet) CurrentMint() string {
	return w.defaultMint
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

func (w *Wallet) pendingProofsByMint() map[string][]storage.DBProof {
	proofsByKeysetId := make(map[string][]storage.DBProof)
	for _, proof := range w.db.GetPendingProofs() {
		proofsByKeysetId[proof.Id] = append(proofsByKeysetId[proof.Id], proof)
	}

	proofsByMint := make(map[string][]storage.DBProof)
	for keysetId, proofs := range proofsByKeysetId {
		for _, mint := range w.mints {
			if mint.activeKeyset.Id == keysetId {
				proofsByMint[mint.mintURL] = append(proofsByMint[mint.mintURL], proofs...)
				break
			}
			for _, inactiveKeyset := range mint.inactiveKeysets {
				if inactiveKeyset.Id == keysetId {
					proofsByMint[mint.mintURL] = append(proofsByMint[mint.mintURL], proofs...)
					break
				}
			}
		}
	}

	return proofsByMint
}

// RemoveSpentProofs will check the state of pending proofs
// and remove the ones in spent state
func (w *Wallet) RemoveSpentProofs() error {
	pendingProofs := w.pendingProofsByMint()

	for mint, proofs := range pendingProofs {
		var Ys []string
		for _, proof := range proofs {
			Ys = append(Ys, proof.Y)
		}

		proofStateRequest := nut07.PostCheckStateRequest{Ys: Ys}
		proofStateResponse, err := PostCheckProofState(mint, proofStateRequest)
		if err != nil {
			return err
		}

		var YsToDelete []string
		for _, state := range proofStateResponse.States {
			if state.State == nut07.Spent {
				YsToDelete = append(YsToDelete, state.Y)
			}
		}

		if err := w.db.DeletePendingProofs(YsToDelete); err != nil {
			return fmt.Errorf("error removing pending proofs: %v", err)
		}
	}

	return nil
}

// ReclaimUnspentProofs will check the state of pending proofs
// and try to reclaim proofs that are in a unspent state
func (w *Wallet) ReclaimUnspentProofs() (uint64, error) {
	pendingProofs := w.pendingProofsByMint()

	var amountReclaimed uint64
	for mintURL, proofs := range pendingProofs {
		var Ys []string
		for _, proof := range proofs {
			Ys = append(Ys, proof.Y)
		}

		proofStateRequest := nut07.PostCheckStateRequest{Ys: Ys}
		proofStateResponse, err := PostCheckProofState(mintURL, proofStateRequest)
		if err != nil {
			return 0, err
		}

		var proofsToReclaim cashu.Proofs
		var pendingYsToDelete []string
		for _, state := range proofStateResponse.States {
			if state.State == nut07.Unspent {
				for _, proof := range proofs {
					if proof.Y == state.Y {
						proofToReclaim := cashu.Proof{
							Amount: proof.Amount,
							Id:     proof.Id,
							Secret: proof.Secret,
							C:      proof.C,
						}
						proofsToReclaim = append(proofsToReclaim, proofToReclaim)
						pendingYsToDelete = append(pendingYsToDelete, proof.Y)
						break
					}
				}
			}
		}

		if len(proofsToReclaim) > 0 {
			mint := w.mints[mintURL]
			req, err := w.createSwapRequest(proofsToReclaim, &mint)
			if err != nil {
				return 0, fmt.Errorf("could not create swap request: %v", err)
			}
			newProofs, err := swap(mintURL, req)
			if err != nil {
				return 0, fmt.Errorf("could not swap proofs: %v", err)
			}
			err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))
			if err != nil {
				return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
			}
			if err := w.db.SaveProofs(newProofs); err != nil {
				return 0, fmt.Errorf("error storing proofs: %v", err)
			}
			if err := w.db.DeletePendingProofs(pendingYsToDelete); err != nil {
				return 0, fmt.Errorf("error removing pending proofs: %v", err)
			}

			amountReclaimed = newProofs.Amount()
		}
	}

	return amountReclaimed, nil
}

// GetPendingMeltQuotes return a list of pending quote ids
func (w *Wallet) GetPendingMeltQuotes() []string {
	pendingProofs := w.db.GetPendingProofs()
	pendingProofsMap := make(map[string][]storage.DBProof)
	var pendingQuotes []string
	for _, proof := range pendingProofs {
		if len(proof.MeltQuoteId) > 0 {
			if _, ok := pendingProofsMap[proof.MeltQuoteId]; !ok {
				pendingQuotes = append(pendingQuotes, proof.MeltQuoteId)
			}
			pendingProofsMap[proof.MeltQuoteId] = append(pendingProofsMap[proof.MeltQuoteId], proof)
		}
	}

	return pendingQuotes
}

func (w *Wallet) GetMintQuotes() []storage.MintQuote {
	return w.db.GetMintQuotes()
}

func (w *Wallet) GetMintQuoteById(id string) *storage.MintQuote {
	return w.db.GetMintQuoteById(id)
}

func (w *Wallet) GetMintQuoteByPaymentRequest(request string) (*storage.MintQuote, error) {
	_, err := decodepay.Decodepay(request)
	if err != nil {
		return nil, fmt.Errorf("invalid payment request: %v", err)
	}

	quotes := w.db.GetMintQuotes()
	for _, quote := range quotes {
		if quote.PaymentRequest == request {
			return &quote, nil
		}
	}

	return nil, errors.New("quote for request does not exist")
}

func (w *Wallet) GetMeltQuotes() []storage.MeltQuote {
	return w.db.GetMeltQuotes()
}

func (w *Wallet) GetMeltQuoteById(id string) *storage.MeltQuote {
	return nil
}
