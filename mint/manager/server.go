package manager

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/mint"
)

const (
	JSONRPC_2  = "2.0"
	socketdir  = "/tmp/gonuts"
	socketname = "gonuts-admin.sock"

	ISSUED_ECASH_REQUEST   = "issued_ecash"
	REDEEMED_ECASH_REQUEST = "redeemed_ecash"
	TOTAL_BALANCE          = "total_balance"
	LIST_KEYSETS           = "list_keysets"
	ROTATE_KEYSET          = "rotate_keyset"
)

type Server struct {
	mint      *mint.Mint
	listener  net.Listener
	socketDir string
}

func SetupServer(mint *mint.Mint) (*Server, error) {
	if err := os.MkdirAll(socketdir, 0700); err != nil {
		return nil, err
	}

	socket := filepath.Join(socketdir, socketname)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(socket, os.ModeSocket|0666); err != nil {
		return nil, err
	}

	return &Server{
		mint:      mint,
		listener:  listener,
		socketDir: socketdir,
	}, nil
}

func (s *Server) Start() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		go s.handleRequest(conn)
	}
}

func (s *Server) Shutdown() error {
	unixListener := s.listener.(*net.UnixListener)
	fileDescriptor, err := unixListener.File()
	if err != nil {
		return err
	}
	if err := unixListener.Close(); err != nil {
		return err
	}
	if err := fileDescriptor.Close(); err != nil {
		return err
	}
	if err := os.RemoveAll(s.socketDir); err != nil {
		return err
	}
	return nil
}

type Request struct {
	JsonRPC string   `json:"jsonrpc"`
	Method  string   `json:"method"`
	Params  []string `json:"params,omitempty"`
	Id      int      `json:"id"`
}

type Response struct {
	JsonRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   Error           `json:"error,omitempty"`
	Id      int             `json:"id"`
}

func NewResponse(result json.RawMessage, id int) Response {
	return Response{
		JsonRPC: JSONRPC_2,
		Result:  result,
		Id:      id,
	}
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewErrorResponse(code int, message string, id int) Response {
	return Response{
		JsonRPC: JSONRPC_2,
		Error: Error{
			Code:    code,
			Message: message,
		},
		Id: id,
	}
}

func writeResponse(conn net.Conn, res Response) error {
	jsonBytes, _ := json.Marshal(res)
	_, err := conn.Write(jsonBytes)
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) handleRequest(conn net.Conn) {
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		errResponse := NewErrorResponse(-32603, "internal server error", -1)
		writeResponse(conn, errResponse)
		return
	}

	var request Request
	if err := json.Unmarshal(buf[:n], &request); err != nil {
		errResponse := NewErrorResponse(-32600, "invalid request", -1)
		writeResponse(conn, errResponse)
		return
	}

	res, jsonErr := s.processRequest(request)
	if jsonErr != nil {
		errResponse := Response{JsonRPC: JSONRPC_2, Error: *jsonErr, Id: request.Id}
		writeResponse(conn, errResponse)
		return
	}

	writeResponse(conn, res)
	return
}

type IssuedEcashResponse struct {
	Keysets     []KeysetIssued `json:"keysets"`
	TotalIssued uint64         `json:"total_issued"`
}

type KeysetIssued struct {
	Id           string `json:"id"`
	AmountIssued uint64 `json:"amount_issued"`
}

type RedeemedEcashResponse struct {
	Keysets       []KeysetRedeemed `json:"keysets"`
	TotalRedeemed uint64           `json:"total_redeemed"`
}

type KeysetRedeemed struct {
	Id             string `json:"id"`
	AmountRedeemed uint64 `json:"amount_redeemed"`
}

type TotalBalanceResponse struct {
	TotalIssued        IssuedEcashResponse   `json:"total_issued"`
	TotalRedeemed      RedeemedEcashResponse `json:"total_redeemed"`
	TotalInCirculation uint64                `json:"total_circulation"`
}

