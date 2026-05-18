package vivero

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestValidateServeAddrRejectsNonLoopbackBindByDefault(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7777", "[::]:7777", ":7777"} {
		err := validateServeAddr(addr)
		if err == nil {
			t.Fatalf("expected %s to be rejected", addr)
		}
		if !strings.Contains(err.Error(), "local-only") {
			t.Fatalf("error should explain local-only control plane: %v", err)
		}
	}
}

func TestValidateServeAddrAllowsExplicitRemoteOverride(t *testing.T) {
	t.Setenv("VIVERO_ALLOW_REMOTE_CONTROL", "1")
	if err := validateServeAddr("0.0.0.0:7777"); err != nil {
		t.Fatalf("explicit remote override should allow non-loopback bind: %v", err)
	}
}

func TestValidateServeAddrAllowsLoopbackAndUnixSockets(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1:7777", "localhost:7777", "[::1]:7777", "unix:/tmp/vivero.sock"} {
		if err := validateServeAddr(addr); err != nil {
			t.Fatalf("%s should be accepted: %v", addr, err)
		}
	}
}

func TestServeRemoteOverrideNameIsDocumented(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "VIVERO_ALLOW_REMOTE_CONTROL") {
		t.Fatal("README should document the explicit remote-control override")
	}
}

func TestCreatePreviewRejectsInvalidTimeout(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodPost, "/previews", bytes.NewBufferString(`{"project":"demo","timeout":"not-a-duration"}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	a.controlPlaneHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "timeout must be a duration") {
		t.Fatalf("response should explain timeout error, body = %s", body)
	}
}
