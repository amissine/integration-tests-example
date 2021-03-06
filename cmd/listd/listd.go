package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/george-e-shaw-iv/integration-tests-example/cmd/listd/configuration"
	"github.com/george-e-shaw-iv/integration-tests-example/cmd/listd/handlers"
	"github.com/george-e-shaw-iv/integration-tests-example/internal/platform/db"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

func main() {
	var mainErr error
	defer func() {
		if mainErr != nil {
			log.WithFields(log.Fields{
				"error": mainErr,
			}).Error("error in main")

			os.Exit(1)
		}
	}()

	cfg, err := configuration.Environment()
	if err != nil {
		mainErr = errors.Wrap(err, "gather env variables")
		return
	}

	dbc, err := db.NewConnection(cfg)
	if err != nil {
		mainErr = errors.Wrap(err, "connect to postgres db")
		return
	}

	server := http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.DaemonPort),
		Handler:        handlers.NewApplication(dbc, cfg),
		ReadTimeout:    cfg.ReadTimeout,
		WriteTimeout:   cfg.WriteTimeout,
		MaxHeaderBytes: 1 << 20,
	}

	// Start listening for requests made to the daemon and create a channel
	// to collect non-HTTP related server errors on.
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("server started, listening on %s", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	// Blocking main and waiting for shutdown of the daemon.
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)

	// Waiting for an osSignal or a non-HTTP related server error.
	select {
	case e := <-serverErrors:
		mainErr = fmt.Errorf("server failed to start: %+v", e)
		return

	case <-osSignals:
	}

	// Clean-up integrated services and gracefully shutdown server.
	if err := dbc.Close(); err != nil {
		log.Printf("error closing database: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown : Graceful shutdown did not complete in %v : %v", cfg.ShutdownTimeout, err)

		if err := server.Close(); err != nil {
			log.Printf("shutdown : Error killing server : %v", err)
		}
	}
}
