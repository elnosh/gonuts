package wallet

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elnosh/gonuts/cashurpc"
	"net/http"
	"net/url"
	"slices"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/wallet/storage"
)

type Wallet struct {
	db storage.DB

	// current mint url
	MintURL string

	// active keysets from current mint
	ActiveKeysets map[string]crypto.Keyset
	// list of inactive keysets (if any) from current mint
	InactiveKeysets map[string]crypto.Keyset

	proofs           cashurpc.Proofs
	domainSeparation bool
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
	allKeysets := wallet.db.GetKeysets()
	mintURL, err := url.Parse(config.CurrentMintURL)
	if err != nil {
		return nil, fmt.Errorf("invalid mint url: %v\n", err)
	}
	wallet.MintURL = mintURL.String()

	activeKeysets, err := GetMintActiveKeysets(wallet.MintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting current keyset from mint: %v", err)
	}
	wallet.ActiveKeysets = activeKeysets

	for _, keyset := range activeKeysets {
		// save current keyset if new
		mintKeysets, ok := allKeysets[keyset.MintURL]
		if !ok {
			err = db.SaveKeyset(keyset)
			if err != nil {
				return nil, fmt.Errorf("error setting up wallet: %v", err)
			}
		} else {
			if _, ok := mintKeysets[keyset.Id]; !ok {
				err = db.SaveKeyset(keyset)
				if err != nil {
					return nil, fmt.Errorf("error setting up wallet: %v", err)
				}
			}
		}
	}

	inactiveKeysets, err := GetCurrentMintInactiveKeysets(wallet.MintURL)
	if err != nil {
		return nil, fmt.Errorf("error setting up wallet: %v", err)
	}
	wallet.InactiveKeysets = inactiveKeysets

	keysetIds := make([]string, len(wallet.ActiveKeysets)+len(wallet.InactiveKeysets))
	idx := 0
	for _, ks := range wallet.ActiveKeysets {
		keysetIds[idx] = ks.Id
		idx++
	}
	for _, ks := range wallet.InactiveKeysets {
		keysetIds[idx] = ks.Id
		idx++
	}
	wallet.proofs = wallet.db.GetProofs(keysetIds)
	wallet.domainSeparation = config.DomainSeparation

	return wallet, nil
}

