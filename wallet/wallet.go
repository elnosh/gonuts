package wallet

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut03"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/wallet/storage"

	decodepay "github.com/nbd-wtf/ln-decodepay"
)

type Wallet struct {
	db storage.DB

	// default mint
	currentMint *walletMint
	// array of mints that have been trusted
	mints map[string]walletMint
}

type walletMint struct {
	mintURL string
	// active keysets from mint
	activeKeysets map[string]crypto.Keyset
	// list of inactive keysets (if any) from mint
	inactiveKeysets map[string]crypto.Keyset
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

func InitStorage(path string) (storage.DB, error) {
	// bolt db atm
	return storage.InitBolt(path)
}

func LoadWallet(config Config) (*Wallet, error) {
	db, err := InitStorage(config.WalletPath)
	if err != nil {
		return nil, fmt.Errorf("InitStorage: %v", err)
	}

	wallet := &Wallet{db: db}
	wallet.mints = wallet.getWalletMints()
	url, err := url.Parse(config.CurrentMintURL)
	if err != nil {
		return nil, fmt.Errorf("invalid mint url: %v", err)
	}
	mintURL := url.String()

	currentMint, err := mintInfo(mintURL)
	if err != nil {
		return nil, err
	}
	wallet.currentMint = currentMint

	_, ok := wallet.mints[mintURL]
	if !ok {
		for _, keyset := range currentMint.activeKeysets {
			db.SaveKeyset(&keyset)
		}
		for _, keyset := range currentMint.inactiveKeysets {
			db.SaveKeyset(&keyset)
		}
	}
	wallet.mints[mintURL] = *currentMint

	return wallet, nil
}

func GetMintActiveKeysets(mintURL string) (map[string]crypto.Keyset, error) {
	keysetsResponse, err := GetActiveKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keyset from mint: %v", err)
	}

	activeKeysets := make(map[string]crypto.Keyset)
	for i, keyset := range keysetsResponse.Keysets {
		activeKeyset := crypto.Keyset{MintURL: mintURL, Unit: keyset.Unit, Active: true}
		keys := make(map[uint64]crypto.KeyPair)
		for amount, key := range keysetsResponse.Keysets[i].Keys {
			pkbytes, err := hex.DecodeString(key)
			if err != nil {
				return nil, err
			}
			pubkey, err := secp256k1.ParsePubKey(pkbytes)
			if err != nil {
				return nil, err
			}
			keys[amount] = crypto.KeyPair{PublicKey: pubkey}
		}
		activeKeyset.Keys = keys
		id := crypto.DeriveKeysetId(activeKeyset.Keys)
		activeKeyset.Id = id
		activeKeysets[id] = activeKeyset
	}

	return activeKeysets, nil
}

