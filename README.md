<!--
* Licensed to the Apache Software Foundation (ASF) under one
* or more contributor license agreements.  See the NOTICE file
* distributed with this work for additional information
* regarding copyright ownership.  The ASF licenses this file
* to you under the Apache License, Version 2.0 (the
* "License"); you may not use this file except in compliance
* with the License.  You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
-->

# YuniKorn Scheduler for Kubernetes (yunikorn-k8shim)

[![Build Status](https://github.com/apache/yunikorn-k8shim/actions/workflows/pre-commit.yml/badge.svg)](https://github.com/apache/yunikorn-k8shim/actions/workflows/pre-commit.yml)
[![codecov](https://codecov.io/gh/apache/yunikorn-k8shim/branch/master/graph/badge.svg)](https://codecov.io/gh/apache/yunikorn-k8shim)
[![Go Report Card](https://goreportcard.com/badge/github.com/apache/yunikorn-k8shim)](https://goreportcard.com/report/github.com/apache/yunikorn-k8shim)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Repo Size](https://img.shields.io/github/repo-size/apache/yunikorn-k8shim)](https://img.shields.io/github/repo-size/apache/yunikorn-k8shim)

YuniKorn scheduler shim for kubernetes is a customized k8s scheduler, it can be deployed in a K8s cluster and work as the scheduler.
This project contains the k8s shim layer code for k8s, it depends on `yunikorn-core` which encapsulates all the actual scheduling logic.
By default, it handles all pods scheduling if pod's spec has field `schedulerName: yunikorn`.

For detailed information on how to build the overall scheduler please see the [build document](https://yunikorn.apache.org/docs/next/developer_guide/build) in the `yunikorn-site`.

## REST API authentication and TLS

The scheduler REST API (`:9080`, `/ws/v1/...`) is served by the shared webservice
from `yunikorn-core`; all authentication, authorization and TLS settings are
handled on the core side via `YUNIKORN_`-prefixed environment variables:

- authentication mode (`mtls`, `shared_secret`, `ldap`, `kerberos`,
	`kerberos_ldap`) — `YUNIKORN_AUTH_MODE` and the related `YUNIKORN_AUTH_*` /
	`YUNIKORN_LDAP_*` / `YUNIKORN_KEYTAB_PATH` variables;
- listener TLS — `YUNIKORN_TLS_CERT_FILE`, `YUNIKORN_TLS_KEY_FILE` and
	optionally `YUNIKORN_TLS_CA_FILE` (the CA used to verify client certificates
	in the `mtls` auth mode).

When `yunikorn-web` fronts this API, its proxy authenticates with its own
web -> k8shim leg: it attaches tokens signed with
`YUNIKORN_K8SHIM_AUTH_SHARED_SECRET` (set on the web side), which this
listener validates with `YUNIKORN_AUTH_MODE=shared_secret` and the same value
in `YUNIKORN_AUTH_SHARED_SECRET`.

See the `yunikorn-core` README and the comments in
`yunikorn-core/pkg/webservice/auth.go` for the full variable list and mode
descriptions. When the variables are not set the behaviour does not change —
the API is served over HTTP without authentication.

In the `service.exposeMetricsOnly` mode the shared webservice serves a single
`/metrics` route. It is protected by the same authentication from the
environment, which can be overridden for this endpoint with
`YUNIKORN_METRICS_AUTH_MODE` (an auth mode, or `none` to disable) and
`YUNIKORN_METRICS_AUTH_SHARED_SECRET` — for example to give Prometheus a
dedicated scrape secret while the main API uses another mode. TLS for this
mode is configured with the existing `service.metricsTls*` keys.

## K8s-shim component build
This component build should only be used for development builds.
Prerequisites and build environment setup is described in the above mentioned build document.

### Build binary
The simplest way to get a local binary that can be run on a local Kubernetes environment is: 
```
make build
```
This command will build a binary `yunikorn-scheduler` under `build/dev` dir. This binary is executable on local environment, as long as `kubectl` is properly configured.

**Note**: It may take few minutes to run this command for the first time, because it needs to download all dependencies.
In case you get an error relating to `checksum mismatch`, run `go clean -modcache` and then rerun `make build`.

### Build run

If the local kubernetes environment is up and running you can build and run the binary via: 
```
make run
```
This will build the code, and run the scheduler with verbose logging. 
It will use default configurations for the scheduler and the local Kubernetes configuration.

### Build and run tests
Unit tests for the shim only can be run via:
```
make test
```
Any changes made to the shim code should not cause any existing tests to fail.

### Build image steps
Build docker image can be triggered by running following command.

```
make image
```

You can set `DOCKER_ARCH`, `REGISTRY` and `VERSION` in the commandline to build docker image with a specified arch, tag and version. For example,
```
make image DOCKER_ARCH=amd64 REGISTRY=yunikorn VERSION=latest
```
This command will build an amd64 binary executable with version `latest` and the docker image tag is `yunikorn/yunikorn:scheduler-amd64-latest`. If not specified, `DOCKER_ARCH` defaults to the build host's architecture.  For example, the Makefile will detect if your host's architecture is i386, amd64, arm64v8 or arm32v7 and your image would be tagged with the corresponding host architecture (i.e. `yunikorn:scheduler-arm64v8-latest` if you are on an M1).

You can run following command to retrieve the meta info for a docker image build, such as component revisions, date of the build, etc.

```
docker inspect --format='{{.Config.Labels}}' yunikorn/yunikorn:scheduler-amd64-latest
```

## Design documents
All design documents are located in our [website](http://yunikorn.apache.org/docs/next/design/architecture). 
The core component design documents also contains the design documents for cross component designs.

## How do I contribute code?

See how to contribute code in [our website](http://yunikorn.apache.org/community/how_to_contribute).
