package wallet

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	bolt "go.etcd.io/bbolt"
)

const (
	MINT_URL = "MINT_URL"
)

type Wallet struct {
	db *bolt.DB

	// current mint url
	mintURL string

	// current keyset
	keyset  *crypto.Keyset
	keysets []crypto.Keyset

	proofs cashu.Proofs
}

func CreateWalletDB() error {
	path := setWalletPath()
	db, err := bolt.Open(filepath.Join(path, "wallet.db"), 0600, nil)
	if err != nil {
		log.Fatalf("error creating wallet: %v", err)
	}

	if walletExists(db) {
		return errors.New("wallet already exists")
	}

	wallet := &Wallet{db: db}
	defer wallet.db.Close()

	err = wallet.initWalletBuckets()
	if err != nil {
		return fmt.Errorf("error creating wallet: %v", err)
	}

	return nil
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

func walletExists(db *bolt.DB) bool {
	exists := false
	db.View(func(tx *bolt.Tx) error {
		keysetsb := tx.Bucket([]byte(keysetsBucket))
		proofsb := tx.Bucket([]byte(proofsBucket))

		if keysetsb != nil && proofsb != nil {
			exists = true
		}
		return nil
	})
	return exists
}

func LoadWallet() (*Wallet, error) {
	path := setWalletPath()
	db, err := bolt.Open(filepath.Join(path, "wallet.db"), 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("error opening db: %v", err)
	}

	if !walletExists(db) {
		return nil, errors.New("wallet does not exist. Create one first")
	}

	wallet := &Wallet{db: db}

	wallet.keysets = wallet.getKeysets()
	wallet.proofs = wallet.getProofs()

	wallet.mintURL = os.Getenv(MINT_URL)
	if wallet.mintURL == "" {
		// if no mint specified, default to localhost
		wallet.mintURL = "https://127.0.0.1:3338"
	}

	keyset, err := getMintCurrentKeyset(wallet.mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting current keyset from mint: %v", err)
	}
	wallet.keyset = keyset

	return wallet, nil
}

func getMintCurrentKeyset(mintURL string) (*crypto.Keyset, error) {
	resp, err := http.Get(mintURL + "/keys")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetRes map[string]string
	json.NewDecoder(resp.Body).Decode(&keysetRes)

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
