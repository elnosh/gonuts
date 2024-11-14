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
	db        storage.WalletDB
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
		mint, err := wallet.AddMint(mintURL)
		if err != nil {
			return nil, fmt.Errorf("error adding new mint: %v", err)
		}
		wallet.currentMint = mint
	} else { // if mint is already known, check if active keyset has changed
		// get last stored active sat keyset
		var lastActiveSatKeyset crypto.WalletKeyset
		for _, keyset := range walletMint.activeKeysets {
			_, err := hex.DecodeString(keyset.Id)
			if keyset.Unit == cashu.Sat.String() && err == nil {
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

// AddMint adds the mint to the list of mints trusted by the wallet
func (w *Wallet) AddMint(mint string) (*walletMint, error) {
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
		if keyset.Unit == cashu.Sat.String() && err == nil {
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
		if !keysetRes.Active && keysetRes.Unit == cashu.Sat.String() && err == nil {
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

// RequestMint requests a mint quote to the wallet's current mint
// for the specified amount
func (w *Wallet) RequestMint(amount uint64, mint string) (*nut04.PostMintQuoteBolt11Response, error) {
	selectedMint, ok := w.mints[mint]
	if !ok {
		return nil, ErrMintNotExist
	}

	mintRequest := nut04.PostMintQuoteBolt11Request{Amount: amount, Unit: cashu.Sat.String()}
	mintResponse, err := PostMintQuoteBolt11(selectedMint.mintURL, mintRequest)
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
		Mint:            selectedMint.mintURL,
		PaymentRequest:  mintResponse.Request,
		PaymentHash:     bolt11.PaymentHash,
		CreatedAt:       int64(bolt11.CreatedAt),
		Paid:            false,
		InvoiceAmount:   uint64(bolt11.MSatoshi / 1000),
		QuoteExpiry:     mintResponse.Expiry,
	}

	if err = w.db.SaveInvoice(invoice); err != nil {
		return nil, fmt.Errorf("error saving invoice: %v", err)
	}

	return mintResponse, nil
}

func (w *Wallet) MintQuoteState(quoteId string) (*nut04.PostMintQuoteBolt11Response, error) {
	invoice := w.db.GetInvoiceByQuoteId(quoteId)
	if invoice == nil {
		return nil, ErrQuoteNotFound
	}

	mint := invoice.Mint
	if len(invoice.Mint) == 0 {
		mint = w.currentMint.mintURL
	}
	return GetMintQuoteState(mint, quoteId)
}

// MintTokens will check whether if the mint quote has been paid.
// If yes, it will create blinded messages that will send to the mint
// to get the blinded signatures.
// If successful, it will unblind the signatures to generate proofs
// and store the proofs in the db.
func (w *Wallet) MintTokens(quoteId string) (uint64, error) {
	invoice := w.db.GetInvoiceByQuoteId(quoteId)
	if invoice == nil {
		return 0, ErrQuoteNotFound
	}
	mint := invoice.Mint
	if len(invoice.Mint) == 0 {
		mint = w.currentMint.mintURL
	}

	mintQuote, err := GetMintQuoteState(mint, quoteId)
	if err != nil {
		return 0, err
	}
	// TODO: remove usage of 'Paid' field after mints have upgraded
	if mintQuote.State == nut04.Issued {
		return 0, errors.New("quote has already been issued")
	}
	if !mintQuote.Paid || mintQuote.State == nut04.Unpaid {
		return 0, errors.New("invoice not paid")
	}

	activeKeyset, err := w.getActiveSatKeyset(mint)
	if err != nil {
		return 0, fmt.Errorf("error getting active sat keyset: %v", err)
	}
	// get counter for keyset
	counter := w.counterForKeyset(activeKeyset.Id)

	split := w.splitWalletTarget(invoice.QuoteAmount, mint)
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

	// mark invoice as redeemed
	invoice.Paid = true
	invoice.SettledAt = time.Now().Unix()
	err = w.db.SaveInvoice(*invoice)
	if err != nil {
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

	keyset, err := w.getActiveSatKeyset(tokenMint)
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
	if _, ok := w.mints[tokenMint]; ok && tokenMint == w.currentMint.mintURL {
		swapToTrusted = false
	}

	if swapToTrusted {
		amountSwapped, err := w.swapToTrusted(proofsToSwap, tokenMint)
		if err != nil {
			return 0, fmt.Errorf("error swapping token to trusted mint: %v", err)
		}
		return amountSwapped, nil
	} else {
		// only add mint if not previously trusted
		_, ok := w.mints[tokenMint]
		if !ok {
			_, err := w.AddMint(tokenMint)
			if err != nil {
				return 0, err
			}
		}

		req, err := w.createSwapRequest(proofsToSwap, tokenMint)
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

		newProofs, err := w.swap(tokenMint, req)
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

	keyset, err := w.getActiveSatKeyset(tokenMint)
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
		_, ok := w.mints[tokenMint]
		if !ok {
			_, err := w.AddMint(tokenMint)
			if err != nil {
				return 0, err
			}
		}

		req, err := w.createSwapRequest(proofs, tokenMint)
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

		newProofs, err := w.swap(tokenMint, req)
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

func (w *Wallet) createSwapRequest(proofs cashu.Proofs, mintURL string) (swapRequestPayload, error) {
	var activeKeyset *crypto.WalletKeyset
	var counter *uint32 = nil
	mint, trustedMint := w.mints[mintURL]
	if !trustedMint {
		// get keys if mint not trusted
		var err error
		activeKeysets, err := GetMintActiveKeysets(mintURL)
		if err != nil {
			return swapRequestPayload{}, err
		}
		for _, keyset := range activeKeysets {
			activeKeyset = &keyset
		}
		mint = walletMint{mintURL: mintURL, activeKeysets: activeKeysets}
	} else {
		var err error
		activeKeyset, err = w.getActiveSatKeyset(mintURL)
		if err != nil {
			return swapRequestPayload{}, fmt.Errorf("error getting active sat keyset: %v", err)
		}
		keysetCounter := w.counterForKeyset(activeKeyset.Id)
		counter = &keysetCounter
	}

	fees := feesForProofs(proofs, &mint)
	split := w.splitWalletTarget(proofs.Amount()-uint64(fees), mintURL)
	outputs, secrets, rs, err := w.createBlindedMessages(split, activeKeyset.Id, counter)
	if err != nil {
		return swapRequestPayload{}, fmt.Errorf("createBlindedMessages: %v", err)
	}

	return swapRequestPayload{
		inputs:  proofs,
		outputs: outputs,
		secrets: secrets,
		rs:      rs,
		keyset:  activeKeyset,
	}, nil
}

func (w *Wallet) swap(mint string, swapRequest swapRequestPayload) (cashu.Proofs, error) {
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
func (w *Wallet) swapToTrusted(proofs cashu.Proofs, mintFromProofs string) (uint64, error) {
	proofsToSwap := proofs
	activeKeysets, err := GetMintActiveKeysets(mintFromProofs)
	if err != nil {
		return 0, err
	}
	mint := &walletMint{mintURL: mintFromProofs, activeKeysets: activeKeysets}

	// if proofs are P2PK locked and sig all, add signatures to swap them first and then melt
	nut10Secret, err := nut10.DeserializeSecret(proofs[0].Secret)
	if err == nil && nut10Secret.Kind == nut10.P2PK && nut11.IsSigAll(nut10Secret) {
		req, err := w.createSwapRequest(proofs, mintFromProofs)
		if err != nil {
			return 0, fmt.Errorf("could not create swap request: %v", err)
		}
		req.outputs, err = nut11.AddSignatureToOutputs(req.outputs, w.privateKey)
		if err != nil {
			return 0, fmt.Errorf("error signing outputs: %v", err)
		}

		newProofs, err := w.swap(mintFromProofs, req)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}
		proofsToSwap = newProofs
	}

	amountSwapped, err := w.swapProofs(proofsToSwap, mint, w.currentMint)
	if err != nil {
		return 0, err
	}

	return amountSwapped, nil
}

func (w *Wallet) CheckMeltQuoteState(quoteId string) (*nut05.PostMeltQuoteBolt11Response, error) {
	invoice := w.db.GetInvoiceByQuoteId(quoteId)
	if invoice == nil {
		return nil, ErrQuoteNotFound
	}

	quote, err := GetMeltQuoteState(invoice.Mint, quoteId)
	if err != nil {
		return nil, err
	}

	if !invoice.Paid {
		// if paid status of invoice has changed, update in db
		if quote.State == nut05.Paid || quote.Paid {
			invoice.Paid = true
			invoice.Preimage = quote.Preimage
			invoice.SettledAt = time.Now().Unix()
			if err := w.db.SaveInvoice(*invoice); err != nil {
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
			change := len(quote.Change)
			// increment the counter if there was change from this quote
			if change > 0 {
				if err := w.db.IncrementKeysetCounter(keysetId, uint32(change)); err != nil {
					return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
				}
			}
		}

		if (quote.State == nut05.Unknown && !quote.Paid) || quote.State == nut05.Unpaid {
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
		}
	}

	return quote, nil
}

// Melt will request the mint to pay the given invoice
func (w *Wallet) Melt(invoice, mintURL string) (*nut05.PostMeltQuoteBolt11Response, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, ErrMintNotExist
	}

	bolt11, err := decodepay.Decodepay(invoice)
	if err != nil {
		return nil, fmt.Errorf("error decoding invoice: %v", err)
	}

	meltRequest := nut05.PostMeltQuoteBolt11Request{Request: invoice, Unit: cashu.Sat.String()}
	meltQuoteResponse, err := PostMeltQuoteBolt11(mintURL, meltRequest)
	if err != nil {
		return nil, err
	}

	amountNeeded := meltQuoteResponse.Amount + meltQuoteResponse.FeeReserve
	proofs, err := w.getProofsForAmount(amountNeeded, &selectedMint, true)
	if err != nil {
		return nil, err
	}

	// set proofs to pending
	if err := w.db.AddPendingProofsByQuoteId(proofs, meltQuoteResponse.Quote); err != nil {
		return nil, fmt.Errorf("error saving pending proofs: %v", err)
	}

	activeKeyset, err := w.getActiveSatKeyset(selectedMint.mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active sat keyset: %v", err)
	}
	counter := w.counterForKeyset(activeKeyset.Id)

	// NUT-08 include blank outputs in request for overpaid lightning fees
	numBlankOutputs := calculateBlankOutputs(meltQuoteResponse.FeeReserve)
	split := make([]uint64, numBlankOutputs)
	outputs, outputsSecrets, outputsRs, err := w.createBlindedMessages(split, activeKeyset.Id, &counter)
	if err != nil {
		return nil, fmt.Errorf("error generating blinded messages for change: %v", err)
	}

	quoteInvoice := storage.Invoice{
		TransactionType: storage.Melt,
		Id:              meltQuoteResponse.Quote,
		Mint:            mintURL,
		QuoteAmount:     amountNeeded,
		InvoiceAmount:   uint64(bolt11.MSatoshi / 1000),
		PaymentRequest:  invoice,
		PaymentHash:     bolt11.PaymentHash,
		CreatedAt:       int64(bolt11.CreatedAt),
		QuoteExpiry:     meltQuoteResponse.Expiry,
	}
	if err := w.db.SaveInvoice(quoteInvoice); err != nil {
		return nil, err
	}

	meltBolt11Request := nut05.PostMeltBolt11Request{
		Quote:   meltQuoteResponse.Quote,
		Inputs:  proofs,
		Outputs: outputs,
	}
	meltBolt11Response, err := PostMeltBolt11(mintURL, meltBolt11Request)
	if err != nil {
		// if there was error with melt, remove proofs from pending and save them for use
		if err := w.db.SaveProofs(proofs); err != nil {
			return nil, fmt.Errorf("error storing proofs: %v", err)
		}
		if err := w.db.DeletePendingProofsByQuoteId(meltQuoteResponse.Quote); err != nil {
			return nil, fmt.Errorf("error removing pending proofs: %v", err)
		}
		return nil, err
	}

	// TODO: deprecate paid field and only use State
	meltState := nut05.Unpaid
	// if state field is present, use that instead of paid
	if meltBolt11Response.State != nut05.Unknown {
		meltState = meltBolt11Response.State
	} else {
		if meltBolt11Response.Paid {
			meltState = nut05.Paid
			meltBolt11Response.State = nut05.Paid
		} else {
			meltState = nut05.Unpaid
			meltBolt11Response.State = nut05.Unpaid
		}
	}

	switch meltState {
	case nut05.Unpaid:
		// if quote is unpaid, remove proofs from pending and add them
		// to proofs available
		if err := w.db.SaveProofs(proofs); err != nil {
			return nil, fmt.Errorf("error storing proofs: %v", err)
		}
		if err := w.db.DeletePendingProofsByQuoteId(meltQuoteResponse.Quote); err != nil {
			return nil, fmt.Errorf("error removing pending proofs: %v", err)
		}
	case nut05.Paid:
		// payment succeeded so remove proofs from pending
		if err := w.db.DeletePendingProofsByQuoteId(meltQuoteResponse.Quote); err != nil {
			return nil, fmt.Errorf("error removing pending proofs: %v", err)
		}

		quoteInvoice.Preimage = meltBolt11Response.Preimage
		quoteInvoice.Paid = true
		quoteInvoice.SettledAt = time.Now().Unix()
		if err := w.db.SaveInvoice(quoteInvoice); err != nil {
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
	if meltBolt11Response.Paid {
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

	proofs := cashu.Proofs{}
	for _, keyset := range selectedMint.activeKeysets {
		keysetProofs := w.db.GetProofsByKeysetId(keyset.Id)
		proofs = append(proofs, keysetProofs...)
	}

	return proofs
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
	proofsToSwap, err := selectProofsToSend(proofs, amount, mint, true)
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
	// TODO: need to check first if 'input_fee_ppk' for keyset has changed
	mintProofs := w.getProofsFromMint(mint.mintURL)
	selectedProofs, err := selectProofsToSend(mintProofs, amount, mint, includeFees)
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
			if keyset.Active && keyset.Unit == cashu.Sat.String() && err == nil {
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
	if len(keysetsResponse.Keysets) > 0 && keysetsResponse.Keysets[0].Unit == cashu.Sat.String() {
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

// GetPendingMeltQuotes return a list of pending quote ids
func (w *Wallet) GetPendingMeltQuotes() []string {
	pendingProofs := w.db.GetPendingProofs()
	pendingProofsMap := make(map[string][]storage.DBProof)
	var pendingQuotes []string
	for _, proof := range pendingProofs {
		if _, ok := pendingProofsMap[proof.MeltQuoteId]; !ok {
			pendingQuotes = append(pendingQuotes, proof.MeltQuoteId)
		}
		pendingProofsMap[proof.MeltQuoteId] = append(pendingProofsMap[proof.MeltQuoteId], proof)
	}

	return pendingQuotes
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
