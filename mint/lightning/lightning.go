package lightning

import (
	"log"
	"os"
)

const (
	LND = "Lnd"
)

// Client interface to interact with a Lightning backend
type Client interface {
	CreateInvoice(amount uint64) (Invoice, error)
	InvoiceStatus(hash string) (Invoice, error)
	FeeReserve(amount uint64) uint64
	SendPayment(request string, amount uint64) (string, error)
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
		log.Fatal("please specify a lightning  backend")
	}

	return nil
}

type Invoice struct {
	PaymentRequest string
	PaymentHash    string
	Settled        bool
	Amount         uint64
	Expiry         uint64
}
