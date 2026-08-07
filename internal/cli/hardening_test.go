package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// --- D1: cross-binding exit-code normalization -------------------------------
//
// The SAME A2A error must yield the SAME Kind/exit/envelope whether it arrived
// over JSON-RPC (numeric code) or HTTP+JSON/REST (google.rpc.Status reason). These
// tests drive both bindings against get/cancel/send and assert identical results
// (design §9.4/§9.5). The reason strings/codes are the real ones the a2a SDK
// resolves: UNAUTHENTICATED (-31401), UNAUTHORIZED (-31403), VERSION_NOT_SUPPORTED
// (-32009), TASK_NOT_FOUND (-32001).

// authBody is a google.rpc.Status body whose ErrorInfo reason resolves to
// a2a.ErrUnauthenticated on the REST transport (§9.4).
func authBody() map[string]any {
	return errorBody(401, "UNAUTHENTICATED", "authentication required", "UNAUTHENTICATED")
}

func unauthorizedBody() map[string]any {
	return errorBody(403, "PERMISSION_DENIED", "permission denied", "UNAUTHORIZED")
}

func versionBody() map[string]any {
	return errorBody(400, "FAILED_PRECONDITION", "unsupported A2A version", "VERSION_NOT_SUPPORTED")
}

// D1: a server AUTH error normalizes to KindAuth -> exit 4 with a2aCode
// UNAUTHENTICATED on BOTH bindings, across get/cancel/send.
func TestExitCode_Auth_CrossBinding(t *testing.T) {
	assertAuth := func(t *testing.T, out, errOut string, code int) {
		t.Helper()
		if code != 4 {
			t.Fatalf("exit = %d, want 4 (AUTH_REQUIRED)\nstderr: %s", code, errOut)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("stdout is not a single valid JSON error object: %v\n%s", err, out)
		}
		if env["code"] != "AUTH_REQUIRED" {
			t.Errorf("envelope code = %v, want AUTH_REQUIRED", env["code"])
		}
		if env["a2aCode"] != "UNAUTHENTICATED" {
			t.Errorf("envelope a2aCode = %v, want UNAUTHENTICATED", env["a2aCode"])
		}
	}

	t.Run("get REST", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newTaskServer(t, taskEndpoint{getFn: func(_, _ string) (int, any) { return 401, authBody() }})
		out, errOut, code := runCLI(t, "get", "t1", "-u", srv.URL, "-o", "json")
		assertAuth(t, out, errOut, code)
	})
	t.Run("get JSON-RPC", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newJSONRPCErrorServer(t, -31401, "unauthenticated")
		out, errOut, code := runCLI(t, "get", "t1", "-u", srv.URL, "--transport", "jsonrpc", "-o", "json")
		assertAuth(t, out, errOut, code)
	})
	t.Run("cancel REST", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newTaskServer(t, taskEndpoint{cancelFn: func(string) (int, any) { return 401, authBody() }})
		out, errOut, code := runCLI(t, "cancel", "t1", "-u", srv.URL, "-o", "json")
		assertAuth(t, out, errOut, code)
	})
	t.Run("cancel JSON-RPC", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newJSONRPCErrorServer(t, -31401, "unauthenticated")
		out, errOut, code := runCLI(t, "cancel", "t1", "-u", srv.URL, "--transport", "jsonrpc", "-o", "json")
		assertAuth(t, out, errOut, code)
	})
	t.Run("send REST", func(t *testing.T) {
		cleanConfigDir(t)
		ss := newSendServer(t, func(sendRecord) (int, any) { return 401, authBody() })
		out, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "-o", "json")
		assertAuth(t, out, errOut, code)
	})
	t.Run("send JSON-RPC", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newJSONRPCErrorServer(t, -31401, "unauthenticated")
		out, errOut, code := runCLI(t, "send", "hi", "-u", srv.URL, "--transport", "jsonrpc", "-o", "json")
		assertAuth(t, out, errOut, code)
	})
}

