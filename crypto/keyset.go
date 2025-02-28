package crypto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
)

const MAX_ORDER = 60

type MintKeyset struct {
	Id                string
	Unit              string
	Active            bool
	DerivationPathIdx uint32
	Keys              map[uint64]KeyPair
	InputFeePpk       uint
}

type KeyPair struct {
	PrivateKey *secp256k1.PrivateKey
	PublicKey  *secp256k1.PublicKey
}

func DeriveKeysetPath(key *hdkeychain.ExtendedKey, index uint32) (*hdkeychain.ExtendedKey, error) {
	// path m/0'
	child, err := key.Derive(hdkeychain.HardenedKeyStart + 0)
	if err != nil {
		return nil, err
	}

	// path m/0'/0' for sat
	unitPath, err := child.Derive(hdkeychain.HardenedKeyStart + 0)
	if err != nil {
		return nil, err
	}

	// path m/0'/0'/index'
	keysetPath, err := unitPath.Derive(hdkeychain.HardenedKeyStart + index)
	if err != nil {
		return nil, err
	}

	return keysetPath, nil
}

func GenerateKeyset(master *hdkeychain.ExtendedKey, index uint32, inputFeePpk uint) (*MintKeyset, error) {
	keys := make(map[uint64]KeyPair, MAX_ORDER)

	keysetPath, err := DeriveKeysetPath(master, index)
	if err != nil {
		return nil, err
	}

	pks := make(map[uint64]*secp256k1.PublicKey)
	for i := 0; i < MAX_ORDER; i++ {
		amount := uint64(math.Pow(2, float64(i)))
		amountPath, err := keysetPath.Derive(hdkeychain.HardenedKeyStart + uint32(i))
		if err != nil {
			return nil, err
		}

		privKey, err := amountPath.ECPrivKey()
		if err != nil {
			return nil, err
		}
		pubKey, err := amountPath.ECPubKey()
		if err != nil {
			return nil, err
		}

		keys[amount] = KeyPair{PrivateKey: privKey, PublicKey: pubKey}
		pks[amount] = pubKey
	}
	keysetId := DeriveKeysetId(pks)

	return &MintKeyset{
		Id:                keysetId,
		Unit:              cashu.Sat.String(),
		Active:            true,
		DerivationPathIdx: index,
		Keys:              keys,
		InputFeePpk:       inputFeePpk,
	}, nil
}

type PublicKeys map[uint64]*secp256k1.PublicKey

// Custom marshaller to display sorted keys
func (pks PublicKeys) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	amounts := make([]uint64, len(pks))
	i := 0
	for k := range pks {
		amounts[i] = k
		i++
	}
	slices.Sort(amounts)

	for j, amount := range amounts {
		if j != 0 {
			buf.WriteByte(',')
		}

		// marshal key
		key, err := json.Marshal(amount)
		if err != nil {
			return nil, err
		}
		buf.WriteByte('"')
		buf.Write(key)
		buf.WriteByte('"')
		buf.WriteByte(':')
		// marshal value
		pubkey := hex.EncodeToString(pks[amount].SerializeCompressed())
		val, err := json.Marshal(pubkey)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (pks PublicKeys) UnmarshalJSON(data []byte) error {
	var tempKeys map[uint64]string
	if err := json.Unmarshal(data, &tempKeys); err != nil {
		return err
	}

	for amount, key := range tempKeys {
		keyBytes, err := hex.DecodeString(key)
		if err != nil {
			return err
		}
		publicKey, err := secp256k1.ParsePubKey(keyBytes)
		if err != nil {
			return fmt.Errorf("invalid public key: %v", err)
		}
		pks[amount] = publicKey
	}
	return nil
}

// DeriveKeysetId returns the string ID derived from the map keyset
// The steps to derive the ID are:
// - sort public keys by their amount in ascending order
// - concatenate all public keys to one byte array
// - HASH_SHA256 the concatenated public keys
// - take the first 14 characters of the hex-encoded hash
// - prefix it with a keyset ID version byte
func DeriveKeysetId(keyset PublicKeys) string {
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

	keys := make([]byte, 0, len(pubkeys)*33)
	for _, key := range pubkeys {
		keys = append(keys, key.pk.SerializeCompressed()...)
	}
	hash := sha256.New()
	hash.Write(keys)

	return "00" + hex.EncodeToString(hash.Sum(nil))[:14]
}

// DerivePublic returns the keyset's public keys as
// a map of amounts uint64 to strings that represents the public key
func (ks *MintKeyset) PublicKeys() PublicKeys {
	pubkeys := make(map[uint64]*secp256k1.PublicKey, len(ks.Keys))
	for amount, key := range ks.Keys {
		pubkeys[amount] = key.PublicKey
	}
	return pubkeys
}

type keysetTemp struct {
	Id          string
	Unit        string
	Active      bool
	Keys        map[uint64]json.RawMessage
	InputFeePpk uint
}

func (ks *MintKeyset) MarshalJSON() ([]byte, error) {
	temp := &keysetTemp{
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

func (ks *MintKeyset) UnmarshalJSON(data []byte) error {
	temp := &keysetTemp{}

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

type keyPairTemp struct {
	PrivateKey []byte `json:"private_key"`
	PublicKey  []byte `json:"public_key"`
}

func (kp *KeyPair) MarshalJSON() ([]byte, error) {
	var privKey []byte

	if kp.PrivateKey != nil {
		privKey = append(privKey, kp.PrivateKey.Serialize()...)
	}
	res := keyPairTemp{
		PrivateKey: privKey,
		PublicKey:  kp.PublicKey.SerializeCompressed(),
	}
	return json.Marshal(res)
}

func (kp *KeyPair) UnmarshalJSON(data []byte) error {
	aux := &keyPairTemp{}
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
type KeysetsMap map[string][]WalletKeyset

type WalletKeyset struct {
	Id          string
	MintURL     string
	Unit        string
	Active      bool
	PublicKeys  map[uint64]*secp256k1.PublicKey
	Counter     uint32
	InputFeePpk uint
}

type walletKeysetTemp struct {
	Id          string
	MintURL     string
	Unit        string
	Active      bool
	PublicKeys  map[uint64][]byte
	Counter     uint32
	InputFeePpk uint
}

func (wk *WalletKeyset) MarshalJSON() ([]byte, error) {
	temp := &walletKeysetTemp{
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
		Counter:     wk.Counter,
		InputFeePpk: wk.InputFeePpk,
	}

	return json.Marshal(temp)
}

func (wk *WalletKeyset) UnmarshalJSON(data []byte) error {
	temp := &walletKeysetTemp{}

	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	wk.Id = temp.Id
	wk.MintURL = temp.MintURL
	wk.Unit = temp.Unit
	wk.Active = temp.Active
	wk.Counter = temp.Counter
	wk.InputFeePpk = temp.InputFeePpk

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
