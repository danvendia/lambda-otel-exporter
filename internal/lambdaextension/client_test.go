// This extension code is borrowed from https://github.com/honeycombio/honeycomb-lambda-extension
// which is itself borrowed from https://github.com/aws-samples/aws-lambda-extensions. The only
// minor change is that registering for Invoke event types has been removed
package lambdaextension

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	testName            = "test-extension"
	testIdentifier      = "test-identifier"
	testFunctionName    = "ThisIsAFunction"
	testFunctionVersion = "$LATEST"
	testFunctionHandler = "handler.test"

	testDeadlineMS   = 676051
	testRequestID    = "3da1f2dc-3222-475e-9205-e2e6c6318895"
	testFunctionARN  = "arn:aws:lambda:us-east-1:123456789012:function:ExtensionTest"
	testTracingType  = "X-Amzn-Trace-Id"
	testTracingValue = "Root=1-5f35ae12-0c0fec141ab77a00bc047aa2;Parent=2be948a625588e32;Sampled=1"
)

func RegisterServer(t *testing.T) *httptest.Server {

	fixtures := []struct {
		name     string
		expected string
		actual   func(r *http.Request) string
	}{
		{
			name:     "request path",
			expected: "/2020-01-01/extension/register",
			actual: func(r *http.Request) string {
				return r.URL.String()
			},
		},
		{
			name:     "request header",
			expected: testName,
			actual: func(r *http.Request) string {
				return r.Header.Get(extensionNameHeader)
			},
		},
	}

	handlerFunc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, tt := range fixtures {
			t.Run(tt.name, func(t *testing.T) {
				got := tt.actual(r)
				if tt.expected != got {
					t.Errorf("got: %#v\nwant: %#v", tt.expected, got)
				}
			})
		}
		defer r.Body.Close()
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Error("Error reading POST body")
		}

		var req map[string][]EventType
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("Error unmarshaling body: %s: %s", body, err)
		}
		events, ok := req["events"]
		if !ok {
			t.Error("Expected result to include events")
		}
		if events[0] != Invoke {
			t.Errorf("Expected invoke, got: %#v", events[0])
		}
		if events[1] != Shutdown {
			t.Errorf("Expected shutdown, got: %#v", events[1])
		}

		resp := RegisterResponse{
			FunctionName:    testFunctionName,
			FunctionVersion: testFunctionVersion,
			Handler:         testFunctionHandler,
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Error("Could not marshal JSON")
		}
		w.Header().Set(extensionIdentifierHeader, testIdentifier)
		_, err = w.Write(b)
		if err != nil {
			t.Errorf("Could write data: %v", err)
		}
	})

	return httptest.NewServer(handlerFunc)
}

func NextEventServer(t *testing.T, eventType EventType) *httptest.Server {
	handlerFunc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := NextEventResponse{
			EventType:          eventType,
			DeadlineMS:         int64(testDeadlineMS),
			RequestID:          testRequestID,
			InvokedFunctionARN: testFunctionARN,
			Tracing: Tracing{
				Type:  testTracingType,
				Value: testTracingValue,
			},
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Error("Could not marshal json")
		}
		_, err = w.Write(b)
		if err != nil {
			t.Errorf("Could write data: %v", err)
		}
	})
	return httptest.NewServer(handlerFunc)
}

func TestRegisterExtension(t *testing.T) {
	server := RegisterServer(t)
	defer server.Close()

	client := New(server.URL, testName)
	ctx := context.TODO()
	resp, err := client.Register(ctx)

	if err != nil {
		t.Error(err)
		return
	}

	if resp == nil {
		t.Error("Unexpected response from register")
		return
	}

	assert.Equal(t, testFunctionName, resp.FunctionName)
	assert.Equal(t, testIdentifier, client.ExtensionID)
}

func TestNextEvent(t *testing.T) {
	server := NextEventServer(t, Shutdown)
	defer server.Close()

	client := New(server.URL, testName)
	ctx := context.TODO()
	if _, err := client.Register(ctx); err != nil {
		t.Error(err)
	}
	res, err := client.NextEvent(ctx)
	if err != nil {
		t.Error(err)
	}
	assert.Equal(t, Shutdown, res.EventType)
	assert.Equal(t, "X-Amzn-Trace-Id", res.Tracing.Type)
}

func TestURL(t *testing.T) {
	client := New("vendia.net/foo", testName)
	assert.Equal(t, "http://vendia.net/foo/2020-01-01/extension", client.baseURL)

	url := client.url("/foo/bar/baz")
	assert.Equal(t, "http://vendia.net/foo/2020-01-01/extension/foo/bar/baz", url)

	client = New("https://mywebsite.com:9000", testName)

	assert.Equal(t, "https://mywebsite.com:9000/2020-01-01/extension", client.baseURL)
	assert.Equal(t, "https://mywebsite.com:9000/2020-01-01/extension/foo/bar", client.url("foo/bar"))
}
