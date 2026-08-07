package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
)

// D5 unit: decideCredentials is the whole cross-origin credential rule (CO-7). It
// must attach to a same-origin (or nothing-to-protect) target, attach+warn to a
// risky target only when opted in, and otherwise withhold+warn.
func TestDecideCredentials(t *testing.T) {
	cases := []struct {
		name                  string
		risky, present, optIn bool
		want                  credAction
	}{
		{"same-origin with creds attaches silently", false, true, false, credAttachSilently},
		{"same-origin with creds ignores opt-in", false, true, true, credAttachSilently},
		{"risky but no creds attaches silently", true, false, false, credAttachSilently},
		{"risky but no creds even opted-in attaches silently", true, false, true, credAttachSilently},
		{"risky with creds, not opted in, withholds+warns", true, true, false, credWithholdWarn},
		{"risky with creds, opted in, attaches+warns", true, true, true, credAttachWarn},
		{"nothing risky nothing present attaches silently", false, false, false, credAttachSilently},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideCredentials(tc.risky, tc.present, tc.optIn); got != tc.want {
				t.Errorf("decideCredentials(%v,%v,%v) = %v, want %v", tc.risky, tc.present, tc.optIn, got, tc.want)
			}
		})
	}
}

// D5 unit: crossOriginLabel names why a target is gated, for the operator warning.
func TestCrossOriginLabel(t *testing.T) {
	cases := []struct {
		name string
		sel  *selection
		want string
	}{
		{"cross-origin only", &selection{crossOrigin: true}, "cross-origin"},
		{"downgraded only", &selection{downgraded: true}, "downgraded"},
		{"both", &selection{crossOrigin: true, downgraded: true}, "cross-origin, downgraded"},
		{"neither (same-origin)", &selection{}, "cross-origin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := crossOriginLabel(tc.sel); got != tc.want {
				t.Errorf("crossOriginLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

// D5 unit: credentialsPresent inspects a CallerSuppliedProvider (fast path) and
// fails safe for an opaque provider (treated as carrying credentials so it is still
// gated). A nil provider carries nothing.
func TestCredentialsPresent(t *testing.T) {
	if credentialsPresent(&CallerSuppliedProvider{}) {
		t.Error("an empty CallerSuppliedProvider must report no credentials")
	}
	if !credentialsPresent(&CallerSuppliedProvider{Bearer: "tok"}) {
		t.Error("a bearer token must report credentials present")
	}
	// An opaque provider that does not expose HasCredentials fails safe (present).
	if !credentialsPresent(opaqueProvider{}) {
		t.Error("an opaque provider must fail safe as credentials-present")
	}
	if credentialsPresent(nil) {
		t.Error("a nil provider carries nothing")
	}
}

type opaqueProvider struct{}

func (opaqueProvider) Headers(context.Context, Target) (map[string]string, error) { return nil, nil }

// D5: selectInterface sets the crossOrigin/downgraded flags that New() gates on.
func TestSelectInterface_CredentialFlags(t *testing.T) {
	t.Run("same-origin sets neither flag", func(t *testing.T) {
		card := &a2a.AgentCard{
			Name:                "same",
			SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://host-a/rest", a2a.TransportProtocolHTTPJSON)},
		}
		sel, err := selectInterface(card, "", "http://host-a", "http://host-a", false, nil)
		if err != nil {
			t.Fatalf("selectInterface: %v", err)
		}
		if sel.crossOrigin || sel.downgraded {
			t.Errorf("same-origin http interface should set neither flag, got crossOrigin=%v downgraded=%v", sel.crossOrigin, sel.downgraded)
		}
	})

	t.Run("different host sets crossOrigin", func(t *testing.T) {
		card := &a2a.AgentCard{
			Name:                "xorigin",
			SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://host-b/rest", a2a.TransportProtocolHTTPJSON)},
		}
		sel, err := selectInterface(card, "", "http://host-a", "http://host-a", false, nil)
		if err != nil {
			t.Fatalf("selectInterface: %v", err)
		}
		if !sel.crossOrigin {
			t.Error("an interface on a different host must set crossOrigin")
		}
		if sel.downgraded {
			t.Error("an http->http cross-origin selection is not a downgrade")
		}
	})

	t.Run("https card, http interface, --insecure sets downgraded", func(t *testing.T) {
		card := &a2a.AgentCard{
			Name:                "downgrade",
			SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://host-a/rest", a2a.TransportProtocolHTTPJSON)},
		}
		sel, err := selectInterface(card, "", "https://host-a", "https://host-a", true, nil)
		if err != nil {
			t.Fatalf("selectInterface under --insecure: %v", err)
		}
		if !sel.downgraded {
			t.Error("an https-fetched card declaring an http interface must set downgraded")
		}
	})

	t.Run("https card, http interface, no --insecure is refused", func(t *testing.T) {
		card := &a2a.AgentCard{
			Name:                "downgrade-refused",
			SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://host-a/rest", a2a.TransportProtocolHTTPJSON)},
		}
		_, err := selectInterface(card, "", "https://host-a", "https://host-a", false, nil)
		if err == nil {
			t.Fatal("a downgrade without --insecure must be refused")
		}
	})
}

// D1: classify normalizes the SAME binding-independent a2a sentinel to the SAME
// Kind/exit and preserves the wire reason on A2ACode, regardless of which transport
// produced it. This is the source-of-truth for cross-binding normalization (§9.4);
// the CLI-level tests then prove both bindings actually resolve to these sentinels.
func TestClassify_Normalization(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind clierr.Kind
		wantExit int
		wantA2A  string
	}{
		{"unauthenticated -> auth/4", a2a.NewError(a2a.ErrUnauthenticated, "no creds"), clierr.KindAuth, 4, "UNAUTHENTICATED"},
		{"unauthorized -> auth/4", a2a.NewError(a2a.ErrUnauthorized, "denied"), clierr.KindAuth, 4, "UNAUTHORIZED"},
		{"version unsupported -> generic/1", a2a.NewError(a2a.ErrVersionNotSupported, "nope"), clierr.KindGeneric, 1, "VERSION_NOT_SUPPORTED"},
		{"task not found -> notfound envelope, exit 1", a2a.NewError(a2a.ErrTaskNotFound, "ghost"), clierr.KindNotFound, 1, "TASK_NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.err, "op failed")
			if clierr.ExitCode(got) != tc.wantExit {
				t.Errorf("exit = %d, want %d (err %v)", clierr.ExitCode(got), tc.wantExit, got)
			}
			ce, ok := got.(*clierr.Error)
			if !ok {
				t.Fatalf("classify returned %T, want *clierr.Error", got)
			}
			if ce.Kind != tc.wantKind {
				t.Errorf("kind = %v, want %v", ce.Kind, tc.wantKind)
			}
			if ce.A2ACode != tc.wantA2A {
				t.Errorf("A2ACode = %q, want %q", ce.A2ACode, tc.wantA2A)
			}
		})
	}
}

// D3: the version-unsupported message is clear and distinct (never a silent
// downgrade) — it names the version mismatch and points at --a2a-version.
func TestClassify_VersionMessageIsDistinct(t *testing.T) {
	got := classify(a2a.NewError(a2a.ErrVersionNotSupported, "bad version"), "send failed")
	msg := got.Error()
	if !strings.Contains(msg, "version") || !strings.Contains(msg, "--a2a-version") {
		t.Errorf("version error should name the version problem and --a2a-version, got %q", msg)
	}
}

// D6(a): the data-plane response body cap wraps the send/get/cancel transport, not
// only the card fetch (CO-8). With a tiny cap an oversized GetTask body surfaces as
// an error naming the size limit; within the cap the same call succeeds — proving
// the cap fires only when exceeded.
func TestClient_DataPlaneBodyCap(t *testing.T) {
	// GetTask (REST) lands at /tasks/{id}; the well-known card points the single
	// HTTP+JSON interface back at this server so the data-plane call routes here.
	newServer := func(padBytes int) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == a2asrv.WellKnownAgentCardPath {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(restCard("http://" + r.Host))
				return
			}
			// A valid Task JSON padded past the cap via a large metadata field.
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"},"metadata":{"pad":"`)
			_, _ = io.WriteString(w, strings.Repeat("x", padBytes))
			_, _ = io.WriteString(w, `"}}`)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	orig := maxDataPlaneBytes
	t.Cleanup(func() { maxDataPlaneBytes = orig })

	t.Run("oversized body is capped", func(t *testing.T) {
		maxDataPlaneBytes = 1 << 10 // 1 KiB
		srv := newServer(4 << 10)   // 4 KiB of padding, well past the cap
		cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = cl.GetTask(context.Background(), "t1", GetOpts{})
		if err == nil {
			t.Fatal("an oversized data-plane body must surface an error, not stream unbounded")
		}
		if !strings.Contains(err.Error(), "size limit") {
			t.Errorf("error should name the size cap, got %v", err)
		}
	})

	t.Run("within-cap body succeeds", func(t *testing.T) {
		maxDataPlaneBytes = 1 << 20 // 1 MiB
		srv := newServer(16)        // tiny padding, well within the cap
		cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		tr, err := cl.GetTask(context.Background(), "t1", GetOpts{})
		if err != nil {
			t.Fatalf("a within-cap body must succeed, got %v", err)
		}
		if tr.State != "TASK_STATE_COMPLETED" {
			t.Errorf("state = %q, want TASK_STATE_COMPLETED", tr.State)
		}
	})
}
