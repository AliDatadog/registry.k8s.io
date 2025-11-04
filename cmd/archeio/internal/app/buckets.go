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

// awsRegionToHostURL returns the base S3 bucket URL for an OCI layer blob given the AWS region
//
// blobs in the buckets should be stored at /containers/images/sha256:$hash
func awsRegionToHostURL(region, defaultURL string) string {
	switch region {
	// each of these has the region in which we have a bucket listed first
	// and then additional regions we're mapping to that bucket
	// based roughly on physical adjacency (and therefore _presumed_ latency)
	//
	// if you add a bucket, add a case for the region it is in, and consider
	// shifting other regions that do not have their own bucket

	//// US East (N. Virginia)
	case "us-east-1", "us-east-2", "us-west-1", "us-west-2":
		return "https://containerimageregistry.s3.us-east-1.amazonaws.com"
	default:
		return defaultURL
	}
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
