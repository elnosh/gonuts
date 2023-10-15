package crypto

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math"
	"sort"
	"strconv"

	"github.com/btcsuite/btcd/btcec/v2"
)

const maxOrder = 64

type Keyset struct {
	Id       string
	KeyPairs []KeyPair
}

type KeyPair struct {
	Amount     uint64
	PrivateKey *btcec.PrivateKey
	PublicKey  *btcec.PublicKey
}

func GenerateKeyset(seed, derivationPath string) *Keyset {
	keyPairs := make([]KeyPair, maxOrder)

	for i := 0; i < maxOrder; i++ {
		amount := uint64(math.Pow(2, float64(i)))
		hash := sha256.Sum256([]byte(seed + derivationPath + strconv.FormatUint(amount, 10)))
		privKey, pubKey := btcec.PrivKeyFromBytes(hash[:])
		keyPairs[i] = KeyPair{Amount: amount, PrivateKey: privKey, PublicKey: pubKey}
	}
	keysetId := DeriveKeysetId(keyPairs)
	return &Keyset{Id: keysetId, KeyPairs: keyPairs}
}

func DeriveKeysetId(keys []KeyPair) string {
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Amount < keys[j].Amount
	})

	pubkeys := make([]byte, 0)
	for _, key := range keys {
		pubkeys = append(pubkeys, key.PublicKey.SerializeCompressed()...)
	}
	hash := sha256.New()
	hash.Write(pubkeys)
	encoded := base64.StdEncoding.EncodeToString(hash.Sum(nil))
	return encoded[:12]
}

func (ks *Keyset) DerivePublic() map[uint64]string {
	pubKeys := make(map[uint64]string)
	for _, key := range ks.KeyPairs {
		pubkey := hex.EncodeToString(key.PublicKey.SerializeCompressed())
		pubKeys[key.Amount] = pubkey
	}

	return pubKeys
}
