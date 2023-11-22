package cashu

type PostMintRequest struct {
	Outputs BlindedMessages `json:"outputs"`
}

type BlindedMessage struct {
	Amount uint64 `json:"amount"`
	B_     string `json:"B_"`
}

type BlindedMessages []BlindedMessage

type BlindedSignature struct {
	Amount uint64 `json:"amount"`
	C_     string `json:"C_"`
	Id     string `json:"id"`
}

type BlindedSignatures []BlindedSignature

type PostMintResponse struct {
	Promises BlindedSignatures `json:"promises"`
}

type Proof struct {
	Amount uint64 `json:"amount"`
	Secret string `json:"secret"`
	C      string `json:"C"`
	Id     string `json:"id"`
}

type Proofs []Proof
