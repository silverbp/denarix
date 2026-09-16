// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package storage wraps an S3-compatible object store (SeaweedFS locally -
// see docker-compose.yml's seaweedfs service, chrislusf/seaweedfs's `-s3`
// gateway) behind a small Put/Get/Delete interface. AttachmentService
// (internal/server/context_service.go) is the only caller: no other part
// of denarix, and no gRPC client, ever addresses this backend directly - files
// are streamed through UploadAttachment/DownloadAttachment instead, so
// swapping SeaweedFS for real S3 (or anything else S3-compatible) later is
// a config change, not a code or API change.
package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Store struct {
	client *minio.Client
	bucket string
}

func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Store, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("creating storage client: %w", err)
	}
	return &Store{client: client, bucket: bucket}, nil
}

// EnsureBucket creates the configured bucket if it doesn't already exist,
// so a fresh SeaweedFS instance (e.g. a first `docker compose up` in local
// dev) works with zero manual setup.
func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("checking bucket %q: %w", s.bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("creating bucket %q: %w", s.bucket, err)
	}
	return nil
}

// NewKey generates a new object key: random and unrelated to any
// caller-supplied filename or entity id, so a leaked/guessed key can't be
// used to enumerate another business's attachments - AttachmentService's
// own auth (RequireBusinessRole) is what actually gates access; this just
// keeps the key itself uninformative.
func NewKey() string {
	return uuid.NewString()
}

// Put uploads r under key. size may be -1 if unknown (minio-go then
// streams via multipart upload instead of a single PUT).
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("uploading %q: %w", key, err)
	}
	return nil
}

// Get returns a reader over the object at key. The caller must Close it.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", key, err)
	}
	// GetObject doesn't itself error on a missing key - the first read does
	// (lazily) - so confirm the object actually exists now rather than
	// handing the caller a reader that fails later mid-stream.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, fmt.Errorf("opening %q: %w", key, err)
	}
	return obj, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("deleting %q: %w", key, err)
	}
	return nil
}