// D1: an UNAUTHORIZED (permission denied) server error also maps to exit 4, proving
// both auth sentinels share the AUTH_REQUIRED slot.
func TestExitCode_Unauthorized_Exit4(t *testing.T) {
	t.Run("REST", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newTaskServer(t, taskEndpoint{getFn: func(_, _ string) (int, any) { return 403, unauthorizedBody() }})
		out, errOut, code := runCLI(t, "get", "t1", "-u", srv.URL, "-o", "json")
		if code != 4 {
			t.Fatalf("exit = %d, want 4\nstderr: %s", code, errOut)
		}
		var env map[string]any
		_ = json.Unmarshal([]byte(out), &env)
		if env["code"] != "AUTH_REQUIRED" || env["a2aCode"] != "UNAUTHORIZED" {
			t.Errorf("envelope = %v, want code=AUTH_REQUIRED a2aCode=UNAUTHORIZED", env)
		}
	})
	t.Run("JSON-RPC", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newJSONRPCErrorServer(t, -31403, "permission denied")
		_, errOut, code := runCLI(t, "get", "t1", "-u", srv.URL, "--transport", "jsonrpc", "-o", "json")
		if code != 4 {
			t.Fatalf("exit = %d, want 4\nstderr: %s", code, errOut)
		}
	})
}

// D1 (cross-binding normalization): the SAME auth error produces the SAME
// Kind/exit/envelope shape on JSON-RPC vs HTTP+JSON. Locks §9.4 as behavior, not
// prose: the two envelopes must agree on code, a2aCode, and exit.
func TestExitCode_Auth_BindingsAgree(t *testing.T) {
	cleanConfigDir(t)
	rest := newTaskServer(t, taskEndpoint{getFn: func(_, _ string) (int, any) { return 401, authBody() }})
	outR, _, codeR := runCLI(t, "get", "t1", "-u", rest.URL, "-o", "json")

	cleanConfigDir(t)
	rpc := newJSONRPCErrorServer(t, -31401, "unauthenticated")
	outJ, _, codeJ := runCLI(t, "get", "t1", "-u", rpc.URL, "--transport", "jsonrpc", "-o", "json")

	if codeR != codeJ {
		t.Errorf("exit codes differ across bindings: REST=%d JSON-RPC=%d", codeR, codeJ)
	}
	var envR, envJ map[string]any
	if err := json.Unmarshal([]byte(outR), &envR); err != nil {
		t.Fatalf("REST envelope invalid: %v\n%s", err, outR)
	}
	if err := json.Unmarshal([]byte(outJ), &envJ); err != nil {
		t.Fatalf("JSON-RPC envelope invalid: %v\n%s", err, outJ)
	}
	if envR["code"] != envJ["code"] || envR["a2aCode"] != envJ["a2aCode"] {
		t.Errorf("envelopes differ across bindings: REST code=%v a2aCode=%v vs JSON-RPC code=%v a2aCode=%v",
			envR["code"], envR["a2aCode"], envJ["code"], envJ["a2aCode"])
	}
}

