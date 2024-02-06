package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

const maxOrder = 64

// mint url to map of keyset id to keyset
type KeysetsMap map[string]map[string]Keyset

type Keyset struct {
	Id      string
	MintURL string
	Unit    string
	Active  bool
	Keys    map[uint64]KeyPair
}

type KeyPair struct {
	PrivateKey *secp256k1.PrivateKey
	PublicKey  *secp256k1.PublicKey
}

func GenerateKeyset(seed, derivationPath string) *Keyset {
	keys := make(map[uint64]KeyPair, maxOrder)

	for i := 0; i < maxOrder; i++ {
		amount := uint64(math.Pow(2, float64(i)))
		hash := sha256.Sum256([]byte(seed + derivationPath + strconv.FormatUint(amount, 10)))
		privKey, pubKey := btcec.PrivKeyFromBytes(hash[:])
		keys[amount] = KeyPair{PrivateKey: privKey, PublicKey: pubKey}
	}
	keysetId := DeriveKeysetId(keys)
	return &Keyset{Id: keysetId, Unit: "sat", Active: true, Keys: keys}
}

func DeriveKeysetId(keyset map[uint64]KeyPair) string {
	type pubkey struct {
		amount uint64
		pk     *secp256k1.PublicKey
	}
	pubkeys := make([]pubkey, len(keyset))
	i := 0
	for amount, key := range keyset {
		pubkeys[i] = pubkey{amount, key.PublicKey}
		i++
	}
	sort.Slice(pubkeys, func(i, j int) bool {
		return pubkeys[i].amount < pubkeys[j].amount
	})

	keys := make([]byte, 0)
	for _, key := range pubkeys {
		keys = append(keys, key.pk.SerializeCompressed()...)
	}
	hash := sha256.New()
	hash.Write(keys)

	return "00" + hex.EncodeToString(hash.Sum(nil))[:14]
}

func (ks *Keyset) DerivePublic() map[uint64]string {
	pubkeys := make(map[uint64]string)
	for amount, key := range ks.Keys {
		pubkey := hex.EncodeToString(key.PublicKey.SerializeCompressed())
		pubkeys[amount] = pubkey
	}
	return pubkeys
}
