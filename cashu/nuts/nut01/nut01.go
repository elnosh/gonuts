// Package nut01 contains structs as defined in [NUT-01]
//
// [NUT-01]: https://github.com/cashubtc/nuts/blob/main/01.md
package nut01

import (
	"encoding/json"

	"github.com/elnosh/gonuts/crypto"
)

type GetKeysResponse struct {
	Keysets []Keyset `json:"keysets"`
}

type Keyset struct {
	Id   string            `json:"id"`
	Unit string            `json:"unit"`
	Keys crypto.PublicKeys `json:"keys"`
}

func (kr *GetKeysResponse) UnmarshalJSON(data []byte) error {
	var tempResponse struct {
		Keysets []json.RawMessage
	}
	if err := json.Unmarshal(data, &tempResponse); err != nil {
		return nil
	}

	keysets := make([]Keyset, len(tempResponse.Keysets))
	for i, k := range tempResponse.Keysets {
		var keyset Keyset
		if err := json.Unmarshal(k, &keyset); err != nil {
			return err
		}
		keysets[i] = keyset
	}
	kr.Keysets = keysets

	return nil
}

func (ks *Keyset) UnmarshalJSON(data []byte) error {
	var tempKeyset struct {
		Id   string
		Unit string
		Keys json.RawMessage
	}

	if err := json.Unmarshal(data, &tempKeyset); err != nil {
		return err
	}

	ks.Id = tempKeyset.Id
	ks.Unit = tempKeyset.Unit

	publicKeys := make(crypto.PublicKeys, len(tempKeyset.Keys))
	if err := json.Unmarshal(tempKeyset.Keys, &publicKeys); err != nil {
		return err
	}
	ks.Keys = publicKeys

	return nil
}
