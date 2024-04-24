package lightning

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	LND_HOST          = "LND_REST_HOST"
	LND_CERT_PATH     = "LND_CERT_PATH"
	LND_MACAROON_PATH = "LND_MACAROON_PATH"
)

const (
	InvoiceExpiryMins = 10
	FeePercent        = 1
)

type LndClient struct {
	host     string
	client   *http.Client
	macaroon string // hex encoded
}

func CreateLndClient() (*LndClient, error) {
	host := os.Getenv(LND_HOST)
	if host == "" {
		return nil, errors.New(LND_HOST + " cannot be empty")
	}
	certPath := os.Getenv(LND_CERT_PATH)
	if certPath == "" {
		return nil, errors.New(LND_CERT_PATH + " cannot be empty")
	}
	macaroonPath := os.Getenv(LND_MACAROON_PATH)
	if macaroonPath == "" {
		return nil, errors.New(LND_MACAROON_PATH + " cannot be empty")
	}

	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("error reading macaroon: os.ReadFile %v", err)
	}
	macaroonHex := hex.EncodeToString(macaroonBytes)
	client, err := httpClient(certPath)
	if err != nil {
		return nil, fmt.Errorf("error creating lnd client: %v", err)
	}

	return &LndClient{host: host, client: client, macaroon: macaroonHex}, nil
}

func httpClient(tlsCert string) (*http.Client, error) {
	cert, err := os.ReadFile(tlsCert)
	if err != nil {
		return nil, fmt.Errorf("error reading cert: %v", err)
	}
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(cert)

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: certPool,
			},
		},
	}, nil
}

func (lnd *LndClient) CreateInvoice(amount uint64) (Invoice, error) {
	body := map[string]any{"value": amount, "expiry": InvoiceExpiryMins * 60}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return Invoice{}, fmt.Errorf("invalid amount: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, lnd.host+"/v1/invoices", bytes.NewBuffer(jsonBody))
	if err != nil {
		return Invoice{}, err
	}
	req.Header.Add("Grpc-Metadata-macaroon", lnd.macaroon)

	resp, err := lnd.client.Do(req)
	if err != nil {
		return Invoice{}, err
	}
	defer resp.Body.Close()

	var res map[string]any
	json.NewDecoder(resp.Body).Decode(&res)
	pr := res["payment_request"].(string)
	paymentHash := res["r_hash"].(string)

	invoice := Invoice{PaymentRequest: pr, PaymentHash: paymentHash,
		Amount: amount,
		Expiry: time.Now().Add(time.Minute * InvoiceExpiryMins).Unix()}
	return invoice, nil
}

func (lnd *LndClient) InvoiceSettled(hash string) (bool, error) {
	hash = strings.ReplaceAll(strings.ReplaceAll(hash, "/", "_"), "+", "-")
	url := lnd.host + "/v2/invoices/lookup?payment_hash=" + hash

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Add("Grpc-Metadata-macaroon", lnd.macaroon)

	resp, err := lnd.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var res map[string]any
	json.NewDecoder(resp.Body).Decode(&res)
	settled := res["state"]

	return settled == "SETTLED", nil
}

func (lnd *LndClient) FeeReserve(request string) (uint64, uint64, error) {
	url := lnd.host + "/v1/payreq/" + request

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Add("Grpc-Metadata-macaroon", lnd.macaroon)

	resp, err := lnd.client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	var res map[string]any
	json.NewDecoder(resp.Body).Decode(&res)

	var satAmount int64
	if amt, ok := res["num_satoshis"]; !ok {
		return 0, 0, errors.New("invoice has no amount")
	} else {
		satAmount, err = strconv.ParseInt(amt.(string), 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid amount: %v", err)
		}

	}

	return uint64(satAmount), uint64(satAmount * FeePercent / 100), nil
}

func (lnd *LndClient) SendPayment(request string) (string, error) {
	url := lnd.host + "/v1/channels/transactions"

	body := map[string]any{"payment_request": request}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("invalid request: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("error making payment: %v", err)
	}
	req.Header.Add("Grpc-Metadata-macaroon", lnd.macaroon)

	resp, err := lnd.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error making payment: %v", err)
	}
	defer resp.Body.Close()

	var res map[string]any
	json.NewDecoder(resp.Body).Decode(&res)

	if paymentErr, ok := res["payment_error"]; ok && len(paymentErr.(string)) > 0 {
		return "", fmt.Errorf("error making payment: %v", paymentErr)
	}

	paymentPreimage := res["payment_preimage"]
	return paymentPreimage.(string), nil
}
