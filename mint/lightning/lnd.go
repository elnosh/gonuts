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
	"strings"
)

const (
	LND_HOST          = "LND_REST_HOST"
	LND_CERT_PATH     = "LND_CERT_PATH"
	LND_MACAROON_PATH = "LND_MACAROON_PATH"
)

type LndClient struct {
	host         string
	tlsCertPath  string
	macaroonPath string
	macaroon     string // hex encoded
}

func CreateLndClient() (*LndClient, error) {
	host := os.Getenv(LND_HOST)
	if host == "" {
		return nil, errors.New(LND_HOST + " cannot be empty")
	}
	certPath := os.Getenv(LND_CERT_PATH)
	if host == "" {
		return nil, errors.New(LND_CERT_PATH + " cannot be empty")
	}
	macaroonPath := os.Getenv(LND_MACAROON_PATH)
	if host == "" {
		return nil, errors.New(LND_MACAROON_PATH + " cannot be empty")
	}

	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("error reading macaroon: os.ReadFile %v", err)
	}
	macaroonHex := hex.EncodeToString(macaroonBytes)

	return &LndClient{host: host, tlsCertPath: certPath,
		macaroonPath: macaroonPath, macaroon: macaroonHex}, nil
}

func (lnd *LndClient) httpClient() *http.Client {
	cert, _ := os.ReadFile(lnd.tlsCertPath)
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(cert)

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: certPool,
			},
		},
	}
}

func (lnd *LndClient) CreateInvoice(amount int64) (Invoice, error) {
	body := map[string]any{"value": amount}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return Invoice{}, fmt.Errorf("invalid amount: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, lnd.host+"/v1/invoices", bytes.NewBuffer(jsonBody))
	req.Header.Add("Grpc-Metadata-macaroon", lnd.macaroon)

	client := lnd.httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return Invoice{}, fmt.Errorf("lnd.CreateInvoice: %v", err)
	}
	defer resp.Body.Close()

	var res map[string]any
	json.NewDecoder(resp.Body).Decode(&res)
	pr := res["payment_request"].(string)
	paymentHash := res["r_hash"].(string)

	invoice := Invoice{PaymentRequest: pr, PaymentHash: paymentHash}
	return invoice, nil
}

func (lnd *LndClient) InvoiceSettled(hash string) bool {
	hash = strings.ReplaceAll(strings.ReplaceAll(hash, "/", "_"), "+", "-")
	url := lnd.host + "/v2/invoices/lookup?payment_hash=" + hash

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Printf("error creating request: %v", err)
	}
	req.Header.Add("Grpc-Metadata-macaroon", lnd.macaroon)

	client := lnd.httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var res map[string]any
	json.NewDecoder(resp.Body).Decode(&res)
	settled := res["state"]

	if settled == "SETTLED" {
		return true
	}

	return false
}