func GetMintInactiveKeysets(mintURL string) (map[string]crypto.Keyset, error) {
	keysetsResponse, err := GetAllKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting keysets from mint: %v", err)
	}

	inactiveKeysets := make(map[string]crypto.Keyset)

	for _, keysetRes := range keysetsResponse.Keysets {
		if !keysetRes.Active {
			keyset := crypto.Keyset{
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

	invoice := lightning.Invoice{
		Id:             mintResponse.Quote,
		PaymentRequest: mintResponse.Request,
		PaymentHash:    bolt11.PaymentHash,
		Amount:         amount,
		Expiry:         mintResponse.Expiry,
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

	// create blinded messages
	activeKeyset := w.GetActiveSatKeyset()
	blindedMessages, secrets, rs, err := createBlindedMessages(invoice.Amount, activeKeyset)
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
	proofs, err := w.ConstructProofs(mintResponse.Signatures, secrets, rs, &activeKeyset)
	if err != nil {
		return nil, fmt.Errorf("error constructing proofs: %v", err)
	}

	// mark invoice as redeemed
	invoice.Settled = true
	invoice.Redeemed = true
	err = w.db.SaveInvoice(*invoice)
	if err != nil {
		return nil, err
	}

	// store proofs in db
	err = w.saveProofs(proofs)
	if err != nil {
		return nil, fmt.Errorf("error storing proofs: %v", err)
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

		// add mint to list of trusted mints
		tokenMintURL := token.Token[0].Mint
		mint, err := w.addMint(tokenMintURL)
		if err != nil {
			return 0, err
		}

		var activeSatKeyset crypto.Keyset
		// for now just taking first keyset val from map and assign it as sat keyset
		for _, k := range mint.activeKeysets {
			activeSatKeyset = k
			break
		}

		// create blinded messages
		outputs, secrets, rs, err := createBlindedMessages(token.TotalAmount(), activeSatKeyset)
		if err != nil {
			return 0, fmt.Errorf("CreateBlindedMessages: %v", err)
		}

		// make swap request to mint
		swapRequest := nut03.PostSwapRequest{Inputs: proofsToSwap, Outputs: outputs}
		swapResponse, err := PostSwap(tokenMintURL, swapRequest)
		if err != nil {
			return 0, err
		}

		// unblind signatures to get proofs and save them to db
		proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
		if err != nil {
			return 0, fmt.Errorf("wallet.ConstructProofs: %v", err)
		}

		w.saveProofs(proofs)
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
		return nil, errors.New("mint does not exist")
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
	}
	return meltBolt11Response, err
}

// GetProofsByMint will return an array of proofs that are from
// the passed mint
func (w *Wallet) GetProofsByMint(mintURL string) (cashu.Proofs, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, errors.New("mint does not exist")
	}

	proofs := cashu.Proofs{}
	for _, keyset := range selectedMint.activeKeysets {
		keysetProofs := w.db.GetProofsByKeysetId(keyset.Id)
		proofs = append(proofs, keysetProofs...)
	}

	for _, keyset := range selectedMint.inactiveKeysets {
		keysetProofs := w.db.GetProofsByKeysetId(keyset.Id)
		proofs = append(proofs, keysetProofs...)
	}

	return proofs, nil
}

// getProofsForAmount will return proofs from mint that equal to given amount.
// It returns error if wallet does not have enough proofs to fulfill amount
func (w *Wallet) getProofsForAmount(amount uint64, mintURL string) (cashu.Proofs, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, errors.New("mint does not exist")
	}

	balanceByMints := w.GetBalanceByMints()
	balance := balanceByMints[mintURL]
	if balance < amount {
		return nil, errors.New("not enough funds in selected mint")
	}

	activeKeysetProofs := cashu.Proofs{}
	inactiveKeysetProofs := cashu.Proofs{}
	mintProofs, err := w.GetProofsByMint(mintURL)
	if err != nil {
		return nil, err
	}

	// use proofs from inactive keysets first
	for _, proof := range mintProofs {
		isInactive := false
		for _, inactiveKeyset := range selectedMint.inactiveKeysets {
			if proof.Id == inactiveKeyset.Id {
				isInactive = true
				break
			}
		}

		if isInactive {
			inactiveKeysetProofs = append(inactiveKeysetProofs, proof)
		} else {
			activeKeysetProofs = append(activeKeysetProofs, proof)
		}
	}

	selectedProofs := cashu.Proofs{}
	var currentProofsAmount uint64 = 0
	addKeysetProofs := func(proofs cashu.Proofs) {
		if currentProofsAmount < amount {
			for _, proof := range proofs {
				selectedProofs = append(selectedProofs, proof)
				currentProofsAmount += proof.Amount

				if currentProofsAmount == amount {
					for _, proof := range selectedProofs {
						w.db.DeleteProof(proof.Secret)
					}
				} else if currentProofsAmount > amount {
					break
				}

			}
		}
	}

	addKeysetProofs(inactiveKeysetProofs)
	addKeysetProofs(activeKeysetProofs)

	var activeSatKeyset crypto.Keyset
	for _, k := range selectedMint.activeKeysets {
		activeSatKeyset = k
		break
	}
	// blinded messages for send amount
	send, secrets, rs, err := createBlindedMessages(amount, activeSatKeyset)
	if err != nil {
		return nil, err
	}

	// blinded messages for change amount
	change, changeSecrets, changeRs, err := createBlindedMessages(currentProofsAmount-amount, activeSatKeyset)
	if err != nil {
		return nil, err
	}

	blindedMessages := make(cashu.BlindedMessages, len(send))
	copy(blindedMessages, send)
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

	proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
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
	return proofsToSend, nil
}

func newBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) cashu.BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return cashu.BlindedMessage{Amount: amount, B_: B_str, Id: id}
}

