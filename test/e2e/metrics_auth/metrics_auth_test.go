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

// End to end tests for the authentication of the yunikorn-k8shim metrics-only
// listener (service.exposeMetricsOnly). The server is built exactly like the
// shim builds it (pkg/shim/metrics_server.go): the core webservice with the
// YUNIKORN_METRICS_AUTH_* override applied, the listener TLS driven solely by
// the service.metricsTls* configmap settings, and a single /metrics route
// serving real Prometheus output. LDAP and Kerberos are provided by
// testcontainers (OpenLDAP and a MIT KDC).
package metricsauth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	coremetrics "github.com/apache/yunikorn-core/pkg/metrics"
	"github.com/apache/yunikorn-core/pkg/webservice"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gotest.tools/v3/assert"
)

// startShimMetricsServer mirrors the k8shim newMetricsServer: tlsEnabled,
// certFile and keyFile play the role of the service.metricsTls* configmap
// settings; any listener TLS coming from the environment is discarded.
func startShimMetricsServer(t *testing.T, env map[string]string, tlsEnabled bool, certFile, keyFile string) string {
	t.Helper()
	cfg := loadConfig(t, env).MetricsConfig()
	cfg.TLS = nil
	if tlsEnabled && certFile != "" && keyFile != "" {
		cfg.TLS = &webservice.TLSConfig{CertFile: certFile, KeyFile: keyFile}
	}
	metricsRoute := webservice.Route{
		Name:    webservice.RouteNameMetrics,
		Method:  http.MethodGet,
		Pattern: "/metrics",
		HandlerFunc: func(w http.ResponseWriter, r *http.Request) {
			coremetrics.GetRuntimeMetrics().Collect()
			promhttp.Handler().ServeHTTP(w, r)
		},
	}
	return startServer(t, cfg, []webservice.Route{metricsRoute})
}

// TestShimMetricsAuthDisabled: without authentication the endpoint serves the
// Prometheus payload openly, and /metrics is the only path that exists.
func TestShimMetricsAuthDisabled(t *testing.T) {
	base := startShimMetricsServer(t, nil, false, "", "")

	resp := doGet(t, http.DefaultClient, base+"/metrics")
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assertPrometheusPayload(t, resp)

	// the security mode's point: every other endpoint ceases to exist
	for _, path := range []string{clusterPath, schedulerPath, "/"} {
		resp = doGet(t, http.DefaultClient, base+path)
		assert.Equal(t, resp.StatusCode, http.StatusNotFound, path)
	}
}

// TestShimMetricsSharedSecret: the metrics listener inherits the main
// shared_secret authentication of the shim.
func TestShimMetricsSharedSecret(t *testing.T) {
	const mainSecret = "main-secret"
	base := startShimMetricsServer(t, map[string]string{
		"YUNIKORN_AUTH_SHARED_SECRET": mainSecret,
	}, false, "", "")

	tests := []struct {
		name string
		opts []func(*http.Request)
		want int
	}{
		{"no token", nil, http.StatusUnauthorized},
		{"foreign secret", []func(*http.Request){withToken(signToken("other", "prometheus", nil, time.Hour))}, http.StatusUnauthorized},
		{"expired token", []func(*http.Request){withToken(signToken(mainSecret, "prometheus", nil, -time.Minute))}, http.StatusUnauthorized},
		{"valid token", []func(*http.Request){withToken(signToken(mainSecret, "prometheus", nil, time.Hour))}, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doGet(t, http.DefaultClient, base+"/metrics", tc.opts...)
			assert.Equal(t, resp.StatusCode, tc.want)
			if tc.want == http.StatusOK {
				assertPrometheusPayload(t, resp)
			}
		})
	}
}

// TestShimMetricsAuthOverrides: YUNIKORN_METRICS_AUTH_* overrides the main
// authentication for this endpoint only — `none` disables it, a dedicated
// secret replaces the main one.
func TestShimMetricsAuthOverrides(t *testing.T) {
	const mainSecret = "main-secret"

	t.Run("none disables metrics authentication", func(t *testing.T) {
		base := startShimMetricsServer(t, map[string]string{
			"YUNIKORN_AUTH_SHARED_SECRET": mainSecret,
			"YUNIKORN_METRICS_AUTH_MODE":  "none",
		}, false, "", "")
		resp := doGet(t, http.DefaultClient, base+"/metrics")
		assert.Equal(t, resp.StatusCode, http.StatusOK)
		assertPrometheusPayload(t, resp)
	})

	t.Run("dedicated metrics secret", func(t *testing.T) {
		base := startShimMetricsServer(t, map[string]string{
			"YUNIKORN_AUTH_SHARED_SECRET":         mainSecret,
			"YUNIKORN_METRICS_AUTH_SHARED_SECRET": "metrics-secret",
		}, false, "", "")
		resp := doGet(t, http.DefaultClient, base+"/metrics",
			withToken(signToken(mainSecret, "prometheus", nil, time.Hour)))
		assert.Equal(t, resp.StatusCode, http.StatusUnauthorized, "main secret must not open metrics")
		resp = doGet(t, http.DefaultClient, base+"/metrics",
			withToken(signToken("metrics-secret", "prometheus", nil, time.Hour)))
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})
}

