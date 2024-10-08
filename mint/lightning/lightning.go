package lightning

import "context"

// Client interface to interact with a Lightning backend
type Client interface {
	ConnectionStatus() error
	CreateInvoice(amount uint64) (Invoice, error)
	InvoiceStatus(hash string) (Invoice, error)
	SendPayment(ctx context.Context, request string, amount uint64) (PaymentStatus, error)
	OutgoingPaymentStatus(ctx context.Context, hash string) (PaymentStatus, error)
	FeeReserve(amount uint64) uint64
}

type Invoice struct {
	PaymentRequest string
	PaymentHash    string
	Preimage       string
	Settled        bool
	Amount         uint64
	Expiry         uint64
}

type State int

const (
	Succeeded State = iota
	Failed
	Pending
)

type PaymentStatus struct {
	Preimage             string
	PaymentStatus        State
	PaymentFailureReason string
}
