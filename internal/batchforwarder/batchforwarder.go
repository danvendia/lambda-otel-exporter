package batchforwarder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	v1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/rs/zerolog/log"
)

const (
	DefaultListenPort    = 4318
	DefaultQueueCapacity = 1000
)

type BatchForwarder struct {
	config     Config
	spanQueue  *spanQueue
	httpClient *http.Client
}

type Config struct {
	DestinationEndpoint string
	Headers             map[string]string
}

func New(config Config) *BatchForwarder {
	return &BatchForwarder{
		config:     config,
		spanQueue:  &spanQueue{[]*v1.ResourceSpans{}, &sync.Mutex{}, DefaultQueueCapacity},
		httpClient: &http.Client{},
	}
}

func (f *BatchForwarder) Run() error {
	http.HandleFunc("/v1/traces", f.addToQueue)

	listenAddr := fmt.Sprintf("0.0.0.0:%v", DefaultListenPort)
	log.Debug().Msgf("listening on %v", listenAddr)
	return http.ListenAndServe(listenAddr, nil)
}

func (f *BatchForwarder) addToQueue(rw http.ResponseWriter, req *http.Request) {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		log.Error().Err(err).Msg("failed to read trace request")
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	// handle both proto and json
	msg := coltracepb.ExportTraceServiceRequest{}
	switch req.Header.Get("Content-Type") {
	case "application/x-protobuf":
		err = proto.Unmarshal(data, &msg)
		if err != nil {
			log.Error().Err(err).Msg("failed to unmarshal protobuf data")
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
	case "application/json":
		err = json.Unmarshal(data, &msg)
		if err != nil {
			log.Error().Err(err).Msg("failed to unmarshal json data")
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
	default:
		log.Error().Err(err).Msg("invalid Content-Type")
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	resourceSpans := msg.GetResourceSpans()
	err = f.spanQueue.Enqueue(resourceSpans)
	if err != nil {
		log.Error().Err(err).Msg("failed to add spans to queue")
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Info().Int("resourceSpans", len(resourceSpans)).Msg("added spans to queue")

	rw.WriteHeader(http.StatusOK)
}

func (f *BatchForwarder) Flush(ctx context.Context) error {
	log.Debug().Msg("started flush")
	start := time.Now()

	spans := f.spanQueue.DequeueAll()
	exportRequest := coltracepb.ExportTraceServiceRequest{ResourceSpans: spans}
	protoExportRequest, err := proto.Marshal(&exportRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal trace service request: %w", err)
	}

	body := bytes.NewBuffer(protoExportRequest)
	req, err := http.NewRequestWithContext(ctx, "POST", f.config.DestinationEndpoint, body)
	if err != nil {
		return fmt.Errorf("failed to create HTTP POST request: %w", err)
	}

	for k, v := range f.config.Headers {
		req.Header.Add(k, v)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute HTTP POST request: %w", err)
	}
	defer resp.Body.Close()

	log.Info().Int("statusCode", resp.StatusCode).TimeDiff("flushDuration", time.Now(), start).Msg("completed flush")
	return nil
}

type spanQueue struct {
	queue    []*v1.ResourceSpans
	mu       *sync.Mutex
	capacity int
}

func (s *spanQueue) Enqueue(spans []*v1.ResourceSpans) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.queue) >= s.capacity {
		return fmt.Errorf("unable to add to queue - at capacity %v", s.capacity)
	}

	s.queue = append(s.queue, spans...)

	return nil
}

func (s *spanQueue) DequeueAll() []*v1.ResourceSpans {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := s.queue
	s.queue = []*v1.ResourceSpans{}
	return items
}
