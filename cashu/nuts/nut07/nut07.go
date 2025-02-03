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
	Witness string `json:"witness,omitempty"`
}

type tempProofState struct {
	Y       string `json:"Y"`
	State   string `json:"state"`
	Witness string `json:"witness,omitempty"`
}

func (state *ProofState) MarshalJSON() ([]byte, error) {
	tempProof := tempProofState{
		Y:       state.Y,
		State:   state.State.String(),
		Witness: state.Witness,
	}
	return json.Marshal(tempProof)
}

func (state *ProofState) UnmarshalJSON(data []byte) error {
	var tempProof tempProofState

	if err := json.Unmarshal(data, &tempProof); err != nil {
		return err
	}

	state.Y = tempProof.Y
	stateVal := StringToState(tempProof.State)
	if stateVal == Unknown {
		return errors.New("invalid state")
	}
	state.State = stateVal
	state.Witness = tempProof.Witness

	return nil
}
