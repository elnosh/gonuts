package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/cashu/nuts/nut03"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/wallet/storage"
)

const (
	MINT_URL = "MINT_URL"
)

type Wallet struct {
	db storage.DB

	// current mint url
	MintURL string

	// active keysets from current mint
	activeKeysets []*crypto.Keyset
	// list of inactive keysets (if any) from current mint
	inactiveKeysets []*crypto.Keyset

	proofs cashu.Proofs
}

func setWalletPath() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	path := filepath.Join(homedir, ".gonuts", "wallet")
	err = os.MkdirAll(path, 0700)
	if err != nil {
		log.Fatal(err)
	}
	return path
}

func InitStorage(path string) (storage.DB, error) {
	// bolt db atm
	return storage.InitBolt(path)
}

func LoadWallet() (*Wallet, error) {
	path := setWalletPath()
	db, err := InitStorage(path)
	if err != nil {
		return nil, fmt.Errorf("InitStorage: %v", err)
	}

	wallet := &Wallet{db: db}
	allKeysets := wallet.db.GetKeysets()

	wallet.MintURL = os.Getenv(MINT_URL)
	if wallet.MintURL == "" {
		// if no mint specified, default to localhost
		wallet.MintURL = "http://127.0.0.1:3338"
	}

	keyset, err := GetMintCurrentKeyset(wallet.MintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting current keyset from mint: %v", err)
	}
	wallet.activeKeysets = []*crypto.Keyset{keyset}

	// save current keyset if new
	mintKeysets, ok := allKeysets[keyset.MintURL]
	if !ok {
		err = db.SaveKeyset(*keyset)
		if err != nil {
			return nil, fmt.Errorf("error setting up wallet: %v", err)
		}
	} else {
		if _, ok := mintKeysets[keyset.Id]; !ok {
			err = db.SaveKeyset(*keyset)
			if err != nil {
				return nil, fmt.Errorf("error setting up wallet: %v", err)
			}
		}
	}

	inactiveKeysets, err := GetCurrentMintInactiveKeysets(keyset.MintURL)
	if err != nil {
		return nil, fmt.Errorf("error setting up wallet: %v", err)
	}
	wallet.inactiveKeysets = inactiveKeysets

	keysetIds := make([]string, len(wallet.activeKeysets)+len(wallet.inactiveKeysets))
	idx := 0
	for _, ks := range wallet.activeKeysets {
		keysetIds[idx] = ks.Id
		idx++
	}
	for _, ks := range wallet.inactiveKeysets {
		keysetIds[idx] = ks.Id
		idx++
	}
	wallet.proofs = wallet.db.GetProofs(keysetIds)

	return wallet, nil
}

func GetMintCurrentKeyset(mintURL string) (*crypto.Keyset, error) {
	resp, err := http.Get(mintURL + "/v1/keys")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetRes nut01.GetKeysResponse
	err = json.NewDecoder(resp.Body).Decode(&keysetRes)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	keyset := &crypto.Keyset{MintURL: mintURL}
	for amount, pubkey := range keysetRes.Keysets[0].Keys {
		pubkeyBytes, err := hex.DecodeString(pubkey)
		if err != nil {
			return nil, err
		}
		kp := crypto.KeyPair{Amount: amount, PublicKey: pubkeyBytes}
		keyset.KeyPairs = append(keyset.KeyPairs, kp)
	}
	keyset.Id = crypto.DeriveKeysetId(keyset.KeyPairs)

	return keyset, nil
}

func GetCurrentMintInactiveKeysets(mintURL string) ([]*crypto.Keyset, error) {
	resp, err := http.Get(mintURL + "/v1/keysets")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetsRes nut02.GetKeysetsResponse
	err = json.NewDecoder(resp.Body).Decode(&keysetsRes)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	currentMintKeysets := []*crypto.Keyset{}
	for _, keysetRes := range keysetsRes.Keysets {
		if !keysetRes.Active {
			keyset := crypto.Keyset{
				Id:      keysetRes.Id,
				MintURL: mintURL,
				Unit:    keysetRes.Unit,
				Active:  keysetRes.Active,
			}
			currentMintKeysets = append(currentMintKeysets, &keyset)
		}
	}
	return currentMintKeysets, nil
}

func (w *Wallet) GetBalance() uint64 {
	var balance uint64 = 0

	for _, proof := range w.proofs {
		balance += proof.Amount
	}

	return balance
}

func (w *Wallet) RequestMint(amount uint64) (nut04.PostMintQuoteBolt11Response, error) {
	mintRequest := nut04.PostMintQuoteBolt11Request{Amount: amount, Unit: "sat"}
	body, err := json.Marshal(mintRequest)
	if err != nil {
		return nut04.PostMintQuoteBolt11Response{}, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := http.Post(w.MintURL+"/v1/mint/quote/bolt11", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nut04.PostMintQuoteBolt11Response{}, err
	}
	defer resp.Body.Close()

	var reqMintResponse nut04.PostMintQuoteBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&reqMintResponse)
	if err != nil {
		return nut04.PostMintQuoteBolt11Response{}, fmt.Errorf("json.Decode: %v", err)
	}

	return reqMintResponse, nil
}

func (w *Wallet) CheckQuotePaid(quoteId string) bool {
	resp, err := http.Get(w.MintURL + "/v1/mint/quote/bolt11/" + quoteId)
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
	outputs, err := json.Marshal(postMintRequest)
	if err != nil {
		return nil, fmt.Errorf("error marshaling blinded messages: %v", err)
	}

	resp, err := http.Post(w.MintURL+"/v1/mint/bolt11", "application/json", bytes.NewBuffer(outputs))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var mintResponse nut04.PostMintBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&mintResponse)
	if err != nil {
		return nil, fmt.Errorf("error decoding response from mint: %v", err)
	}

	return mintResponse.Signatures, nil
}

