package server

import (
	"fmt"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type MinimalGRPCServer struct {
	listenPort uint32
	listenProtocol string
	stopGracePeriod time.Duration  // How long we'll give the server to stop after asking nicely before we kill it
	serviceRegistrationFuncs []func(*grpc.Server)
}

func (server MinimalGRPCServer) Run() error {
	grpcServer := grpc.NewServer()

	for _, registrationFunc := range server.serviceRegistrationFuncs {
		registrationFunc(grpcServer)
	}

	listenAddressStr := fmt.Sprintf(":%v", server.listenPort)
	listener, err := net.Listen(server.listenProtocol, listenAddressStr)
	if err != nil {
		return stacktrace.Propagate(
			err,
			"An error occurred creating the listener on %v/%v",
			server.listenProtocol,
			server.listenPort,
		)
	}

	// Signals are used to interrupt the server, so we catch them here
	termSignalChan := make(chan os.Signal, 1)
	signal.Notify(termSignalChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	grpcServerResultChan := make(chan error)

	go func() {
		var resultErr error = nil
		if err := grpcServer.Serve(listener); err != nil {
			resultErr = stacktrace.Propagate(err, "The gRPC server exited with an error")
		}
		grpcServerResultChan <- resultErr
	}()

	// Wait until we get a shutdown signal
	<- termSignalChan

	serverStoppedChan := make(chan interface{})
	go func() {
		grpcServer.GracefulStop()
		serverStoppedChan <- nil
	}()
	select {
	case <- serverStoppedChan:
		logrus.Debug("gRPC server has exited gracefully")
	case <- time.After(server.stopGracePeriod):
		logrus.Warnf("gRPC server failed to stop gracefully after %v; hard-stopping now...", server.stopGracePeriod)
		grpcServer.Stop()
		logrus.Debug("gRPC server was forcefully stopped")
	}
	if err := <- grpcServerResultChan; err != nil {
		// Technically this doesn't need to be an error, but we make it so to fail loudly
		return stacktrace.Propagate(err, "gRPC server returned an error after it was done serving")
	}

	return nil
}