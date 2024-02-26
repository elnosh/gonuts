package wallet

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
)

type Wallet struct {
	db storage.DB

	// default mint
	currentMint *walletMint
	// array of mints that have been trusted
	mints map[string]walletMint

	proofs           cashu.Proofs
	domainSeparation bool
}

type walletMint struct {
	mintURL string
	// active keysets from mint
	activeKeysets map[string]crypto.Keyset
	// list of inactive keysets (if any) from mint
	inactiveKeysets map[string]crypto.Keyset
}

// get mint info and save keysets to db
func mintInfo(mintURL string) (*walletMint, error) {
	activeKeysets, err := GetMintActiveKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting current keyset from mint: %v", err)
	}

	inactiveKeysets, err := GetMintInactiveKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error setting up wallet: %v", err)
	}

	return &walletMint{mintURL, activeKeysets, inactiveKeysets}, nil
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
	//allKeysets := wallet.db.GetKeysets()
	wallet.mints = wallet.getWalletMints()
	mintURL, err := url.Parse(config.CurrentMintURL)
	if err != nil {
		return nil, fmt.Errorf("invalid mint url: %v", err)
	}

	currentMint, err := mintInfo(mintURL.String())
	if err != nil {
		return nil, fmt.Errorf("error getting keysets from mint: %v", err)
	}
	wallet.currentMint = currentMint

	_, ok := wallet.mints[mintURL.String()]
	if !ok {
		for _, keyset := range currentMint.activeKeysets {
			db.SaveKeyset(keyset)
		}
		for _, keyset := range currentMint.inactiveKeysets {
			db.SaveKeyset(keyset)
		}
	}

	wallet.proofs = wallet.db.GetProofs()
	wallet.domainSeparation = config.DomainSeparation

	return wallet, nil
}

func GetMintActiveKeysets(mintURL string) (map[string]crypto.Keyset, error) {
	keysetsResponse, err := GetActiveKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keyset from mint: %v", err)
	}

	activeKeysets := make(map[string]crypto.Keyset)
	for i, keyset := range keysetsResponse.Keysets {
		activeKeyset := crypto.Keyset{MintURL: mintURL, Unit: keyset.Unit}
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

func (w *Wallet) GetBalance() uint64 {
	var balance uint64 = 0

	for _, proof := range w.proofs {
		balance += proof.Amount
	}

	return balance
}

func (w *Wallet) RequestMint(amount uint64) (*nut04.PostMintQuoteBolt11Response, error) {
	mintRequest := nut04.PostMintQuoteBolt11Request{Amount: amount, Unit: "sat"}
	return PostMintQuoteBolt11(w.currentMint.mintURL, mintRequest)
}

func (w *Wallet) CheckQuotePaid(quoteId string) bool {
	resp, err := http.Get(w.currentMint.mintURL + "/v1/mint/quote/bolt11/" + quoteId)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var reqMintResponse nut04.PostMintQuoteBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&reqMintResponse)
	if err != nil {
		return false
	}

	return reqMintResponse.Paid
}

func (w *Wallet) MintTokens(quoteId string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	postMintRequest := nut04.PostMintBolt11Request{Quote: quoteId, Outputs: blindedMessages}
	mintResponse, err := PostMintBolt11(w.currentMint.mintURL, postMintRequest)
	if err != nil {
		return nil, err
	}
	return mintResponse.Signatures, nil
}

func (w *Wallet) Send(amount uint64) (*cashu.Token, error) {
	proofsToSend, err := w.getProofsForAmount(amount)
	if err != nil {
		return nil, err
	}

	// mint url passed here needs to change. Need to do it based on selected proofs
	token := cashu.NewToken(proofsToSend, w.currentMint.mintURL, "sat")
	return &token, nil
}

