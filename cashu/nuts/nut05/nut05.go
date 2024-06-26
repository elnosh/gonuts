// Package nut05 contains structs as defined in [NUT-05]
//
// [NUT-05]: https://github.com/cashubtc/nuts/blob/main/05.md
package nut05

import (
	"encoding/json"

	"github.com/elnosh/gonuts/cashu"
)

type State int

const (
	Unpaid State = iota
	Pending
	Paid
	Unknown
)

func (state State) String() string {
	switch state {
	case Unpaid:
		return "UNPAID"
	case Pending:
		return "PENDING"
	case Paid:
		return "PAID"
	default:
		return "unknown"
	}
}

type PostMeltQuoteBolt11Request struct {
	Request string `json:"request"`
	Unit    string `json:"unit"`
}

type PostMeltQuoteBolt11Response struct {
	Quote      string `json:"quote"`
	Amount     uint64 `json:"amount"`
	FeeReserve uint64 `json:"fee_reserve"`
	State      State  `json:"state"`
	Paid       bool   `json:"paid"` // DEPRECATED: use state instead
	Expiry     int64  `json:"expiry"`
	Preimage   string `json:"payment_preimage,omitempty"`
}

type PostMeltBolt11Request struct {
	Quote  string       `json:"quote"`
	Inputs cashu.Proofs `json:"inputs"`
}

// Custom marshaler to display state as string
func (quoteResponse *PostMeltQuoteBolt11Response) MarshalJSON() ([]byte, error) {
	var response = struct {
		Quote      string `json:"quote"`
		Amount     uint64 `json:"amount"`
		FeeReserve uint64 `json:"fee_reserve"`
		State      string `json:"state"`
		Paid       bool   `json:"paid"` // DEPRECATED: use state instead
		Expiry     int64  `json:"expiry"`
		Preimage   string `json:"payment_preimage,omitempty"`
	}{
		Quote:      quoteResponse.Quote,
		Amount:     quoteResponse.Amount,
		FeeReserve: quoteResponse.FeeReserve,
		State:      quoteResponse.State.String(),
		Paid:       quoteResponse.Paid,
		Expiry:     quoteResponse.Expiry,
		Preimage:   quoteResponse.Preimage,
	}
	return json.Marshal(response)
}
