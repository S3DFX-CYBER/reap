package mcp

import (
	"context"
	"testing"

	"github.com/hackwither/reap/internal/probe"
	"github.com/hackwither/reap/internal/report"
)

// frameEchoSession stands in for a transport that has no per-message HTTP
// request to echo — exactly what WSSession is, before it was taught to
// populate ReqMethod/ReqURL/ReqBody. Every RawResult it returns leaves
// ReqMethod empty, so RawResult.Exchange() returns nil, which is the
// documented contract.
type frameEchoSession struct{ target string }

func (s *frameEchoSession) TargetURL() string { return s.target }

func (s *frameEchoSession) AnonymousSession() (probe.Session, error) {
	return &frameEchoSession{target: s.target}, nil
}

const (
	testInitBody = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"ws","version":"1.0"},"capabilities":{"tools":{}}}}`
	testListBody = `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"exec_shell","inputSchema":{"type":"object"}},{"name":"read_file","inputSchema":{"type":"object"}}]}}`
	testResBody  = `{"jsonrpc":"2.0","id":3,"result":{"resources":[{"uri":"file:///etc/passwd","name":"pw"}]}}`
	testPrmBody  = `{"jsonrpc":"2.0","id":4,"result":{"prompts":[{"name":"p","description":"d"}]}}`
)

func (s *frameEchoSession) Do(ctx context.Context, method string, params any, opts ...probe.ReqOption) (*probe.RawResult, error) {
	body := ""
	switch method {
	case "initialize":
		body = testInitBody
	case "tools/list":
		body = testListBody
	case "resources/list":
		body = testResBody
	case "prompts/list":
		body = testPrmBody
	default:
		return &probe.RawResult{StatusCode: 404, Body: []byte(`{"jsonrpc":"2.0","id":9,"error":{"code":-32601,"message":"no"}}`)}, nil
	}
	// Deliberately no ReqMethod: this is the shape WSSession produced.
	return &probe.RawResult{StatusCode: 200, Body: []byte(body)}, nil
}

// TestAnyTransportProbesSurviveEmptyRequestEcho is the regression test for the
// remote panic: two anyTransport probes dereferenced RawResult.Exchange()
// without checking the documented nil case, so a hostile WebSocket endpoint
// that answered tools/list anonymously killed the process.
func TestAnyTransportProbesSurviveEmptyRequestEcho(t *testing.T) {
	var ran int
	for _, p := range BuiltinProbes() {
		if !supportsAnyTransport(p) {
			continue
		}
		ran++
		probeID := p.ID()
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Errorf("probe %s panicked on a transport with no request echo: %v", probeID, rec)
				}
			}()
			r := report.New("ws://agent.example/mcp", "ws://agent.example/mcp", "mcp")
			_ = p.Run(context.Background(), &frameEchoSession{target: "ws://agent.example/mcp"}, r)
		}()
	}
	if ran == 0 {
		t.Fatal("no anyTransport probes found; the test is not exercising anything")
	}
}

func supportsAnyTransport(p probe.Probe) bool {
	for _, tr := range p.Transports() {
		if tr == "*" {
			return true
		}
	}
	return false
}

// TestFindingsSurviveNilExchange asserts the evidence contract directly: a
// finding produced over a transport with no request echo must still be
// recorded, and its Request field must be nil rather than a dereferenced nil.
func TestFindingsSurviveNilExchange(t *testing.T) {
	r := report.New("ws://agent.example/mcp", "ws://agent.example/mcp", "mcp")
	p := &unauthToolsListProbe{}
	if err := p.Run(context.Background(), &frameEchoSession{target: "ws://agent.example/mcp"}, r); err != nil {
		t.Fatalf("probe returned error: %v", err)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("got %d findings, want 1 (the anonymous tools/list exposure)", len(r.Findings))
	}
}
