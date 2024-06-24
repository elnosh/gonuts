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
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
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

func SetupMintServer(config Config) (*MintServer, error) {
	mint, err := LoadMint(config)
	if err != nil {
		return nil, err
	}

	logger, err := setupLogger()
	if err != nil {
		return nil, err
	}
	mintServer := &MintServer{mint: mint, logger: logger}
	mintServer.setupHttpServer(config.Port)
	return mintServer, nil
}

func setupLogger() (*slog.Logger, error) {
	replacer := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.SourceKey {
			source := a.Value.Any().(*slog.Source)
			source.File = filepath.Base(source.File)
			source.Function = filepath.Base(source.Function)
		}
		return a
	}

	mintPath := mintPath()
	logFile, err := os.OpenFile(filepath.Join(mintPath, "mint.log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("error opening log file: %v", err)
	}
	logWriter := io.MultiWriter(os.Stdout, logFile)

	return slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{AddSource: true, ReplaceAttr: replacer})), nil
}

func (ms *MintServer) LogInfo(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	ms.logger.Info(msg)
}

// func (m *Mint) LogError(format string, v ...any) {
// 	msg := fmt.Sprintf(format, v...)
// 	m.logger.Error(msg)
// }

func (ms *MintServer) setupHttpServer(port string) {
	r := mux.NewRouter()

	r.HandleFunc("/v1/keys", ms.getActiveKeysets).Methods(http.MethodGet)
	r.HandleFunc("/v1/keysets", ms.getKeysetsList).Methods(http.MethodGet)
	r.HandleFunc("/v1/keys/{id}", ms.getKeysetById).Methods(http.MethodGet)
	r.HandleFunc("/v1/mint/quote/{method}", ms.mintRequest).Methods(http.MethodPost)
	r.HandleFunc("/v1/mint/quote/{method}/{quote_id}", ms.mintQuoteState).Methods(http.MethodGet)
	r.HandleFunc("/v1/mint/{method}", ms.mintTokensRequest).Methods(http.MethodPost)
	r.HandleFunc("/v1/swap", ms.swapRequest).Methods(http.MethodPost)
	r.HandleFunc("/v1/melt/quote/{method}", ms.meltQuoteRequest).Methods(http.MethodPost)
	r.HandleFunc("/v1/melt/quote/{method}/{quote_id}", ms.meltQuoteState).Methods(http.MethodGet)
	r.HandleFunc("/v1/melt/{method}", ms.meltTokens).Methods(http.MethodPost)
	r.HandleFunc("/v1/info", ms.mintInfo).Methods(http.MethodGet)

	if len(port) == 0 {
		port = "3338"
	}
	server := &http.Server{
		Addr:    "127.0.0.1:" + port,
		Handler: r,
	}

	ms.httpServer = server
}

func (ms *MintServer) writeResponse(
	rw http.ResponseWriter,
	req *http.Request,
	response []byte,
	logmsg string,
) {
	ms.logger.Info(logmsg, slog.Group("request", slog.String("method", req.Method),
		slog.String("url", req.URL.String()), slog.Int("code", http.StatusOK)))

	rw.Header().Set("Content-Type", "application/json")
	rw.Write(response)
}

func (ms *MintServer) writeErr(rw http.ResponseWriter, req *http.Request, errResponse error, errLogMsg ...string) {
	code := http.StatusBadRequest

	log := errResponse.Error()
	// if errLogMsg passed, then log msg different than err response
	if len(errLogMsg) > 0 {
		log = errLogMsg[0]
	}

	ms.logger.Error(log, slog.Group("request", slog.String("method", req.Method),
		slog.String("url", req.URL.String()), slog.Int("code", code)))

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	errRes, _ := json.Marshal(errResponse)
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

	ks, ok := ms.mint.Keysets[id]
	if !ok {
		ms.writeErr(rw, req, cashu.KeysetNotExistErr)
		return
	}

	getKeysResponse := buildKeysResponse(map[string]crypto.Keyset{ks.Id: ks})
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

	var mintReq nut04.PostMintQuoteBolt11Request
	err := decodeJsonReqBody(req, &mintReq)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	reqMintResponse, err := ms.mint.RequestMintQuote(method, mintReq.Amount, mintReq.Unit)
	if err != nil {
		cashuErr, ok := err.(*cashu.Error)
		// RequestMintQuote will return err from lightning backend if invoice
		// generation fails. Log that err from backend but return generic response to request
		if ok && cashuErr.Code == cashu.InvoiceErrCode {
			ms.writeErr(rw, req, cashu.StandardErr, cashuErr.Error())
			return
		}
		ms.writeErr(rw, req, err)
		return
	}

	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	logmsg := fmt.Sprintf("mint request for %v %v", mintReq.Amount, mintReq.Unit)
	ms.writeResponse(rw, req, jsonRes, logmsg)
}

