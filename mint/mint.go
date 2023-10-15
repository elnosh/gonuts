package mint

import (
	"log"
	"os"
	"path/filepath"

	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/config"
	bolt "go.etcd.io/bbolt"
)

type Mint struct {
	db *bolt.DB
	// current keyset
	Keyset *crypto.Keyset
}

func SetupMint(config config.Config) *Mint {
	path := setMintDBPath()
	db, err := bolt.Open(filepath.Join(path, "mint.db"), 0600, nil)
	if err != nil {
		log.Fatalf("error starting mint: %v", err)
	}

	keyset := crypto.GenerateKeyset(config.PrivateKey, config.DerivationPath)

	return &Mint{db: db, Keyset: keyset}
}

func setMintDBPath() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	path := filepath.Join(homedir, ".gonuts", "mint")
	err = os.MkdirAll(path, 0700)
	if err != nil {
		log.Fatal(err)
	}
	return path
}
