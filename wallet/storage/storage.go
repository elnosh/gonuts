package storage

import (
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
)

type DB interface {
	GetProofs() cashu.Proofs
	GetKeysets() []crypto.Keyset
}
