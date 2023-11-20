package lightning

import (
	"log"
	"os"
)

const (
	LND = "Lnd"
)

type Client interface {
	CreateInvoice(amount int64) (string, error)
}

func NewLightningClient() Client {
	backend := os.Getenv("LIGHTNING_BACKEND")

	switch backend {
	case LND:
		lndClient, err := CreateLndClient()
		if err != nil {
			log.Fatalf("error setting up lightning backend: %v", err)
		}
		return lndClient

	default:
		log.Fatal("please specify a lignting backend")
	}

	return nil
}

type Invoice struct {
	Hash     string
	Settled  bool
	Redeemed bool
}
