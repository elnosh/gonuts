package nut03

import "github.com/elnosh/gonuts/cashu"

type PostSwapRequest struct {
	Inputs  cashu.Proofs          `json:"inputs"`
	Outputs cashu.BlindedMessages `json:"outputs"`
}

type PostSwapResponse struct {
	Signatures cashu.BlindedSignatures `json:"signatures"`
}
