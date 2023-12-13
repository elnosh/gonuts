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

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
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

	// current keyset
	keyset  *crypto.Keyset
	keysets []crypto.Keyset

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
	wallet.keysets = wallet.db.GetKeysets()
	wallet.proofs = wallet.db.GetProofs()

	wallet.MintURL = os.Getenv(MINT_URL)
	if wallet.MintURL == "" {
		// if no mint specified, default to localhost
		wallet.MintURL = "http://127.0.0.1:3338"
	}

	// keyset, err := getMintCurrentKeyset(wallet.mintURL)
	// if err != nil {
	// 	return nil, fmt.Errorf("error getting current keyset from mint: %v", err)
	// }
	// wallet.keyset = keyset

	return wallet, nil
}

func GetMintCurrentKeyset(mintURL string) (*crypto.Keyset, error) {
	resp, err := http.Get(mintURL + "/v1/keys")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	//var keysetRes map[string]string
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

func (w *Wallet) GetBalance() uint64 {
	var balance uint64 = 0

	for _, proof := range w.proofs {
		balance += proof.Amount
	}

	return balance
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
