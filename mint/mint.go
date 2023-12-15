package mint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/config"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	bolt "go.etcd.io/bbolt"
)

type Mint struct {
	db *bolt.DB

	// active keysets
	ActiveKeysets []crypto.Keyset

	// list of all keysets (both active and inactive)
	Keysets []crypto.Keyset

	LightningClient lightning.Client
}

func LoadMint(config config.Config) (*Mint, error) {
	path := setMintDBPath()
	db, err := bolt.Open(filepath.Join(path, "mint.db"), 0600, nil)
	if err != nil {
		log.Fatalf("error starting mint: %v", err)
	}

	keyset := crypto.GenerateKeyset(config.PrivateKey, config.DerivationPath)
	mint := &Mint{db: db, ActiveKeysets: []crypto.Keyset{*keyset}}
	err = mint.initMintBuckets()
	if err != nil {
		return nil, fmt.Errorf("error setting up db: %v", err)
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

// creates lightning invoice and saves it in db
func (m *Mint) RequestInvoice(amount uint64) (*lightning.Invoice, error) {
	invoice, err := m.LightningClient.CreateInvoice(amount)
	if err != nil {
		return nil, fmt.Errorf("error creating invoice: %v", err)
	}

	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		return nil, fmt.Errorf("error creating invoice: %v", err)
	}

	hash := sha256.Sum256(randomBytes)
	hashStr := hex.EncodeToString(hash[:])

	invoice.Id = hashStr
	err = m.SaveInvoice(invoice)
	if err != nil {
		return nil, fmt.Errorf("error creating invoice: %v", err)
	}

	return &invoice, nil
}

// id - quote id to lookup invoice
func (m *Mint) MintTokens(id string, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {
	invoice := m.GetInvoice(id)
	if invoice == nil {
		return nil, cashu.InvoiceNotExistErr
	}

	var blindedSignatures cashu.BlindedSignatures

	settled := m.LightningClient.InvoiceSettled(invoice.PaymentHash)
	if settled {
		if invoice.Redeemed {
			return nil, cashu.InvoiceTokensIssuedErr
		}

		var totalAmount uint64 = 0
		for _, message := range blindedMessages {
			totalAmount += message.Amount
		}

		if totalAmount > invoice.Amount {
			return nil, cashu.OutputsOverInvoiceErr
		}

		var err error
		blindedSignatures, err = cashu.SignBlindedMessages(blindedMessages, &m.ActiveKeysets[0])
		if err != nil {
			return nil, cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}

		invoice.Settled = true
		invoice.Redeemed = true
		m.SaveInvoice(*invoice)
	} else {
		return nil, cashu.InvoiceNotPaidErr
	}

	return blindedSignatures, nil
}

func (m *Mint) Swap(proofs cashu.Proofs, blindedMessages cashu.BlindedMessages) (cashu.BlindedSignatures, error) {

	for _, proof := range proofs {
		dbProof := m.GetProof(proof.Secret)
		if dbProof != nil {
			return nil, cashu.ProofAlreadyUsedErr
		}
		secret, err := hex.DecodeString(proof.Secret)
		if err != nil {
			cashuErr := cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
			return nil, cashuErr
		}

		var privateKey []byte
		for _, kp := range m.ActiveKeysets[0].KeyPairs {
			if kp.Amount == proof.Amount {
				privateKey = kp.PrivateKey
			}
		}
		k := secp256k1.PrivKeyFromBytes(privateKey)

		Cbytes, err := hex.DecodeString(proof.C)
		if err != nil {
			cashuErr := cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
			return nil, cashuErr
		}

		C, err := secp256k1.ParsePubKey(Cbytes)
		if err != nil {
			cashuErr := cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
			return nil, cashuErr
		}

		if !crypto.Verify(secret, k, C) {
			return nil, cashu.InvalidProofErr
		}
	}

	// if verification complete, sign blinded messages and add used proofs to db
	blindedSignatures, err := cashu.SignBlindedMessages(blindedMessages, &m.ActiveKeysets[0])
	if err != nil {
		cashuErr := cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		return nil, cashuErr
	}

	for _, proof := range proofs {
		m.SaveProof(proof)
	}

	return blindedSignatures, nil
}
