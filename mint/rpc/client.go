package rpc

import (
	"crypto/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"log/slog"
)

func CreateGrpcClient(address string, insecure bool) (*grpc.ClientConn, error) {
	if !insecure {
		return unaryInterceptorDialTls(address)
	}
	return unaryInterceptorDial(address)
}

// custom dial function for grpc requests.
func unaryInterceptorDialTls(address string) (*grpc.ClientConn, error) {
	// Create a certificate pool and add the self-signed certificate to it
	grpcOption := make([]grpc.DialOption, 0)
	// wagctl will always use tls
	h2creds := credentials.NewTLS(&tls.Config{NextProtos: []string{"h2"}})
	grpcOption = append(grpcOption, grpc.WithTransportCredentials(h2creds))
	return unaryInterceptorDial(address, grpcOption...)
}

func unaryInterceptorDial(address string, option ...grpc.DialOption) (*grpc.ClientConn, error) {
	// wagctl will always use tls
	if option == nil {
		option = append(option, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	conn, err := grpc.Dial(address, option...)
	if err != nil {
		slog.Error("could not connect", "error", err, "address", address)
		return nil, err
	}
	return conn, nil
}
