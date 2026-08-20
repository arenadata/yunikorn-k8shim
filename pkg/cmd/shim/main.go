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

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/julienschmidt/httprouter"
	"go.uber.org/zap"

	"github.com/apache/yunikorn-core/pkg/webservice"

	"github.com/apache/yunikorn-k8shim/pkg/client"
	"github.com/apache/yunikorn-k8shim/pkg/common/constants"
	"github.com/apache/yunikorn-k8shim/pkg/plugin/predicates"

	corelog "github.com/apache/yunikorn-core/pkg/log"

	"github.com/apache/yunikorn-core/pkg/entrypoint"
	"github.com/apache/yunikorn-k8shim/pkg/conf"
	"github.com/apache/yunikorn-k8shim/pkg/log"
	"github.com/apache/yunikorn-k8shim/pkg/shim"
)

const socketPath = "/tmp/k8shim.sock"

func main() {
	log.Log(log.Shim).Info(conf.GetBuildInfoString())

	predicates.EnableOptionalKubernetesFeatureGates()

	configMaps, err := client.LoadBootstrapConfigMaps()
	if err != nil {
		log.Log(log.Shim).Fatal("Unable to bootstrap configuration", zap.Error(err))
	}

	err = conf.UpdateConfigMaps(configMaps, true)
	if err != nil {
		log.Log(log.Shim).Fatal("Unable to load initial configmaps", zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Log(log.Shim).Info("Starting scheduler", zap.String("name", constants.SchedulerName))

	var serviceContext *entrypoint.ServiceContext
	if conf.GetSchedulerConf().ExposeMetricsOnly {
		// Security mode: start core services with the full web app DISABLED. The core
		// web app would expose the entire REST/UI/debug API (clusters, config, pprof,
		// state dump, event streams, ...) on :9080. Instead the shim serves ONLY the
		// metrics endpoint (see KubernetesShim.metricsServer); every other endpoint
		// ceases to exist. We initialize the core logger explicitly because, unlike
		// StartAllServicesWithLogger, StartAllServicesWithParams does not do it.
		log.Log(log.Shim).Warn("exposeMetricsOnly enabled: serving only /metrics on :9080; " +
			"all other REST endpoints are disabled, including /ws/v1/validate-conf - if the admission " +
			"controller is deployed with config validation, it will fail open (admit configmaps unvalidated)")
		corelog.InitializeLogger(log.RootLogger(), log.GetZapConfigs())
		serviceContext = entrypoint.StartAllServicesWithParams(false, false)

		go func() {
			if err = localServer(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Log(log.Shim).Fatal("Unable to start server", zap.Error(err))
			}
		}()
	} else {
		// Default: start all core services including the full web app on :9080.
		serviceContext = entrypoint.StartAllServicesWithLogger(log.RootLogger(), log.GetZapConfigs())
	}

	if serviceContext.RMProxy != nil {
		ss := shim.NewShimScheduler(serviceContext.RMProxy, conf.GetSchedulerConf(), configMaps)
		if err := ss.Run(); err != nil {
			log.Log(log.Shim).Fatal("Unable to start scheduler", zap.Error(err))
		}

		<-ctx.Done()
		log.Log(log.Shim).Info("Shutdown signal received, exiting...")
		ss.Stop()
	}
}

func localServer(ctx context.Context) error {
	router := httprouter.New()
	for _, rt := range webservice.Routes() {
		router.Handler(rt.Method, rt.Pattern, rt.HandlerFunc)
	}

	socketFilePath := os.Getenv("YUNIKORN_K8SHIM_SOCKET_PATH")
	if len(socketFilePath) == 0 {
		socketFilePath = socketPath
	}

	//nolint:gosec
	_ = os.Remove(socketFilePath)

	listener, err := net.Listen("unix", socketFilePath)
	if err != nil {
		return fmt.Errorf("unable to listen on socket %s: %w", socketFilePath, err)
	}
	defer func() { _ = listener.Close() }()

	//nolint:gosec
	if err = os.Chmod(socketFilePath, 0660); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	srv := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Log(log.Shim).Error("Error shutting down metrics server", zap.Error(err))
		}
		_ = os.Remove(socketFilePath)
	}()

	return srv.Serve(listener)
}
