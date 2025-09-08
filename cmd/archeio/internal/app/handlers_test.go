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
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"k8s.io/registry.k8s.io/pkg/net/cloudcidrs"
)

func TestMakeHandler(t *testing.T) {
	registryConfig := RegistryConfig{
		UpstreamUsGAR:   Registry{Endpoint: "https://gcr.io", Namespace: "datadoghq"},
		UpstreamEuGAR:   Registry{Endpoint: "https://eu.gcr.io", Namespace: "datadoghq"},
		UpstreamAsiaGAR: Registry{Endpoint: "https://asia.gcr.io", Namespace: "datadoghq"},
		UpstreamACR:     Registry{Endpoint: "https://datadoghq.azurecr.io"},
		UpstreamCDN:     Registry{Endpoint: "https://d1u5qnb27isorz.cloudfront.net"},
		InfoURL:         "https://docs.datadoghq.com/",
		PrivacyURL:      "https://www.datadoghq.com/legal/privacy/",
	}

	handler := MakeHandler(registryConfig)
	testCases := []struct {
		Name           string
		Request        *http.Request
		ExpectedStatus int
		ExpectedURL    string
	}{
		{
			Name:           "/",
			Request:        httptest.NewRequest("GET", "http://localhost:8080/", nil),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    registryConfig.InfoURL,
		},
		{
			Name:           "/privacy",
			Request:        httptest.NewRequest("GET", "http://localhost:8080/privacy", nil),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    registryConfig.PrivacyURL,
		},
		{
			Name:           "/v3/",
			Request:        httptest.NewRequest("GET", "http://localhost:8080/v3/", nil),
			ExpectedStatus: http.StatusNotFound,
		},
		{
			Name:           "/v2/",
			Request:        httptest.NewRequest("GET", "http://localhost:8080/v2/", nil),
			ExpectedStatus: http.StatusOK,
		},
		{
			Name:           "/v2",
			Request:        httptest.NewRequest("GET", "http://localhost:8080/v2", nil),
			ExpectedStatus: http.StatusOK,
		},
		{
			Name:           "/v2",
			Request:        httptest.NewRequest("HEAD", "http://localhost:8080/v2", nil),
			ExpectedStatus: http.StatusOK,
		},
		{
			Name:           "/v2/",
			Request:        httptest.NewRequest("POST", "http://localhost:8080/v2/", nil),
			ExpectedStatus: http.StatusMethodNotAllowed,
		},
		{
			Name:           "/v2/pause/manifests/latest",
			Request:        httptest.NewRequest("GET", "http://localhost:8080/v2/pause/manifests/latest", nil),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://d1u5qnb27isorz.cloudfront.net/v2/pause/manifests/latest",
		},
		{
			Name:           "/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
			Request:        httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://d1u5qnb27isorz.cloudfront.net/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
		},
	}
	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, tc.Request)
			response := recorder.Result()
			if response == nil {
				t.Fatalf("nil response")
			}
			if response.StatusCode != tc.ExpectedStatus {
				t.Fatalf(
					"expected status: %v, but got status: %v",
					http.StatusText(tc.ExpectedStatus),
					http.StatusText(response.StatusCode),
				)
			}
			location, err := response.Location()
			if err != nil {
				if !errors.Is(err, http.ErrNoLocation) {
					t.Fatalf("failed to get response location with error: %v", err)
				} else if tc.ExpectedURL != "" {
					t.Fatalf("expected url: %q but no location was available", tc.ExpectedURL)
				}
			} else if location.String() != tc.ExpectedURL {
				t.Fatalf(
					"expected url: %q, but got: %q",
					tc.ExpectedURL,
					location,
				)
			}
		})
	}
}

type fakeBlobsChecker struct {
	knownURLs map[string]bool
}

func (f *fakeBlobsChecker) BlobExists(blobURL string) bool {
	return f.knownURLs[blobURL]
}