// TestShimMetricsLDAP: METRICS_AUTH_MODE=ldap authenticates the scraper via
// BasicAuth against the directory and authorizes it through the role groups —
// the service role reaches /metrics, the viewer role does not.
func TestShimMetricsLDAP(t *testing.T) {
	l := startLDAPContainer(t)
	base := startShimMetricsServer(t, mergeEnv(ldapEnv(l), map[string]string{
		"YUNIKORN_METRICS_AUTH_MODE": "ldap",
		// in the ldap mode the shared secret signs the YK_AUTH cookie
		"YUNIKORN_METRICS_AUTH_SHARED_SECRET": "metrics-cookie-secret",
		"YUNIKORN_LDAP_ADMIN_GROUPS":          "yk-admins",
		"YUNIKORN_LDAP_VIEWER_GROUPS":         "yk-viewers",
		"YUNIKORN_LDAP_SERVICE_GROUPS":        "yk-service",
	}), false, "", "")

	t.Run("service role scrapes metrics", func(t *testing.T) {
		resp := doGet(t, http.DefaultClient, base+"/metrics", withBasic("svc1", "svc1pw"))
		assert.Equal(t, resp.StatusCode, http.StatusOK)
		assertPrometheusPayload(t, resp)
	})

	t.Run("admin role is allowed", func(t *testing.T) {
		resp := doGet(t, http.DefaultClient, base+"/metrics", withBasic("admin1", "admin1pw"))
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})

	t.Run("viewer role is forbidden", func(t *testing.T) {
		resp := doGet(t, http.DefaultClient, base+"/metrics", withBasic("viewer1", "viewer1pw"))
		assert.Equal(t, resp.StatusCode, http.StatusForbidden)
	})

	t.Run("wrong password rejected", func(t *testing.T) {
		resp := doGet(t, http.DefaultClient, base+"/metrics", withBasic("svc1", "wrong"))
		assert.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	})
}

// TestShimMetricsKerberos: METRICS_AUTH_MODE=kerberos protects the endpoint
// with SPNEGO against the shim keytab.
func TestShimMetricsKerberos(t *testing.T) {
	kdc := startKDCContainer(t)
	base := startShimMetricsServer(t, map[string]string{
		"YUNIKORN_METRICS_AUTH_MODE": "kerberos",
		"YUNIKORN_KEYTAB_PATH":       kdc.KeytabFile,
	}, false, "", "")

	t.Run("no ticket challenged", func(t *testing.T) {
		resp := doGet(t, http.DefaultClient, base+"/metrics")
		assert.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	})

	t.Run("valid ticket scrapes metrics", func(t *testing.T) {
		resp := spnegoGet(t, spnegoClient(t, kdc, "alice", "alicepw"), base+"/metrics")
		assert.Equal(t, resp.StatusCode, http.StatusOK)
		assertPrometheusPayload(t, resp)
	})
}

// TestShimMetricsSharedSecretOverTLS: the secured scrape setup — listener TLS
// from the service.metricsTls* settings combined with token authentication.
// Listener TLS configured through the environment is ignored by this server.
func TestShimMetricsSharedSecretOverTLS(t *testing.T) {
	const mainSecret = "main-secret"
	pki := newPKI(t, "metrics-ca")
	cert, key := pki.issue(t, "metrics-server")

	t.Run("token over configmap TLS", func(t *testing.T) {
		base := startShimMetricsServer(t, map[string]string{
			"YUNIKORN_AUTH_SHARED_SECRET": mainSecret,
		}, true, cert, key)
		assert.Assert(t, strings.HasPrefix(base, "https://"))

		client := tlsClient(t, pki.CAFile, "", "")
		resp := doGet(t, client, base+"/metrics")
		assert.Equal(t, resp.StatusCode, http.StatusUnauthorized)
		resp = doGet(t, client, base+"/metrics",
			withToken(signToken(mainSecret, "prometheus", nil, time.Hour)))
		assert.Equal(t, resp.StatusCode, http.StatusOK)
		assertPrometheusPayload(t, resp)
	})

	t.Run("environment TLS is ignored", func(t *testing.T) {
		base := startShimMetricsServer(t, map[string]string{
			"YUNIKORN_AUTH_SHARED_SECRET": mainSecret,
			"YUNIKORN_TLS_CERT_FILE":      cert,
			"YUNIKORN_TLS_KEY_FILE":       key,
		}, false, "", "")
		assert.Assert(t, strings.HasPrefix(base, "http://"),
			"the metrics listener TLS comes from the configmap, not the environment")
		resp := doGet(t, http.DefaultClient, base+"/metrics",
			withToken(signToken(mainSecret, "prometheus", nil, time.Hour)))
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})
}

// TestShimMetricsMTLSHasNoClientCA documents a limitation: the metrics
// listener drops the environment TLS section (including its CA file) and the
// service.metricsTls* settings carry no CA, so METRICS_AUTH_MODE=mtls has no
// pool to verify client certificates against — every client is rejected
// during the handshake.
func TestShimMetricsMTLSHasNoClientCA(t *testing.T) {
	pki := newPKI(t, "metrics-ca")
	serverCert, serverKey := pki.issue(t, "metrics-server")
	clientCert, clientKey := pki.issue(t, "scraper")

	base := startShimMetricsServer(t, map[string]string{
		"YUNIKORN_METRICS_AUTH_MODE": "mtls",
		// the CA configured here is discarded by the metrics listener
		"YUNIKORN_TLS_CA_FILE": pki.CAFile,
	}, true, serverCert, serverKey)

	_, err := tryGet(tlsClient(t, pki.CAFile, clientCert, clientKey), base+"/metrics")
	assert.Assert(t, err != nil,
		"mtls on the metrics listener cannot verify any client certificate: no CA pool is configurable")
}
