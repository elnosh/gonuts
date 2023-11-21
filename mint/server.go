package mint

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/config"
	"github.com/elnosh/gonuts/crypto"
	"github.com/gorilla/mux"
)

type MintServer struct {
	httpServer *http.Server
	mint       *Mint
	logger     *slog.Logger
}

func StartMintServer(server *MintServer) {
	server.logger.Info("mint server listening on: " + server.httpServer.Addr)
	log.Fatal(server.httpServer.ListenAndServe())
}

func SetupMintServer(config config.Config) (*MintServer, error) {
	mint, err := LoadMint(config)
	if err != nil {
		return nil, err
	}
	logger := slog.Default()
	mintServer := &MintServer{mint: mint, logger: logger}
	mintServer.setupHttpServer()
	return mintServer, nil
}

func (ms *MintServer) setupHttpServer() {
	r := mux.NewRouter()

	r.HandleFunc("/keys", ms.getPublicKeyset).Methods(http.MethodGet)
	r.HandleFunc("/keys/{id}", ms.getKeysetById).Methods(http.MethodGet)
	r.HandleFunc("/keysets", ms.getKeysetsList).Methods(http.MethodGet)
	r.HandleFunc("/mint", ms.requestMint).Methods(http.MethodGet)
	r.HandleFunc("/mint", ms.postMint).Methods(http.MethodPost)

	server := &http.Server{
		Addr:    "127.0.0.1:3338",
		Handler: r,
	}

	ms.httpServer = server
}

var KeysErrMsg = "unable to serve keys"

func (ms *MintServer) getPublicKeyset(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")

	publicKeyset := ms.mint.Keyset.DerivePublic()

	jsonKeyset, err := json.Marshal(publicKeyset)
	if err != nil {
		http.Error(rw, KeysErrMsg, http.StatusInternalServerError)
		return
	}

	rw.Write(jsonKeyset)
	return
}

func (ms *MintServer) getKeysetById(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)

	id, ok := vars["id"]
	if !ok {
		http.Error(rw, "please specify a keyset ID", http.StatusBadRequest)
		return
	}
	id = strings.ReplaceAll(strings.ReplaceAll(id, "_", "/"), "-", "+")

	var keyset *crypto.Keyset
	for _, ks := range ms.mint.Keysets {
		if ks.Id == id {
			keyset = ks
		}
	}

	if keyset == nil {
		http.Error(rw, "keyset does not exist", http.StatusNotFound)
		return
	}

	jsonRes, err := json.Marshal(keyset.DerivePublic())
	rw.Header().Set("Content-Type", "application/json")
	if err != nil {
		http.Error(rw, KeysErrMsg, http.StatusInternalServerError)
		return
	}

	rw.Write(jsonRes)
}

type KeysetsResponse struct {
	KeysetIds []string `json:"keysets"`
}

func (ms *MintServer) getKeysetsList(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")

	keysetRes := KeysetsResponse{KeysetIds: ms.mint.KeysetList()}

	jsonRes, err := json.Marshal(keysetRes)
	if err != nil {
		http.Error(rw, "unable to serve keysets", http.StatusInternalServerError)
		return
	}

	rw.Write(jsonRes)
	return
}

type RequestMintResponse struct {
	PaymentRequest string `json:"pr"`
	Hash           string `json:"hash"`
}

func (ms *MintServer) requestMint(rw http.ResponseWriter, req *http.Request) {
	amount := req.URL.Query().Get("amount")
	if amount == "" {
		http.Error(rw, "specify an amount", http.StatusBadRequest)
		return
	}

	satsAmount, err := strconv.ParseInt(amount, 10, 64)
	if err != nil {
		http.Error(rw, "invalid amount", http.StatusBadRequest)
		return
	}

	pr, hash, err := ms.mint.RequestInvoice(satsAmount)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
	}

	reqMintResponse := RequestMintResponse{PaymentRequest: pr, Hash: hash}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.Write(jsonRes)
}

func (ms *MintServer) postMint(rw http.ResponseWriter, req *http.Request) {
	hash := req.URL.Query().Get("hash")
	if hash == "" {
		http.Error(rw, "specify hash", http.StatusBadRequest)
		return
	}

	invoice := ms.mint.GetInvoice(hash)
	if invoice == nil {
		http.Error(rw, "invoice not found", http.StatusNotFound)
		return
	}

	if !invoice.Settled {
		settled := ms.mint.LightningClient.InvoiceSettled(invoice.PaymentHash)
		if !settled {
			http.Error(rw, "invoice has not been paid", http.StatusBadRequest)
			return
		}

		var mintRequest cashu.PostMintRequest
		err := json.NewDecoder(req.Body).Decode(&mintRequest)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		blindedSignatures, err := cashu.SignBlindedMessages(mintRequest.Outputs, ms.mint.Keyset)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		invoice.Settled = true
		invoice.Redeemed = true
		ms.mint.SaveInvoice(*invoice)

		response := cashu.PostMintResponse{Promises: blindedSignatures}
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(response)
		return
	} else {
		if invoice.Redeemed {
			http.Error(rw, "tokens have already been minted", http.StatusBadRequest)
			return
		}
	}
}
