package wallet

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut03"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut07"
	"github.com/elnosh/gonuts/cashu/nuts/nut09"
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

	wallet := &Wallet{db: db, masterKey: masterKey}
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
	keysetsResponse, err := GetActiveKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keyset from mint: %v", err)
	}

	activeKeysets := make(map[string]crypto.WalletKeyset)
	for _, keyset := range keysetsResponse.Keysets {
		_, err := hex.DecodeString(keyset.Id)
		if keyset.Unit == "sat" && err == nil {
			activeKeyset := crypto.WalletKeyset{MintURL: mintURL, Unit: keyset.Unit, Active: true}
			keys, err := mapPubKeys(keysetsResponse.Keysets[0].Keys)
			if err != nil {
				return nil, err
			}

			activeKeyset.PublicKeys = keys
			id := crypto.DeriveKeysetId(activeKeyset.PublicKeys)
			activeKeyset.Id = id
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
				Id:      keysetRes.Id,
				MintURL: mintURL,
				Unit:    keysetRes.Unit,
				Active:  keysetRes.Active,
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

// CheckQuotePaid reports whether the mint quote has been paid
func (w *Wallet) CheckQuotePaid(quoteId string) bool {
	mintQuote, err := GetMintQuoteState(w.currentMint.mintURL, quoteId)
	if err != nil {
		return false
	}

	return mintQuote.Paid
}

// MintTokens will check whether if the mint quote has been paid.
// If yes, it will create blinded messages that will send to the mint
// to get the blinded signatures.
// If successful, it will unblind the signatures to generate proofs
// and store the proofs in the db.
func (w *Wallet) MintTokens(quoteId string) (cashu.Proofs, error) {
	mintQuote, err := GetMintQuoteState(w.currentMint.mintURL, quoteId)
	if err != nil {
		return nil, err
	}
	if !mintQuote.Paid {
		return nil, errors.New("invoice not paid")
	}

	invoice, err := w.GetInvoiceByPaymentRequest(mintQuote.Request)
	if err != nil {
		return nil, err
	}
	if invoice == nil {
		return nil, errors.New("invoice not found")
	}

	activeKeyset := w.GetActiveSatKeyset()
	// get counter for keyset
	counter := w.counterForKeyset(activeKeyset.Id)

	blindedMessages, secrets, rs, err := w.createBlindedMessages(invoice.QuoteAmount, activeKeyset.Id, counter)
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
	proofs, err := constructProofs(mintResponse.Signatures, secrets, rs, &activeKeyset)
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
func (w *Wallet) Send(amount uint64, mintURL string) (*cashu.Token, error) {
	proofsToSend, err := w.getProofsForAmount(amount, mintURL)
	if err != nil {
		return nil, err
	}

	token := cashu.NewToken(proofsToSend, mintURL, "sat")
	return &token, nil
}

// Receives Cashu token. If swap is true, it will swap the funds to the configured default mint.
// If false, it will add the proofs from the mint and add that mint to the list of trusted mints.
func (w *Wallet) Receive(token cashu.Token, swap bool) (uint64, error) {
	if swap {
		trustedMintProofs, err := w.swapToTrusted(token)
		if err != nil {
			return 0, fmt.Errorf("error swapping token to trusted mint: %v", err)
		}
		return trustedMintProofs.Amount(), nil
	} else {
		proofsToSwap := make(cashu.Proofs, 0)
		for _, tokenProof := range token.Token {
			proofsToSwap = append(proofsToSwap, tokenProof.Proofs...)
		}

		tokenMintURL := token.Token[0].Mint
		// only add mint if not previously trusted
		walletMint, ok := w.mints[tokenMintURL]
		if !ok {
			mint, err := w.addMint(tokenMintURL)
			if err != nil {
				return 0, err
			}
			walletMint = *mint
		}

		var activeSatKeyset crypto.WalletKeyset
		for _, k := range walletMint.activeKeysets {
			activeSatKeyset = k
			break
		}
		counter := w.counterForKeyset(activeSatKeyset.Id)

		// create blinded messages
		outputs, secrets, rs, err := w.createBlindedMessages(token.TotalAmount(), activeSatKeyset.Id, counter)
		if err != nil {
			return 0, fmt.Errorf("createBlindedMessages: %v", err)
		}

		// make swap request to mint
		swapRequest := nut03.PostSwapRequest{Inputs: proofsToSwap, Outputs: outputs}
		swapResponse, err := PostSwap(tokenMintURL, swapRequest)
		if err != nil {
			return 0, err
		}

		// unblind signatures to get proofs and save them to db
		proofs, err := constructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
		if err != nil {
			return 0, fmt.Errorf("wallet.ConstructProofs: %v", err)
		}

		w.saveProofs(proofs)

		err = w.incrementKeysetCounter(activeSatKeyset.Id, uint32(len(outputs)))
		if err != nil {
			return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
		}

		return proofs.Amount(), nil
	}
}

// swapToTrusted will swap the proofs from mint in the token
// to the wallet's configured default mint
func (w *Wallet) swapToTrusted(token cashu.Token) (cashu.Proofs, error) {
	invoicePct := 0.99
	tokenAmount := token.TotalAmount()
	tokenMintURL := token.Token[0].Mint
	amount := float64(tokenAmount) * invoicePct

	proofsToSwap := make(cashu.Proofs, 0)
	for _, tokenProof := range token.Token {
		proofsToSwap = append(proofsToSwap, tokenProof.Proofs...)
	}

	var mintResponse *nut04.PostMintQuoteBolt11Response
	var meltQuoteResponse *nut05.PostMeltQuoteBolt11Response
	var err error

	for {
		// request a mint quote from the configured default mint
		// this will generate an invoice from the trusted mint
		mintResponse, err = w.RequestMint(uint64(amount))
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
		if meltQuoteResponse.Amount+meltQuoteResponse.FeeReserve > tokenAmount {
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
func (w *Wallet) Melt(invoice string, mint string) (*nut05.PostMeltBolt11Response, error) {
	selectedMint, ok := w.mints[mint]
	if !ok {
		return nil, ErrMintNotExist
	}

	meltRequest := nut05.PostMeltQuoteBolt11Request{Request: invoice, Unit: "sat"}
	meltQuoteResponse, err := PostMeltQuoteBolt11(selectedMint.mintURL, meltRequest)
	if err != nil {
		return nil, err
	}

	amountNeeded := meltQuoteResponse.Amount + meltQuoteResponse.FeeReserve
	proofs, err := w.getProofsForAmount(amountNeeded, mint)
	if err != nil {
		return nil, err
	}

	meltBolt11Request := nut05.PostMeltBolt11Request{Quote: meltQuoteResponse.Quote, Inputs: proofs}
	meltBolt11Response, err := PostMeltBolt11(selectedMint.mintURL, meltBolt11Request)
	if err != nil || !meltBolt11Response.Paid {
		// save proofs if invoice was not paid
		w.saveProofs(proofs)
	} else if meltBolt11Response.Paid { // save invoice to db
		bolt11, err := decodepay.Decodepay(invoice)
		if err != nil {
			return nil, fmt.Errorf("error decoding bolt11 invoice: %v", err)
		}

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

// getProofsForAmount will return proofs from mint that equal to given amount.
// It returns error if wallet does not have enough proofs to fulfill amount
func (w *Wallet) getProofsForAmount(amount uint64, mintURL string) (cashu.Proofs, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, ErrMintNotExist
	}

	balanceByMints := w.GetBalanceByMints()
	balance := balanceByMints[mintURL]
	if balance < amount {
		return nil, ErrInsufficientMintBalance
	}

	selectedProofs := cashu.Proofs{}
	var currentProofsAmount uint64 = 0
	addKeysetProofs := func(proofs cashu.Proofs) {
		for _, proof := range proofs {
			if currentProofsAmount >= amount {
				break
			}

			selectedProofs = append(selectedProofs, proof)
			currentProofsAmount += proof.Amount
		}
	}

	// use proofs from inactive keysets first
	addKeysetProofs(w.getInactiveProofsByMint(mintURL))
	addKeysetProofs(w.getActiveProofsByMint(mintURL))

	// if proofs stored fulfill amount, delete them from db and return them
	if currentProofsAmount == amount {
		for _, proof := range selectedProofs {
			w.db.DeleteProof(proof.Secret)
		}
		return selectedProofs, nil
	}

	var activeSatKeyset crypto.WalletKeyset
	for _, k := range selectedMint.activeKeysets {
		activeSatKeyset = k
		break
	}

	counter := w.counterForKeyset(activeSatKeyset.Id)

	// blinded messages for send amount
	send, secrets, rs, err := w.createBlindedMessages(amount, activeSatKeyset.Id, counter)
	if err != nil {
		return nil, err
	}

	counter += uint32(len(send))

	blindedMessages := make(cashu.BlindedMessages, len(send))
	copy(blindedMessages, send)

	// blinded messages for change amount
	change, changeSecrets, changeRs, err := w.createBlindedMessages(currentProofsAmount-amount, activeSatKeyset.Id, counter)
	if err != nil {
		return nil, err
	}

	blindedMessages = append(blindedMessages, change...)
	secrets = append(secrets, changeSecrets...)
	rs = append(rs, changeRs...)

	// sort messages, secrets and rs
	for i := 0; i < len(blindedMessages)-1; i++ {
		for j := i + 1; j < len(blindedMessages); j++ {
			if blindedMessages[i].Amount > blindedMessages[j].Amount {
				// Swap blinded messages
				blindedMessages[i], blindedMessages[j] = blindedMessages[j], blindedMessages[i]

				// Swap secrets
				secrets[i], secrets[j] = secrets[j], secrets[i]

				// Swap rs
				rs[i], rs[j] = rs[j], rs[i]
			}
		}
	}

	swapRequest := nut03.PostSwapRequest{Inputs: selectedProofs, Outputs: blindedMessages}
	swapResponse, err := PostSwap(selectedMint.mintURL, swapRequest)
	if err != nil {
		return nil, err
	}

	for _, proof := range selectedProofs {
		w.db.DeleteProof(proof.Secret)
	}

	proofs, err := constructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
	if err != nil {
		return nil, fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	proofsToSend := make(cashu.Proofs, len(send))
	for i, sendmsg := range send {
		for j, proof := range proofs {
			if sendmsg.Amount == proof.Amount {
				proofsToSend[i] = proof
				proofs = slices.Delete(proofs, j, j+1)
				break
			}
		}
	}

	// remaining proofs are change proofs to save to db
	w.saveProofs(proofs)

	err = w.incrementKeysetCounter(activeSatKeyset.Id, uint32(len(blindedMessages)))
	if err != nil {
		return nil, fmt.Errorf("error incrementing keyset counter: %v", err)
	}

	return proofsToSend, nil
}

// returns Blinded messages, secrets - [][]byte, and list of r
func (w *Wallet) createBlindedMessages(amount uint64, keysetId string, counter uint32) (cashu.BlindedMessages, []string, []*secp256k1.PrivateKey, error) {
	splitAmounts := cashu.AmountSplit(amount)
	splitLen := len(splitAmounts)

	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)

	keysetDerivationPath, err := nut13.DeriveKeysetPath(w.masterKey, keysetId)
	if err != nil {
		return nil, nil, nil, err
	}

	for i, amt := range splitAmounts {
		B_, secret, r, err := blindMessage(keysetDerivationPath, counter)
		if err != nil {
			return nil, nil, nil, err
		}

		blindedMessages[i] = newBlindedMessage(keysetId, amt, B_)
		secrets[i] = secret
		rs[i] = r
		counter++
	}

	return blindedMessages, secrets, rs, nil
}

func newBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) cashu.BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return cashu.BlindedMessage{Amount: amount, B_: B_str, Id: id}
}

func blindMessage(path *hdkeychain.ExtendedKey, counter uint32) (
	*secp256k1.PublicKey,
	string,
	*secp256k1.PrivateKey,
	error,
) {
	r, err := nut13.DeriveBlindingFactor(path, counter)
	if err != nil {
		return nil, "", nil, err
	}

	secret, err := nut13.DeriveSecret(path, counter)
	if err != nil {
		return nil, "", nil, err
	}

	B_, r, err := crypto.BlindMessage(secret, r)
	if err != nil {
		return nil, "", nil, err
	}

	return B_, secret, r, nil
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

// get active sat keyset for current mint
func (w *Wallet) GetActiveSatKeyset() crypto.WalletKeyset {
	var activeKeyset crypto.WalletKeyset
	for _, keyset := range w.currentMint.activeKeysets {
		// ignore keysets with non-hex id
		_, err := hex.DecodeString(keyset.Id)
		if err != nil {
			continue
		}

		if keyset.Unit == "sat" {
			activeKeyset = keyset
			break
		}
	}
	return activeKeyset
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
		keys, err = mapPubKeys(keysetsResponse.Keysets[0].Keys)
		if err != nil {
			return nil, err
		}
	}

	return keys, nil
}

func mapPubKeys(keys nut01.KeysMap) (map[uint64]*secp256k1.PublicKey, error) {
	publicKeys := make(map[uint64]*secp256k1.PublicKey)
	for amount, key := range keys {
		pkbytes, err := hex.DecodeString(key)
		if err != nil {
			return nil, err
		}
		pubkey, err := secp256k1.ParsePubKey(pkbytes)
		if err != nil {
			return nil, err
		}
		publicKeys[amount] = pubkey
	}
	return publicKeys, nil
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
					B_, secret, r, err := blindMessage(keysetDerivationPath, counter)
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
