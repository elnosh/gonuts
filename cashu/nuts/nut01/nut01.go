package nut01

import (
	"bytes"
	"encoding/json"
	"slices"
)

type GetKeysResponse struct {
	Keysets []Keyset `json:"keysets"`
}

type Keyset struct {
	Id   string  `json:"id"`
	Unit string  `json:"unit"`
	Keys KeysMap `json:"keys"`
}

type KeysMap map[uint64]string

// custom marshaller to display sorted keys
func (km KeysMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	amounts := make([]uint64, len(km))
	i := 0
	for k := range km {
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
		pubkey := km[amount]
		val, err := json.Marshal(pubkey)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}
