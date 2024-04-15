// Package nut04 contains structs as defined in [NUT-04]
//
// [NUT-04]: https://github.com/cashubtc/nuts/blob/main/04.md
package nut04

import "github.com/elnosh/gonuts/cashu"

type PostMintQuoteBolt11Request struct {
	Amount uint64 `json:"amount"`
	Unit   string `json:"unit"`
}

type PostMintQuoteBolt11Response struct {
	Quote   string `json:"quote"`
	Request string `json:"request"`
	Paid    bool   `json:"paid"`
	Expiry  int64  `json:"expiry"`
}

type PostMintBolt11Request struct {
	Quote   string                `json:"quote"`
	Outputs cashu.BlindedMessages `json:"outputs"`
}

type PostMintBolt11Response struct {
	Signatures cashu.BlindedSignatures `json:"signatures"`
}
