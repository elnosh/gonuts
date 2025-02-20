package lightning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	InvoiceExpiryTimeCLN = 3600 // 1 hour
	FeePercentCLN        = 0.01
)

// CLNConfig holds configuration for the CLN backend
type CLNConfig struct {
	RestURL string
	Rune    string
}

// CLNClient interacts with a CLN node over REST
type CLNClient struct {
	config CLNConfig
}

// SetupCLNClient initializes a CLNClient
func SetupCLNClient(config CLNConfig) (*CLNClient, error) {
	return &CLNClient{config: config}, nil
}

// ConnectionStatus checks if the CLN node is reachable
func (cln *CLNClient) ConnectionStatus() error {
	url := fmt.Sprintf("%s/v1/getinfo", cln.config.RestURL)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Rune", cln.config.Rune)
	req.Header.Set("accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Accept both 200 (OK) and 201 (Created) as successful responses
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to connect to CLN: %s", resp.Status)
	}

	return nil
}


// CreateInvoice generates a new invoice
func (cln *CLNClient) CreateInvoice(amount uint64) (Invoice, error) {
	url := fmt.Sprintf("%s/v1/invoice", cln.config.RestURL)

	// Convert amount to millisatoshis (msat)
	amountMsat := amount * 1000

	// CLN requires a description, so we add one
	body := map[string]interface{}{
		"amount_msat": fmt.Sprintf("%dmsat", amountMsat), 
		"label":       fmt.Sprintf("cashu-%d", time.Now().Unix()),
		"description": "Cashu Lightning Invoice", // required description
		"expiry":      InvoiceExpiryTimeCLN,
	}
	jsonData, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return Invoice{}, err
	}
	req.Header.Set("Rune", cln.config.Rune)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return Invoice{}, err
	}
	defer resp.Body.Close()

	// Accept both 200 OK and 201 Created
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Invoice{}, fmt.Errorf("failed to create invoice: %s", resp.Status)
	}

	var response struct {
		Bolt11      string `json:"bolt11"`
		PaymentHash string `json:"payment_hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return Invoice{}, err
	}

	return Invoice{
		PaymentRequest: response.Bolt11,
		PaymentHash:    response.PaymentHash,
		Amount:         amount,
		Expiry:         InvoiceExpiryTimeCLN,
	}, nil
}

// InvoiceStatus checks the status of an invoice
func (cln *CLNClient) InvoiceStatus(hash string) (Invoice, error) {
	url := fmt.Sprintf("%s/v1/listinvoices", cln.config.RestURL)

	// Prepare the request body
	body := map[string]interface{}{
		"payment_hash": hash,
	}
	jsonData, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return Invoice{}, err
	}
	req.Header.Set("Rune", cln.config.Rune)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return Invoice{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Invoice{}, fmt.Errorf("failed to get invoice status: %s", resp.Status)
	}
	

	var response struct {
		Invoices []struct {
			Label        string `json:"label"`
			Bolt11       string `json:"bolt11"`
			PaymentHash  string `json:"payment_hash"`
			AmountMsat   uint64 `json:"amount_msat"`
			Status       string `json:"status"`
			Description  string `json:"description"`
			ExpiresAt    int64  `json:"expires_at"`
			CreatedIndex int    `json:"created_index"`
		} `json:"invoices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return Invoice{}, err
	}

	if len(response.Invoices) == 0 {
		return Invoice{}, fmt.Errorf("invoice not found")
	}

	invoice := response.Invoices[0]
	invoiceSettled := invoice.Status == "paid"

	return Invoice{
		PaymentHash:    invoice.PaymentHash,
		PaymentRequest: invoice.Bolt11,
		Settled:        invoiceSettled,
		Amount:         invoice.AmountMsat / 1000, // Convert from msats to sats
		Expiry:         uint64(invoice.ExpiresAt),
	}, nil
}


