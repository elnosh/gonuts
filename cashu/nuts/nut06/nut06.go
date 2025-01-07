// Package nut06 contains structs as defined in [NUT-06]
//
// [NUT-06]: https://github.com/cashubtc/nuts/blob/main/06.md
package nut06

import (
	"encoding/json"
)

type MintInfo struct {
	Name            string        `json:"name"`
	Pubkey          string        `json:"pubkey"`
	Version         string        `json:"version"`
	Description     string        `json:"description"`
	LongDescription string        `json:"description_long,omitempty"`
	Contact         []ContactInfo `json:"contact,omitempty"`
	Motd            string        `json:"motd,omitempty"`
	IconURL         string        `json:"icon_url,omitempty"`
	URLs            []string      `json:"urls,omitempty"`
	Time            int64         `json:"time,omitempty"`
	Nuts            Nuts          `json:"nuts"`
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
		IconURL         string          `json:"icon_url,omitempty"`
		URLs            []string        `json:"urls,omitempty"`
		Time            int64           `json:"time,omitempty"`
		Nuts            Nuts            `json:"nuts"`
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
	mi.IconURL = tempInfo.IconURL
	mi.URLs = tempInfo.URLs
	mi.Time = tempInfo.Time
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

type Supported struct {
	Supported bool `json:"supported"`
}

type Nuts struct {
	Nut04 NutSetting  `json:"4"`
	Nut05 NutSetting  `json:"5"`
	Nut07 Supported   `json:"7"`
	Nut08 Supported   `json:"8"`
	Nut09 Supported   `json:"9"`
	Nut10 Supported   `json:"10"`
	Nut11 Supported   `json:"11"`
	Nut12 Supported   `json:"12"`
	Nut14 Supported   `json:"14"`
	Nut15 *NutSetting `json:"15,omitempty"`
}

// custom unmarshaller because format to signal support for nut-15 changed.
// So it will first try to Unmarshal to new format and if there is an error
// it will try old format
func (nuts *Nuts) UnmarshalJSON(data []byte) error {
	var tempNuts struct {
		Nut04 NutSetting      `json:"4"`
		Nut05 NutSetting      `json:"5"`
		Nut07 Supported       `json:"7"`
		Nut08 Supported       `json:"8"`
		Nut09 Supported       `json:"9"`
		Nut10 Supported       `json:"10"`
		Nut11 Supported       `json:"11"`
		Nut12 Supported       `json:"12"`
		Nut14 Supported       `json:"14"`
		Nut15 json.RawMessage `json:"15,omitempty"`
	}

	if err := json.Unmarshal(data, &tempNuts); err != nil {
		return err
	}

	nuts.Nut04 = tempNuts.Nut04
	nuts.Nut05 = tempNuts.Nut05
	nuts.Nut07 = tempNuts.Nut07
	nuts.Nut08 = tempNuts.Nut08
	nuts.Nut09 = tempNuts.Nut09
	nuts.Nut10 = tempNuts.Nut10
	nuts.Nut11 = tempNuts.Nut11
	nuts.Nut12 = tempNuts.Nut12
	nuts.Nut14 = tempNuts.Nut14

	if err := json.Unmarshal(tempNuts.Nut15, &nuts.Nut15); err != nil {
		var nut15Methods []MethodSetting
		if err := json.Unmarshal(tempNuts.Nut15, &nut15Methods); err != nil {
			nuts.Nut15 = &NutSetting{Methods: []MethodSetting{}}
		}
		nuts.Nut15 = &NutSetting{Methods: nut15Methods}
	}

	return nil
}
