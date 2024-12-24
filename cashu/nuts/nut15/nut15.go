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

	_, ok := mintInfo.Nuts[15]
	if ok {
		return true, nil
	}

	// TODO: removing this check for now. This format was added in a recent change so
	// it is not in the latest releases of other mints. Add when it's more widely implemented.
	// _, ok := mintInfo.Nuts[15].(map[string]interface{})
	// if !ok {
	// 	return false, nil
	// }

	// nut15Methods, ok := nut15["methods"].([]nut06.MethodSetting)
	// if !ok {
	// 	return false, nil
	// }
	//
	// for _, method := range nut15Methods {
	// 	if method.Unit == unit.String() {
	// 		return true, nil
	// 	}
	// }

	return false, nil
}
