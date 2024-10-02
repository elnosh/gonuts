package mint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/cashu/nuts/nut03"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/cashu/nuts/nut07"
	"github.com/elnosh/gonuts/cashu/nuts/nut09"
	"github.com/elnosh/gonuts/crypto"
	"github.com/gorilla/mux"
)

type MintServer struct {
	httpServer *http.Server
	mint       *Mint
}

func (ms *MintServer) Start() error {
	ms.mint.logger.Info("mint server listening on: " + ms.httpServer.Addr)
	err := ms.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	} else if err == http.ErrServerClosed {
		ms.mint.logger.Info("shutdown complete")
	}
	return nil
}

func SetupMintServer(config Config) (*MintServer, error) {
	mint, err := LoadMint(config)
	if err != nil {
		return nil, err
	}

	mintServer := &MintServer{mint: mint}
	err = mintServer.setupHttpServer(config.Port)
	if err != nil {
		return nil, err
	}
	return mintServer, nil
}

func (ms *MintServer) Shutdown() {
	ms.mint.logger.Info("starting shutdown")
	ms.mint.db.Close()
	ms.httpServer.Shutdown(context.Background())
}

func (ms *MintServer) setupHttpServer(port string) error {
	r := mux.NewRouter()

	r.HandleFunc("/v1/keys", ms.getActiveKeysets).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/v1/keysets", ms.getKeysetsList).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/v1/keys/{id}", ms.getKeysetById).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/v1/mint/quote/{method}", ms.mintRequest).Methods(http.MethodGet, http.MethodPost, http.MethodOptions)
	r.HandleFunc("/v1/mint/quote/{method}/{quote_id}", ms.mintQuoteState).Methods(http.MethodGet, http.MethodPost, http.MethodOptions)
	r.HandleFunc("/v1/mint/{method}", ms.mintTokensRequest).Methods(http.MethodPost, http.MethodOptions)
	r.HandleFunc("/v1/swap", ms.swapRequest).Methods(http.MethodPost, http.MethodOptions)
	r.HandleFunc("/v1/melt/quote/{method}", ms.meltQuoteRequest).Methods(http.MethodPost, http.MethodOptions)
	r.HandleFunc("/v1/melt/quote/{method}/{quote_id}", ms.meltQuoteState).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/v1/melt/{method}", ms.meltTokens).Methods(http.MethodPost, http.MethodOptions)
	r.HandleFunc("/v1/checkstate", ms.tokenStateCheck).Methods(http.MethodPost, http.MethodOptions)
	r.HandleFunc("/v1/restore", ms.restoreSignatures).Methods(http.MethodPost, http.MethodOptions)
	r.HandleFunc("/v1/info", ms.mintInfo).Methods(http.MethodGet, http.MethodOptions)

	r.Use(setupHeaders)

	if len(port) == 0 {
		return errors.New("port cannot be empty")
	}
	server := &http.Server{
		Addr:    "127.0.0.1:" + port,
		Handler: r,
	}

	ms.httpServer = server
	return nil
}

func setupHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.Header().Set("Access-Control-Allow-Credentials", "true")
		rw.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		rw.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, origin")

		if req.Method == http.MethodOptions {
			return
		}

		next.ServeHTTP(rw, req)
	})
}

func (ms *MintServer) logRequest(req *http.Request, statusCode int, format string, args ...any) {
	// this is done to preserve the source position in the log msg from where this
	// method is called. Otherwise all messages would be logged with
	// source line of this log method and not the original caller
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	r := slog.NewRecord(time.Now(), slog.LevelInfo, fmt.Sprintf(format, args...), pcs[0])

	r.Add(slog.Group("request",
		slog.String("method", req.Method),
		slog.String("url", req.URL.String())),
	)
	// add status code attr to log if present
	if statusCode >= 100 {
		r.Add(slog.Int("code", statusCode))
	}
	_ = ms.mint.logger.Handler().Handle(context.Background(), r)
}

// errResponse is the error that will be written in the response
// errLogMsg is the error to log
func (ms *MintServer) writeErr(rw http.ResponseWriter, req *http.Request, errResponse error, errLogMsg ...string) {
	code := http.StatusBadRequest

	log := errResponse.Error()
	// if errLogMsg passed, then log msg different than err response
	if len(errLogMsg) > 0 {
		log = errLogMsg[0]
	}

	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	r := slog.NewRecord(time.Now(), slog.LevelError, log, pcs[0])
	r.Add(slog.Group("request",
		slog.String("method", req.Method),
		slog.String("url", req.URL.String())),
		slog.Int("code", code),
	)
	_ = ms.mint.logger.Handler().Handle(context.Background(), r)

	rw.WriteHeader(code)
	errRes, _ := json.Marshal(errResponse)
	rw.Write(errRes)
}

func (ms *MintServer) getActiveKeysets(rw http.ResponseWriter, req *http.Request) {
	getKeysResponse := buildKeysResponse(ms.mint.activeKeysets)
	jsonRes, err := json.Marshal(getKeysResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}
	ms.logRequest(req, http.StatusOK, "returning active keysets")
	rw.Write(jsonRes)
}