func (ms *MintServer) mintQuoteState(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]
	quoteId := vars["quote_id"]

	mintQuoteStateResponse, err := ms.mint.GetMintQuoteState(method, quoteId)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}
	jsonRes, err := json.Marshal(mintQuoteStateResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.writeResponse(rw, req, jsonRes, "")
}

func (ms *MintServer) mintTokensRequest(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]

	var mintReq nut04.PostMintBolt11Request
	err := decodeJsonReqBody(req, &mintReq)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	blindedSignatures, err := ms.mint.MintTokens(method, mintReq.Quote, mintReq.Outputs)
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

func (ms *MintServer) meltQuoteRequest(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]

	var meltRequest nut05.PostMeltQuoteBolt11Request
	err := decodeJsonReqBody(req, &meltRequest)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	meltQuote, err := ms.mint.MeltRequest(method, meltRequest.Request, meltRequest.Unit)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	quoteResponse := nut05.PostMeltQuoteBolt11Response{
		Quote:      meltQuote.Id,
		Amount:     meltQuote.Amount,
		FeeReserve: meltQuote.FeeReserve,
		Paid:       meltQuote.Paid,
		Expiry:     meltQuote.Expiry,
	}

	jsonRes, err := json.Marshal(quoteResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.writeResponse(rw, req, jsonRes, "melt quote request")
}

func (ms *MintServer) meltQuoteState(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]
	quoteId := vars["quote_id"]

	meltQuote, err := ms.mint.GetMeltQuoteState(method, quoteId)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	quoteState := nut05.PostMeltQuoteBolt11Response{
		Quote:      meltQuote.Id,
		Amount:     meltQuote.Amount,
		FeeReserve: meltQuote.FeeReserve,
		Paid:       meltQuote.Paid,
		Expiry:     meltQuote.Expiry,
	}

	jsonRes, err := json.Marshal(quoteState)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.writeResponse(rw, req, jsonRes, "")
}

func (ms *MintServer) meltTokens(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]

	var meltTokensRequest nut05.PostMeltBolt11Request
	err := decodeJsonReqBody(req, &meltTokensRequest)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	meltQuote, err := ms.mint.MeltTokens(method, meltTokensRequest.Quote, meltTokensRequest.Inputs)
	if err != nil {
		cashuErr, ok := err.(*cashu.Error)
		if ok && cashuErr.Code == cashu.InvoiceErrCode {
			ms.writeErr(rw, req, cashu.BuildCashuError("unable to send payment", cashu.InvoiceErrCode), cashuErr.Error())
			return
		}
		ms.writeErr(rw, req, err)
		return
	}

	meltTokenResponse := nut05.PostMeltBolt11Response{
		Paid:     meltQuote.Paid,
		Preimage: meltQuote.Preimage,
	}

	jsonRes, err := json.Marshal(meltTokenResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}
	ms.writeResponse(rw, req, jsonRes, "")
}

func (ms *MintServer) mintInfo(rw http.ResponseWriter, req *http.Request) {
	jsonRes, err := json.Marshal(ms.mint.MintInfo)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}
	ms.writeResponse(rw, req, jsonRes, "returning mint info")
}

func buildKeysResponse(keysets map[string]crypto.Keyset) nut01.GetKeysResponse {
	keysResponse := nut01.GetKeysResponse{}

	for _, keyset := range keysets {
		pks := keyset.DerivePublic()
		keyRes := nut01.Keyset{Id: keyset.Id, Unit: keyset.Unit, Keys: pks}
		keysResponse.Keysets = append(keysResponse.Keysets, keyRes)
	}

	return keysResponse
}

func (ms *MintServer) buildAllKeysetsResponse() nut02.GetKeysetsResponse {
	keysetsResponse := nut02.GetKeysetsResponse{}

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
