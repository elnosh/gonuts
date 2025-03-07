package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/mint"
	"github.com/gorilla/mux"
)

type Server struct {
	httpServer *http.Server
	mint       *mint.Mint
}

func SetupServer(mint *mint.Mint) (*Server, error) {
	mintServer := &Server{
		mint: mint,
	}
	err := mintServer.setupHttpServer()
	if err != nil {
		return nil, err
	}
	return mintServer, nil
}

func (s *Server) Start() error {
	err := s.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown() error {
	if err := s.httpServer.Shutdown(context.Background()); err != nil {
		return err
	}
	return nil
}

func (s *Server) setupHttpServer() error {
	r := mux.NewRouter()

	r.HandleFunc("/issued", s.getIssuedEcash).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/issued/{keyset_id}", s.getIssuedByKeyset).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/redeemed", s.getRedeemedEcash).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/redeemed/{keyset_id}", s.getRedeemedByKeyset).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/totalbalance", s.getTotalEcash).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/keysets", s.getKeysets).Methods(http.MethodGet, http.MethodOptions)
	r.HandleFunc("/rotatekeyset", s.rotateKeyset).Methods(http.MethodPost, http.MethodOptions)

	r.Use(setupHeaders)

	server := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: r,
	}

	s.httpServer = server
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

type IssuedEcashResponse struct {
	Keysets     []KeysetIssued `json:"keysets"`
	TotalIssued uint64         `json:"total_issued"`
}

type KeysetIssued struct {
	Id           string `json:"id"`
	AmountIssued uint64 `json:"amount_issued"`
}

func (s *Server) getIssuedEcash(rw http.ResponseWriter, req *http.Request) {
	issuedEcash, err := s.issuedEcash()
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte(err.Error()))
		return
	}

	response, _ := json.Marshal(issuedEcash)
	rw.Write(response)
}

func (s *Server) getIssuedByKeyset(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	id := vars["keyset_id"]

	issuedEcashMap, err := s.mint.IssuedEcash()
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		errmsg := fmt.Sprintf("unable to get issued ecash from db: %v", err)
		rw.Write([]byte(errmsg))
		return
	}

	amountIssued, ok := issuedEcashMap[id]
	if !ok {
		rw.WriteHeader(http.StatusBadRequest)
		errRes, _ := json.Marshal(cashu.UnknownKeysetErr)
		rw.Write(errRes)
		return
	}

	issuedByKeyset := KeysetIssued{
		Id:           id,
		AmountIssued: amountIssued,
	}

	response, _ := json.Marshal(issuedByKeyset)
	rw.Write(response)
}

type RedeemedEcashResponse struct {
	Keysets       []KeysetRedeemed `json:"keysets"`
	TotalRedeemed uint64           `json:"total_redeemed"`
}

type KeysetRedeemed struct {
	Id             string `json:"id"`
	AmountRedeemed uint64 `json:"amount_redeemed"`
}

func (s *Server) getRedeemedEcash(rw http.ResponseWriter, req *http.Request) {
	redeemedEcash, err := s.redeemedEcash()
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte(err.Error()))
		return
	}

	response, _ := json.Marshal(redeemedEcash)
	rw.Write(response)
}

func (s *Server) getRedeemedByKeyset(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	id := vars["keyset_id"]

	redeemedEcashMap, err := s.mint.RedeemedEcash()
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		errmsg := fmt.Sprintf("unable to get redeemed ecash from db: %v", err)
		rw.Write([]byte(errmsg))
		return
	}

	amountRedeemed, ok := redeemedEcashMap[id]
	if !ok {
		rw.WriteHeader(http.StatusBadRequest)
		errRes, _ := json.Marshal(cashu.UnknownKeysetErr)
		rw.Write(errRes)
		return
	}

	redeemedByKeyset := KeysetRedeemed{
		Id:             id,
		AmountRedeemed: amountRedeemed,
	}

	response, _ := json.Marshal(redeemedByKeyset)
	rw.Write(response)
}

type TotalBalanceResponse struct {
	TotalIssued        IssuedEcashResponse   `json:"total_issued"`
	TotalRedeemed      RedeemedEcashResponse `json:"total_redeemed"`
	TotalInCirculation uint64                `json:"total_circulation"`
}

// returns total amount of ecash in circulation
func (s *Server) getTotalEcash(rw http.ResponseWriter, req *http.Request) {
	issuedEcash, err := s.issuedEcash()
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte(err.Error()))
		return
	}

	redeemedEcash, err := s.redeemedEcash()
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte(err.Error()))
		return
	}

	totalBalance := TotalBalanceResponse{
		TotalIssued:        issuedEcash,
		TotalRedeemed:      redeemedEcash,
		TotalInCirculation: issuedEcash.TotalIssued - redeemedEcash.TotalRedeemed,
	}
	response, _ := json.Marshal(totalBalance)
	rw.Write(response)
}

// same response from NUT-02 /v1/keysets
func (s *Server) getKeysets(rw http.ResponseWriter, req *http.Request) {
	keysetsResponse := s.mint.ListKeysets()
	response, _ := json.Marshal(keysetsResponse)
	rw.Write(response)
}

func (s *Server) rotateKeyset(rw http.ResponseWriter, req *http.Request) {
	fee := req.URL.Query().Get("fee")
	if len(fee) == 0 {
		rw.WriteHeader(http.StatusBadRequest)
		rw.Write([]byte("fee for keyset not specified"))
		return
	}

	keysetFee, err := strconv.Atoi(fee)
	if err != nil || keysetFee < 0 {
		rw.WriteHeader(http.StatusBadRequest)
		rw.Write([]byte("invalid fee"))
		return
	}

	newKeyset, err := s.mint.RotateKeyset(uint(keysetFee))
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte(err.Error()))
		return
	}

	response, _ := json.Marshal(newKeyset)
	rw.Write(response)
}

func (s *Server) issuedEcash() (IssuedEcashResponse, error) {
	issuedEcashMap, err := s.mint.IssuedEcash()
	if err != nil {
		return IssuedEcashResponse{}, fmt.Errorf("unable to get issued ecash from db: %v", err)
	}

	var issuedEcash IssuedEcashResponse
	var totalIssued uint64
	for keysetId, amount := range issuedEcashMap {
		issuedByKeyset := KeysetIssued{Id: keysetId, AmountIssued: amount}
		issuedEcash.Keysets = append(issuedEcash.Keysets, issuedByKeyset)
		totalIssued += amount
	}
	issuedEcash.TotalIssued = totalIssued
	return issuedEcash, nil
}

func (s *Server) redeemedEcash() (RedeemedEcashResponse, error) {
	redeemedEcashMap, err := s.mint.RedeemedEcash()
	if err != nil {
		return RedeemedEcashResponse{}, fmt.Errorf("unable to get redeemed ecash from db: %v", err)
	}

	var redeemedEcash RedeemedEcashResponse
	var totalRedeemed uint64
	for keysetId, amount := range redeemedEcashMap {
		redeemedByKeyset := KeysetRedeemed{Id: keysetId, AmountRedeemed: amount}
		redeemedEcash.Keysets = append(redeemedEcash.Keysets, redeemedByKeyset)
		totalRedeemed += amount
	}
	redeemedEcash.TotalRedeemed = totalRedeemed
	return redeemedEcash, nil
}