// --- D3: --a2a-version unsupported surfacing ---------------------------------
//
// A server that rejects the signaled A2A version surfaces a CLEAR, DISTINCT error
// (never a silent downgrade). Exit code is the reviewer-disposition GENERIC (1);
// the envelope carries a2aCode VERSION_NOT_SUPPORTED and the message points the
// operator at --a2a-version (spec §11.2).
func TestVersionUnsupported_Surfaced(t *testing.T) {
	assertVersion := func(t *testing.T, out, errOut string, code int) {
		t.Helper()
		if code != 1 {
			t.Fatalf("exit = %d, want 1 (GENERIC, reviewer-disposition)\nstderr: %s", code, errOut)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("stdout is not a single valid JSON error object: %v\n%s", err, out)
		}
		if env["a2aCode"] != "VERSION_NOT_SUPPORTED" {
			t.Errorf("envelope a2aCode = %v, want VERSION_NOT_SUPPORTED", env["a2aCode"])
		}
		msg, _ := env["message"].(string)
		if !strings.Contains(msg, "version") || !strings.Contains(msg, "--a2a-version") {
			t.Errorf("message must clearly surface the version rejection and --a2a-version, got %q", msg)
		}
	}

	t.Run("send REST", func(t *testing.T) {
		cleanConfigDir(t)
		ss := newSendServer(t, func(sendRecord) (int, any) { return 400, versionBody() })
		out, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--a2a-version", "9.9", "-o", "json")
		assertVersion(t, out, errOut, code)
	})
	t.Run("get REST", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newTaskServer(t, taskEndpoint{getFn: func(_, _ string) (int, any) { return 400, versionBody() }})
		out, errOut, code := runCLI(t, "get", "t1", "-u", srv.URL, "--a2a-version", "9.9", "-o", "json")
		assertVersion(t, out, errOut, code)
	})
	t.Run("get JSON-RPC", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newJSONRPCErrorServer(t, -32009, "unsupported version")
		out, errOut, code := runCLI(t, "get", "t1", "-u", srv.URL, "--transport", "jsonrpc", "--a2a-version", "9.9", "-o", "json")
		assertVersion(t, out, errOut, code)
	})
}

// --- D2: credential env-var equivalents + precedence -------------------------
//
// A2A_BEARER / A2A_API_KEY provide credentials when the flag is unset; an explicit
// flag always wins (flag > env > unset). Proven by recording the Authorization /
// X-API-Key header a same-origin send actually put on the wire (§10.1).

// credRecorder captures the credential headers seen on data-plane requests.
type credRecorder struct {
	mu     sync.Mutex
	auth   string
	apiKey string
	hits   int
}

func (c *credRecorder) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a := r.Header.Get("Authorization"); a != "" {
		c.auth = a
	}
	if k := r.Header.Get("X-API-Key"); k != "" {
		c.apiKey = k
	}
	c.hits++
}

func (c *credRecorder) snapshot() (auth, apiKey string, hits int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.auth, c.apiKey, c.hits
}

