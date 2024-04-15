// Package nut05 contains structs as defined in [NUT-05]
//
// [NUT-05]: https://github.com/cashubtc/nuts/blob/main/05.md
package nut05

import "github.com/elnosh/gonuts/cashu"

type PostMeltQuoteBolt11Request struct {
	Request string `json:"request"`
	Unit    string `json:"unit"`
}

type PostMeltQuoteBolt11Response struct {
	Quote      string `json:"quote"`
	Amount     uint64 `json:"amount"`
	FeeReserve uint64 `json:"fee_reserve"`
	Paid       bool   `json:"paid"`
	Expiry     int64  `json:"expiry"`
}

type PostMeltBolt11Request struct {
	Quote  string       `json:"quote"`
	Inputs cashu.Proofs `json:"inputs"`
}

type PostMeltBolt11Response struct {
	Paid     bool   `json:"paid"`
	Preimage string `json:"payment_preimage"`
}