func (w *Wallet) Send(amount uint64) (*cashu.Token, error) {
	proofsToSend, err := w.getProofsForAmount(amount)
	if err != nil {
		return nil, err
	}

	token := cashu.NewToken(proofsToSend, w.MintURL, "sat")
	return &token, nil
}

func (w *Wallet) Receive(token cashu.Token) error {
	proofsToSwap := make(cashu.Proofs, 0)
	var proofsAmount uint64 = 0

	for _, TokenProof := range token.Token {
		for _, proof := range TokenProof.Proofs {
			proofsAmount += proof.Amount
			proofsToSwap = append(proofsToSwap, proof)
		}
	}

	outputs, secrets, rs, err := cashu.CreateBlindedMessages(proofsAmount)
	if err != nil {
		return fmt.Errorf("CreateBlindedMessages: %v", err)
	}

	swapRequest := nut03.PostSwapRequest{Inputs: proofsToSwap, Outputs: outputs}
	reqBody, err := json.Marshal(swapRequest)
	if err != nil {
		return fmt.Errorf("error marshaling request body: %v", err)
	}

	resp, err := http.Post(w.MintURL+"/v1/swap", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var swapResponse nut03.PostSwapResponse
	err = json.NewDecoder(resp.Body).Decode(&swapResponse)
	if err != nil {
		return fmt.Errorf("error decoding response from mint: %v", err)
	}

	mintKeyset, err := GetMintCurrentKeyset(w.MintURL)
	if err != nil {
		return fmt.Errorf("error getting mint keyset: %v", err)
	}

	proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, mintKeyset)
	if err != nil {
		return fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	w.StoreProofs(proofs)
	return nil
}

func (w *Wallet) Melt(meltRequest nut05.PostMeltQuoteBolt11Request) (nut05.PostMeltBolt11Response, error) {
	body, err := json.Marshal(meltRequest)
	if err != nil {
		return nut05.PostMeltBolt11Response{}, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := http.Post(w.MintURL+"/v1/melt/quote/bolt11", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nut05.PostMeltBolt11Response{}, err
	}
	defer resp.Body.Close()

	var meltResponse nut05.PostMeltQuoteBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&meltResponse)
	if err != nil {
		return nut05.PostMeltBolt11Response{}, fmt.Errorf("error decoding response from mint: %v", err)
	}

	amountNeeded := meltResponse.Amount + meltResponse.FeeReserve
	proofs, err := w.getProofsForAmount(amountNeeded)
	if err != nil {
		return nut05.PostMeltBolt11Response{}, err
	}

	meltBolt11Request := nut05.PostMeltBolt11Request{Quote: meltResponse.Quote, Inputs: proofs}
	jsonReq, err := json.Marshal(meltBolt11Request)
	if err != nil {
		return nut05.PostMeltBolt11Response{}, err

	}

	resp, err = http.Post(w.MintURL+"/v1/melt/bolt11", "application/json", bytes.NewBuffer(jsonReq))
	if err != nil {
		return nut05.PostMeltBolt11Response{}, err
	}
	defer resp.Body.Close()

	var meltBolt11Response nut05.PostMeltBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&meltBolt11Response)
	if err != nil {
		return nut05.PostMeltBolt11Response{}, fmt.Errorf("error decoding response from mint: %v", err)
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
		for _, inactiveKeyset := range w.inactiveKeysets {
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
	for _, proof := range inactiveKeysetProofs {
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

	if currentProofsAmount < amount {
		for _, proof := range activeKeysetProofs {
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

	// blinded messages for send amount
	send, secrets, rs, err := cashu.CreateBlindedMessages(amount)
	if err != nil {
		return nil, err
	}

	// blinded messages for change amount
	change, changeSecrets, changeRs, err := cashu.CreateBlindedMessages(currentProofsAmount - amount)
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
	reqBody, err := json.Marshal(swapRequest)
	if err != nil {
		return nil, fmt.Errorf("error marshaling request body: %v", err)
	}

	resp, err := http.Post(w.MintURL+"/v1/swap", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	for _, proof := range selectedProofs {
		w.db.DeleteProof(proof.Secret)
	}

	var swapResponse nut03.PostSwapResponse
	err = json.NewDecoder(resp.Body).Decode(&swapResponse)
	if err != nil {
		return nil, fmt.Errorf("error decoding response from mint: %v", err)
	}

	mintKeyset, err := GetMintCurrentKeyset(w.MintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting mint keyset: %v", err)
	}

	proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, mintKeyset)
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
	w.StoreProofs(proofs)
	return proofsToSend, nil
}

func (w *Wallet) ConstructProofs(blindedSignatures cashu.BlindedSignatures,
	secrets [][]byte, rs []*secp256k1.PrivateKey, keyset *crypto.Keyset) (cashu.Proofs, error) {

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

		var pubKey []byte
		for _, kp := range keyset.KeyPairs {
			if kp.Amount == blindedSignature.Amount {
				pubKey = kp.PublicKey
			}
		}

		K, err := secp256k1.ParsePubKey(pubKey)
		if err != nil {
			return nil, err
		}

		C := crypto.UnblindSignature(C_, rs[i], K)
		Cstr := hex.EncodeToString(C.SerializeCompressed())

		secret := hex.EncodeToString(secrets[i])
		proof := cashu.Proof{Amount: blindedSignature.Amount,
			Secret: secret, C: Cstr, Id: blindedSignature.Id}

		proofs[i] = proof
	}

	return proofs, nil
}

func (w *Wallet) StoreProofs(proofs cashu.Proofs) error {
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
