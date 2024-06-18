package nut07

type State int

const (
	Unspent State = iota
	Pending
	Spent
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