func GetMintActiveKeysets(mintURL string) (map[string]crypto.Keyset, error) {
	resp, err := http.Get(mintURL + "/v1/keys")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetRes cashurpc.KeysResponse
	err = json.NewDecoder(resp.Body).Decode(&keysetRes)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	activeKeysets := make(map[string]crypto.Keyset)
	for i, keyset := range keysetRes.Keysets {
		activeKeyset := crypto.Keyset{MintURL: mintURL, Unit: keyset.Unit}
		keys := make(map[uint64]crypto.KeyPair)
		for amount, key := range keysetRes.Keysets[i].Keys {
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

func GetCurrentMintInactiveKeysets(mintURL string) (map[string]crypto.Keyset, error) {
	resp, err := http.Get(mintURL + "/v1/keysets")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetsRes cashurpc.KeysResponse
	err = json.NewDecoder(resp.Body).Decode(&keysetsRes)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	inactiveKeysets := make(map[string]crypto.Keyset)

	for _, keysetRes := range keysetsRes.Keysets {
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

	for _, proof := range w.proofs.Proofs {
		balance += proof.Amount
	}

	return balance
}

func (w *Wallet) RequestMint(amount uint64) (*cashurpc.PostMintQuoteResponse, error) {
	mintRequest := cashurpc.PostMintQuoteRequest{Amount: amount, Unit: cashurpc.UnitType_UNIT_TYPE_SAT}
	body, err := json.Marshal(mintRequest)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := httpPost(w.MintURL+"/v1/mint/quote/bolt11", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var reqMintResponse cashurpc.PostMintQuoteResponse
	err = json.NewDecoder(resp.Body).Decode(&reqMintResponse)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return &reqMintResponse, nil
}

func (w *Wallet) CheckQuotePaid(quoteId string) bool {
	resp, err := http.Get(w.MintURL + "/v1/mint/quote/bolt11/" + quoteId)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var reqMintResponse cashurpc.PostMintQuoteResponse
	err = json.NewDecoder(resp.Body).Decode(&reqMintResponse)
	if err != nil {
		return false
	}

	return reqMintResponse.Paid
}

func (w *Wallet) MintTokens(quoteId string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	postMintRequest := &cashurpc.PostMintRequest{Quote: quoteId, Outputs: blindedMessages}
	outputs, err := json.Marshal(postMintRequest)
	if err != nil {
		return nil, fmt.Errorf("error marshaling blinded messages: %v", err)
	}

	resp, err := httpPost(w.MintURL+"/v1/mint/bolt11", "application/json", bytes.NewBuffer(outputs))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var mintResponse cashurpc.PostMintResponse
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
	proofsToSwap := &cashurpc.Proofs{Proofs: make([]*cashurpc.Proof, 0)}

	for _, tokenProof := range token.Token {
		proofsToSwap.Proofs = append(proofsToSwap.Proofs, tokenProof.Proofs.Proofs...)
	}

	activeSatKeyset := w.GetActiveSatKeyset()
	outputs, secrets, rs, err := w.CreateBlindedMessages(token.TotalAmount(), activeSatKeyset)
	if err != nil {
		return fmt.Errorf("CreateBlindedMessages: %v", err)
	}

	swapRequest := cashurpc.SwapRequest{Inputs: proofsToSwap, Outputs: outputs}
	reqBody, err := json.Marshal(swapRequest)
	if err != nil {
		return fmt.Errorf("error marshaling request body: %v", err)
	}

	resp, err := httpPost(w.MintURL+"/v1/swap", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var swapResponse cashurpc.SwapResponse
	err = json.NewDecoder(resp.Body).Decode(&swapResponse)
	if err != nil {
		return fmt.Errorf("error decoding response from mint: %v", err)
	}

	proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
	if err != nil {
		return fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	w.StoreProofs(proofs)
	return nil
}

func (w *Wallet) Melt(meltRequest *cashurpc.PostMeltQuoteRequest) (*cashurpc.PostMeltResponse, error) {
	body, err := json.Marshal(meltRequest)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := httpPost(w.MintURL+"/v1/melt/quote/bolt11", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var meltResponse cashurpc.PostMeltQuoteResponse
	err = json.NewDecoder(resp.Body).Decode(&meltResponse)
	if err != nil {
		return nil, fmt.Errorf("error decoding response from mint: %v", err)
	}

	amountNeeded := meltResponse.Amount + meltResponse.FeeReserve
	proofs, err := w.getProofsForAmount(amountNeeded)
	if err != nil {
		return nil, err
	}

	meltBolt11Request := cashurpc.PostMeltRequest{Quote: meltResponse.Quote, Inputs: proofs}
	jsonReq, err := json.Marshal(meltBolt11Request)
	if err != nil {
		return nil, err

	}

	resp, err = httpPost(w.MintURL+"/v1/melt/bolt11", "application/json", bytes.NewBuffer(jsonReq))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var meltBolt11Response *cashurpc.PostMeltResponse
	err = json.NewDecoder(resp.Body).Decode(&meltResponse)
	if err != nil {
		return nil, fmt.Errorf("error decoding response from mint: %v", err)
	}

	// only delete proofs after invoice has been paid
	if meltBolt11Response.Paid {
		for _, proof := range proofs.Proofs {
			w.db.DeleteProof(proof.Secret)
		}
	}

	return meltBolt11Response, nil
}

func (w *Wallet) getProofsForAmount(amount uint64) (*cashurpc.Proofs, error) {
	balance := w.GetBalance()
	if balance < amount {
		return nil, errors.New("not enough funds")
	}

	// use proofs from inactive keysets first
	activeKeysetProofs := &cashurpc.Proofs{}
	inactiveKeysetProofs := &cashurpc.Proofs{}
	for _, proof := range w.proofs.Proofs {
		isInactive := false
		for _, inactiveKeyset := range w.InactiveKeysets {
			if proof.Id == inactiveKeyset.Id {
				isInactive = true
				break
			}
		}

		if isInactive {
			inactiveKeysetProofs.Proofs = append(inactiveKeysetProofs.Proofs, proof)
		} else {
			activeKeysetProofs.Proofs = append(activeKeysetProofs.Proofs, proof)
		}
	}

	selectedProofs := &cashurpc.Proofs{}
	var currentProofsAmount uint64 = 0
	addKeysetProofs := func(proofs *cashurpc.Proofs) {
		if currentProofsAmount < amount {
			for _, proof := range proofs.Proofs {
				selectedProofs.Proofs = append(selectedProofs.Proofs, proof)
				currentProofsAmount += proof.Amount

				if currentProofsAmount == amount {
					for _, proof := range selectedProofs.Proofs {
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

	swapRequest := cashurpc.SwapRequest{Inputs: selectedProofs, Outputs: blindedMessages}
	reqBody, err := json.Marshal(swapRequest)
	if err != nil {
		return nil, fmt.Errorf("error marshaling request body: %v", err)
	}

	resp, err := httpPost(w.MintURL+"/v1/swap", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	for _, proof := range selectedProofs.Proofs {
		w.db.DeleteProof(proof.Secret)
	}

	var swapResponse cashurpc.SwapResponse
	err = json.NewDecoder(resp.Body).Decode(&swapResponse)
	if err != nil {
		return nil, fmt.Errorf("error decoding response from mint: %v", err)
	}

	proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
	if err != nil {
		return nil, fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	proofsToSend := &cashurpc.Proofs{Proofs: make([]*cashurpc.Proof, len(send))}
	for i, sendmsg := range send {
		for j, proof := range proofs.Proofs {
			if sendmsg.Amount == proof.Amount {
				proofsToSend.Proofs[i] = proof
				proofs.Proofs = slices.Delete(proofs.Proofs, j, j+1)
				break
			}
		}
	}

	// remaining proofs are change proofs to save to db
	w.StoreProofs(proofs)
	return proofsToSend, nil
}

func NewBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) *cashurpc.BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return &cashurpc.BlindedMessage{Amount: amount, B_: B_str, Id: id}
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
	secrets []string, rs []*secp256k1.PrivateKey, keyset *crypto.Keyset) (*cashurpc.Proofs, error) {

	if len(blindedSignatures) != len(secrets) && len(blindedSignatures) != len(rs) {
		return nil, errors.New("lengths do not match")
	}

	proofs := &cashurpc.Proofs{Proofs: make([]*cashurpc.Proof, len(blindedSignatures))}
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

		proof := &cashurpc.Proof{Amount: blindedSignature.Amount,
			Secret: secrets[i], C: Cstr, Id: blindedSignature.Id}

		proofs.Proofs[i] = proof
	}

	return proofs, nil
}

func (w *Wallet) GetActiveSatKeyset() crypto.Keyset {
	var activeKeyset crypto.Keyset
	for _, keyset := range w.ActiveKeysets {
		if keyset.Unit == "sat" {
			activeKeyset = keyset
			break
		}
	}
	return activeKeyset
}

func (w *Wallet) StoreProofs(proofs *cashurpc.Proofs) error {
	for _, proof := range proofs.Proofs {
		err := w.db.SaveProof(proof)
		if err != nil {
			return err
		}
	}
	w.proofs.Proofs = append(w.proofs.Proofs, proofs.Proofs...)
	return nil
}

func (w *Wallet) SaveInvoice(invoice lightning.Invoice) error {
	return w.db.SaveInvoice(invoice)
}

func (w *Wallet) GetInvoice(pr string) *lightning.Invoice {
	return w.db.GetInvoice(pr)
}
