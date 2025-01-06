package nut15

import (
	"errors"
	"fmt"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/wallet/client"
)

var (
	ErrSplitTooShort = errors.New("length of split too short")
)

// IsMppSupported returns whether the mint supports NUT-15 for the specified unit
func IsMppSupported(mint string, unit cashu.Unit) (bool, error) {
	mintInfo, err := client.GetMintInfo(mint)
	if err != nil {
		return false, fmt.Errorf("error getting info from mint: %v", err)
	}

	for _, method := range mintInfo.Nuts.Nut15.Methods {
		if method.Unit == unit.String() {
			return true, nil
		}
	}

	return false, nil
}
