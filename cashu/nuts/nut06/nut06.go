// Package nut06 contains structs as defined in [NUT-06]
//
// [NUT-06]: https://github.com/cashubtc/nuts/blob/main/06.md
package nut06

import (
	"bytes"
	"encoding/json"
	"slices"
)

type MintInfo struct {
	Name            string        `json:"name"`
	Pubkey          string        `json:"pubkey"`
	Version         string        `json:"version"`
	Description     string        `json:"description"`
	LongDescription string        `json:"description_long,omitempty"`
	Contact         []ContactInfo `json:"contact,omitempty"`
	Motd            string        `json:"motd,omitempty"`
	Nuts            NutsMap       `json:"nuts"`
}

type ContactInfo struct {
	Method string `json:"method"`
	Info   string `json:"info"`
}

// custom unmarshal to ignore contact field if on old format
func (mi *MintInfo) UnmarshalJSON(data []byte) error {
	var tempInfo struct {
		Name            string          `json:"name"`
		Pubkey          string          `json:"pubkey"`
		Version         string          `json:"version"`
		Description     string          `json:"description"`
		LongDescription string          `json:"description_long,omitempty"`
		Contact         json.RawMessage `json:"contact,omitempty"`
		Motd            string          `json:"motd,omitempty"`
		Nuts            NutsMap         `json:"nuts"`
	}

	if err := json.Unmarshal(data, &tempInfo); err != nil {
		return err
	}

	mi.Name = tempInfo.Name
	mi.Pubkey = tempInfo.Pubkey
	mi.Version = tempInfo.Version
	mi.Description = tempInfo.Description
	mi.LongDescription = tempInfo.LongDescription
	mi.Motd = tempInfo.Motd
	mi.Nuts = tempInfo.Nuts
	json.Unmarshal(tempInfo.Contact, &mi.Contact)

	return nil
}

type NutSetting struct {
	Methods  []MethodSetting `json:"methods"`
	Disabled bool            `json:"disabled"`
}

type MethodSetting struct {
	Method    string `json:"method"`
	Unit      string `json:"unit"`
	MinAmount uint64 `json:"min_amount,omitempty"`
	MaxAmount uint64 `json:"max_amount,omitempty"`
}

type NutsMap map[int]any

// Custom marshaller to display supported nuts in order
func (nm NutsMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	nuts := make([]int, len(nm))
	i := 0
	for k := range nm {
		nuts[i] = k
		i++
	}
	slices.Sort(nuts)

	for j, num := range nuts {
		if j != 0 {
			buf.WriteByte(',')
		}

		// marshal key
		key, err := json.Marshal(num)
		if err != nil {
			return nil, err
		}
		buf.WriteByte('"')
		buf.Write(key)
		buf.WriteByte('"')
		buf.WriteByte(':')
		// marshal value
		nutVal := nm[num]
		val, err := json.Marshal(nutVal)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}
