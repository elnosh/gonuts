package nut02

type GetKeysetsResponse struct {
	Keysets []Keyset `json:"keysets"`
}

type Keyset struct {
	Id     string `json:"id"`
	Unit   string `json:"unit"`
	Active bool   `json:"active"`
}
