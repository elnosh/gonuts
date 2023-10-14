package main

import (
	"encoding/json"
	"fmt"

	"github.com/elnosh/gonuts/crypto"
)

func main() {
	keyset := crypto.GenerateKeyset("seed", "path")

	fmt.Printf("keyset id: %v\n", keyset.Id)

	pubkeyset := keyset.DerivePublic()
	jsonKeyset, err := json.Marshal(pubkeyset)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Printf("%s\n", jsonKeyset)
}