// Receives Cashu token. If swap is true, it will swap the funds to the configured default mint.
// If false, it will trust the mint and add the proofs from the trusted mint.
func (w *Wallet) Receive(token cashu.Token, swap bool) error {
	proofsToSwap := make(cashu.Proofs, 0)

	for _, tokenProof := range token.Token {
		proofsToSwap = append(proofsToSwap, tokenProof.Proofs...)
	}

	activeSatKeyset := w.GetActiveSatKeyset()
	outputs, secrets, rs, err := w.CreateBlindedMessages(token.TotalAmount(), activeSatKeyset)
	if err != nil {
		return fmt.Errorf("CreateBlindedMessages: %v", err)
	}

	swapRequest := nut03.PostSwapRequest{Inputs: proofsToSwap, Outputs: outputs}
	swapResponse, err := PostSwap(w.currentMint.mintURL, swapRequest)
	if err != nil {
		return err
	}

	proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
	if err != nil {
		return fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	w.SaveProofs(proofs)
	return nil
}

func (w *Wallet) Melt(invoice string) (*nut05.PostMeltBolt11Response, error) {
	meltRequest := nut05.PostMeltQuoteBolt11Request{Request: invoice, Unit: "sat"}
	meltQuoteResponse, err := PostMeltQuoteBolt11(w.currentMint.mintURL, meltRequest)
	if err != nil {
		return nil, err
	}

	amountNeeded := meltQuoteResponse.Amount + meltQuoteResponse.FeeReserve
	proofs, err := w.getProofsForAmount(amountNeeded)
	if err != nil {
		return nil, err
	}

	meltBolt11Request := nut05.PostMeltBolt11Request{Quote: meltQuoteResponse.Quote, Inputs: proofs}
	meltBolt11Response, err := PostMeltBolt11(w.currentMint.mintURL, meltBolt11Request)
	if err != nil {
		return nil, err
	}

	// only delete proofs after invoice has been paid
	if meltBolt11Response.Paid {
		for _, proof := range proofs {
			w.db.DeleteProof(proof.Secret)
		}
	}

	return meltBolt11Response, nil
}

func (w *Wallet) getProofsForAmount(amount uint64) (cashu.Proofs, error) {
	balance := w.GetBalance()
	if balance < amount {
		return nil, errors.New("not enough funds")
	}

	// use proofs from inactive keysets first
	activeKeysetProofs := cashu.Proofs{}
	inactiveKeysetProofs := cashu.Proofs{}
	for _, proof := range w.proofs {
		isInactive := false
		for _, inactiveKeyset := range w.currentMint.inactiveKeysets {
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

	activeSatKeyset := w.GetActiveSatKeyset()
	// blinded messages for send amount
	send, secrets, rs, err := w.CreateBlindedMessages(amount, activeSatKeyset)
	if err != nil {
		return nil, err
	}

	// blinded messages for change amount
	change, changeSecrets, changeRs, err := w.CreateBlindedMessages(currentProofsAmount-amount, activeSatKeyset)
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
	swapResponse, err := PostSwap(w.currentMint.mintURL, swapRequest)
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
	w.SaveProofs(proofs)
	return proofsToSend, nil
}

func NewBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) cashu.BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return cashu.BlindedMessage{Amount: amount, B_: B_str, Id: id}
}

// returns Blinded messages, secrets - [][]byte, and list of r
func (w *Wallet) CreateBlindedMessages(amount uint64, keyset crypto.Keyset) (cashu.BlindedMessages, []string, []*secp256k1.PrivateKey, error) {
	splitAmounts := cashu.AmountSplit(amount)
	splitLen := len(splitAmounts)

	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)

	for i, amt := range splitAmounts {
		// create random secret
		secretBytes := make([]byte, 32)
		_, err := rand.Read(secretBytes)
		if err != nil {
			return nil, nil, nil, err
		}
		secret := hex.EncodeToString(secretBytes)

		// generate new private key r
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		var B_ *secp256k1.PublicKey
		if w.domainSeparation {
			B_, r, err = crypto.BlindMessageDomainSeparated(secret, r)
			if err != nil {
				return nil, nil, nil, err
			}
		} else {
			B_, r = crypto.BlindMessage(secret, r)
		}
		blindedMessage := NewBlindedMessage(keyset.Id, amt, B_)
		blindedMessages[i] = blindedMessage
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}

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

func (w *Wallet) SaveProofs(proofs cashu.Proofs) error {
	for _, proof := range proofs {
		err := w.db.SaveProof(proof)
		if err != nil {
			return err
		}
	}
	w.proofs = append(w.proofs, proofs...)
	return nil
}

func (w *Wallet) SaveInvoice(invoice lightning.Invoice) error {
	return w.db.SaveInvoice(invoice)
}

func (w *Wallet) GetInvoice(pr string) *lightning.Invoice {
	return w.db.GetInvoice(pr)
}
