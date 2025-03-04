package lightning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	InvoiceExpiryTimeCLN = 3600 // 1 hour
	FeePercentCLN        = 0.01
	DebugLogging         = true // Toggle this for logs
)

// Set up logging to a file
func init() {
	logFile, err := os.OpenFile("cln_debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile) // Include timestamps & file info
}

func debugLog(format string, args ...interface{}) {
	if DebugLogging {
		message := fmt.Sprintf(format, args...)
		if strings.Contains(strings.ToLower(message), "error") { // Only log errors
			log.Println(message)
		}
	}
}

// CLNConfig holds configuration for the CLN backend
type CLNConfig struct {
	RestURL string
	Rune    string
}

// CLNClient interacts with a CLN node over REST
type CLNClient struct {
	config CLNConfig
	client *http.Client
}

// SetupCLNClient initializes a CLNClient with a shared HTTP client
func SetupCLNClient(config CLNConfig) (*CLNClient, error) {
	return &CLNClient{
		config: config,
		client: &http.Client{Timeout: 30 * time.Second}, // Reuse client with timeout
	}, nil
}

// helper function to create a request with headers
func (cln *CLNClient) newRequest(method, url string, body interface{}) (*http.Request, error) {
	var jsonData []byte
	if body != nil {
		var err error
		jsonData, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequest(method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Rune", cln.config.Rune)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// ConnectionStatus checks if the CLN node is reachable
func (cln *CLNClient) ConnectionStatus() error {
	url := fmt.Sprintf("%s/v1/getinfo", cln.config.RestURL)

	req, err := cln.newRequest("POST", url, map[string]string{})
	if err != nil {
		return err
	}

	resp, err := cln.client.Do(req)
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

// Modify CreateInvoice to include debug logs
func (cln *CLNClient) CreateInvoice(amount uint64) (Invoice, error) {
	url := fmt.Sprintf("%s/v1/invoice", cln.config.RestURL)
	label := fmt.Sprintf("cashu-%d-%s", time.Now().Unix(), uuid.NewString())

	body := map[string]interface{}{
		"amount_msat": fmt.Sprintf("%dmsat", amount*1000),
		"label":       label,
		"description": "Cashu Lightning Invoice",
		"expiry":      InvoiceExpiryTimeCLN,
	}

	debugLog("Creating invoice: %v", body) // Log request data

	req, err := cln.newRequest("POST", url, body)
	if err != nil {
		return Invoice{}, err
	}

	resp, err := cln.client.Do(req)
	if err != nil {
		return Invoice{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Invoice{}, err
	}

	debugLog("Invoice response: %s", string(bodyBytes)) // Log response

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Invoice{}, fmt.Errorf("failed to create invoice: %s - %s", resp.Status, string(bodyBytes))
	}

	var response struct {
		Bolt11      string `json:"bolt11"`
		PaymentHash string `json:"payment_hash"`
		Error       string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return Invoice{}, fmt.Errorf("failed to parse response: %w", err)
	}

	if response.Error != "" {
		return Invoice{}, fmt.Errorf("CLN error: %s", response.Error)
	}

	return Invoice{
		PaymentRequest: response.Bolt11,
		PaymentHash:    response.PaymentHash,
		Amount:         amount,
		Expiry:         InvoiceExpiryTimeCLN,
	}, nil
}

// Modify InvoiceStatus to include logs
func (cln *CLNClient) InvoiceStatus(hash string) (Invoice, error) {
	url := fmt.Sprintf("%s/v1/listinvoices", cln.config.RestURL)

	body := map[string]interface{}{"payment_hash": hash}
	debugLog("Checking invoice status: %v", body)

	req, err := cln.newRequest("POST", url, body)
	if err != nil {
		return Invoice{}, err
	}

	resp, err := cln.client.Do(req)
	if err != nil {
		return Invoice{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Invoice{}, err
	}

	debugLog("Invoice status response: %s", string(bodyBytes))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Invoice{}, fmt.Errorf("failed to get invoice status: %s - %s", resp.Status, string(bodyBytes))
	}

	var response struct {
		Invoices []struct {
			Label        string `json:"label"`
			Bolt11       string `json:"bolt11"`
			PaymentHash  string `json:"payment_hash"`
			AmountMsat   uint64 `json:"amount_msat"`
			Status       string `json:"status"`
			ExpiresAt    int64  `json:"expires_at"`
		} `json:"invoices"`
	}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return Invoice{}, err
	}

	if len(response.Invoices) == 0 {
		return Invoice{}, fmt.Errorf("invoice not found")
	}

	invoice := response.Invoices[0]
	invoiceSettled := invoice.Status == "paid"

	debugLog("Invoice %s status: %s", hash, invoice.Status)

	return Invoice{
		PaymentHash:    invoice.PaymentHash,
		PaymentRequest: invoice.Bolt11,
		Settled:        invoiceSettled,
		Amount:         invoice.AmountMsat / 1000,
		Expiry:         uint64(invoice.ExpiresAt),
	}, nil
}

// Modify SendPayment to include logs
func (cln *CLNClient) SendPayment(ctx context.Context, request string, maxFee uint64) (PaymentStatus, error) {
	url := fmt.Sprintf("%s/v1/pay", cln.config.RestURL)

	body := map[string]interface{}{"bolt11": request}
	debugLog("Sending payment: %v", body)

	req, err := cln.newRequest("POST", url, body)
	if err != nil {
		return PaymentStatus{}, err
	}

	resp, err := cln.client.Do(req)
	if err != nil {
		return PaymentStatus{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return PaymentStatus{}, err
	}

	debugLog("Payment response: %s", string(bodyBytes))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return PaymentStatus{}, fmt.Errorf("failed to send payment: %s - %s", resp.Status, string(bodyBytes))
	}

	var response struct {
		Preimage string `json:"payment_preimage"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return PaymentStatus{}, err
	}

	status := Pending
	if response.Status == "complete" {
		status = Succeeded
	} else if response.Status == "failed" {
		status = Failed
	}

	debugLog("Payment status: %s, Preimage: %s", response.Status, response.Preimage)

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
		"bolt11":        request,
		"partial_msat":  fmt.Sprintf("%dmsat", amountMsat),  // Specify partial amount
		"maxfee":        fmt.Sprintf("%dmsat", maxFee*1000), // Ensure max fee is in msats
		"maxfeepercent": 0.5,                                // Allow up to 0.5% in fees
		"retry_for":     60,                                 // Retry for up to 60 seconds
	}

	req, err := cln.newRequest("POST", url, body)
	if err != nil {
		return PaymentStatus{}, err
	}

	resp, err := cln.client.Do(req)
	if err != nil {
		return PaymentStatus{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return PaymentStatus{}, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return PaymentStatus{}, fmt.Errorf("failed to send partial payment: %s - %s", resp.Status, string(bodyBytes))
	}

	var response struct {
		Status   string `json:"status"`
		Preimage string `json:"payment_preimage,omitempty"`
		Attempts []struct {
			Status     string `json:"status"`
			FailReason string `json:"failreason,omitempty"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return PaymentStatus{}, fmt.Errorf("failed to parse response: %w", err)
	}

	// Handle potential retries for MPP
	for _, attempt := range response.Attempts {
		if attempt.Status == "failed" && attempt.FailReason == "WIRE_MPP_TIMEOUT" {
			// Exclude failed routes on retry
			body["exclude"] = []string{"last_failed_route"}
			req, _ := cln.newRequest("POST", url, body)
			resp, err = cln.client.Do(req)
			if err != nil {
				return PaymentStatus{}, err
			}
			defer resp.Body.Close()

			bodyBytes, _ = io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
				return PaymentStatus{}, fmt.Errorf("failed retry: %s - %s", resp.Status, string(bodyBytes))
			}
			_ = json.Unmarshal(bodyBytes, &response)
		}
	}

	// Map final status
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
	url := fmt.Sprintf("%s/v1/listpays", cln.config.RestURL)

	body := map[string]string{"payment_hash": paymentHash}
	req, err := cln.newRequest("POST", url, body)
	if err != nil {
		return PaymentStatus{}, err
	}

	resp, err := cln.client.Do(req)
	if err != nil {
		return PaymentStatus{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return PaymentStatus{}, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return PaymentStatus{}, fmt.Errorf("failed to check payment status: %s - %s", resp.Status, string(bodyBytes))
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

	for _, pay := range listPaysResponse.Pays {
		if pay.PaymentHash == paymentHash {
			switch pay.Status {
			case "complete":
				return PaymentStatus{PaymentStatus: Succeeded, Preimage: pay.PaymentPreimage}, nil
			case "failed":
				return PaymentStatus{PaymentStatus: Failed}, nil
			default:
				return PaymentStatus{PaymentStatus: Pending}, nil
			}
		}
	}

	// If we don't find the payment, assume failure (instead of PENDING)
	return PaymentStatus{PaymentStatus: Failed}, nil
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
	timeout := time.After(5 * time.Minute) // Timeout after 5 minutes

	for {
		select {
		case <-timeout:
			return Invoice{}, fmt.Errorf("invoice subscription timed out")
		default:
			invoice, err := sub.client.InvoiceStatus(sub.paymentHash)
			if err != nil {
				return Invoice{}, err
			}

			if invoice.Settled {
				return invoice, nil
			}

			time.Sleep(sub.pollInterval)
		}
	}
}