// SendPayment pays an invoice
func (cln *CLNClient) SendPayment(ctx context.Context, request string, maxFee uint64) (PaymentStatus, error) {
	url := fmt.Sprintf("%s/v1/pay", cln.config.RestURL)
	body := map[string]string{
		"bolt11": request,
	}
	jsonData, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return PaymentStatus{}, err
	}
	req.Header.Set("Rune", cln.config.Rune)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return PaymentStatus{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return PaymentStatus{}, fmt.Errorf("failed to send payment: %s", resp.Status)
	}

	var response struct {
		Preimage string `json:"preimage"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return PaymentStatus{}, err
	}

	status := Pending
	if response.Status == "complete" {
		status = Succeeded
	} else if response.Status == "failed" {
		status = Failed
	}

	return PaymentStatus{
		Preimage:      response.Preimage,
		PaymentStatus: status,
	}, nil
}

// PayPartialAmount sends a partial payment using CLN
func (cln *CLNClient) PayPartialAmount(
	ctx context.Context,
	request string,
	amountMsat uint64,
	maxFee uint64,
) (PaymentStatus, error) {
	url := fmt.Sprintf("%s/v1/pay", cln.config.RestURL)

	body := map[string]interface{}{
		"bolt11":      request,
		"amount_msat": fmt.Sprintf("%dmsat", amountMsat), // Specify partial amount
		"maxfee":      maxFee,
	}
	jsonData, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return PaymentStatus{}, err
	}
	req.Header.Set("Rune", cln.config.Rune)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return PaymentStatus{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return PaymentStatus{}, fmt.Errorf("failed to send partial payment: %s", resp.Status)
	}

	var response struct {
		Status   string `json:"status"`
		Preimage string `json:"preimage,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return PaymentStatus{}, err
	}

	// Map status
	status := Pending
	if response.Status == "complete" {
		status = Succeeded
	} else if response.Status == "failed" {
		status = Failed
	}

	return PaymentStatus{
		Preimage:      response.Preimage,
		PaymentStatus: status,
	}, nil
}

func (cln *CLNClient) OutgoingPaymentStatus(ctx context.Context, paymentHash string) (PaymentStatus, error) {
    // For CLN, we need to query the payment status using the payment hash
    url := fmt.Sprintf("%s/v1/listpays", cln.config.RestURL)
    
    body := map[string]string{
        "payment_hash": paymentHash,
    }
    jsonData, err := json.Marshal(body)
    if err != nil {
        return PaymentStatus{}, err
    }

    req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
    if err != nil {
        return PaymentStatus{}, err
    }
    req.Header.Set("Rune", cln.config.Rune)
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Accept", "application/json")

    client := &http.Client{
        Timeout: 30 * time.Second,
    }
    resp, err := client.Do(req)
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return PaymentStatus{PaymentStatus: Pending}, nil
        }
        return PaymentStatus{}, err
    }
    defer resp.Body.Close()

    bodyBytes, err := io.ReadAll(resp.Body)
    if err != nil {
        return PaymentStatus{}, err
    }

    if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
        return PaymentStatus{}, fmt.Errorf("failed to check payment status: %s - %s", 
            resp.Status, string(bodyBytes))
    }

    var listPaysResponse struct {
        Pays []struct {
            PaymentHash     string `json:"payment_hash"`
            Status          string `json:"status"`
            PaymentPreimage string `json:"preimage,omitempty"`
        } `json:"pays"`
    }

    if err := json.Unmarshal(bodyBytes, &listPaysResponse); err != nil {
        return PaymentStatus{}, fmt.Errorf("failed to parse response: %w", err)
    }

    // Find the payment with the matching hash
    for _, pay := range listPaysResponse.Pays {
        if pay.PaymentHash == paymentHash {
            switch pay.Status {
            case "complete":
                return PaymentStatus{
                    PaymentStatus: Succeeded,
                    Preimage:      pay.PaymentPreimage,
                }, nil
            case "pending":
                return PaymentStatus{PaymentStatus: Pending}, nil
            case "failed":
                return PaymentStatus{
                    PaymentStatus:        Failed,
                }, nil
            default:
                return PaymentStatus{PaymentStatus: Failed}, nil
            }
        }
    }

    // If we don't find the payment, we treat it as pending
    return PaymentStatus{PaymentStatus: Pending}, nil
}

// FeeReserve estimates fees
func (cln *CLNClient) FeeReserve(amount uint64) uint64 {
	return uint64(float64(amount) * FeePercentCLN)
}

// SubscribeInvoice polls CLN's invoice status until it's settled or an error occurs
func (cln *CLNClient) SubscribeInvoice(ctx context.Context, paymentHash string) (InvoiceSubscriptionClient, error) {
	sub := &CLNInvoiceSub{
		client:       cln,
		paymentHash:  paymentHash,
		pollInterval: 3 * time.Second, // Adjust polling frequency as needed
	}
	return sub, nil
}

// CLNInvoiceSub implements InvoiceSubscriptionClient for CLN
type CLNInvoiceSub struct {
	client       *CLNClient
	paymentHash  string
	pollInterval time.Duration
}

// Recv checks invoice status in a loop
func (sub *CLNInvoiceSub) Recv() (Invoice, error) {
	for {
		invoice, err := sub.client.InvoiceStatus(sub.paymentHash)
		if err != nil {
			return Invoice{}, err
		}

		// If the invoice is settled, return immediately
		if invoice.Settled {
			return invoice, nil
		}

		// Wait before checking again
		time.Sleep(sub.pollInterval)
	}
}
