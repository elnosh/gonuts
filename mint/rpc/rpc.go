package rpc

import (
	"context"
	"fmt"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"log/slog"
	log "log/slog"
	"net"
	"net/http"
	"time"
)

// Server represents a gRPC server.
//
// It contains a net.Listener for listening to incoming connections,
// a *grpc.Server for handling gRPC requests, an array of
// grpc_auth.AuthFunc for authenticating gRPC requests, an array of middleware
// functions for the HTTP gateway, and a ServiceHandlerFromEndpointRegistration
// function for registering the server with the gateway.
type Server struct {
	listener            net.Listener
	GRPC                *grpc.Server
	authFuncs           []grpc_auth.AuthFunc
	middlewares         []func(http.HandlerFunc) http.HandlerFunc
	gateWayRegistration []ServiceHandlerFromEndpointRegistration
}

// ServerOption is a functional option pattern for configuring a Server.
type ServerOption func(*Server)

func WithServiceHandlerFromEndpointRegistration(registration ...ServiceHandlerFromEndpointRegistration) ServerOption {
	return func(r *Server) {
		r.gateWayRegistration = registration
	}
}

// ServiceHandlerFromEndpointRegistration is a function type that registers a gRPC server endpoint with a
// runtime.ServeMux and initializes it with the specified options. It takes a context.
type ServiceHandlerFromEndpointRegistration func(
	ctx context.Context,
	mux *runtime.ServeMux,
	endpoint string,
	opts []grpc.DialOption,
) (err error)

// RegisterService registers a service implementation to the given registrar using the provided service descriptor and implementation interface.
// It calls registrar.RegisterService(desc, impl) to perform the registration.
func (s *Server) RegisterService(registrar grpc.ServiceRegistrar, desc *grpc.ServiceDesc, impl interface{}) {
	registrar.RegisterService(desc, impl)
}
func (s *Server) Serve() error {
	return s.GRPC.Serve(s.listener)
}

// DurationToTimeMillisFields converts the duration to milliseconds and uses the key `grpc.time_ms`.
func DurationToTimeMillisFields(duration time.Duration) logging.Fields {
	slog.Info("help")
	return logging.Fields{"grpc.time_ms", float32(duration.Nanoseconds()/1000) / 1000}
}

func NewServer(opt ...ServerOption) *Server {
	service := &Server{}
	for _, options := range opt {
		if options != nil {
			options(service)
		}
	}
	server := createGrpcWithHealthServer(
		service,
		net.JoinHostPort("0.0.0.0", "3339"),
		grpc.ChainUnaryInterceptor([]grpc.UnaryServerInterceptor{
			logging.UnaryServerInterceptor(InterceptorLogger(slog.Default()), logging.WithDurationField(DurationToTimeMillisFields),
				logging.WithLogOnEvents(logging.StartCall, logging.FinishCall, logging.PayloadReceived, logging.PayloadSent)),
		}...),
	)
	service.GRPC = server
	if service.gateWayRegistration != nil {
		go func() {
			err := startGateway(service.gateWayRegistration, service.middlewares...)
			if err != nil {
				log.Error("error starting grpc gateway proxy", "error", err)
			}
		}()
	}
	/// register grpc services with GRPC
	return service
}

// startGateway starts the gRPC gateway proxy server.
// It registers the gRPC server endpoint and handles incoming HTTP requests by proxying them to the gRPC server.
// The function takes a registration function, which is responsible for registering the service handler from the endpoint.
// It also supports optional middlewares to be applied to the HTTP handler.
// The gateway configuration is loaded from the global Configuration variable.
// The function returns an error if there was an issue while serving the HTTP requests.
func startGateway(
	registration []ServiceHandlerFromEndpointRegistration,
	middlewares ...func(http.HandlerFunc) http.HandlerFunc,
) error {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register gRPC server endpoint
	// Note: Make sure the gRPC server is running properly and accessible
	serverOptions := make([]runtime.ServeMuxOption, 0)
	serverOptions = append(serverOptions,
		runtime.WithMetadata(func(ctx context.Context, request *http.Request) metadata.MD {
			metaData := metadata.New(map[string]string{"x-api-key": request.Header.Get("X-API-Key")})
			md, ok := metadata.FromOutgoingContext(ctx)
			if ok {
				metaData.Append("auth-user-bin", md["auth-user-bin"][0])
			}
			return metaData
		}),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONBuiltin{}))

	mux := runtime.NewServeMux(serverOptions...)

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	endpoint := fmt.Sprintf("%s:%d", "0.0.0.0", 3339)
	for _, endpointRegistration := range registration {
		err := endpointRegistration(ctx, mux, endpoint, opts)
		if err != nil {
			return err
		}
	}

	// todo -- get rid of MustInvoke invocation soon (not big issue, because its called on start not runtime)
	server := http.Server{
		Handler: Use(func(writer http.ResponseWriter, request *http.Request) {
			mux.ServeHTTP(writer, request)
		}, middlewares...),
		Addr:        "0.0.0.0:3338",
		ReadTimeout: time.Second * 30,
	}
	// Start HTTP server (and proxy calls to gRPC server endpoint)
	return fmt.Errorf("error while serving http: %w", server.ListenAndServe())
}

// InterceptorLogger is a function that returns a logger function for grpc server interceptors.
// The logger function logs the incoming messages with the specified logger in the appropriate log level.
// It also checks if the message should be skipped based on the context.
//
// Parameters:
//   - logger: A pointer to a slog.Logger to use for logging.
//
// Return:
//   - logging.LoggerFunc: The logger function used by the grpc server interceptors.
func InterceptorLogger(logger *slog.Logger) logging.LoggerFunc {
	return func(ctx context.Context, lvl logging.Level, msg string, fields ...any) {
		// check if message should be skipped (start/finish call for health check for instance)
		switch lvl {
		case logging.LevelDebug:
			logger.With(fields...).DebugContext(ctx, msg)
		case logging.LevelInfo:
			logger.With(fields...).InfoContext(ctx, msg)
		case logging.LevelWarn:
			logger.With(fields...).WarnContext(ctx, msg)
		case logging.LevelError:
			logger.With(fields...).ErrorContext(ctx, msg)
		default:
			logger.ErrorContext(ctx, "invalid log level", "log level", lvl)
		}
	}
}

func Use(h http.HandlerFunc, middleware ...func(http.HandlerFunc) http.HandlerFunc) http.HandlerFunc {
	for _, m := range middleware {
		h = m(h)
	}
	return h
}

// createGrpcWithHealthServer starts a GRPC server with health checks enabled.
// It calls the createGrpcServer function to create the GRPC server,
// and then registers a health server with default SERVING response.
// Finally, it returns the created server.
func createGrpcWithHealthServer(service *Server, address string, options ...grpc.ServerOption) *grpc.Server {
	server := createGrpcServer(service, address, options...)
	hs := health.NewServer()                        // will default to respond with SERVING
	grpc_health_v1.RegisterHealthServer(server, hs) // registration
	return server
}

// createGrpcServer creates a gRPC server with the specified service, address, and options.
// It creates a TCP listener on the given address and assigns it to the service's listener field.
// Then it creates a new gRPC server with the provided options and assigns it to the service's GRPC field.
// Returns the created gRPC server.
func createGrpcServer(service *Server, address string, options ...grpc.ServerOption) *grpc.Server {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		panic(err)
	}
	service.listener = lis

	s := grpc.NewServer(
		options...,
	)
	service.GRPC = s
	return s
}
