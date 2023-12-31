package mint

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/cashu/nuts/nut03"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
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

	logger := getLogger()
	mintServer := &MintServer{mint: mint, logger: logger}
	mintServer.setupHttpServer()
	return mintServer, nil
}

func getLogger() *slog.Logger {
	replacer := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.SourceKey {
			source := a.Value.Any().(*slog.Source)
			source.File = filepath.Base(source.File)
			source.Function = filepath.Base(source.Function)
		}
		return a
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true, ReplaceAttr: replacer}))
}

func (ms *MintServer) LogInfo(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	ms.logger.Info(msg)
}

// func (m *Mint) LogError(format string, v ...any) {
// 	msg := fmt.Sprintf(format, v...)
// 	m.logger.Error(msg)
// }

func (ms *MintServer) setupHttpServer() {
	r := mux.NewRouter()

	r.HandleFunc("/v1/keys", ms.getActiveKeysets).Methods(http.MethodGet)
	r.HandleFunc("/v1/keysets", ms.getKeysetsList).Methods(http.MethodGet)
	r.HandleFunc("/v1/keys/{id}", ms.getKeysetById).Methods(http.MethodGet)
	r.HandleFunc("/v1/mint/quote/{method}", ms.mintRequest).Methods(http.MethodPost)
	r.HandleFunc("/v1/mint/quote/{method}/{quote_id}", ms.getQuoteState).Methods(http.MethodGet)
	r.HandleFunc("/v1/mint/{method}", ms.mintTokensRequest).Methods(http.MethodPost)
	r.HandleFunc("/v1/swap", ms.swapRequest).Methods(http.MethodPost)

	server := &http.Server{
		Addr:    "127.0.0.1:3338",
		Handler: r,
	}

	ms.httpServer = server
}

func (ms *MintServer) writeResponse(rw http.ResponseWriter, req *http.Request,
	response []byte, logmsg string) {
	ms.logger.Info(logmsg, slog.Group("request", slog.String("method", req.Method),
		slog.String("url", req.URL.String()), slog.Int("code", http.StatusOK)))
	rw.Header().Set("Content-Type", "application/json")
	rw.Write(response)
}

func (ms *MintServer) writeErr(rw http.ResponseWriter, req *http.Request, err error) {
	code := http.StatusBadRequest
	ms.logger.Error(err.Error(), slog.Group("request", slog.String("method", req.Method),
		slog.String("url", req.URL.String()), slog.Int("code", code)))
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	errRes, _ := json.Marshal(err)
	rw.Write(errRes)
}

func (ms *MintServer) getActiveKeysets(rw http.ResponseWriter, req *http.Request) {
	getKeysResponse := buildKeysResponse(ms.mint.ActiveKeysets)
	jsonRes, err := json.Marshal(getKeysResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.writeResponse(rw, req, jsonRes, "returning active keysets")
}

func (ms *MintServer) getKeysetsList(rw http.ResponseWriter, req *http.Request) {
	getKeysetsResponse := ms.buildAllKeysetsResponse()
	jsonRes, err := json.Marshal(getKeysetsResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.writeResponse(rw, req, jsonRes, "returning all keysets")
}

func (ms *MintServer) getKeysetById(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	id := vars["id"]

	var keyset *crypto.Keyset
	for _, ks := range ms.mint.Keysets {
		if ks.Id == id {
			keyset = &ks
		}
	}

	if keyset == nil {
		ms.writeErr(rw, req, cashu.KeysetNotExistErr)
		return
	}

	getKeysResponse := buildKeysResponse([]crypto.Keyset{*keyset})
	jsonRes, err := json.Marshal(getKeysResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.writeResponse(rw, req, jsonRes, "returned keyset with id: "+id)
}

func (ms *MintServer) mintRequest(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]

	if method != "bolt11" {
		ms.writeErr(rw, req, cashu.PaymentMethodNotSupportedErr)
		return
	}

	var mintReq nut04.PostMintQuoteBolt11Request
	err := decodeJsonReqBody(req, &mintReq)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	if mintReq.Unit != "sat" {
		ms.writeErr(rw, req, cashu.UnitNotSupportedErr)
		return
	}

	invoice, err := ms.mint.RequestInvoice(mintReq.Amount)
	if err != nil {
		ms.writeErr(rw, req, cashu.Error{Detail: err.Error(), Code: cashu.InvoiceErrCode})
		return
	}

	reqMintResponse := nut04.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: invoice.Settled, Expiry: invoice.Expiry}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	logmsg := fmt.Sprintf("mint request for %v %v", mintReq.Amount, mintReq.Unit)
	ms.writeResponse(rw, req, jsonRes, logmsg)
}

func (ms *MintServer) getQuoteState(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]
	if method != "bolt11" {
		ms.writeErr(rw, req, cashu.PaymentMethodNotSupportedErr)
		return
	}

	quoteId := vars["quote_id"]
	invoice := ms.mint.GetInvoice(quoteId)
	if invoice == nil {
		ms.writeErr(rw, req, cashu.InvoiceNotExistErr)
		return
	}

	settled := ms.mint.LightningClient.InvoiceSettled(invoice.PaymentHash)
	if settled != invoice.Settled {
		invoice.Settled = settled
		ms.mint.SaveInvoice(*invoice)
	}

	reqMintResponse := nut04.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: settled, Expiry: invoice.Expiry}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.writeResponse(rw, req, jsonRes, "")
}

