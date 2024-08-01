package mint

import (
	"github.com/elnosh/gonuts/cashu/nuts/nut06"
	"github.com/elnosh/gonuts/mint/lightning"
)

type Config struct {
	DerivationPathIdx uint32
	Port              string
	MintPath          string
	DBMigrationPath   string
	InputFeePpk       uint
	MintInfo          MintInfo
	Limits            MintLimits
	LightningClient   lightning.Client
}

type MintInfo struct {
	Name            string
	Description     string
	LongDescription string
	Contact         []nut06.ContactInfo
	Motd            string
}

type MintMethodSettings struct {
	MinAmount uint64
	MaxAmount uint64
}

type MeltMethodSettings struct {
	MinAmount uint64
	MaxAmount uint64
}

type MintLimits struct {
	MaxBalance      uint64
	MintingSettings MintMethodSettings
	MeltingSettings MeltMethodSettings
}
