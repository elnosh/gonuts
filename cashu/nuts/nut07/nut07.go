package nut07

import (
	"encoding/json"
	"errors"
)

type State int

const (
	Unspent State = iota
	Pending
	Spent
	Unknown
)

func (state State) String() string {
	switch state {
	case Unspent:
		return "UNSPENT"
	case Pending:
		return "PENDING"
	case Spent:
		return "SPENT"
	default:
		return "unknown"
	}
}

func StringToState(state string) State {
	switch state {
	case "UNSPENT":
		return Unspent
	case "PENDING":
		return Pending
	case "SPENT":
		return Spent
	}
	return Unknown
}

type PostCheckStateRequest struct {
	Ys []string `json:"Ys"`
}

type PostCheckStateResponse struct {
	States []ProofState `json:"states"`
}

type ProofState struct {
	Y       string `json:"Y"`
	State   State  `json:"state"`
	Witness string `json:"witness"`
}

func (state *ProofState) UnmarshalJSON(data []byte) error {
	var proofString struct {
		Y       string `json:"Y"`
		State   string `json:"state"`
		Witness string `json:"witness"`
	}

	if err := json.Unmarshal(data, &proofString); err != nil {
		return err
	}

	state.Y = proofString.Y
	stateVal := StringToState(proofString.State)
	if stateVal == Unknown {
		return errors.New("invalid state")
	}
	state.State = stateVal
	state.Witness = proofString.Witness

	return nil
}
