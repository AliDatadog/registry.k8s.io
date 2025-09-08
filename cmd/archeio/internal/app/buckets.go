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
	"net/http"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// TODO: replace with a more dynamic way to get the bucket URL
var knownS3Buckets = map[string]string{
	"us-east-1":      "https://adel-us-east-1.s3.dualstack.us-east-1.amazonaws.com",
	"ap-southeast-1": "https://adel-ap-southeast-1.s3.dualstack.ap-southeast-1.amazonaws.com",
	"eu-central-1":   "https://adel-eu-central-1.s3.dualstack.eu-central-1.amazonaws.com",
}

// awsRegionToHostURL returns the base S3 bucket URL for an OCI layer blob given the AWS region
//
// blobs in the buckets should be stored at /containers/images/sha256:$hash
func awsRegionToHostURL(region, defaultURL string) string {
	if url, ok := knownS3Buckets[region]; ok {
		return url
	}
	return defaultURL
}

// blobChecker are used to check if a blob exists, possibly with caching
type blobChecker interface {
	// BlobExists should check that blobURL exists
	// bucket and layerHash may be used for caching purposes
	BlobExists(blobURL string) bool
}

// cachedBlobChecker just performs an HTTP HEAD check against the blob
//
// TODO: potentially replace with a caching implementation
// should be plenty fast for now, HTTP HEAD on s3 is cheap
type cachedBlobChecker struct {
	blobCache
}

func newCachedBlobChecker() *cachedBlobChecker {
	return &cachedBlobChecker{}
}

type blobCache struct {
	m sync.Map
}

func (b *blobCache) Get(blobURL string) bool {
	_, exists := b.m.Load(blobURL)
	return exists
}

func (b *blobCache) Put(blobURL string) {
	b.m.Store(blobURL, struct{}{})
}

func (c *cachedBlobChecker) BlobExists(blobURL string) bool {
	if c.blobCache.Get(blobURL) {
		klog.V(3).InfoS("blob existence cache hit", "url", blobURL)
		return true
	}
	klog.V(3).InfoS("blob existence cache miss", "url", blobURL)
	// NOTE: this client will still share http.DefaultTransport
	// We do not wish to share the rest of the client state currently
	client := &http.Client{
		// ensure sensible timeouts
		Timeout: time.Second * 5,
	}
	r, err := client.Head(blobURL)
	// fallback to assuming blob is unavailable on errors
	if err != nil {
		klog.Errorf("failed to HEAD %s: %v", blobURL, err)
		return false
	}
	r.Body.Close()
	// if the blob exists it HEAD should return 200 OK
	// this is true for S3 and for OCI registries
	if r.StatusCode == http.StatusOK {
		c.blobCache.Put(blobURL)
		return true
	}
	return false
}