func TestMakeV2Handler(t *testing.T) {
	// Build new registry config matching updated code
	registryConfig := RegistryConfig{
		UpstreamUsGAR:   Registry{Endpoint: "https://gcr.io", Namespace: "datadoghq"},
		UpstreamEuGAR:   Registry{Endpoint: "https://eu.gcr.io", Namespace: "datadoghq"},
		UpstreamAsiaGAR: Registry{Endpoint: "https://asia.gcr.io", Namespace: "datadoghq"},
		UpstreamACR:     Registry{Endpoint: "https://datadoghq.azurecr.io"},
		UpstreamCDN:     Registry{Endpoint: "https://d1u5qnb27isorz.cloudfront.net"},
		InfoURL:         "https://docs.datadoghq.com/",
		PrivacyURL:      "https://www.datadoghq.com/legal/privacy/",
	}

	// S3/CDN known blobs
	blobs := fakeBlobsChecker{
		knownURLs: map[string]bool{
			"https://adel.us-east-1.s3.dualstack.us-east-1.amazonaws.com/v2/pause/manifests/latest":                                                                            true,
			"https://adel.us-east-1.s3.dualstack.us-east-1.amazonaws.com/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e":               true,
			"https://adel-reg.ap-southeast-1.s3.dualstack.ap-southeast-1.amazonaws.com/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e": true,
			"https://adel-reg.eu-central-1.s3.dualstack.eu-central-1.amazonaws.com/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e":     true,
			"https://d1u5qnb27isorz.cloudfront.net/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e":                                     true,
		},
	}

	// Override regionMapper with a fake mapper for deterministic IP -> cloud/region
	orig := regionMapper
	t.Cleanup(func() { regionMapper = orig })
	regionMapper = fakeIPMapper{m: map[string]cloudcidrs.IPInfo{
		"10.0.0.1": {Cloud: cloudcidrs.AWS, Region: "us-east-1"},
		"10.0.0.2": {Cloud: cloudcidrs.AWS, Region: "eu-central-1"},
		"10.0.0.3": {Cloud: cloudcidrs.AWS, Region: "ap-southeast-1"},
		"10.0.0.4": {Cloud: cloudcidrs.GCP, Region: "europe-west1"},
		"10.0.0.5": {Cloud: cloudcidrs.GCP, Region: "asia-southeast1"},
		"10.0.0.6": {Cloud: cloudcidrs.AZ, Region: "westeurope"},
		"10.0.0.7": {Cloud: cloudcidrs.AWS, Region: "eu-west-1"}, // no bucket -> CDN
		"10.0.0.8": {},                                           // not cloud -> CDN
	}}

	handler := makeV2Handler(registryConfig, &blobs)

	testCases := []struct {
		Name           string
		Request        *http.Request
		ExpectedStatus int
		ExpectedURL    string
	}{
		{
			Name: "Blob AWS us-east-1 -> S3",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil)
				r.RemoteAddr = "10.0.0.1:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://adel.us-east-1.s3.dualstack.us-east-1.amazonaws.com/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
		},
		{
			Name: "Blob AWS eu-central-1 -> S3",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil)
				r.RemoteAddr = "10.0.0.2:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://adel-reg.eu-central-1.s3.dualstack.eu-central-1.amazonaws.com/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
		},
		{
			Name: "Blob AWS ap-southeast-1 -> S3",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil)
				r.RemoteAddr = "10.0.0.3:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://adel-reg.ap-southeast-1.s3.dualstack.ap-southeast-1.amazonaws.com/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
		},
		{
			Name: "Blob AWS unknown bucket -> CDN fallback",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil)
				r.RemoteAddr = "10.0.0.7:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://d1u5qnb27isorz.cloudfront.net/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
		},
		{
			Name: "Bogus remote addr -> 400",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil)
				r.RemoteAddr = "35.180.1.1asdfasdfsd:888"
				return r
			}(),
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name: "/v2/_catalog -> 404",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/_catalog", nil)
				r.RemoteAddr = "10.0.0.1:1234"
				return r
			}(),
			ExpectedStatus: http.StatusNotFound,
		},
		{
			Name: "Manifest from known AWS -> AWS S3",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/manifests/latest", nil)
				r.RemoteAddr = "10.0.0.1:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://adel.us-east-1.s3.dualstack.us-east-1.amazonaws.com/v2/pause/manifests/latest",
		},
		{
			Name: "Manifest from unknown AWS -> CDN",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/manifests/latest", nil)
				r.RemoteAddr = "10.0.0.7:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://d1u5qnb27isorz.cloudfront.net/v2/pause/manifests/latest",
		},
		{
			Name: "Blob not present in S3 -> fallback to CDN",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1234567", nil)
				r.RemoteAddr = "10.0.0.1:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://d1u5qnb27isorz.cloudfront.net/v2/pause/blobs/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1234567",
		},
		{
			Name: "Manifest not present in S3 -> fallback to CDN",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/manifests/aaaaa", nil)
				r.RemoteAddr = "10.0.0.1:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://d1u5qnb27isorz.cloudfront.net/v2/pause/manifests/aaaaa",
		},
		{
			Name: "GCP EU -> EU GCR",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/manifests/latest", nil)
				r.RemoteAddr = "10.0.0.4:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://eu.gcr.io/v2/datadoghq/pause/manifests/latest",
		},
		{
			Name: "GCP Asia -> Asia GCR (blob)",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil)
				r.RemoteAddr = "10.0.0.5:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://asia.gcr.io/v2/datadoghq/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
		},
		{
			Name: "Azure -> Azure ACR",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil)
				r.RemoteAddr = "10.0.0.6:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://datadoghq.azurecr.io/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
		},
		{
			Name: "Azure IP GET /v2 -> redirect to ACR",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2", nil)
				r.RemoteAddr = "10.0.0.6:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://datadoghq.azurecr.io/v2",
		},
		{
			Name: "Unknown IP -> CDN fallback",
			Request: func() *http.Request {
				r := httptest.NewRequest("GET", "http://localhost:8080/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e", nil)
				r.RemoteAddr = "10.0.0.8:1234"
				return r
			}(),
			ExpectedStatus: http.StatusTemporaryRedirect,
			ExpectedURL:    "https://d1u5qnb27isorz.cloudfront.net/v2/pause/blobs/sha256:da86e6ba6ca197bf6bc5e9d900febd906b133eaa4750e6bed647b0fbe50ed43e",
		},
	}

	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			handler(recorder, tc.Request)
			response := recorder.Result()
			if response == nil {
				t.Fatalf("nil response")
			}
			if response.StatusCode != tc.ExpectedStatus {
				t.Fatalf(
					"expected status: %v, but got status: %v",
					http.StatusText(tc.ExpectedStatus),
					http.StatusText(response.StatusCode),
				)
			}
			location, err := response.Location()
			if err != nil {
				if !errors.Is(err, http.ErrNoLocation) {
					t.Fatalf("failed to get response location with error: %v", err)
				} else if tc.ExpectedURL != "" {
					t.Fatalf("expected url: %q but no location was available", tc.ExpectedURL)
				}
			} else if location.String() != tc.ExpectedURL {
				t.Fatalf(
					"expected url: %q, but got: %q",
					tc.ExpectedURL,
					location,
				)
			}
		})
	}
}

// fakeIPMapper implements cloudcidrs IPMapper for tests
type fakeIPMapper struct{ m map[string]cloudcidrs.IPInfo }

func (f fakeIPMapper) GetIP(ip netip.Addr) (cloudcidrs.IPInfo, bool) {
	v, ok := f.m[ip.String()]
	return v, ok
}
