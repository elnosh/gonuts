package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
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
	// only bolt db atm
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

	wallet.mintURL = os.Getenv(MINT_URL)
	if wallet.mintURL == "" {
		// if no mint specified, default to localhost
		wallet.mintURL = "http://127.0.0.1:3338"
	}

	// keyset, err := getMintCurrentKeyset(wallet.mintURL)
	// if err != nil {
	// 	return nil, fmt.Errorf("error getting current keyset from mint: %v", err)
	// }
	// wallet.keyset = keyset

	return wallet, nil
}

func GetMintCurrentKeyset(mintURL string) (*crypto.Keyset, error) {
	resp, err := http.Get(mintURL + "/keys")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetRes map[string]string
	err = json.NewDecoder(resp.Body).Decode(&keysetRes)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	keyset := &crypto.Keyset{MintURL: mintURL}
	for amountStr, pubkey := range keysetRes {
		amount, err := strconv.ParseUint(amountStr, 10, 64)
		if err != nil {
			return nil, err
		}

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

func (w *Wallet) RequestMint(amount uint64) (*cashu.RequestMintResponse, error) {
	amountStr := strconv.FormatUint(amount, 10)

	resp, err := http.Get(w.mintURL + "/mint?amount=" + amountStr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var reqMintResponse cashu.RequestMintResponse
	err = json.NewDecoder(resp.Body).Decode(&reqMintResponse)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return &reqMintResponse, nil
}

func (w *Wallet) MintTokens(hash string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	msg := cashu.PostMintRequest{Outputs: blindedMessages}
	outputs, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("error marshaling blinded messages: %v", err)
	}

	resp, err := http.Post(w.mintURL+"/mint?hash="+hash, "application/json", bytes.NewBuffer(outputs))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var mintResponse cashu.PostMintResponse
	err = json.NewDecoder(resp.Body).Decode(&mintResponse)
	if err != nil {
		return nil, fmt.Errorf("error decoding response from mint: %v", err)
	}

	return mintResponse.Promises, nil
}

func (w *Wallet) ConstructProofs(blindedSignatures cashu.BlindedSignatures,
	secrets [][]byte, rs []*secp256k1.PrivateKey, keyset *crypto.Keyset) (cashu.Proofs, error) {

	// check that length of slices match

	proofs := make(cashu.Proofs, len(blindedSignatures))

	for i, blindedSignature := range blindedSignatures {
		C_bytes, err := hex.DecodeString(blindedSignature.C_)
		if err != nil {
			return nil, fmt.Errorf("error unblinding signature: %v", err)
		}
		C_, err := secp256k1.ParsePubKey(C_bytes)
		if err != nil {
			return nil, fmt.Errorf("error unblinding signature: %v", err)
		}

		var pubKey []byte
		for _, kp := range keyset.KeyPairs {
			if kp.Amount == blindedSignature.Amount {
				pubKey = kp.PublicKey
			}
		}

		K, err := secp256k1.ParsePubKey(pubKey)
		if err != nil {
			return nil, fmt.Errorf("error unblinding signature: %v", err)
		}

		C := crypto.UnblindSignature(C_, rs[i], K)
		Cstr := hex.EncodeToString(C.SerializeCompressed())

		secret := hex.EncodeToString(secrets[i])
		proof := cashu.Proof{Amount: blindedSignature.Amount,
			Secret: secret, C: Cstr, Id: blindedSignature.Id}

		proofs = append(proofs, proof)
	}

	return proofs, nil
}

func (w *Wallet) SaveInvoice(invoice lightning.Invoice) error {
	return w.db.SaveInvoice(invoice)
}

func (w *Wallet) GetInvoice(pr string) *lightning.Invoice {
	return w.db.GetInvoice(pr)
}