func (ms *MintServer) getKeysetsList(rw http.ResponseWriter, req *http.Request) {
	getKeysetsResponse := ms.buildAllKeysetsResponse()
	jsonRes, err := json.Marshal(getKeysetsResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}
	ms.logRequest(req, http.StatusOK, "returning list of all keysets")
	rw.Write(jsonRes)
}

func (ms *MintServer) getKeysetById(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	id := vars["id"]

	ks, ok := ms.mint.keysets[id]
	if !ok {
		ms.writeErr(rw, req, cashu.UnknownKeysetErr)
		return
	}

	getKeysResponse := buildKeysResponse(map[string]crypto.MintKeyset{ks.Id: ks})
	jsonRes, err := json.Marshal(getKeysResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "returning keyset with id: %v", id)
	rw.Write(jsonRes)
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

	ms.logRequest(req, 0, "mint request for %v %v", mintReq.Amount, mintReq.Unit)
	mintQuote, err := ms.mint.RequestMintQuote(method, mintReq.Amount, mintReq.Unit)
	if err != nil {
		cashuErr, ok := err.(*cashu.Error)
		// note: if there was internal error from lightning backend generating invoice
		// or error from db, log that error but return generic response
		if ok {
			if cashuErr.Code == cashu.LightningBackendErrCode || cashuErr.Code == cashu.DBErrCode {
				ms.writeErr(rw, req, cashu.StandardErr, cashuErr.Error())
				return
			}
		}
		ms.writeErr(rw, req, err)
		return
	}

	mintQuoteResponse := nut04.PostMintQuoteBolt11Response{
		Quote:   mintQuote.Id,
		Request: mintQuote.PaymentRequest,
		State:   mintQuote.State,
		Paid:    false,
		Expiry:  mintQuote.Expiry,
	}
	jsonRes, err := json.Marshal(&mintQuoteResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "created mint quote %v", mintQuote.Id)
	rw.Write(jsonRes)
}

func (ms *MintServer) mintQuoteState(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]
	quoteId := vars["quote_id"]

	mintQuote, err := ms.mint.GetMintQuoteState(method, quoteId)
	if err != nil {
		cashuErr, ok := err.(*cashu.Error)
		// note: if there was internal error from lightning backend
		// or error from db, log that error but return generic response
		if ok {
			if cashuErr.Code == cashu.LightningBackendErrCode || cashuErr.Code == cashu.DBErrCode {
				ms.writeErr(rw, req, cashu.StandardErr, cashuErr.Error())
				return
			}
		}

		ms.writeErr(rw, req, err)
		return
	}

	paid := mintQuote.State == nut04.Paid || mintQuote.State == nut04.Issued
	mintQuoteStateResponse := nut04.PostMintQuoteBolt11Response{
		Quote:   mintQuote.Id,
		Request: mintQuote.PaymentRequest,
		State:   mintQuote.State,
		Paid:    paid, // DEPRECATED: remove after wallets have upgraded
		Expiry:  mintQuote.Expiry,
	}
	jsonRes, err := json.Marshal(&mintQuoteStateResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "returning mint quote with state '%s'", mintQuote.State)
	rw.Write(jsonRes)
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
		cashuErr, ok := err.(*cashu.Error)
		// note: if there was internal error from lightning backend
		// or error from db, log that error but return generic response
		if ok {
			if cashuErr.Code == cashu.LightningBackendErrCode || cashuErr.Code == cashu.DBErrCode {
				ms.writeErr(rw, req, cashu.StandardErr, cashuErr.Error())
				return
			}
		}

		ms.writeErr(rw, req, err)
		return
	}

	signatures := nut04.PostMintBolt11Response{Signatures: blindedSignatures}
	jsonRes, err := json.Marshal(&signatures)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "returning signatures on mint tokens request")
	rw.Write(jsonRes)
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
		cashuErr, ok := err.(*cashu.Error)
		// note: if there was internal error from db
		// log that error but return generic response
		if ok && cashuErr.Code == cashu.DBErrCode {
			ms.writeErr(rw, req, cashu.StandardErr, cashuErr.Error())
			return
		}

		ms.writeErr(rw, req, err)
		return
	}

	signatures := nut03.PostSwapResponse{Signatures: blindedSignatures}
	jsonRes, err := json.Marshal(&signatures)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "returning signatures on swap request")
	rw.Write(jsonRes)
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

	meltQuote, err := ms.mint.RequestMeltQuote(method, meltRequest.Request, meltRequest.Unit)
	if err != nil {
		cashuErr, ok := err.(*cashu.Error)
		// note: if there was internal error from db
		// log that error but return generic response
		if ok && cashuErr.Code == cashu.DBErrCode {
			ms.writeErr(rw, req, cashu.StandardErr, cashuErr.Error())
			return
		}
		ms.writeErr(rw, req, err)
		return
	}

	meltQuoteResponse := &nut05.PostMeltQuoteBolt11Response{
		Quote:      meltQuote.Id,
		Amount:     meltQuote.Amount,
		FeeReserve: meltQuote.FeeReserve,
		State:      meltQuote.State,
		Paid:       false,
		Expiry:     meltQuote.Expiry,
	}

	jsonRes, err := json.Marshal(&meltQuoteResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK,
		"returning melt quote '%v' for invoice with payment hash: %v", meltQuote.Id, meltQuote.PaymentHash)

	rw.Write(jsonRes)
}

