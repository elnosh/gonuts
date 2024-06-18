package nut09

import "github.com/elnosh/gonuts/cashu"

type PostRestoreRequest struct {
	Outputs cashu.BlindedMessages `json:"outputs"`
}

type PostRestoreResponse struct {
	Outputs    cashu.BlindedMessages   `json:"outputs"`
	Signatures cashu.BlindedSignatures `json:"signatures"`
}
