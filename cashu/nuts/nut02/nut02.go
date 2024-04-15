// Package nut02 contains structs as defined in [NUT-02]
//
// [NUT-02]: https://github.com/cashubtc/nuts/blob/main/02.md
package nut02

type GetKeysetsResponse struct {
	Keysets []Keyset `json:"keysets"`
}

type Keyset struct {
	Id     string `json:"id"`
	Unit   string `json:"unit"`
	Active bool   `json:"active"`
}
