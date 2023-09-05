package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"vendia/lambda-otel-exporter/internal/batchforwarder"
	"vendia/lambda-otel-exporter/internal/lambdaextension"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	HoneycombTraceEndpoint = "https://api.honeycomb.io/v1/traces"
)

var (
	// This environment variable is set in the extension environment. It's expected to be
	// a hostname:port combination.
	runtimeAPI = os.Getenv("AWS_LAMBDA_RUNTIME_API")

	// extension API configuration
	extensionName   = filepath.Base(os.Args[0])
	extensionClient = lambdaextension.New(runtimeAPI, extensionName)

	// when run in local mode, we don't attempt to register the extension or subscribe
	localMode = false

	// allow configuring debug logging
	debug = envOrElseBool("OTEL_DEBUG", false)
)

func init() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.With().Str("source", "lambda-otel-exporter").Logger()
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log.Logger = log.With().Caller().Logger()
	}
}

func main() {
	flag.BoolVar(&localMode, "localMode", false, "do not attempt to register")
	flag.Parse()

	honeycombDataset := os.Getenv("HONEYCOMB_DATASET")
	honeycombApiKey := os.Getenv("HONEYCOMB_API_KEY")
	if honeycombDataset == "" || honeycombApiKey == "" {
		log.Fatal().Msg("missing required env vars HONEYCOMB_DATASET/HONEYCOMB_API_KEY")
	}

	ctx, cancel := context.WithCancel(context.Background())

	// exit cleanly on SIGTERM or SIGINT
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sigs
		cancel()
		log.Info().Str("s", s.String()).Msg("exiting due to signal")
	}()

	log.Debug().Msg("starting batch forwarder")
	batchForwarder := batchforwarder.New(batchforwarder.Config{
		DestinationEndpoint: HoneycombTraceEndpoint,
		Headers: map[string]string{
			"x-honeycomb-dataset": honeycombDataset,
			"x-honeycomb-team":    honeycombApiKey,
		},
	})
	go func() {
		log.Error().Err(batchForwarder.Run()).Msg("batchForwarder server failed")
	}()

	// if running in localMode, just wait on the context to be cancelled
	if localMode {
		<-ctx.Done()
		return
	}

	log.Debug().Msg("registering extension")
	_, err := extensionClient.Register(ctx)
	if err != nil {
		log.Panic().Err(err).Msg("could not register extension")
	}

	// poll the extension API for the next invoke/shutdown event
	for {
		select {
		case <-ctx.Done():
			return
		default:
			response, err := extensionClient.NextEvent(ctx)
			if err != nil {
				log.Warn().Err(err).Msg("error from nextevent")
				continue
			}

			log.Debug().Interface("response", response).Msg("received event")
			switch eventType := response.EventType; eventType {
			case lambdaextension.Invoke:
				flushCtx, flushCancel := context.WithTimeout(ctx, time.Duration(time.Second*3))
				err = batchForwarder.Flush(flushCtx)
				if err != nil {
					log.Error().Err(err).Msg("failed to flush")
				}
				flushCancel()
			case lambdaextension.Shutdown:
				flushCtx, flushCancel := context.WithTimeout(ctx, time.Until(time.UnixMilli(response.DeadlineMS)))
				err = batchForwarder.Flush(flushCtx)
				if err != nil {
					log.Error().Err(err).Msg("failed to flush on shutdown")
				}
				flushCancel()
				cancel()
				return
			}
		}
	}
}

// envOrElseBool is a helper function that checks for env variable and falls back to a boolean
func envOrElseBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		v, err := strconv.ParseBool(value)
		if err != nil {
			return fallback
		}
		return v
	}
	return fallback
}
