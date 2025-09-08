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

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	registrytypes "github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/aws/smithy-go"

	"k8s.io/klog/v2"
)

type s3Uploader struct {
	svc            *s3.Client
	uploader       *manager.Uploader
	reuploadLayers bool
	dryRun         bool
	seen           map[string]struct{}
	seenMutex      sync.RWMutex
}

func newS3Uploader(dryRun bool) (*s3Uploader, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("us-east-1"))
	if err != nil {
		return nil, err
	}
	if dryRun {
		// Use anonymous credentials for dry run
		cfg.Credentials = aws.AnonymousCredentials{}
	}
	// Create S3 client
	client := s3.NewFromConfig(cfg)
	r := &s3Uploader{
		dryRun: dryRun,
		svc:    client,
		seen:   make(map[string]struct{}),
	}
	// Create uploader
	r.uploader = manager.NewUploader(client)
	return r, nil
}

func (s *s3Uploader) UploadImage(bucket string, ref name.Reference, blobs []v1.Layer, tags []string, manifestMediaType registrytypes.MediaType, opts ...crane.Option) error {
	keys := strings.Split(ref.Context().RepositoryStr(), "/")
	if len(keys) != 2 {
		panic(fmt.Errorf("invalid repository: %s", keys))
	}
	imageName := keys[1]
	// Upload layers and config
	for _, layer := range blobs {
		if err := s.copyLayerToS3(bucket, imageName, layer); err != nil {
			return err
		}
	}
	m, err := manifestBlobFromRef(ref, opts...)
	if err != nil {
		return err
	}
	return s.copyManifestToS3(bucket, imageName, m, manifestMediaType, tags)
}

// imageBlob requires the subset of v1.Layer methods
// required for uploading a blob
type imageBlob interface {
	Digest() (v1.Hash, error)
	Compressed() (io.ReadCloser, error)
}

type manifestBlob struct {
	raw    []byte
	digest v1.Hash
}

func manifestBlobFromRef(ref name.Reference, opts ...crane.Option) (*manifestBlob, error) {
	p := strings.Split(ref.Name(), "@")
	if len(p) != 2 {
		return nil, errors.New("invalid reference")
	}
	digest, err := v1.NewHash(p[1])
	if err != nil {
		return nil, err
	}
	manifest, err := crane.Manifest(ref.Name(), opts...)
	if err != nil {
		return nil, err
	}
	return &manifestBlob{
		raw:    manifest,
		digest: digest,
	}, nil
}

func (m *manifestBlob) Digest() (v1.Hash, error) {
	return m.digest, nil
}

func (m *manifestBlob) Compressed() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.raw)), nil
}

func (s *s3Uploader) copyManifestToS3(bucket, imageName string, m *manifestBlob, manifestMediaType registrytypes.MediaType, tags []string) error {
	digest, err := m.Digest()
	if err != nil {
		return err
	}
	// First upload the manifest for the digest
	key := keyForManifest(imageName, digest.String())
	err = s.copyToS3(bucket, key, string(manifestMediaType), m)
	if err != nil {
		return err
	}

	// Upload manifests for all tags (i.e. 7.65.0)
	for _, tag := range tags {
		key := keyForManifest(imageName, tag)
		if err := s.copyToS3(bucket, key, string(manifestMediaType), m); err != nil {
			return err
		}
	}
	return nil
}

func (s *s3Uploader) copyLayerToS3(bucket, imageName string, layer imageBlob) error {
	digest, err := layer.Digest()
	if err != nil {
		return err
	}
	key := keyForLayer(imageName, digest.String())

	return s.copyToS3(bucket, key, "application/octet-stream", layer)
}

func (s *s3Uploader) copyToS3(bucket, key, contentType string, layer imageBlob) error {
	digest, err := layer.Digest()
	if err != nil {
		klog.Errorf("failed to get digest: %v", err)
		return err
	}
	if !s.reuploadLayers {
		exists, err := s.blobExists(bucket, key)
		if err != nil {
			klog.Errorf("failed to check if blob exists: %v", err)
		} else if exists {
			klog.Infof("Layer already exists: %s", key)
			return nil
		}
	}
	r, err := layer.Compressed()
	if err != nil {
		klog.Errorf("failed to get compressed layer: %v", err)
		return err
	}
	defer r.Close()
	uploadInput := &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        r,
		ContentType: aws.String(contentType),
	}
	// TODO: what if it isn't sha256?
	if digest.Algorithm == "SHA256" {
		b, err := hex.DecodeString(digest.Hex)
		if err != nil {
			klog.Errorf("failed to decode digest: %v", err)
			return err
		}
		uploadInput.ChecksumSHA256 = aws.String(base64.StdEncoding.EncodeToString(b))
	}
	// skip actually uploading if this is a dry-run, otherwise finally upload
	if s.dryRun {
		klog.Infof("uploadInput: %+v", uploadInput)
		return nil
	}
	klog.Infof("Uploading: %s", key)
	_, err = s.uploader.Upload(context.TODO(), uploadInput)
	return err
}

func keyForLayer(imageName string, digest string) string {
	return fmt.Sprintf("v2/%s/blobs/%s", imageName, digest)
}

func keyForManifest(imageName string, digest string) string {
	return fmt.Sprintf("v2/%s/manifests/%s", imageName, digest)
}

func (s *s3Uploader) blobExists(bucket, key string) (bool, error) {
	s.seenMutex.RLock()
	if _, ok := s.seen[key]; ok {
		s.seenMutex.RUnlock()
		return true, nil
	}
	s.seenMutex.RUnlock()
	s.seenMutex.Lock()
	s.seen[key] = struct{}{}
	s.seenMutex.Unlock()
	_, err := s.svc.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *types.NotFound
		var apiErr smithy.APIError
		if errors.As(err, &notFound) {
			return false, nil
		} else if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "NotFound" {
				return false, nil
			}
		}
		return false, err
	}

	return true, nil
}
