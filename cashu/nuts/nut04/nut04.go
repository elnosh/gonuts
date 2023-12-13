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

type PostMintBolt11Response struct {
	Quote   string                `json:"quote"`
	Outputs cashu.BlindedMessages `json:"outputs"`
}
