package mint

import (
	"time"

	"github.com/elnosh/gonuts/cashu/nuts/nut06"
	"github.com/elnosh/gonuts/mint/lightning"
)

type LogLevel int

const (
	Info LogLevel = iota
	Debug
	Disable
)

type Config struct {
	DerivationPathIdx uint32
	Port              string
	MintPath          string
	InputFeePpk       uint
	MintInfo          MintInfo
	Limits            MintLimits
	LightningClient   lightning.Client
	EnableMPP         bool
	LogLevel          LogLevel
	// NOTE: using this value for testing
	MeltTimeout *time.Duration
}

type MintInfo struct {
	Name            string
	Description     string
	LongDescription string
	Contact         []nut06.ContactInfo
	Motd            string
	IconURL         string
	URLs            []string
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
