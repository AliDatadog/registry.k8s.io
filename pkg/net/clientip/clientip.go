/*
Copyright 2022 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Get gets the client IP for an http.Request
//
// NOTE: currently only two scenarios are supported:
// 1. no loadbalancer, local testing
// 2. behind Google Cloud LoadBalancer (as in cloudrun)
//
// Note that in particular we do not support hitting the CloudRun endpoint
// directly (though we could easily do so here). Cloud Armor is on the GCLB,
// so directly accessing the CloudRun endpoint would bypass that.
//
// At this time we have no need to complicate it further.
func Get(r *http.Request) (netip.Addr, error) {
	rawXFwdFor := r.Header.Get("X-Forwarded-For")
	// clearly we are not in cloud if this header is not set, we can use
	// r.RemoteAddr in that case to support local testing
	// Go http server will always set this value for us
	if rawXFwdFor == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return netip.Addr{}, err
		}
		return netip.ParseAddr(host)
	}
	// assume we are in aws lambda, get <client-ip> from load balancer header
	// local tests with direct connection to the server can also fake this
	// header which is fine + useful
	//
	// we want the contents between the second to last comma and the last comma
	// or if only one comma between the start of the string and the last comma
	keys := strings.FieldsFunc(rawXFwdFor, func(r rune) bool {
		return r == ',' || r == ' '
	})
	return netip.ParseAddr(keys[0])
}
