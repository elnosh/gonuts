package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

type KeysetTemp struct {
	Id      string
	MintURL string
	Unit    string
	Active  bool
	Keys    map[uint64]json.RawMessage
}

func (ks *Keyset) MarshalJSON() ([]byte, error) {
	temp := &KeysetTemp{
		Id:      ks.Id,
		MintURL: ks.MintURL,
		Unit:    ks.Unit,
		Active:  ks.Active,
		Keys: func() map[uint64]json.RawMessage {
			m := make(map[uint64]json.RawMessage)
			for k, v := range ks.Keys {
				b, _ := json.Marshal(&v)
				m[k] = b
			}
			return m
		}(),
	}

	return json.Marshal(temp)
}

func (ks *Keyset) UnmarshalJSON(data []byte) error {
	temp := &KeysetTemp{}

	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	ks.Id = temp.Id
	ks.MintURL = temp.MintURL
	ks.Unit = temp.Unit
	ks.Active = temp.Active

	ks.Keys = make(map[uint64]KeyPair)
	for k, v := range temp.Keys {
		var kp KeyPair
		err := json.Unmarshal(v, &kp)
		if err != nil {
			return err
		}
		ks.Keys[k] = kp
	}

	return nil
}

type KeyPairTemp struct {
	PrivateKey []byte `json:"private_key"`
	PublicKey  []byte `json:"public_key"`
}

func (kp *KeyPair) MarshalJSON() ([]byte, error) {
	res := KeyPairTemp{
		PrivateKey: kp.PrivateKey.Serialize(),
		PublicKey:  kp.PublicKey.SerializeCompressed(),
	}
	return json.Marshal(res)
}

func (kp *KeyPair) UnmarshalJSON(data []byte) error {
	aux := &KeyPairTemp{}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	kp.PrivateKey = secp256k1.PrivKeyFromBytes(aux.PrivateKey)

	var err error
	kp.PublicKey, err = secp256k1.ParsePubKey(aux.PublicKey)
	if err != nil {
		return err
	}

	return nil
}