func (ms *MintServer) meltQuoteState(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]
	quoteId := vars["quote_id"]

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
	defer cancel()

	meltQuote, err := ms.mint.GetMeltQuoteState(ctx, method, quoteId)
	if err != nil {
		cashuErr, ok := err.(*cashu.Error)
		// note: if there was internal error from lightning backend
		// or error from db, log that error but return generic response
		if ok {
			if cashuErr.Code == cashu.LightningBackendErrCode || cashuErr.Code == cashu.DBErrCode {
				ms.writeErr(rw, req, cashu.StandardErr, cashuErr.Error())
				return
			}
		}
		ms.writeErr(rw, req, err)
		return
	}

	paid := meltQuote.State == nut05.Paid
	quoteState := &nut05.PostMeltQuoteBolt11Response{
		Quote:      meltQuote.Id,
		Amount:     meltQuote.Amount,
		FeeReserve: meltQuote.FeeReserve,
		State:      meltQuote.State,
		Paid:       paid,
		Expiry:     meltQuote.Expiry,
		Preimage:   meltQuote.Preimage,
	}

	jsonRes, err := json.Marshal(&quoteState)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "returning melt quote with state '%s'", meltQuote.State)
	rw.Write(jsonRes)
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	meltQuote, err := ms.mint.MeltTokens(ctx, method, meltTokensRequest.Quote, meltTokensRequest.Inputs)
	if err != nil {
		cashuErr, ok := err.(*cashu.Error)
		// note: if there was internal error from lightning backend
		// or error from db, log that error but return generic response
		if ok {
			if cashuErr.Code == cashu.LightningBackendErrCode {
				responseError := cashu.BuildCashuError("unable to send payment", cashu.StandardErrCode)
				ms.writeErr(rw, req, responseError, cashuErr.Error())
				return
			} else if cashuErr.Code == cashu.DBErrCode {
				ms.writeErr(rw, req, cashu.StandardErr, cashuErr.Error())
				return
			}
		}
		ms.writeErr(rw, req, err)
		return
	}

	paid := meltQuote.State == nut05.Paid
	meltQuoteResponse := &nut05.PostMeltQuoteBolt11Response{
		Quote:      meltQuote.Id,
		Amount:     meltQuote.Amount,
		FeeReserve: meltQuote.FeeReserve,
		State:      meltQuote.State,
		Paid:       paid,
		Expiry:     meltQuote.Expiry,
		Preimage:   meltQuote.Preimage,
	}

	jsonRes, err := json.Marshal(&meltQuoteResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK,
		"return from melt tokens for quote '%v'. Quote state: %s", meltQuote.Id, meltQuote.State)

	rw.Write(jsonRes)
}

func (ms *MintServer) tokenStateCheck(rw http.ResponseWriter, req *http.Request) {
	var stateRequest nut07.PostCheckStateRequest
	err := decodeJsonReqBody(req, &stateRequest)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	proofStates, err := ms.mint.ProofsStateCheck(stateRequest.Ys)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr, err.Error())
		return
	}

	checkStateResponse := nut07.PostCheckStateResponse{States: proofStates}
	jsonRes, err := json.Marshal(&checkStateResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "returning proof states")
	rw.Write(jsonRes)
}

func (ms *MintServer) restoreSignatures(rw http.ResponseWriter, req *http.Request) {
	var restoreRequest nut09.PostRestoreRequest
	err := decodeJsonReqBody(req, &restoreRequest)
	if err != nil {
		ms.writeErr(rw, req, err)
		return
	}

	blindedMessages, blindedSignatures, err := ms.mint.RestoreSignatures(restoreRequest.Outputs)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr, err.Error())
		return
	}

	restoreResponse := nut09.PostRestoreResponse{
		Outputs:    blindedMessages,
		Signatures: blindedSignatures,
	}
	jsonRes, err := json.Marshal(&restoreResponse)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "returning signatures from restore request")
	rw.Write(jsonRes)
}

func (ms *MintServer) mintInfo(rw http.ResponseWriter, req *http.Request) {
	mintInfo, err := ms.mint.RetrieveMintInfo()
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr, err.Error())
		return
	}

	jsonRes, err := json.Marshal(&mintInfo)
	if err != nil {
		ms.writeErr(rw, req, cashu.StandardErr)
		return
	}

	ms.logRequest(req, http.StatusOK, "returning mint info")
	rw.Write(jsonRes)
}

func buildKeysResponse(keysets map[string]crypto.MintKeyset) nut01.GetKeysResponse {
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

	for _, keyset := range ms.mint.keysets {
		keysetRes := nut02.Keyset{
			Id:          keyset.Id,
			Unit:        keyset.Unit,
			Active:      keyset.Active,
			InputFeePpk: keyset.InputFeePpk,
		}
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

		default:
			cashuErr = cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}
		return cashuErr
	}

	return nil
}
