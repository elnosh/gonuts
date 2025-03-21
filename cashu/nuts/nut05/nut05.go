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

func StringToState(state string) State {
	switch state {
	case "UNPAID":
		return Unpaid
	case "PENDING":
		return Pending
	case "PAID":
		return Paid
	}
	return Unknown
}

type PostMeltQuoteBolt11Request struct {
	Request string               `json:"request"`
	Unit    string               `json:"unit"`
	Options map[string]MppOption `json:"options,omitempty"`
}

type MppOption struct {
	AmountMsat uint64 `json:"amount"`
}

type PostMeltQuoteBolt11Response struct {
	Quote      string                  `json:"quote"`
	Request    string                  `json:"request"`
	Amount     uint64                  `json:"amount"`
	Unit       string                  `json:"unit"`
	FeeReserve uint64                  `json:"fee_reserve"`
	State      State                   `json:"state"`
	Expiry     uint64                  `json:"expiry"`
	Preimage   string                  `json:"payment_preimage,omitempty"`
	Change     cashu.BlindedSignatures `json:"change,omitempty"`
}

type PostMeltBolt11Request struct {
	Quote   string                `json:"quote"`
	Inputs  cashu.Proofs          `json:"inputs"`
	Outputs cashu.BlindedMessages `json:"outputs,omitempty"`
}

type tempQuote struct {
	Quote      string                  `json:"quote"`
	Request    string                  `json:"request"`
	Amount     uint64                  `json:"amount"`
	Unit       string                  `json:"unit"`
	FeeReserve uint64                  `json:"fee_reserve"`
	State      string                  `json:"state"`
	Expiry     uint64                  `json:"expiry"`
	Preimage   string                  `json:"payment_preimage,omitempty"`
	Change     cashu.BlindedSignatures `json:"change,omitempty"`
}

func (quoteResponse *PostMeltQuoteBolt11Response) MarshalJSON() ([]byte, error) {
	var tempQuote = tempQuote{
		Quote:      quoteResponse.Quote,
		Request:    quoteResponse.Request,
		Amount:     quoteResponse.Amount,
		Unit:       quoteResponse.Unit,
		FeeReserve: quoteResponse.FeeReserve,
		State:      quoteResponse.State.String(),
		Expiry:     quoteResponse.Expiry,
		Preimage:   quoteResponse.Preimage,
		Change:     quoteResponse.Change,
	}
	return json.Marshal(tempQuote)
}

func (quoteResponse *PostMeltQuoteBolt11Response) UnmarshalJSON(data []byte) error {
	tempQuote := &tempQuote{}

	if err := json.Unmarshal(data, tempQuote); err != nil {
		return err
	}

	quoteResponse.Quote = tempQuote.Quote
	quoteResponse.Request = tempQuote.Request
	quoteResponse.Amount = tempQuote.Amount
	quoteResponse.Unit = tempQuote.Unit
	quoteResponse.FeeReserve = tempQuote.FeeReserve
	state := StringToState(tempQuote.State)
	quoteResponse.State = state
	quoteResponse.Expiry = tempQuote.Expiry
	quoteResponse.Preimage = tempQuote.Preimage
	quoteResponse.Change = tempQuote.Change

	return nil
}
