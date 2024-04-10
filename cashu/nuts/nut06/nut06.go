package nut06

import (
	"bytes"
	"encoding/json"
	"slices"
)

type MintInfo struct {
	Name            string     `json:"name"`
	Pubkey          string     `json:"pubkey"`
	Version         string     `json:"version"`
	Description     string     `json:"description"`
	LongDescription string     `json:"description_long,omitempty"`
	Contact         [][]string `json:"contact,omitempty"`
	Motd            string     `json:"motd,omitempty"`
	Nuts            NutsMap    `json:"nuts"`
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
