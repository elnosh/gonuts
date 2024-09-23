package lightning

// Client interface to interact with a Lightning backend
type Client interface {
	CreateInvoice(amount uint64) (Invoice, error)
	InvoiceStatus(hash string) (Invoice, error)
	SendPayment(request string, amount uint64) (string, error)
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