func (s *Server) processRequest(req Request) (Response, *Error) {
	switch req.Method {
	case ISSUED_ECASH_REQUEST:
		return s.handleIssuedEcashReq(req)

	case REDEEMED_ECASH_REQUEST:
		return s.handleRedeemedEcashRequest(req)

	case TOTAL_BALANCE:
		return s.handleTotalBalanceRequest(req)

	case LIST_KEYSETS:
		keysets := s.mint.ListKeysets()
		result, _ := json.Marshal(keysets)
		return NewResponse(result, req.Id), nil

	case ROTATE_KEYSET:
		return s.handleRotateKeyset(req)

	default:
		return Response{}, &Error{Code: -32601, Message: "invalid method"}
	}
}

func (s *Server) handleIssuedEcashReq(req Request) (Response, *Error) {
	if len(req.Params) > 0 {
		issuedEcashMap, err := s.mint.IssuedEcash()
		if err != nil {
			return Response{}, &Error{Code: -32000, Message: err.Error()}
		}

		id := req.Params[0]
		amountIssued, ok := issuedEcashMap[id]
		if !ok {
			return Response{}, &Error{Code: -32000, Message: cashu.UnknownKeysetErr.Error()}
		}
		issuedByKeyset := KeysetIssued{
			Id:           id,
			AmountIssued: amountIssued,
		}
		result, _ := json.Marshal(issuedByKeyset)
		return NewResponse(result, req.Id), nil
	} else {
		issuedEcash, err := s.issuedEcash()
		if err != nil {
			return Response{}, &Error{Code: -32000, Message: err.Error()}
		}
		result, _ := json.Marshal(issuedEcash)
		return NewResponse(result, req.Id), nil
	}
}

func (s *Server) handleRedeemedEcashRequest(req Request) (Response, *Error) {
	if len(req.Params) > 0 {
		redeemedEcashMap, err := s.mint.RedeemedEcash()
		if err != nil {
			return Response{}, &Error{Code: -32000, Message: err.Error()}
		}

		id := req.Params[0]
		amountRedeemed, ok := redeemedEcashMap[id]
		if !ok {
			return Response{}, &Error{Code: -32000, Message: cashu.UnknownKeysetErr.Error()}
		}
		redeemedByKeyset := KeysetRedeemed{
			Id:             id,
			AmountRedeemed: amountRedeemed,
		}
		result, _ := json.Marshal(redeemedByKeyset)
		return NewResponse(result, req.Id), nil
	} else {
		redeemedEcash, err := s.redeemedEcash()
		if err != nil {
			return Response{}, &Error{Code: -32000, Message: err.Error()}
		}
		result, _ := json.Marshal(redeemedEcash)
		return NewResponse(result, req.Id), nil
	}
}

func (s *Server) handleTotalBalanceRequest(req Request) (Response, *Error) {
	issuedEcash, err := s.issuedEcash()
	if err != nil {
		return Response{}, &Error{Code: -32000, Message: err.Error()}
	}

	redeemedEcash, err := s.redeemedEcash()
	if err != nil {
		return Response{}, &Error{Code: -32000, Message: err.Error()}
	}

	totalBalance := TotalBalanceResponse{
		TotalIssued:        issuedEcash,
		TotalRedeemed:      redeemedEcash,
		TotalInCirculation: issuedEcash.TotalIssued - redeemedEcash.TotalRedeemed,
	}
	result, _ := json.Marshal(totalBalance)
	return NewResponse(result, req.Id), nil
}

func (s *Server) handleRotateKeyset(req Request) (Response, *Error) {
	if len(req.Params) < 1 {
		return Response{}, &Error{-32000, "fee not included"}
	} else {
		fee := req.Params[0]

		keysetFee, err := strconv.Atoi(fee)
		if err != nil || keysetFee < 0 {
			return Response{}, &Error{-32000, "invalid fee"}
		}

		newKeyset, err := s.mint.RotateKeyset(uint(keysetFee))
		if err != nil {
			return Response{}, &Error{-32000, err.Error()}
		}

		result, _ := json.Marshal(newKeyset)
		return NewResponse(result, req.Id), nil
	}
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