// returns Blinded messages, secrets - [][]byte, and list of r
func createBlindedMessages(amount uint64, keyset crypto.Keyset) (cashu.BlindedMessages, []string, []*secp256k1.PrivateKey, error) {
	splitAmounts := cashu.AmountSplit(amount)
	splitLen := len(splitAmounts)

	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)

	for i, amt := range splitAmounts {
		// generate new private key r
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		var B_ *secp256k1.PublicKey
		var secret string
		// generate random secret until it finds valid point
		for {
			secretBytes := make([]byte, 32)
			_, err = rand.Read(secretBytes)
			if err != nil {
				return nil, nil, nil, err
			}
			secret = hex.EncodeToString(secretBytes)
			B_, r, err = crypto.BlindMessage(secret, r)
			if err == nil {
				break
			}
		}

		blindedMessage := newBlindedMessage(keyset.Id, amt, B_)
		blindedMessages[i] = blindedMessage
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}

// ConstructProofs unblinds the blindedSignatures and returns the proofs
func (w *Wallet) ConstructProofs(blindedSignatures cashu.BlindedSignatures,
	secrets []string, rs []*secp256k1.PrivateKey, keyset *crypto.Keyset) (cashu.Proofs, error) {

	if len(blindedSignatures) != len(secrets) && len(blindedSignatures) != len(rs) {
		return nil, errors.New("lengths do not match")
	}

	proofs := make(cashu.Proofs, len(blindedSignatures))
	for i, blindedSignature := range blindedSignatures {
		C_bytes, err := hex.DecodeString(blindedSignature.C_)
		if err != nil {
			return nil, err
		}
		C_, err := secp256k1.ParsePubKey(C_bytes)
		if err != nil {
			return nil, err
		}

		K := keyset.Keys[blindedSignature.Amount].PublicKey
		C := crypto.UnblindSignature(C_, rs[i], K)
		Cstr := hex.EncodeToString(C.SerializeCompressed())

		proof := cashu.Proof{Amount: blindedSignature.Amount,
			Secret: secrets[i], C: Cstr, Id: blindedSignature.Id}

		proofs[i] = proof
	}

	return proofs, nil
}

func (w *Wallet) GetActiveSatKeyset() crypto.Keyset {
	var activeKeyset crypto.Keyset
	for _, keyset := range w.currentMint.activeKeysets {
		if keyset.Unit == "sat" {
			activeKeyset = keyset
			break
		}
	}
	return activeKeyset
}

func (w *Wallet) getWalletMints() map[string]walletMint {
	walletMints := make(map[string]walletMint)

	keysets := w.db.GetKeysets()
	for k, mintKeysets := range keysets {
		activeKeysets := make(map[string]crypto.Keyset)
		inactiveKeysets := make(map[string]crypto.Keyset)
		for _, keyset := range mintKeysets {
			if keyset.Active {
				activeKeysets[keyset.Id] = keyset
			} else {
				inactiveKeysets[keyset.Id] = keyset
			}
		}
		walletMints[k] = walletMint{mintURL: k, activeKeysets: activeKeysets, inactiveKeysets: inactiveKeysets}
	}

	return walletMints
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

func (w *Wallet) saveProofs(proofs cashu.Proofs) error {
	for _, proof := range proofs {
		err := w.db.SaveProof(proof)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Wallet) GetInvoiceByPaymentRequest(pr string) (*lightning.Invoice, error) {
	bolt11, err := decodepay.Decodepay(pr)
	if err != nil {
		return nil, fmt.Errorf("invalid payment request: %v", err)
	}

	return w.db.GetInvoice(bolt11.PaymentHash), nil
}

func (w *Wallet) GetInvoiceByPaymentHash(hash string) *lightning.Invoice {
	return w.db.GetInvoice(hash)
}

func (w *Wallet) GetAllInvoices() []lightning.Invoice {
	return w.db.GetInvoices()
}
