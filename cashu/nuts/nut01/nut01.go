package nut01

type GetKeysResponse struct {
	Keysets []Keyset `json:"keysets"`
}

type Keyset struct {
	Id   string            `json:"id"`
	Unit string            `json:"unit"`
	Keys map[uint64]string `json:"keys"`
}
