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
	ctx "context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	coremetrics "github.com/apache/yunikorn-core/pkg/metrics"

	"github.com/apache/yunikorn-k8shim/pkg/log"
)

// metricsPort reuses the port of the (now disabled) core web app so the scheduler keeps
// listening on the well-known :9080 (only the path changes, to the Prometheus default
// /metrics).
const metricsPort = 9080

// newMetricsMux serves only /metrics; every other path returns 404. The handler
// mirrors core's getMetrics: Collect() is the only thing that populates the
// yunikorn_runtime_go_* families, so it must run before promhttp serves.
//
// pprof registers on http.DefaultServeMux via its init(); we never serve that mux, so
// do NOT introduce a ListenAndServe(addr, nil) in this binary.
func newMetricsMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		coremetrics.GetRuntimeMetrics().Collect()
		promhttp.Handler().ServeHTTP(w, r)
	})
	return mux
}

// metricsServer is owned by KubernetesShim (started in Run, stopped in Stop) and only
// created when service.exposeMetricsOnly is enabled. When certFile and keyFile are both
// set the server serves HTTPS; otherwise it serves plaintext HTTP.
type metricsServer struct {
	server   *http.Server
	certFile string
	keyFile  string
}

func newMetricsServer(port int, tlsEnabled bool, certFile, keyFile string) *metricsServer {
	m := &metricsServer{
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           newMetricsMux(),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
	// TLS is only used when explicitly enabled and both cert and key paths are provided;
	// anything short of that falls back to plaintext to stay backward compatible.
	if tlsEnabled && certFile != "" && keyFile != "" {
		m.certFile = certFile
		m.keyFile = keyFile
	}
	return m
}

func (m *metricsServer) start() {
	tlsEnabled := m.certFile != "" && m.keyFile != ""
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	log.Log(log.Shim).Info("metrics-only web server started",
		zap.String("addr", m.server.Addr), zap.String("scheme", scheme))
	go func() {
		var err error
		if tlsEnabled {
			err = m.server.ListenAndServeTLS(m.certFile, m.keyFile)
		} else {
			err = m.server.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Log(log.Shim).Error("metrics server error", zap.Error(err))
		}
	}()
}

func (m *metricsServer) stop() {
	c, cancel := ctx.WithTimeout(ctx.Background(), 5*time.Second)
	defer cancel()
	if err := m.server.Shutdown(c); err != nil {
		log.Log(log.Shim).Warn("metrics server shutdown error", zap.Error(err))
	}
}
