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

const MAX_ORDER = 64

type Keyset struct {
	Id          string
	Unit        string
	Active      bool
	Keys        map[uint64]KeyPair
	InputFeePpk uint
}

type KeyPair struct {
	PrivateKey *secp256k1.PrivateKey
	PublicKey  *secp256k1.PublicKey
}

func GenerateKeyset(seed, derivationPath string, inputFeePpk uint) *Keyset {
	keys := make(map[uint64]KeyPair, MAX_ORDER)

	pks := make(map[uint64]*secp256k1.PublicKey)
	for i := 0; i < MAX_ORDER; i++ {
		amount := uint64(math.Pow(2, float64(i)))
		hash := sha256.Sum256([]byte(seed + derivationPath + strconv.FormatUint(amount, 10)))
		privKey, pubKey := btcec.PrivKeyFromBytes(hash[:])
		keys[amount] = KeyPair{PrivateKey: privKey, PublicKey: pubKey}
		pks[amount] = pubKey
	}
	keysetId := DeriveKeysetId(pks)

	return &Keyset{
		Id:          keysetId,
		Unit:        "sat",
		Active:      true,
		Keys:        keys,
		InputFeePpk: inputFeePpk,
	}
}

// DeriveKeysetId returns the string ID derived from the map keyset
// The steps to derive the ID are:
// - sort public keys by their amount in ascending order
// - concatenate all public keys to one byte array
// - HASH_SHA256 the concatenated public keys
// - take the first 14 characters of the hex-encoded hash
// - prefix it with a keyset ID version byte
func DeriveKeysetId(keyset map[uint64]*secp256k1.PublicKey) string {
	type pubkey struct {
		amount uint64
		pk     *secp256k1.PublicKey
	}
	pubkeys := make([]pubkey, len(keyset))
	i := 0
	for amount, key := range keyset {
		pubkeys[i] = pubkey{amount, key}
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

// DerivePublic returns the keyset's public keys as
// a map of amounts uint64 to strings that represents the public key
func (ks *Keyset) DerivePublic() map[uint64]string {
	pubkeys := make(map[uint64]string)
	for amount, key := range ks.Keys {
		pubkey := hex.EncodeToString(key.PublicKey.SerializeCompressed())
		pubkeys[amount] = pubkey
	}
	return pubkeys
}

type KeysetTemp struct {
	Id          string
	Unit        string
	Active      bool
	Keys        map[uint64]json.RawMessage
	InputFeePpk uint
}

func (ks *Keyset) MarshalJSON() ([]byte, error) {
	temp := &KeysetTemp{
		Id:     ks.Id,
		Unit:   ks.Unit,
		Active: ks.Active,
		Keys: func() map[uint64]json.RawMessage {
			m := make(map[uint64]json.RawMessage)
			for k, v := range ks.Keys {
				b, _ := json.Marshal(&v)
				m[k] = b
			}
			return m
		}(),
		InputFeePpk: ks.InputFeePpk,
	}

	return json.Marshal(temp)
}

func (ks *Keyset) UnmarshalJSON(data []byte) error {
	temp := &KeysetTemp{}

	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	ks.Id = temp.Id
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
	var privKey []byte

	if kp.PrivateKey != nil {
		privKey = append(privKey, kp.PrivateKey.Serialize()...)
	}
	res := KeyPairTemp{
		PrivateKey: privKey,
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

// KeysetsMap maps a mint url to map of string keyset id to keyset
type KeysetsMap map[string]map[string]WalletKeyset

type WalletKeyset struct {
	Id         string
	MintURL    string
	Unit       string
	Active     bool
	PublicKeys map[uint64]*secp256k1.PublicKey
	Counter    uint32
}

type WalletKeysetTemp struct {
	Id         string
	MintURL    string
	Unit       string
	Active     bool
	PublicKeys map[uint64][]byte
	Counter    uint32
}

func (wk *WalletKeyset) MarshalJSON() ([]byte, error) {
	temp := &WalletKeysetTemp{
		Id:      wk.Id,
		MintURL: wk.MintURL,
		Unit:    wk.Unit,
		Active:  wk.Active,
		PublicKeys: func() map[uint64][]byte {
			m := make(map[uint64][]byte)
			for k, v := range wk.PublicKeys {
				m[k] = v.SerializeCompressed()
			}
			return m
		}(),
		Counter: wk.Counter,
	}

	return json.Marshal(temp)
}

func (wk *WalletKeyset) UnmarshalJSON(data []byte) error {
	temp := &WalletKeysetTemp{}

	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	wk.Id = temp.Id
	wk.MintURL = temp.MintURL
	wk.Unit = temp.Unit
	wk.Active = temp.Active
	wk.Counter = temp.Counter

	wk.PublicKeys = make(map[uint64]*secp256k1.PublicKey)
	for k, v := range temp.PublicKeys {
		kp, err := secp256k1.ParsePubKey(v)
		if err != nil {
			return err
		}

		wk.PublicKeys[k] = kp
	}

	return nil
}
