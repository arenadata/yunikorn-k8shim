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

# /metrics authentication e2e tests

End to end tests for the authentication of the metrics-only listener the shim
serves with `service.exposeMetricsOnly` enabled.

The server under test is built exactly like `pkg/shim/metrics_server.go`
builds it: the shared core webservice (`webservice.NewWebServer`) configured
from `YUNIKORN_*` environment variables with the `YUNIKORN_METRICS_AUTH_*`
override applied, listener TLS driven solely by the `service.metricsTls*`
configmap settings, and a single `/metrics` route serving real Prometheus
output. This module is separate from the kind-based e2e suites: it needs
Docker (for [testcontainers](https://golang.testcontainers.org/)) but no
Kubernetes cluster.

External dependencies run in testcontainers:

- **OpenLDAP** (`osixia/openldap`), seeded from `testdata/ldap/seed.ldif`
- **MIT Kerberos KDC**, built from `testdata/kdc/`

## Running

Requires Docker.

```sh
make e2e_metrics_auth_test
# or
cd test/e2e/metrics_auth && go test ./...
```

The LDAP and KDC containers start once per `go test` run, on first use.

## Coverage

| Variant | Tests |
|---|---|
| disabled | open Prometheus payload; every other path is 404 |
| inherited `shared_secret` | missing/foreign/expired/valid tokens against the main shim secret |
| `YUNIKORN_METRICS_AUTH_*` overrides | `none` disables auth for metrics only; a dedicated metrics secret replaces the main one |
| `ldap` | scraper BasicAuth against the directory; service and admin roles reach `/metrics`, viewer is 403, wrong password is 401 |
| `kerberos` | SPNEGO-protected scrape against the shim keytab |
| TLS | listener TLS comes from the `service.metricsTls*` settings; `YUNIKORN_TLS_*` is ignored for this listener |
| `mtls` limitation | documented: the metrics listener has no configurable client CA pool, so mtls rejects every scraper |

## Deployment notes discovered by these tests

- **mtls cannot protect the metrics-only listener**: the metrics server drops
  the environment TLS section (including `YUNIKORN_TLS_CA_FILE`) and the
  `service.metricsTls*` settings carry only a certificate and key, so
  `YUNIKORN_METRICS_AUTH_MODE=mtls` has no CA pool to verify scrapers against
  and rejects every client during the handshake.
- **MIT krb5 ≥ 1.20 KDC**: service tickets carry a minimal PAC by default and
  the SPNEGO service cannot decode it. Create the HTTP service principal with
  `+no_auth_data_required` (see `testdata/kdc/entrypoint.sh`), or the
  `kerberos` mode rejects every ticket. Active Directory KDCs are not
  affected.
- **LDAP ACLs**: group membership is looked up on the connection re-bound as
  the authenticated user, so users need read access to the user and group
  subtrees (Active Directory default). For OpenLDAP an explicit
  `to * by users read` ACL is required (see `testdata/ldap/acl.ldif`).
