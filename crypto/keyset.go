package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"

	"github.com/btcsuite/btcd/btcec/v2"
)

const maxOrder = 64

// mint url to map of keyset id to keyset
type KeysetsMap map[string]map[string]Keyset

type Keyset struct {
	Id       string
	MintURL  string
	Unit     string
	Active   bool
	KeyPairs []KeyPair
}

type KeyPair struct {
	Amount     uint64
	PrivateKey []byte
	PublicKey  []byte
}

func GenerateKeyset(seed, derivationPath string) *Keyset {
	keyPairs := make([]KeyPair, maxOrder)

	for i := 0; i < maxOrder; i++ {
		amount := uint64(math.Pow(2, float64(i)))
		hash := sha256.Sum256([]byte(seed + derivationPath + strconv.FormatUint(amount, 10)))
		privKey, pubKey := btcec.PrivKeyFromBytes(hash[:])
		keyPairs[i] = KeyPair{Amount: amount, PrivateKey: privKey.Serialize(), PublicKey: pubKey.SerializeCompressed()}
	}
	keysetId := DeriveKeysetId(keyPairs)
	return &Keyset{Id: keysetId, Unit: "sat", Active: true, KeyPairs: keyPairs}
}

func DeriveKeysetId(keys []KeyPair) string {
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Amount < keys[j].Amount
	})

	pubkeys := make([]byte, 0)
	for _, key := range keys {
		pubkeys = append(pubkeys, key.PublicKey...)
	}
	hash := sha256.New()
	hash.Write(pubkeys)

	return "00" + hex.EncodeToString(hash.Sum(nil))[:14]
}

func (ks *Keyset) DerivePublic() map[uint64]string {
	pubKeys := make(map[uint64]string)
	for _, key := range ks.KeyPairs {
		pubkey := hex.EncodeToString(key.PublicKey)
		pubKeys[key.Amount] = pubkey
	}

	return pubKeys
}
