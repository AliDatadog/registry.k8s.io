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

package app

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"k8s.io/klog/v2"

	ddlambda "github.com/DataDog/datadog-lambda-go"
	"k8s.io/registry.k8s.io/pkg/net/clientip"
	"k8s.io/registry.k8s.io/pkg/net/cloudcidrs"
)

var regionMapper = cloudcidrs.NewIPMapper()

// statusRecorder wraps http.ResponseWriter to capture the final status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.statusCode = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

type Registry struct {
	Endpoint  string
	Namespace string
}

type RegistryConfig struct {
	UpstreamUsGAR   Registry
	UpstreamEuGAR   Registry
	UpstreamAsiaGAR Registry
	UpstreamACR     Registry
	UpstreamCDN     Registry
	InfoURL         string
	PrivacyURL      string
}

// MakeHandler returns the root archeio HTTP handler
//
// upstream registry should be the url to the primary registry
// archeio is fronting.
//
// Exact behavior should be documented in docs/request-handling.md
func MakeHandler(rc RegistryConfig) http.Handler {
	blobs := newCachedBlobChecker()
	doV2 := makeV2Handler(rc, blobs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		klog.Infof("Handling request: %s %s", r.Method, r.URL.Path)

		// Prepare metrics recording
		recorder := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		cloud := "unknown"
		region := "unknown"
		// Best-effort resolve client cloud/region for tagging
		if ip, err := clientip.Get(r); err == nil {
			if info, ok := regionMapper.GetIP(ip); ok {
				if info.Cloud != "" {
					cloud = info.Cloud
				}
				if info.Region != "" {
					region = info.Region
				}
			}
		}
		defer func() {
			status := recorder.statusCode
			if status == 0 {
				status = http.StatusOK
			}
			tags := []string{
				fmt.Sprintf("method:%s", r.Method),
				fmt.Sprintf("path:%s", r.URL.Path),
				fmt.Sprintf("status_code:%d", status),
				fmt.Sprintf("cloud:%s", cloud),
				fmt.Sprintf("region:%s", region),
			}
			// Emit Lambda metrics via embedded logs for the Datadog Lambda extension
			ddlambda.Metric("archeio.http.request", 1, tags...)
			ddlambda.Distribution("archeio.http.latency_ms", float64(time.Since(start).Milliseconds()), tags...)
		}()
		// only allow GET, HEAD
		// this is all a client needs to pull images
		// we do *not* support mutation
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(recorder, "Only GET and HEAD are allowed.", http.StatusMethodNotAllowed)
			return
		}
		// all valid registry requests should be at /v2/
		// v1 API is super old and not supported by GCR anymore.
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/v2"):
			doV2(recorder, r)
		case path == "/":
			http.Redirect(recorder, r, rc.InfoURL, http.StatusTemporaryRedirect)
		case strings.HasPrefix(path, "/privacy"):
			http.Redirect(recorder, r, rc.PrivacyURL, http.StatusTemporaryRedirect)
		default:
			klog.V(2).InfoS("unknown request", "path", path)
			http.NotFound(recorder, r)
		}
	})
}

