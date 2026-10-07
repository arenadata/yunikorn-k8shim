/*
 Licensed to the Apache Software Foundation (ASF) under one
 or more contributor license agreements.  See the NOTICE file
 distributed with this work for additional information
 regarding copyright ownership.  The ASF licenses this file
 to you under the Apache License, Version 2.0 (the
 "License"); you may not use this file except in compliance
 with the License.  You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package shim

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestMetricsServerNoAuth(t *testing.T) {
	m := newMetricsServer(metricsPort, false, "", "")

	rec := httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// everything else does not exist in metrics-only mode
	rec = httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws/v1/clusters", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMetricsServerSharedSecretAuth(t *testing.T) {
	secret := "metrics-secret"
	t.Setenv("YUNIKORN_AUTH_MODE", "shared_secret")
	t.Setenv("YUNIKORN_AUTH_SHARED_SECRET", secret)

	m := newMetricsServer(metricsPort, false, "", "")

	// no token: rejected
	rec := httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// token signed with a wrong secret: rejected
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Token "+signSharedSecretToken(t, "wrong", "prometheus"))
	rec = httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// valid token: served
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Token "+signSharedSecretToken(t, secret, "prometheus"))
	rec = httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMetricsServerAuthOverride(t *testing.T) {
	t.Setenv("YUNIKORN_AUTH_MODE", "shared_secret")
	t.Setenv("YUNIKORN_AUTH_SHARED_SECRET", "main-secret")

	// override `none`: /metrics is served without authentication
	t.Setenv("YUNIKORN_METRICS_AUTH_MODE", "none")
	m := newMetricsServer(metricsPort, false, "", "")
	rec := httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// override with a dedicated scrape secret: the main secret is not accepted
	t.Setenv("YUNIKORN_METRICS_AUTH_MODE", "shared_secret")
	t.Setenv("YUNIKORN_METRICS_AUTH_SHARED_SECRET", "scrape-secret")
	m = newMetricsServer(metricsPort, false, "", "")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Token "+signSharedSecretToken(t, "main-secret", "prometheus"))
	rec = httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Token "+signSharedSecretToken(t, "scrape-secret", "prometheus"))
	rec = httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMetricsServerMTLSAuth(t *testing.T) {
	t.Setenv("YUNIKORN_AUTH_MODE", "mtls")
	// the mtls mode is only valid with a CA to verify client certificates against
	t.Setenv("YUNIKORN_TLS_CA_FILE", filepath.Join(t.TempDir(), "ca.crt"))

	m := newMetricsServer(metricsPort, false, "", "")
	assert.Assert(t, m != nil, "a valid mtls configuration must start the metrics server")

	// a request without a verified client certificate is rejected
	rec := httptest.NewRecorder()
	m.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestMetricsServerInvalidAuthConfig guards against failing open: an auth mode
// missing its required settings must keep the listener closed rather than serve
// /metrics without authentication.
func TestMetricsServerInvalidAuthConfig(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"mtls without a CA":            {"YUNIKORN_AUTH_MODE": "mtls"},
		"shared_secret without secret": {"YUNIKORN_AUTH_MODE": "shared_secret"},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			assert.Assert(t, newMetricsServer(metricsPort, false, "", "") == nil,
				"an invalid auth configuration must not start the metrics server")
		})
	}
}

func signSharedSecretToken(t *testing.T, secret, user string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"user": user, "exp": time.Now().Add(time.Hour).Unix()})
	assert.NilError(t, err, "failed to marshal token payload")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return base64.StdEncoding.EncodeToString(payload) + "." + hex.EncodeToString(mac.Sum(nil))
}
