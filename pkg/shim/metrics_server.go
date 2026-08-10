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
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	coremetrics "github.com/apache/yunikorn-core/pkg/metrics"
	"github.com/apache/yunikorn-core/pkg/webservice"

	"github.com/apache/yunikorn-k8shim/pkg/log"
)

// metricsPort reuses the port of the (now disabled) core web app so the scheduler keeps
// listening on the well-known :9080 (only the path changes, to the Prometheus default
// /metrics).
const metricsPort = 9080

// metricsServer is the metrics-only web server used when service.exposeMetricsOnly is
// enabled: the shared core webservice server with a single /metrics route, so every
// other path ceases to exist. Authentication comes from the webservice configuration;
// the YUNIKORN_METRICS_AUTH_* variables override it for this endpoint (`none`
// disables it). TLS comes from the service.metricsTls* configmap settings: when
// certFile and keyFile are both set the server serves HTTPS.
type metricsServer struct {
	server *webservice.WebServer
}

// metricsRoute mirrors core's getMetrics: Collect() is the only thing that populates
// the yunikorn_runtime_go_* families, so it must run before promhttp serves.
func metricsRoute() webservice.Route {
	return webservice.Route{
		Name:    webservice.RouteNameMetrics,
		Method:  http.MethodGet,
		Pattern: "/metrics",
		HandlerFunc: func(w http.ResponseWriter, r *http.Request) {
			coremetrics.GetRuntimeMetrics().Collect()
			promhttp.Handler().ServeHTTP(w, r)
		},
	}
}

func newMetricsServer(port int, tlsEnabled bool, certFile, keyFile string) *metricsServer {
	cfg, err := webservice.LoadConfig()
	if err != nil {
		log.Log(log.Shim).Error("unable to load webservice configuration", zap.Error(err))
	}
	// apply the /metrics authentication override, if any
	cfg = cfg.MetricsConfig()
	if cfg != nil {
		// the metrics listener TLS is driven by the configmap, not the environment;
		// TLS is only used when explicitly enabled and both cert and key paths are
		// provided, anything short of that falls back to plaintext to stay backward
		// compatible
		cfg.TLS = nil
		if tlsEnabled && certFile != "" && keyFile != "" {
			cfg.TLS = &webservice.TLSConfig{CertFile: certFile, KeyFile: keyFile}
		}
	}
	return &metricsServer{
		server: webservice.NewWebServer(cfg, fmt.Sprintf(":%d", port), []webservice.Route{metricsRoute()}),
	}
}

func (m *metricsServer) start() {
	m.server.Start()
}

func (m *metricsServer) stop() {
	if err := m.server.Stop(); err != nil {
		log.Log(log.Shim).Warn("metrics server shutdown error", zap.Error(err))
	}
}