// newCredSendServer is a SAME-ORIGIN send server (card + data plane on one host, so
// the B2 check passes and creds are forwarded by default) that records the
// credential headers each send carried.
func newCredSendServer(t *testing.T, rec *credRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskCardJSON("http://" + r.Host))
			return
		}
		rec.record(r)
		writeJSONStatus(w, 200, taskResultBody("t1", "c1", "TASK_STATE_COMPLETED"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCredentials_EnvVarPrecedence(t *testing.T) {
	t.Run("env var supplies bearer when flag unset", func(t *testing.T) {
		cleanConfigDir(t)
		t.Setenv(envBearer, "env-token")
		rec := &credRecorder{}
		srv := newCredSendServer(t, rec)
		_, errOut, code := runCLI(t, "send", "hi", "-u", srv.URL, "-o", "json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if auth, _, _ := rec.snapshot(); auth != "Bearer env-token" {
			t.Errorf("Authorization on wire = %q, want %q (from A2A_BEARER)", auth, "Bearer env-token")
		}
	})

	t.Run("explicit flag overrides env", func(t *testing.T) {
		cleanConfigDir(t)
		t.Setenv(envBearer, "env-token")
		rec := &credRecorder{}
		srv := newCredSendServer(t, rec)
		_, errOut, code := runCLI(t, "send", "hi", "-u", srv.URL, "--bearer", "flag-token", "-o", "json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if auth, _, _ := rec.snapshot(); auth != "Bearer flag-token" {
			t.Errorf("Authorization on wire = %q, want %q (flag wins over env)", auth, "Bearer flag-token")
		}
	})

	t.Run("unset flag and env sends nothing", func(t *testing.T) {
		cleanConfigDir(t)
		t.Setenv(envBearer, "")
		t.Setenv(envAPIKey, "")
		rec := &credRecorder{}
		srv := newCredSendServer(t, rec)
		_, errOut, code := runCLI(t, "send", "hi", "-u", srv.URL, "-o", "json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if auth, key, _ := rec.snapshot(); auth != "" || key != "" {
			t.Errorf("no credentials should be sent, got Authorization=%q X-API-Key=%q", auth, key)
		}
	})

	t.Run("A2A_API_KEY supplies the api key when flag unset", func(t *testing.T) {
		cleanConfigDir(t)
		t.Setenv(envAPIKey, "env-key")
		rec := &credRecorder{}
		srv := newCredSendServer(t, rec)
		_, errOut, code := runCLI(t, "send", "hi", "-u", srv.URL, "-o", "json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if _, key, _ := rec.snapshot(); key != "env-key" {
			t.Errorf("X-API-Key on wire = %q, want %q (from A2A_API_KEY)", key, "env-key")
		}
	})
}

// --- D5: cross-origin / downgrade credential opt-in (CO-7) -------------------
//
// Credentials are WITHHELD from a cross-origin (or downgraded) interface target by
// default (with a warning), and forwarded only under --allow-cross-origin-credentials
// (also warned). Same-origin is unaffected (covered by D2 above). Proven across
// get / cancel / send (blocking) over REST and send --stream over JSON-RPC — the
// gate is decided once in client.New, so this exercises every command path.

// newXOriginCardServer serves ONLY the well-known card, advertising a single
// interface (of the given binding) that points at a DIFFERENT host (the data
// server), so selection is cross-origin.
func newXOriginCardServer(t *testing.T, ifaceURL, binding string) *httptest.Server {
	t.Helper()
	card := map[string]any{
		"name":               "XOrigin Agent",
		"description":        "advertises a cross-origin interface",
		"version":            "1.0.0",
		"capabilities":       map[string]any{"streaming": binding == "JSONRPC", "pushNotifications": false},
		"defaultInputModes":  []string{"text"},
		"defaultOutputModes": []string{"text"},
		"supportedInterfaces": []map[string]any{
			{"url": ifaceURL, "protocolBinding": binding, "protocolVersion": "1.0"},
		},
		"skills": []map[string]any{
			{"id": "echo", "name": "Echo", "description": "echoes", "tags": []string{"t"}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(card)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newRESTDataServer is a cross-origin REST data plane (no card) that records the
// credential headers on every send/get/cancel and returns a success body.
func newRESTDataServer(t *testing.T, rec *credRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case strings.HasSuffix(r.URL.Path, "/message:send"):
			writeJSONStatus(w, 200, taskResultBody("t1", "c1", "TASK_STATE_COMPLETED"))
		case strings.HasSuffix(r.URL.Path, ":cancel"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/tasks/"), ":cancel")
			writeJSONStatus(w, 200, taskDoc(id, "c1", "TASK_STATE_CANCELED"))
		case strings.HasPrefix(r.URL.Path, "/tasks/"):
			id := strings.TrimPrefix(r.URL.Path, "/tasks/")
			writeJSONStatus(w, 200, taskDoc(id, "c1", "TASK_STATE_COMPLETED"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCredentialGate_CrossOrigin_REST(t *testing.T) {
	type cmd struct {
		name string
		args []string
	}
	commands := []cmd{
		{"get", []string{"get", "t1"}},
		{"cancel", []string{"cancel", "t1"}},
		{"send", []string{"send", "hi"}},
	}

	for _, c := range commands {
		t.Run(c.name+" default withholds", func(t *testing.T) {
			cleanConfigDir(t)
			rec := &credRecorder{}
			data := newRESTDataServer(t, rec)
			card := newXOriginCardServer(t, data.URL, "HTTP+JSON")
			args := append(append([]string{}, c.args...), "-u", card.URL, "--bearer", "tok", "-o", "json")
			_, errOut, code := runCLI(t, args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
			}
			if auth, _, hits := rec.snapshot(); auth != "" {
				t.Errorf("cross-origin default must NOT forward credentials, got Authorization=%q (hits=%d)", auth, hits)
			}
			if !strings.Contains(errOut, "not forwarding") || !strings.Contains(errOut, "cross-origin") {
				t.Errorf("withholding must warn about the cross-origin target, got %q", errOut)
			}
		})

		t.Run(c.name+" opt-in forwards", func(t *testing.T) {
			cleanConfigDir(t)
			rec := &credRecorder{}
			data := newRESTDataServer(t, rec)
			card := newXOriginCardServer(t, data.URL, "HTTP+JSON")
			args := append(append([]string{}, c.args...), "-u", card.URL, "--bearer", "tok", "--allow-cross-origin-credentials", "-o", "json")
			_, errOut, code := runCLI(t, args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
			}
			if auth, _, _ := rec.snapshot(); auth != "Bearer tok" {
				t.Errorf("opt-in must forward credentials, got Authorization=%q", auth)
			}
			if !strings.Contains(errOut, "forwarding caller credentials") {
				t.Errorf("opt-in forwarding should still warn, got %q", errOut)
			}
		})
	}
}

// D5: same-origin is unaffected by the gate — credentials ride by default with no
// withholding warning (the counter-case to the cross-origin default above).
func TestCredentialGate_SameOrigin_Forwards(t *testing.T) {
	cleanConfigDir(t)
	rec := &credRecorder{}
	srv := newCredSendServer(t, rec)
	_, errOut, code := runCLI(t, "send", "hi", "-u", srv.URL, "--bearer", "tok", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if auth, _, _ := rec.snapshot(); auth != "Bearer tok" {
		t.Errorf("same-origin must forward credentials by default, got %q", auth)
	}
	if strings.Contains(errOut, "not forwarding") {
		t.Errorf("same-origin must NOT emit a withholding warning, got %q", errOut)
	}
}

// newXOriginStreamServer is a cross-origin JSON-RPC data plane (no card) that
// records credential headers and serves SendStreamingMessage (SSE) + GetTask so
// send --stream exercises the credential gate on the streaming path too.
func newXOriginStreamServer(t *testing.T, rec *credRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		var req struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "SendStreamingMessage":
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("no flusher")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()
			payload, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"task": taskJSON("t1", "c1", "TASK_STATE_COMPLETED")},
			})
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case "GetTask":
			writeRPCResult(w, req.ID, taskJSON("t1", "c1", "TASK_STATE_COMPLETED"))
		default:
			writeRPCResult(w, req.ID, map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCredentialGate_CrossOrigin_Stream(t *testing.T) {
	t.Run("default withholds on stream", func(t *testing.T) {
		cleanConfigDir(t)
		rec := &credRecorder{}
		data := newXOriginStreamServer(t, rec)
		card := newXOriginCardServer(t, data.URL, "JSONRPC")
		_, errOut, code := runCLI(t, "send", "--stream", "hi", "-u", card.URL, "--transport", "jsonrpc", "--bearer", "tok", "--timeout", "5s")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if auth, _, _ := rec.snapshot(); auth != "" {
			t.Errorf("cross-origin stream default must NOT forward credentials, got %q", auth)
		}
		if !strings.Contains(errOut, "not forwarding") {
			t.Errorf("stream withholding should warn, got %q", errOut)
		}
	})

	t.Run("opt-in forwards on stream", func(t *testing.T) {
		cleanConfigDir(t)
		rec := &credRecorder{}
		data := newXOriginStreamServer(t, rec)
		card := newXOriginCardServer(t, data.URL, "JSONRPC")
		_, errOut, code := runCLI(t, "send", "--stream", "hi", "-u", card.URL, "--transport", "jsonrpc", "--bearer", "tok", "--allow-cross-origin-credentials", "--timeout", "5s")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if auth, _, _ := rec.snapshot(); auth != "Bearer tok" {
			t.Errorf("opt-in must forward credentials on the stream, got %q", auth)
		}
	})
}
