package mint

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/elnosh/gonuts/config"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	bolt "go.etcd.io/bbolt"
)

type Mint struct {
	db *bolt.DB

	// current keyset
	Keyset *crypto.Keyset

	// list of all keysets
	Keysets []*crypto.Keyset

	LightningClient lightning.Client
}

func LoadMint(config config.Config) (*Mint, error) {
	path := setMintDBPath()
	db, err := bolt.Open(filepath.Join(path, "mint.db"), 0600, nil)
	if err != nil {
		log.Fatalf("error starting mint: %v", err)
	}

	keyset := crypto.GenerateKeyset(config.PrivateKey, config.DerivationPath)
	mint := &Mint{db: db, Keyset: keyset}
	err = mint.InitKeysetsBucket(*keyset)
	if err != nil {
		return nil, fmt.Errorf("error setting keyset: %v", err)
	}
	mint.Keysets = mint.GetKeysets()

	mint.LightningClient = lightning.NewLightningClient()

	return mint, nil
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

func (m *Mint) KeysetList() []string {
	keysetIds := make([]string, len(m.Keysets))

	for i, keyset := range m.Keysets {
		keysetIds[i] = keyset.Id
	}
	return keysetIds
}