func makeV2Handler(rc RegistryConfig, blobs blobChecker) func(w http.ResponseWriter, r *http.Request) {
	// matches blob and manifests requests, captures the requested blob hash and the manifest's reference
	// https://github.com/opencontainers/distribution-spec/blob/main/spec.md#pull
	// Blobs are at `/v2/<name>/blobs/<digest>`
	// Manifests are at `/v2/<name>/manifests/<reference>`
	reBlobOrManifest := regexp.MustCompile("^/v2/.*/(blobs|manifests)/.*$")
	// capture these in a http handler lambda
	return func(w http.ResponseWriter, r *http.Request) {
		rPath := r.URL.Path
		// check the client IP and determine the best backend
		// It is also crucial for oauth2 token validation
		clientIP, err := clientip.Get(r)
		if err != nil {
			// this should not happen
			klog.ErrorS(err, "failed to get client IP")
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Stay in the same cloud provider
		ipInfo, _ := regionMapper.GetIP(clientIP)

		// when the client attempts to probe the API for auth
		// For Azure, we redirect to the upstream registry to handle the auth token
		// as ACR requires this token.
		// For others, we serve 200 OK as we'll redirect to s3 or cloudfront
		if rPath == "/v2/" || rPath == "/v2" {
			if ipInfo.Cloud == cloudcidrs.AZ {
				redirectURL := redirectUpstream(rc, rPath, ipInfo, rc.UpstreamACR)
				klog.V(2).Infof("redirecting oauth request to %s", redirectURL)
				http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
				return
			}
			klog.V(2).InfoS("serving 200 OK for /v2/ check", "path", rPath)
			// NOTE: OCI does not require this, but the docker v2 spec include it, and GCR sets this
			// Docker distribution v2 clients may fallback to an older version if this is not set.
			w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
			w.WriteHeader(http.StatusOK)
			return
		}
		// we don't support the non-standard _catalog API
		// https://github.com/kubernetes/registry.k8s.io/issues/162
		if rPath == "/v2/_catalog" {
			http.Error(w, "_catalog is not supported", http.StatusNotFound)
			return
		}

		// If the request is not a blob or manifest request, forward it to an upstream registry (not cdn)
		matches := reBlobOrManifest.MatchString(rPath)
		if !matches {
			klog.Infof("not a blob or manifest request: %v", rPath)
			redirectURL := redirectUpstream(rc, rPath, ipInfo, rc.UpstreamUsGAR)
			klog.Infof("redirecting manifest request to %s", redirectURL)
			http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}

		// If the request is a blob or manifest request, forward it to a matching registry OR CDN
		if ipInfo.Cloud != cloudcidrs.AWS {
			klog.Infof("cloud not aws: %v", ipInfo)
			redirectURL := redirectUpstream(rc, rPath, ipInfo, rc.UpstreamCDN)
			klog.Infof("redirecting blob or manifest request to %s", redirectURL)
			http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}

		// If the request is from AWS, check if the blob is available in our AWS layer storage for the region
		// check if blob is available in our AWS layer storage for the region
		region := ipInfo.Region
		bucketURL := awsRegionToHostURL(region, rc.UpstreamCDN.Endpoint)
		// this matches GCR's GCS layout, which we will use for other buckets
		blobURL, err := url.JoinPath(bucketURL, rPath)
		if err != nil {
			klog.ErrorS(err, "failed to join URL path", "path", rPath, "bucketURL", bucketURL)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		klog.Infof("Checking if blob exists: %s", blobURL)
		if blobs.BlobExists(blobURL) {
			// blob known to be available in AWS, redirect client there
			klog.V(2).Infof("AWS: redirecting blob request to %s", blobURL)
			http.Redirect(w, r, blobURL, http.StatusTemporaryRedirect)
			return
		}

		// fall back to redirect to cdn
		redirectURL := redirectUpstream(rc, rPath, ipInfo, rc.UpstreamCDN)
		klog.InfoS("redirecting blob request to upstream registry", "path", rPath, "redirect", redirectURL)
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
	}
}

func redirectUpstream(rc RegistryConfig, originalPath string, ipInfo cloudcidrs.IPInfo, defaultRegistry Registry) string {
	reg := defaultRegistry

	// Determine endpoint based on provider and region
	switch ipInfo.Cloud {
	case cloudcidrs.AZ:
		klog.Infof("Redirecting to Azure endpoint")
		reg = rc.UpstreamACR
	case cloudcidrs.GCP:
		if strings.HasPrefix(ipInfo.Region, "europe") ||
			strings.HasPrefix(ipInfo.Region, "me-") ||
			strings.HasPrefix(ipInfo.Region, "africa") {
			klog.Infof("Redirecting to GCP EU endpoint")
			reg = rc.UpstreamEuGAR
		}
		if strings.HasPrefix(ipInfo.Region, "asia-") ||
			strings.HasPrefix(ipInfo.Region, "australia-") {
			klog.Infof("Redirecting to GCP Asia endpoint")
			reg = rc.UpstreamAsiaGAR
		}
	default:
		klog.Infof("Redirecting to default endpoint %v", reg)
	}

	// Build the redirect URL
	redirectUrl, err := url.JoinPath(reg.Endpoint, "/v2/", reg.Namespace, strings.TrimPrefix(originalPath, "/v2"))
	if err != nil {
		panic("failed to join URL path: " + err.Error())
	}
	return redirectUrl
}