func (ms *MintServer) mintTokensRequest(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]
	if method != "bolt11" {
		ms.writeErr(rw, req, cashu.PaymentMethodNotSupportedErr)
		return
	}

	var mintReq nut04.PostMintBolt11Request
	err := decodeJsonReqBody(req, &mintReq)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	blindedSignatures, err := ms.mint.MintTokens(mintReq.Quote, mintReq.Outputs)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}
	signatures := nut04.PostMintBolt11Response{Signatures: blindedSignatures}

	jsonRes, err := json.Marshal(signatures)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}
	ms.writeResponse(rw, req, jsonRes, "returned signatures on mint tokens request")
}

func (ms *MintServer) swapRequest(rw http.ResponseWriter, req *http.Request) {
	var swapReq nut03.PostSwapRequest
	err := decodeJsonReqBody(req, &swapReq)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	blindedSignatures, err := ms.mint.Swap(swapReq.Inputs, swapReq.Outputs)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	signatures := nut03.PostSwapResponse{Signatures: blindedSignatures}
	jsonRes, err := json.Marshal(signatures)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}
	ms.writeResponse(rw, req, jsonRes, "returned signatures on swap request")
}

func buildKeysResponse(keysets []crypto.Keyset) nut01.GetKeysResponse {
	keysResponse := nut01.GetKeysResponse{}

	for _, keyset := range keysets {
		pks := keyset.DerivePublic()
		keyRes := nut01.Keyset{Id: keyset.Id, Unit: keyset.Unit, Keys: pks}
		keysResponse.Keysets = append(keysResponse.Keysets, keyRes)
	}

	return keysResponse
}

func (ms *MintServer) buildAllKeysetsResponse() nut02.GetKeysetResponse {
	keysetsResponse := nut02.GetKeysetResponse{}

	for _, keyset := range ms.mint.Keysets {
		keysetRes := nut02.Keyset{Id: keyset.Id, Unit: keyset.Unit, Active: keyset.Active}
		keysetsResponse.Keysets = append(keysetsResponse.Keysets, keysetRes)
	}

	return keysetsResponse
}

func decodeJsonReqBody(req *http.Request, dst any) error {
	ct := req.Header.Get("Content-Type")
	if ct != "" {
		mediaType := strings.ToLower(strings.Split(ct, ";")[0])
		if mediaType != "application/json" {
			ctError := cashu.BuildCashuError("Content-Type header is not application/json", cashu.StandardErrCode)
			return ctError
		}
	}

	dec := json.NewDecoder(req.Body)
	// error if unknown field is specified in the json req body
	dec.DisallowUnknownFields()

	err := dec.Decode(&dst)
	if err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		var cashuErr *cashu.Error

		switch {
		case errors.As(err, &syntaxErr):
			msg := fmt.Sprintf("bad json at %d", syntaxErr.Offset)
			cashuErr = cashu.BuildCashuError(msg, cashu.StandardErrCode)

		case errors.As(err, &typeErr):
			msg := fmt.Sprintf("invalid %v for field %q", typeErr.Value, typeErr.Field)
			cashuErr = cashu.BuildCashuError(msg, cashu.StandardErrCode)

		case errors.Is(err, io.EOF):
			return cashu.EmptyBodyErr

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			invalidField := strings.TrimPrefix(err.Error(), "json: unknown field ")
			msg := fmt.Sprintf("Request body contains unknown field %s", invalidField)
			cashuErr = cashu.BuildCashuError(msg, cashu.StandardErrCode)

		default:
			cashuErr = cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}
		return cashuErr
	}

	return nil
}
