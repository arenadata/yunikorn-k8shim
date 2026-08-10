#!/bin/sh
#
# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

set -e

REALM="EXAMPLE.ORG"

if [ ! -f /var/lib/krb5kdc/principal ]; then
    kdb5_util create -s -r "$REALM" -P masterkey
    # +no_auth_data_required stops the KDC from including a PAC in service
    # tickets: the gokrb5-based SPNEGO service in yunikorn-core cannot decode
    # the minimal (non-AD) PAC that MIT krb5 >= 1.20 issues by default
    kadmin.local -q "addprinc -randkey +no_auth_data_required HTTP/localhost@$REALM"
    kadmin.local -q "ktadd -k /var/keytabs/service.keytab HTTP/localhost@$REALM"
    kadmin.local -q "addprinc -pw admin1pw admin1@$REALM"
    kadmin.local -q "addprinc -pw viewer1pw viewer1@$REALM"
    kadmin.local -q "addprinc -pw alicepw alice@$REALM"
fi

exec krb5kdc -n
